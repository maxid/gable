package auth

import (
	"net/http"
	"time"

	"github.com/maxid/gable/web"
)

// mfaVerifyForm completes a login that stopped at the second factor.
type mfaVerifyForm struct {
	Ticket string `json:"ticket" binding:"required"`
	Code   string `json:"code" binding:"required"`
}

// mfaCodeForm carries the code for an operation on an already authenticated account.
type mfaCodeForm struct {
	Code string `json:"code" binding:"required"`
}

// MFAChallengeVO is the login answer when a second factor is due. It is deliberately not
// a TokenVO: there is no token in it.
type MFAChallengeVO struct {
	MFARequired bool   `json:"mfaRequired"`
	Ticket      string `json:"ticket"`
	ExpiresIn   int    `json:"expiresIn"`
}

// challengeTTL bounds how long a login may sit waiting for a code.
const challengeTTL = 5 * time.Minute

func (h *Handlers) challengeStore() ChallengeStore {
	if h.Challenges == nil {
		h.Challenges = NewMemoryChallengeStore()
	}
	return h.Challenges
}

func (h *Handlers) challengeLifetime() time.Duration {
	if h.ChallengeTTL > 0 {
		return h.ChallengeTTL
	}
	return challengeTTL
}

// loginWithMFA runs the password check and, for an account with an active second factor,
// answers with a ticket instead of tokens.
func (h *Handlers) loginWithMFA(ctx *web.Context, request LoginRequest) error {
	principal, err := h.Service.Authenticate(ctx.Request().Context(), request)
	if err != nil {
		return err
	}

	active, err := h.MFA.Active(ctx.Request().Context(), principal.TenantID, principal.Username)
	if err != nil {
		return err
	}
	if !active {
		pair, err := h.Service.Issue(ctx.Request().Context(), principal)
		if err != nil {
			return err
		}
		h.writeSession(ctx, pair)
		return ctx.JSON(http.StatusOK, tokenVO(pair))
	}

	ticket, err := newTicket()
	if err != nil {
		return err
	}
	ttl := h.challengeLifetime()
	if err := h.challengeStore().PutChallenge(ctx.Request().Context(), ticket, MFAChallenge{
		Principal: principal,
		ExpiresAt: time.Now().Add(ttl),
	}); err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, MFAChallengeVO{
		MFARequired: true,
		Ticket:      ticket,
		ExpiresIn:   int(ttl.Seconds()),
	})
}

// mfaVerify exchanges a login ticket plus a code for tokens. The ticket is consumed even
// when the code is wrong, so a stolen ticket buys exactly one guess.
func (h *Handlers) mfaVerify(ctx *web.Context) error {
	var form mfaVerifyForm
	if err := ctx.Bind(&form); err != nil {
		return err
	}
	challenge, ok, err := h.challengeStore().TakeChallenge(ctx.Request().Context(), form.Ticket)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMFAInvalidCode
	}
	if err := h.MFA.Verify(ctx.Request().Context(), challenge.Principal.TenantID, challenge.Principal.Username, form.Code); err != nil {
		return err
	}
	pair, err := h.Service.Issue(ctx.Request().Context(), challenge.Principal)
	if err != nil {
		return err
	}
	h.writeSession(ctx, pair)
	return ctx.JSON(http.StatusOK, tokenVO(pair))
}

// currentUser is the account behind the request; the MFA endpoints act on it alone.
func (h *Handlers) currentUser(ctx *web.Context) (Principal, error) {
	principal, ok := PrincipalFromContext(ctx.Request().Context())
	if !ok {
		token := h.accessToken(ctx.Request())
		if token == "" {
			return Principal{}, ErrMissingToken
		}
		verified, err := h.Service.Verify(ctx.Request().Context(), token)
		if err != nil {
			return Principal{}, err
		}
		principal = verified
	}
	if principal.Username == "" {
		return Principal{}, ErrMissingToken
	}
	return principal, nil
}

func (h *Handlers) mfaStatus(ctx *web.Context) error {
	principal, err := h.currentUser(ctx)
	if err != nil {
		return err
	}
	status, err := h.MFA.Status(ctx.Request().Context(), principal.TenantID, principal.Username)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, status)
}

// mfaEnroll hands out a new secret and recovery codes. Nothing changes for login until
// Activate succeeds, so a half-finished enrollment cannot lock anybody out.
func (h *Handlers) mfaEnroll(ctx *web.Context) error {
	principal, err := h.currentUser(ctx)
	if err != nil {
		return err
	}
	enrollment, err := h.MFA.Enroll(ctx.Request().Context(), principal.TenantID, principal.Username)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, enrollment)
}

func (h *Handlers) mfaActivate(ctx *web.Context) error {
	var form mfaCodeForm
	if err := ctx.Bind(&form); err != nil {
		return err
	}
	principal, err := h.currentUser(ctx)
	if err != nil {
		return err
	}
	if err := h.MFA.Activate(ctx.Request().Context(), principal.TenantID, principal.Username, form.Code); err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, map[string]bool{"active": true})
}

func (h *Handlers) mfaDisable(ctx *web.Context) error {
	var form mfaCodeForm
	if err := ctx.Bind(&form); err != nil {
		return err
	}
	principal, err := h.currentUser(ctx)
	if err != nil {
		return err
	}
	if err := h.MFA.Disable(ctx.Request().Context(), principal.TenantID, principal.Username, form.Code); err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, map[string]bool{"active": false})
}
