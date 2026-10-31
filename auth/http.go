package auth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// TokenVO is the camelCase token payload used by the platform frontend.
type TokenVO struct {
	TokenType            string `json:"tokenType"`
	AccessToken          string `json:"accessToken"`
	RefreshToken         string `json:"refreshToken"`
	ExpiresIn            int    `json:"expiresIn"`
	ExistOtherToken      bool   `json:"existOtherToken"`
	RedirectURI          string `json:"redirectUri"`
	Key                  string `json:"key"`
	NeedChangePassword   bool   `json:"needChangePassword"`
	ChangePasswordReason string `json:"changePasswordReason"`
	VerifyToken          string `json:"verifyToken"`
}

// NewTokenVO maps a token pair to the frontend's camelCase shape.
func NewTokenVO(pair *TokenPair) TokenVO { return tokenVO(pair) }

func tokenVO(pair *TokenPair) TokenVO {
	return TokenVO{
		TokenType:    pair.TokenType,
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		Key:          pair.GlobalKey,
	}
}

// Handlers exposes login, token and introspection endpoints.
type Handlers struct {
	Service    *TokenService
	Tenants    tenant.LoginResolver
	Codes      AuthorizationCodeStore
	Authorizer Authorizer
	LoginURL   func(returnTo string) string
	CodeTTL    time.Duration
}

// NewHandlers returns handlers for a token service.
func NewHandlers(service *TokenService, tenants tenant.LoginResolver) *Handlers {
	return &Handlers{
		Service: service,
		Tenants: tenants,
		Codes:   NewMemoryAuthorizationCodeStore(),
	}
}

// Routes registers the passport and OAuth2 endpoints with the default prefix.
func (h *Handlers) Routes(router *web.Router) { h.Mount(router, "") }

// Mount registers the endpoints with a custom passport prefix.
func (h *Handlers) Mount(router *web.Router, passportPrefix string) {
	if passportPrefix == "" {
		passportPrefix = "/passport"
	}
	router.POST(passportPrefix+"/login", h.login).Doc("password login").In(loginForm{}).Out(TokenVO{})
	router.POST(passportPrefix+"/login/refresh", h.refresh).Doc("refresh tokens").In(refreshForm{}).Out(TokenVO{})
	router.GET(passportPrefix+"/logout", h.logout).Doc("revoke the current token")
	router.GET(passportPrefix+"/me", h.me).Doc("current principal")

	router.GET("/oauth2/authorize", h.authorize).Doc("OAuth2 authorization endpoint")
	router.POST("/oauth2/authorize", h.authorize).Doc("OAuth2 authorization endpoint")
	router.GET("/.well-known/oauth-authorization-server", h.metadata).Doc("OAuth2 server metadata")
	router.GET("/.well-known/openid-configuration", h.metadata).Doc("OpenID configuration")
	router.POST("/oauth2/token", h.token).Doc("OAuth2 token endpoint")
	router.GET("/oauth2/jwks", h.jwks).Doc("OAuth2 JSON Web Key Set")
	router.GET("/oauth2/userinfo", h.userInfo).Doc("OpenID Connect userinfo")
	router.POST("/oauth2/introspect", h.introspect).Doc("OAuth2 token introspection")
	router.POST("/oauth2/revoke", h.revoke).Doc("OAuth2 token revocation")
}

type loginForm struct {
	Username     string `json:"username" binding:"required"`
	Password     string `json:"password" binding:"required"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	TenantCode   string `json:"tenantCode"`
}

type refreshForm struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

func (h *Handlers) login(ctx *web.Context) error {
	var form loginForm
	if err := ctx.Bind(&form); err != nil {
		return err
	}
	tenantID, err := h.resolveTenant(ctx, form.TenantCode)
	if err != nil {
		return err
	}
	pair, err := h.Service.Login(ctx.Request().Context(), LoginRequest{
		TenantID:     tenantID,
		Username:     form.Username,
		Password:     form.Password,
		ClientID:     form.ClientID,
		ClientSecret: form.ClientSecret,
	})
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, tokenVO(pair))
}

func (h *Handlers) refresh(ctx *web.Context) error {
	var form refreshForm
	if err := ctx.Bind(&form); err != nil {
		return err
	}
	pair, err := h.Service.Refresh(ctx.Request().Context(), form.RefreshToken)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, tokenVO(pair))
}

func (h *Handlers) logout(ctx *web.Context) error {
	token := bearerToken(ctx.Request())
	if token != "" {
		_ = h.Service.Revoke(ctx.Request().Context(), token)
	}
	return ctx.JSON(http.StatusOK, true)
}

func (h *Handlers) me(ctx *web.Context) error {
	principal, ok := PrincipalFromContext(ctx.Request().Context())
	if !ok {
		token := bearerToken(ctx.Request())
		if token == "" {
			return ErrMissingToken
		}
		verified, err := h.Service.Verify(ctx.Request().Context(), token)
		if err != nil {
			return err
		}
		principal = verified
	}
	return ctx.JSON(http.StatusOK, map[string]any{
		"id":       principal.Subject,
		"username": principal.Username,
		"tenantId": principal.TenantID,
		"clientId": principal.ClientID,
		"userType": principal.UserType,
		"scopes":   principal.Scopes,
		"roles":    principal.Roles,
	})
}

func (h *Handlers) token(ctx *web.Context) error {
	if err := ctx.Request().ParseForm(); err != nil {
		return oauthError(ctx, "invalid_request", "invalid form body")
	}
	form := ctx.Request().PostForm
	grant := form.Get("grant_type")
	clientID, clientSecret := clientCredentials(ctx.Request(), form)

	var pair *TokenPair
	var err error
	switch grant {
	case "password", "":
		tenantID, tenantErr := h.resolveTenant(ctx, form.Get("tenant_code"))
		if tenantErr != nil {
			return tenantErr
		}
		pair, err = h.Service.Login(ctx.Request().Context(), LoginRequest{
			TenantID:     tenantID,
			Username:     form.Get("username"),
			Password:     form.Get("password"),
			ClientID:     clientID,
			ClientSecret: clientSecret,
		})
	case "client_credentials":
		pair, err = h.Service.ClientCredentials(ctx.Request().Context(), clientID, clientSecret)
	case "authorization_code":
		pair, err = h.exchangeCode(ctx)
	case "refresh_token":
		pair, err = h.Service.Refresh(ctx.Request().Context(), form.Get("refresh_token"))
	default:
		return oauthError(ctx, "unsupported_grant_type", "grant type is not supported")
	}
	if err != nil {
		code := "invalid_grant"
		if err == ErrInvalidClient {
			code = "invalid_client"
		}
		return oauthError(ctx, code, err.Error())
	}
	response := map[string]any{
		"access_token": pair.AccessToken,
		"token_type":   pair.TokenType,
		"expires_in":   pair.ExpiresIn,
		"scope":        pair.Scope,
		"global_key":   pair.GlobalKey,
	}
	if pair.RefreshToken != "" {
		response["refresh_token"] = pair.RefreshToken
	}
	if pair.IDToken != "" {
		response["id_token"] = pair.IDToken
	}
	ctx.Gin().JSON(http.StatusOK, response)
	return nil
}

func (h *Handlers) jwks(ctx *web.Context) error {
	document, err := h.Service.JWKS()
	if err != nil {
		return err
	}
	ctx.Gin().JSON(http.StatusOK, document)
	return nil
}

func (h *Handlers) userInfo(ctx *web.Context) error {
	token := bearerToken(ctx.Request())
	principal, err := h.Service.Verify(ctx.Request().Context(), token)
	if err != nil {
		return err
	}
	subject := principal.Subject
	if subject == "" {
		subject = principal.Username
	}
	ctx.Gin().JSON(http.StatusOK, map[string]any{
		"sub":                subject,
		"preferred_username": principal.Username,
		"email":              principal.Email,
		"name":               principal.Name,
		"tenant_id":          principal.TenantID,
	})
	return nil
}

func (h *Handlers) introspect(ctx *web.Context) error {
	if err := ctx.Request().ParseForm(); err != nil {
		return oauthError(ctx, "invalid_request", "invalid form body")
	}
	result, err := h.Service.Introspect(ctx.Request().Context(), ctx.Request().PostForm.Get("token"))
	if err != nil {
		return err
	}
	ctx.Gin().JSON(http.StatusOK, map[string]any{
		"active":     result.Active,
		"client_id":  result.ClientID,
		"username":   result.Username,
		"tenant_id":  result.TenantID,
		"user_type":  result.UserType,
		"scope":      result.Scope,
		"exp":        result.ExpiresAt,
		"iat":        result.IssuedAt,
		"token_type": "Bearer",
	})
	return nil
}

func (h *Handlers) revoke(ctx *web.Context) error {
	if err := ctx.Request().ParseForm(); err != nil {
		return oauthError(ctx, "invalid_request", "invalid form body")
	}
	if err := h.Service.Revoke(ctx.Request().Context(), ctx.Request().PostForm.Get("token")); err != nil {
		return err
	}
	ctx.Gin().Status(http.StatusOK)
	return nil
}

func (h *Handlers) resolveTenant(ctx *web.Context, code string) (int64, error) {
	if h.Tenants != nil {
		resolved, err := h.Tenants.ResolveLogin(ctx.Request().Context(), ctx.Request().Host, code)
		if err != nil {
			return 0, err
		}
		return resolved.ID, nil
	}
	if current, ok := tenant.FromContext(ctx.Request().Context()); ok {
		return current.ID, nil
	}
	if raw := strings.TrimSpace(code); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return id, nil
		}
	}
	return 0, nil
}

func bearerToken(request *http.Request) string {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func clientCredentials(request *http.Request, form map[string][]string) (string, string) {
	clientID := firstFormValue(form, "client_id")
	clientSecret := firstFormValue(form, "client_secret")
	if user, pass, ok := request.BasicAuth(); ok {
		if clientID == "" {
			clientID = user
		}
		if clientSecret == "" {
			clientSecret = pass
		}
	}
	return clientID, clientSecret
}

func firstFormValue(form map[string][]string, key string) string {
	if values := form[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func oauthError(ctx *web.Context, code, description string) error {
	ctx.Gin().JSON(http.StatusBadRequest, map[string]any{
		"error":             code,
		"error_description": strings.TrimSpace(description),
	})
	return nil
}
