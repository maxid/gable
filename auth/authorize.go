package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/maxid/gable/web"
)

// AuthorizationRequest is a validated OAuth2 authorization request.
type AuthorizationRequest struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Nonce               string
}

// Scopes returns the requested scopes.
func (r AuthorizationRequest) Scopes() []string { return strings.Fields(r.Scope) }

// Authorizer decides whether a principal may grant the requested scopes.
type Authorizer interface {
	Authorize(ctx context.Context, principal Principal, request AuthorizationRequest) (bool, error)
}

// AllowAllAuthorizer grants every authorized request. Use it only in examples
// and tests; production should ask the user or check stored grants.
type AllowAllAuthorizer struct{}

// Authorize implements Authorizer.
func (AllowAllAuthorizer) Authorize(context.Context, Principal, AuthorizationRequest) (bool, error) {
	return true, nil
}

// AuthorizationCode is the server-side state of one code.
type AuthorizationCode struct {
	ClientID            string
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	Nonce               string
	Principal           Principal
	ExpiresAt           time.Time
}

// AuthorizationCodeStore persists codes by their hash.
type AuthorizationCodeStore interface {
	Put(ctx context.Context, key string, code AuthorizationCode) error
	Take(ctx context.Context, key string) (AuthorizationCode, bool, error)
}

// MemoryAuthorizationCodeStore is an in-memory AuthorizationCodeStore.
type MemoryAuthorizationCodeStore struct {
	mu    sync.Mutex
	codes map[string]AuthorizationCode
}

// NewMemoryAuthorizationCodeStore returns an empty store.
func NewMemoryAuthorizationCodeStore() *MemoryAuthorizationCodeStore {
	return &MemoryAuthorizationCodeStore{codes: map[string]AuthorizationCode{}}
}

func (s *MemoryAuthorizationCodeStore) Put(_ context.Context, key string, code AuthorizationCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[key] = code
	return nil
}

func (s *MemoryAuthorizationCodeStore) Take(_ context.Context, key string) (AuthorizationCode, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.codes[key]
	delete(s.codes, key)
	if !ok || time.Now().After(code.ExpiresAt) {
		return AuthorizationCode{}, false, nil
	}
	return code, true, nil
}

// AuthorizationServerOptions configures the authorize endpoint.
type AuthorizationServerOptions struct {
	Authorizer Authorizer
	Codes      AuthorizationCodeStore
	LoginURL   func(returnTo string) string
	CodeTTL    time.Duration
}

// ConfigureAuthorizationServer replaces the authorize endpoint dependencies.
func (h *Handlers) ConfigureAuthorizationServer(opts AuthorizationServerOptions) {
	if opts.Authorizer != nil {
		h.Authorizer = opts.Authorizer
	}
	if opts.Codes != nil {
		h.Codes = opts.Codes
	}
	if opts.LoginURL != nil {
		h.LoginURL = opts.LoginURL
	}
	if opts.CodeTTL > 0 {
		h.CodeTTL = opts.CodeTTL
	}
}

func (h *Handlers) authorize(ctx *web.Context) error {
	request, err := parseAuthorizationRequest(ctx.Request())
	if err != nil {
		return authorizeJSONError(ctx, "invalid_request", err.Error())
	}
	client, err := h.client(ctx.Request().Context(), request.ClientID)
	if err != nil {
		return authorizeJSONError(ctx, "invalid_client", "unknown client")
	}
	if !redirectAllowed(client, request.RedirectURI) {
		return authorizeJSONError(ctx, "invalid_request", "redirect_uri is not registered")
	}
	if request.ResponseType != "code" {
		return authorizeRedirectError(ctx, request, "unsupported_response_type", "only code is supported")
	}
	if !scopesAllowed(client, request.Scopes()) {
		return authorizeRedirectError(ctx, request, "invalid_scope", "scope is not allowed")
	}
	if request.CodeChallenge == "" || request.CodeChallengeMethod != "S256" {
		return authorizeRedirectError(ctx, request, "invalid_request", "PKCE S256 is required")
	}

	principal, ok := PrincipalFromContext(ctx.Request().Context())
	if !ok {
		if token := h.accessToken(ctx.Request()); token != "" {
			if verified, err := h.Service.Verify(ctx.Request().Context(), token); err == nil {
				principal, ok = verified, true
			}
		}
	}
	if !ok {
		if h.LoginURL != nil {
			ctx.Gin().Redirect(http.StatusFound, h.LoginURL(ctx.Request().URL.String()))
			return nil
		}
		return ErrMissingToken
	}

	if h.Authorizer == nil {
		return authorizeRedirectError(ctx, request, "access_denied", "authorization is not configured")
	}
	allowed, err := h.Authorizer.Authorize(ctx.Request().Context(), principal, request)
	if err != nil {
		return err
	}
	if !allowed {
		return authorizeRedirectError(ctx, request, "access_denied", "the request was denied")
	}

	code := randomToken()
	codeTTL := h.CodeTTL
	if codeTTL <= 0 {
		codeTTL = 5 * time.Minute
	}
	if err := h.Codes.Put(ctx.Request().Context(), h.Service.tokenKey(code), AuthorizationCode{
		ClientID:            client.ID,
		RedirectURI:         request.RedirectURI,
		Scope:               request.Scope,
		CodeChallenge:       request.CodeChallenge,
		CodeChallengeMethod: request.CodeChallengeMethod,
		Nonce:               request.Nonce,
		Principal:           principal,
		ExpiresAt:           time.Now().Add(codeTTL),
	}); err != nil {
		return err
	}

	redirect, _ := url.Parse(request.RedirectURI)
	query := redirect.Query()
	query.Set("code", code)
	if request.State != "" {
		query.Set("state", request.State)
	}
	redirect.RawQuery = query.Encode()
	ctx.Gin().Redirect(http.StatusFound, redirect.String())
	return nil
}

func (h *Handlers) client(ctx context.Context, clientID string) (*Client, error) {
	if h.Service.clients == nil {
		return nil, ErrInvalidClient
	}
	client, err := h.Service.clients.FindClient(ctx, clientID)
	if err != nil || client == nil || client.Disabled {
		return nil, ErrInvalidClient
	}
	return client, nil
}

func (h *Handlers) exchangeCode(ctx *web.Context) (*TokenPair, error) {
	if err := ctx.Request().ParseForm(); err != nil {
		return nil, ErrInvalidGrant
	}
	form := ctx.Request().PostForm
	code := form.Get("code")
	if code == "" {
		return nil, ErrInvalidGrant
	}
	clientID, clientSecret := clientCredentials(ctx.Request(), form)
	codeData, ok, err := h.Codes.Take(ctx.Request().Context(), h.Service.tokenKey(code))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidGrant
	}
	if codeData.ClientID != clientID && clientID != "" {
		return nil, ErrInvalidGrant
	}
	if redirect := form.Get("redirect_uri"); redirect != codeData.RedirectURI {
		return nil, ErrInvalidGrant
	}
	client, err := h.client(ctx.Request().Context(), codeData.ClientID)
	if err != nil {
		return nil, ErrInvalidClient
	}
	if !client.Public && client.Secret != "" && !secretEqual(client.Secret, clientSecret) {
		return nil, ErrInvalidClient
	}
	if codeData.CodeChallenge != "" {
		verifier := form.Get("code_verifier")
		challenge := sha256.Sum256([]byte(verifier))
		expected := base64.RawURLEncoding.EncodeToString(challenge[:])
		if verifier == "" || !secretEqual(codeData.CodeChallenge, expected) {
			return nil, ErrInvalidGrant
		}
	}
	principal := codeData.Principal
	principal.Scopes = strings.Fields(codeData.Scope)
	pair, err := h.Service.Issue(ctx.Request().Context(), principal)
	if err != nil {
		return nil, err
	}
	if h.Service.HasOIDC() && hasScope(principal.Scopes, "openid") {
		idToken, err := h.Service.SignIDToken(principal, codeData.ClientID, codeData.Nonce)
		if err != nil {
			return nil, err
		}
		pair.IDToken = idToken
	}
	return pair, nil
}

func parseAuthorizationRequest(request *http.Request) (AuthorizationRequest, error) {
	values := request.URL.Query()
	if request.Method == http.MethodPost {
		if err := request.ParseForm(); err != nil {
			return AuthorizationRequest{}, err
		}
		values = request.PostForm
	}
	return AuthorizationRequest{
		ClientID:            values.Get("client_id"),
		RedirectURI:         values.Get("redirect_uri"),
		ResponseType:        values.Get("response_type"),
		Scope:               values.Get("scope"),
		State:               values.Get("state"),
		CodeChallenge:       values.Get("code_challenge"),
		CodeChallengeMethod: values.Get("code_challenge_method"),
		Nonce:               values.Get("nonce"),
	}, nil
}

func redirectAllowed(client *Client, redirect string) bool {
	if redirect == "" {
		return false
	}
	for _, candidate := range client.RedirectURIs {
		if candidate == redirect {
			return true
		}
	}
	return false
}

func hasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func scopesAllowed(client *Client, scopes []string) bool {
	if len(client.Scopes) == 0 {
		return true
	}
	for _, scope := range scopes {
		found := false
		for _, allowed := range client.Scopes {
			if allowed == scope {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func authorizeJSONError(ctx *web.Context, code, description string) error {
	ctx.Gin().JSON(http.StatusBadRequest, map[string]any{"error": code, "error_description": description})
	return nil
}

func authorizeRedirectError(ctx *web.Context, request AuthorizationRequest, code, description string) error {
	redirect, err := url.Parse(request.RedirectURI)
	if err != nil {
		return authorizeJSONError(ctx, code, description)
	}
	query := redirect.Query()
	query.Set("error", code)
	if description != "" {
		query.Set("error_description", description)
	}
	if request.State != "" {
		query.Set("state", request.State)
	}
	redirect.RawQuery = query.Encode()
	ctx.Gin().Redirect(http.StatusFound, redirect.String())
	return nil
}

func (h *Handlers) metadata(ctx *web.Context) error {
	issuer := strings.TrimRight(h.Service.Config().Issuer, "/")
	base := issuer
	document := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth2/authorize",
		"token_endpoint":                        base + "/oauth2/token",
		"introspection_endpoint":                base + "/oauth2/introspect",
		"revocation_endpoint":                   base + "/oauth2/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token", "client_credentials", "password"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"userinfo_endpoint":                     base + "/oauth2/userinfo",
		"scopes_supported":                      []string{"openid", "profile", "email", "read"},
		"claims_supported":                      []string{"sub", "iss", "aud", "exp", "iat", "nonce", "preferred_username", "email", "name", "tenant_id"},
	}
	if h.Service.HasOIDC() {
		document["jwks_uri"] = base + "/oauth2/jwks"
		document["id_token_signing_alg_values_supported"] = []string{"RS256"}
		document["subject_types_supported"] = []string{"public"}
	}
	ctx.Gin().JSON(http.StatusOK, document)
	return nil
}
