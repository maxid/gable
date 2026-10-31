package oauthclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAuthCodeAndExchange(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") == "" {
				t.Errorf("token form = %v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-1", "token_type": "Bearer"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer token-1" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "42", "login": "alice", "email": "a@example.com", "name": "Alice"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(Provider{
		Name:         "github",
		ClientID:     "client",
		ClientSecret: "secret",
		AuthURL:      server.URL + "/authorize",
		TokenURL:     server.URL + "/token",
		UserInfoURL:  server.URL + "/userinfo",
		RedirectURL:  "http://app.example/callback",
		Scopes:       []string{"read:user"},
	})
	if err != nil {
		t.Fatal(err)
	}

	authURL, verifier, err := client.AuthCodeURL(context.Background(), "github", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != "state-1" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("auth url = %s", authURL)
	}
	if verifier == "" {
		t.Fatal("empty verifier")
	}

	info, err := client.Exchange(context.Background(), "github", "state-1", "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Subject != "42" || info.Username != "alice" || info.Email != "a@example.com" || info.Name != "Alice" {
		t.Fatalf("info = %+v", info)
	}

	if _, err := client.Exchange(context.Background(), "github", "state-1", "code-1"); err == nil {
		t.Fatal("state must be single use")
	}
}

func TestUnknownProvider(t *testing.T) {
	client, err := New(Provider{Name: "x", AuthURL: "http://a", TokenURL: "http://t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.AuthCodeURL(context.Background(), "missing", "s"); err == nil {
		t.Fatal("expected unknown provider error")
	}
	if _, err := New(Provider{}); err == nil {
		t.Fatal("expected provider validation error")
	}
}
