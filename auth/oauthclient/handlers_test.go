package oauthclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/maxid/gable/auth"
	"github.com/maxid/gable/web"
)

func TestSocialLoginHandlers(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-1", "token_type": "Bearer"})
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "42", "login": "alice", "email": "a@example.com"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()

	client, err := New(Provider{
		Name: "github", ClientID: "client", ClientSecret: "secret",
		AuthURL: providerServer.URL + "/authorize", TokenURL: providerServer.URL + "/token",
		UserInfoURL: providerServer.URL + "/userinfo", RedirectURL: "http://app/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokenService(auth.Config{Secret: "jwt-secret"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := NewHandlers(client, tokens, func(_ context.Context, provider string, info UserInfo) (auth.Principal, error) {
		if provider != "github" || info.Subject != "42" {
			t.Fatalf("provider = %s, info = %+v", provider, info)
		}
		return auth.Principal{Username: "alice", TenantID: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	server := web.New(web.WithTitle("social"))
	server.Register(handlers)
	login := httptest.NewRequest(http.MethodGet, "/oauth2/login/github", nil)
	loginRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginRecorder, login)
	if loginRecorder.Code != http.StatusFound {
		t.Fatalf("login = %d %s", loginRecorder.Code, loginRecorder.Body)
	}
	location, err := url.Parse(loginRecorder.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	if state == "" || location.Query().Get("code_challenge") == "" {
		t.Fatalf("location = %s", location)
	}

	callback := httptest.NewRequest(http.MethodGet, "/oauth2/callback/github?state="+state+"&code=code-1", nil)
	callbackRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(callbackRecorder, callback)
	if callbackRecorder.Code != http.StatusOK {
		t.Fatalf("callback = %d %s", callbackRecorder.Code, callbackRecorder.Body)
	}
	var envelope struct {
		Data auth.TokenVO `json:"data"`
	}
	if err := json.Unmarshal(callbackRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.AccessToken == "" {
		t.Fatalf("token = %+v", envelope.Data)
	}
	if _, err := tokens.Verify(context.Background(), envelope.Data.AccessToken); err != nil {
		t.Fatal(err)
	}
}
