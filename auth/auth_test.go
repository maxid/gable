package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

func testService(t *testing.T, format AccessFormat) *TokenService {
	t.Helper()
	hash, err := (BcryptHasher{}).Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	users := NewMemoryUserStore(User{
		ID: "1", Username: "admin", PasswordHash: hash, UserType: "1",
		TenantID: 1, Scopes: []string{"read"}, Roles: []string{"admin"},
	})
	clients := NewMemoryClientStore(Client{
		ID: "console", Secret: "client-secret", Scopes: []string{"read"}, TenantID: 1,
	})
	cfg := Config{Issuer: "test", Secret: "jwt-secret", AccessFormat: format}
	service, err := NewTokenService(cfg, users, clients, NewMemoryTokenStore())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestJWTLoginRefreshAndRevoke(t *testing.T) {
	service := testService(t, AccessJWT)
	ctx := context.Background()

	pair, err := service.Login(ctx, LoginRequest{TenantID: 1, Username: "admin", Password: "secret", ClientID: "console", ClientSecret: "client-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" || pair.ExpiresIn <= 0 {
		t.Fatalf("pair = %+v", pair)
	}
	principal, err := service.Verify(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Username != "admin" || principal.TenantID != 1 || principal.UserType != "1" {
		t.Fatalf("principal = %+v", principal)
	}

	refreshed, err := service.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken == pair.AccessToken || refreshed.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh must rotate both tokens")
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); err != ErrInvalidGrant {
		t.Fatalf("old refresh err = %v", err)
	}

	_ = service.Revoke(ctx, refreshed.AccessToken)
	if _, err := service.Refresh(ctx, refreshed.RefreshToken); err != ErrInvalidGrant {
		t.Fatalf("refresh after revoke = %v", err)
	}
}

func TestOpaqueTokenFormatAndIntrospection(t *testing.T) {
	service := testService(t, AccessOpaque)
	ctx := context.Background()
	pair, err := service.Login(ctx, LoginRequest{TenantID: 1, Username: "admin", Password: "secret", ClientID: "console", ClientSecret: "client-secret"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(pair.AccessToken, ":")
	if len(parts) != 4 || parts[0] != "console" || parts[1] != "admin" || parts[2] != "1" {
		t.Fatalf("opaque token = %q", pair.AccessToken)
	}
	if _, err := service.Verify(ctx, pair.AccessToken); err != nil {
		t.Fatal(err)
	}
	result, err := service.Introspect(ctx, pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Active || result.ClientID != "console" || result.TenantID != 1 {
		t.Fatalf("introspection = %+v", result)
	}
	_ = service.Revoke(ctx, pair.AccessToken)
	result, _ = service.Introspect(ctx, pair.AccessToken)
	if result.Active {
		t.Fatal("revoked opaque token is still active")
	}
}

func TestPasswordSaltAndClientCredentials(t *testing.T) {
	users := NewMemoryUserStore()
	hash, _ := (BcryptHasher{}).Hash("salt:secret")
	_ = users.Add(User{ID: "2", Username: "salt", PasswordHash: hash, Salt: "salt", TenantID: 1, UserType: "1"})
	clients := NewMemoryClientStore(Client{ID: "svc", Secret: "svc-secret", Scopes: []string{"jobs"}, TenantID: 1})
	service, err := NewTokenService(Config{Secret: "jwt", AccessFormat: AccessJWT}, users, clients, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(context.Background(), LoginRequest{TenantID: 1, Username: "salt", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	pair, err := service.ClientCredentials(context.Background(), "svc", "svc-secret")
	if err != nil {
		t.Fatal(err)
	}
	if pair.RefreshToken != "" {
		t.Fatal("client credentials must not issue a refresh token")
	}
	if _, err := service.Verify(context.Background(), pair.AccessToken); err != nil {
		t.Fatal(err)
	}
}

func TestHandlersAndMiddleware(t *testing.T) {
	service := testService(t, AccessJWT)
	handlers := NewHandlers(service, tenant.NewSingleLoginResolver(tenant.DefaultConfig()))

	server := web.New(web.WithTitle("auth"))
	server.Register(handlers)
	server.Use(handlers.Middleware())
	server.Router().GET("/protected", func(ctx *web.Context) error {
		principal, ok := PrincipalFromContext(ctx.Request().Context())
		if !ok {
			return ErrMissingToken
		}
		return ctx.JSON(200, map[string]string{"username": principal.Username})
	}).Doc("protected")

	body := bytes.NewBufferString(`{"username":"admin","password":"secret","clientId":"console","clientSecret":"client-secret","tenantCode":"management"}`)
	request := httptest.NewRequest(http.MethodPost, "/passport/login", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("login = %d %s", recorder.Code, recorder.Body)
	}
	var envelope struct {
		Data TokenVO `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.AccessToken == "" || envelope.Data.TokenType != "Bearer" {
		t.Fatalf("token vo = %+v", envelope.Data)
	}

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", unauthorized.Code)
	}

	protected := httptest.NewRequest(http.MethodGet, "/protected", nil)
	protected.Header.Set("Authorization", "Bearer "+envelope.Data.AccessToken)
	authorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(authorized, protected)
	if authorized.Code != 200 {
		t.Fatalf("protected = %d %s", authorized.Code, authorized.Body)
	}

	tokenRequest := httptest.NewRequest(http.MethodPost, "/oauth2/token", bytes.NewBufferString("grant_type=password&username=admin&password=secret&client_id=console&client_secret=client-secret&tenant_code=management"))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(tokenRecorder, tokenRequest)
	if tokenRecorder.Code != 200 {
		t.Fatalf("oauth token = %d %s", tokenRecorder.Code, tokenRecorder.Body)
	}
	var tokenResponse map[string]any
	if err := json.Unmarshal(tokenRecorder.Body.Bytes(), &tokenResponse); err != nil {
		t.Fatal(err)
	}
	if tokenResponse["access_token"] == "" || tokenResponse["token_type"] != "Bearer" {
		t.Fatalf("token response = %+v", tokenResponse)
	}

	introspect := httptest.NewRequest(http.MethodPost, "/oauth2/introspect", bytes.NewBufferString("token="+tokenResponse["access_token"].(string)))
	introspect.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	introspectRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(introspectRecorder, introspect)
	var introspection map[string]any
	if err := json.Unmarshal(introspectRecorder.Body.Bytes(), &introspection); err != nil {
		t.Fatal(err)
	}
	if introspection["active"] != true {
		t.Fatalf("introspection = %+v", introspection)
	}
}

func TestTenantSource(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{TenantID: 7, Operator: true})
	principal, ok, err := TenantSource().Principal(ctx, nil)
	if err != nil || !ok || principal.TenantID != 7 || !principal.Operator {
		t.Fatalf("tenant source = %+v, %v, %v", principal, ok, err)
	}
}
