package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

func rsaTestKey(t *testing.T) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block)), &key.PublicKey
}

func oidcServer(t *testing.T) (*web.Server, *TokenService, *rsa.PublicKey) {
	t.Helper()
	pemText, public := rsaTestKey(t)
	hash, _ := (BcryptHasher{}).Hash("secret")
	users := NewMemoryUserStore(User{
		ID: "1", Username: "admin", PasswordHash: hash, UserType: "1",
		TenantID: 1, Email: "admin@example.com", Name: "Admin",
	})
	clients := NewMemoryClientStore(Client{
		ID: "web", Secret: "web-secret", Scopes: []string{"openid", "read"},
		RedirectURIs: []string{"http://app.example/callback"}, TenantID: 1,
	})
	service, err := NewTokenService(Config{
		Issuer: "http://auth.example", Secret: "jwt-secret",
		RSAKeyPEM: pemText, KeyID: "test-key",
	}, users, clients, nil)
	if err != nil {
		t.Fatal(err)
	}
	handlers := NewHandlers(service, tenant.NewSingleLoginResolver(tenant.DefaultConfig()))
	handlers.ConfigureAuthorizationServer(AuthorizationServerOptions{Authorizer: AllowAllAuthorizer{}})
	server := web.New(web.WithTitle("oidc"))
	server.Register(handlers)
	return server, service, public
}

func TestOIDCAuthorizationCodeAndJWKS(t *testing.T) {
	server, service, public := oidcServer(t)
	login, err := service.Login(context.Background(), LoginRequest{TenantID: 1, Username: "admin", Password: "secret", ClientID: "web", ClientSecret: "web-secret"})
	if err != nil {
		t.Fatal(err)
	}
	verifier, challenge := pkcePair(t)

	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {"web"},
		"redirect_uri":          {"http://app.example/callback"},
		"scope":                 {"openid read"},
		"state":                 {"state-1"},
		"nonce":                 {"nonce-1"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	request := httptest.NewRequest(http.MethodGet, "/oauth2/authorize?"+values.Encode(), nil)
	request.Header.Set("Authorization", "Bearer "+login.AccessToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusFound {
		t.Fatalf("authorize = %d %s", recorder.Code, recorder.Body)
	}
	location, _ := url.Parse(recorder.Header().Get("Location"))
	code := location.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://app.example/callback"},
		"client_id":     {"web"},
		"client_secret": {"web-secret"},
		"code_verifier": {verifier},
	}
	tokenRequest := httptest.NewRequest(http.MethodPost, "/oauth2/token", strings.NewReader(form.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(tokenRecorder, tokenRequest)
	if tokenRecorder.Code != 200 {
		t.Fatalf("token = %d %s", tokenRecorder.Code, tokenRecorder.Body)
	}
	var response map[string]any
	if err := json.Unmarshal(tokenRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	idToken, _ := response["id_token"].(string)
	if idToken == "" {
		t.Fatalf("response = %+v", response)
	}

	claims := &idTokenClaims{}
	parsed, err := jwt.ParseWithClaims(idToken, claims, func(token *jwt.Token) (any, error) {
		return public, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience("web"), jwt.WithIssuer("http://auth.example"))
	if err != nil || !parsed.Valid {
		t.Fatalf("id_token = %v", err)
	}
	if claims.Nonce != "nonce-1" || claims.Username != "admin" || claims.Email != "admin@example.com" {
		t.Fatalf("claims = %+v", claims)
	}

	jwksRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(jwksRecorder, httptest.NewRequest(http.MethodGet, "/oauth2/jwks", nil))
	if jwksRecorder.Code != 200 {
		t.Fatalf("jwks = %d", jwksRecorder.Code)
	}
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(jwksRecorder.Body.Bytes(), &jwks); err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 1 || jwks.Keys[0]["kid"] != "test-key" || jwks.Keys[0]["alg"] != "RS256" {
		t.Fatalf("jwks = %+v", jwks)
	}

	userInfo := httptest.NewRequest(http.MethodGet, "/oauth2/userinfo", nil)
	userInfo.Header.Set("Authorization", "Bearer "+response["access_token"].(string))
	userRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(userRecorder, userInfo)
	if userRecorder.Code != 200 {
		t.Fatalf("userinfo = %d %s", userRecorder.Code, userRecorder.Body)
	}
	var info map[string]any
	if err := json.Unmarshal(userRecorder.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["preferred_username"] != "admin" || info["email"] != "admin@example.com" {
		t.Fatalf("userinfo = %+v", info)
	}
}
