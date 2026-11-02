package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	gableerrors "github.com/maxid/gable/errors"
)

// MFA errors.
var (
	// ErrMFARequired is returned when a password is correct but a second factor is due.
	ErrMFARequired = gableerrors.New("auth_mfa_required", "a second factor is required").WithStatus(401)
	// ErrMFAInvalidCode covers a wrong, expired or already used code.
	ErrMFAInvalidCode = gableerrors.New("auth_mfa_invalid_code", "invalid verification code").WithStatus(401)
	// ErrMFANotEnrolled means the account has no active second factor.
	ErrMFANotEnrolled = gableerrors.New("auth_mfa_not_enrolled", "no second factor is active").WithStatus(400)
)

// RecoveryCodeCount is how many one-time codes an enrollment hands out.
const RecoveryCodeCount = 10

// MFAState is what is stored for one account. The secret and the recovery codes are the
// only secrets the framework keeps for MFA; the codes are stored hashed.
type MFAState struct {
	TenantID int64
	// Username is the account key, for the same reason the user store uses it: a subject
	// is not carried by every token format, and the login that checks MFA has a username.
	Username string
	Secret   string
	Active   bool
	// RecoveryCodes are SHA-256 hashes; the plaintext only exists in the enrollment
	// response, because a code that can be shown again has not been used as a second
	// factor.
	RecoveryCodes []string
	CreatedAt     time.Time
	ActivatedAt   time.Time
	LastUsedAt    time.Time
}

// MFAStore persists MFA state per tenant and account.
type MFAStore interface {
	GetMFA(ctx context.Context, tenantID int64, username string) (*MFAState, error)
	PutMFA(ctx context.Context, state *MFAState) error
}

// MemoryMFAStore is the in-process MFA store. It is fine for a single instance and for
// tests; a deployment with more than one replica needs a shared implementation.
type MemoryMFAStore struct {
	mu     sync.RWMutex
	states map[string]MFAState
}

// NewMemoryMFAStore returns an empty store.
func NewMemoryMFAStore() *MemoryMFAStore {
	return &MemoryMFAStore{states: map[string]MFAState{}}
}

func mfaKey(tenantID int64, username string) string {
	return fmt.Sprintf("%d:%s", tenantID, strings.ToLower(username))
}

// GetMFA implements MFAStore.
func (s *MemoryMFAStore) GetMFA(_ context.Context, tenantID int64, username string) (*MFAState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[mfaKey(tenantID, username)]
	if !ok {
		return nil, nil
	}
	copied := state
	copied.RecoveryCodes = append([]string(nil), state.RecoveryCodes...)
	return &copied, nil
}

// PutMFA implements MFAStore.
func (s *MemoryMFAStore) PutMFA(_ context.Context, state *MFAState) error {
	if state == nil {
		return fmt.Errorf("auth: nil mfa state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := *state
	stored.RecoveryCodes = append([]string(nil), state.RecoveryCodes...)
	s.states[mfaKey(state.TenantID, state.Username)] = stored
	return nil
}

// Enrollment is the one-time answer to "set up MFA": the secret and the recovery codes
// are shown once and never returned again.
type Enrollment struct {
	Secret        string   `json:"secret"`
	URL           string   `json:"otpauthUrl"`
	RecoveryCodes []string `json:"recoveryCodes"`
}

// MFAStatus is what an account may see about its own second factor.
type MFAStatus struct {
	Enrolled        bool       `json:"enrolled"`
	Active          bool       `json:"active"`
	RecoveryCodes   int        `json:"recoveryCodes"`
	ActivatedAt     *time.Time `json:"activatedAt,omitempty"`
	LastUsedAt      *time.Time `json:"lastUsedAt,omitempty"`
	RecoveryCodeUse []string   `json:"-"`
}

// MFAService enrolls, activates and verifies a second factor.
type MFAService struct {
	store  MFAStore
	cfg    TOTPConfig
	now    func() time.Time
	random func([]byte) (int, error)
}

// NewMFAService builds the service. A nil store falls back to memory.
func NewMFAService(cfg TOTPConfig, store MFAStore) (*MFAService, error) {
	cfg = cfg.normalize()
	service := &MFAService{store: store, cfg: cfg, now: time.Now, random: rand.Read}
	if service.store == nil {
		service.store = NewMemoryMFAStore()
	}
	return service, nil
}

// Config returns the TOTP policy in use.
func (s *MFAService) Config() TOTPConfig { return s.cfg }

// Status reports the account's second factor without revealing any secret.
func (s *MFAService) Status(ctx context.Context, tenantID int64, username string) (MFAStatus, error) {
	state, err := s.store.GetMFA(ctx, tenantID, username)
	if err != nil {
		return MFAStatus{}, err
	}
	if state == nil {
		return MFAStatus{}, nil
	}
	status := MFAStatus{
		Enrolled:      true,
		Active:        state.Active,
		RecoveryCodes: len(state.RecoveryCodes),
	}
	if !state.ActivatedAt.IsZero() {
		activated := state.ActivatedAt
		status.ActivatedAt = &activated
	}
	if !state.LastUsedAt.IsZero() {
		used := state.LastUsedAt
		status.LastUsedAt = &used
	}
	return status, nil
}

// Active reports whether the account must present a second factor at login.
func (s *MFAService) Active(ctx context.Context, tenantID int64, username string) (bool, error) {
	state, err := s.store.GetMFA(ctx, tenantID, username)
	if err != nil || state == nil {
		return false, err
	}
	return state.Active, nil
}

// Enroll starts (or restarts) enrollment. The account is not protected until Activate
// succeeds with a code from the authenticator, which proves the secret was actually
// imported.
func (s *MFAService) Enroll(ctx context.Context, tenantID int64, username string) (*Enrollment, error) {
	secret, err := NewTOTPSecret()
	if err != nil {
		return nil, err
	}
	codes, hashed, err := newRecoveryCodes(s.random)
	if err != nil {
		return nil, err
	}
	// Re-enrolling clears the previous secret and codes: keeping a second live secret is
	// how a "lost" device stays usable.
	state := &MFAState{
		TenantID:      tenantID,
		Username:      username,
		Secret:        secret,
		Active:        false,
		RecoveryCodes: hashed,
		CreatedAt:     s.now().UTC(),
	}
	if err := s.store.PutMFA(ctx, state); err != nil {
		return nil, err
	}
	return &Enrollment{
		Secret:        secret,
		URL:           OTPAuthURL(s.cfg.Issuer, username, secret, s.cfg),
		RecoveryCodes: codes,
	}, nil
}

// Activate confirms enrollment with a code from the authenticator app.
func (s *MFAService) Activate(ctx context.Context, tenantID int64, username string, code string) error {
	state, err := s.store.GetMFA(ctx, tenantID, username)
	if err != nil {
		return err
	}
	if state == nil || state.Secret == "" {
		return ErrMFANotEnrolled
	}
	if !VerifyTOTP(state.Secret, code, s.now(), s.cfg) {
		return ErrMFAInvalidCode
	}
	state.Active = true
	state.ActivatedAt = s.now().UTC()
	return s.store.PutMFA(ctx, state)
}

// Verify checks a TOTP code or a recovery code, and consumes a recovery code when one is
// used so it cannot be replayed.
func (s *MFAService) Verify(ctx context.Context, tenantID int64, username string, code string) error {
	state, err := s.store.GetMFA(ctx, tenantID, username)
	if err != nil {
		return err
	}
	if state == nil || !state.Active {
		return ErrMFANotEnrolled
	}
	if VerifyTOTP(state.Secret, code, s.now(), s.cfg) {
		state.LastUsedAt = s.now().UTC()
		return s.store.PutMFA(ctx, state)
	}

	hashed := hashRecoveryCode(code)
	for index, candidate := range state.RecoveryCodes {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(hashed)) != 1 {
			continue
		}
		state.RecoveryCodes = append(state.RecoveryCodes[:index:index], state.RecoveryCodes[index+1:]...)
		state.LastUsedAt = s.now().UTC()
		return s.store.PutMFA(ctx, state)
	}
	return ErrMFAInvalidCode
}

// Disable turns the second factor off, which requires a working code: an attacker with a
// stolen session must not be able to remove it.
func (s *MFAService) Disable(ctx context.Context, tenantID int64, username string, code string) error {
	if err := s.Verify(ctx, tenantID, username, code); err != nil {
		return err
	}
	state, err := s.store.GetMFA(ctx, tenantID, username)
	if err != nil {
		return err
	}
	if state == nil {
		return ErrMFANotEnrolled
	}
	state.Active = false
	state.Secret = ""
	state.RecoveryCodes = nil
	return s.store.PutMFA(ctx, state)
}

func newRecoveryCodes(random func([]byte) (int, error)) ([]string, []string, error) {
	codes := make([]string, 0, RecoveryCodeCount)
	hashed := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		buffer := make([]byte, 5)
		if _, err := random(buffer); err != nil {
			return nil, nil, fmt.Errorf("auth: generate recovery code: %w", err)
		}
		// Crockford-ish alphabet: no vowels, so a code cannot spell a word, and no
		// look-alike characters, because these get read aloud.
		const alphabet = "0123456789bcdfghjkmnpqrstvwxyz"
		var builder strings.Builder
		for _, b := range buffer {
			builder.WriteByte(alphabet[int(b)%len(alphabet)])
		}
		code := builder.String()
		codes = append(codes, code)
		hashed = append(hashed, hashRecoveryCode(code))
	}
	return codes, hashed, nil
}

// hashRecoveryCode stores a code so that a leaked store cannot be replayed. Codes are
// short by design, so this is not a password hash: 10^? guesses are bounded by the
// endpoint's rate limiting, not by the hash cost.
func hashRecoveryCode(code string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
