package oauthclient

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/maxid/gable/auth"
	"github.com/maxid/gable/web"
)

// PrincipalResolver maps an external profile to a gable principal. It is where
// an application creates or links its local user.
type PrincipalResolver func(ctx context.Context, provider string, info UserInfo) (auth.Principal, error)

// Handlers exposes the social login redirect and callback.
type Handlers struct {
	Client  *Client
	Tokens  *auth.TokenService
	Resolve PrincipalResolver
}

// NewHandlers returns social login handlers.
func NewHandlers(client *Client, tokens *auth.TokenService, resolve PrincipalResolver) (*Handlers, error) {
	if client == nil || tokens == nil || resolve == nil {
		return nil, fmt.Errorf("oauthclient: client, token service and resolver are required")
	}
	return &Handlers{Client: client, Tokens: tokens, Resolve: resolve}, nil
}

// Routes registers /oauth2/login/:provider and /oauth2/callback/:provider.
func (h *Handlers) Routes(router *web.Router) {
	router.GET("/oauth2/login/:provider", h.login).Doc("start social login")
	router.GET("/oauth2/callback/:provider", h.callback).Doc("finish social login")
}

func (h *Handlers) login(ctx *web.Context) error {
	provider := ctx.Param("provider")
	state := randomState()
	target, _, err := h.Client.AuthCodeURL(ctx.Request().Context(), provider, state)
	if err != nil {
		return err
	}
	ctx.Gin().Redirect(http.StatusFound, target)
	return nil
}

func (h *Handlers) callback(ctx *web.Context) error {
	provider := ctx.Param("provider")
	if providerError := ctx.Query("error"); providerError != "" {
		return fmt.Errorf("oauthclient: provider error: %s", providerError)
	}
	state := ctx.Query("state")
	code := ctx.Query("code")
	if state == "" || code == "" {
		return fmt.Errorf("oauthclient: state and code are required")
	}
	info, err := h.Client.Exchange(ctx.Request().Context(), provider, state, code)
	if err != nil {
		return err
	}
	principal, err := h.Resolve(ctx.Request().Context(), provider, *info)
	if err != nil {
		return err
	}
	pair, err := h.Tokens.Issue(ctx.Request().Context(), principal)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, auth.NewTokenVO(pair))
}

func randomState() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic("oauthclient: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
