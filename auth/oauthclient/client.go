// Package oauthclient implements the OAuth2 authorization-code + PKCE client
// side used for social login.
package oauthclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Provider describes one external identity provider.
type Provider struct {
	Name         string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	Scopes       []string
	RedirectURL  string
	MapUser      func(map[string]any) (UserInfo, error)
}

// UserInfo is the normalized external profile.
type UserInfo struct {
	Subject  string         `json:"subject"`
	Username string         `json:"username"`
	Email    string         `json:"email"`
	Name     string         `json:"name"`
	Raw      map[string]any `json:"raw,omitempty"`
}

// StateStore persists the PKCE verifier for a state value.
type StateStore interface {
	Put(ctx context.Context, state, verifier string, expiresAt time.Time) error
	Take(ctx context.Context, state string) (string, bool, error)
}

// MemoryStateStore is an in-memory StateStore.
type MemoryStateStore struct {
	mu     sync.Mutex
	states map[string]stateEntry
}

type stateEntry struct {
	verifier  string
	expiresAt time.Time
}

// NewMemoryStateStore returns an empty store.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{states: map[string]stateEntry{}}
}

func (s *MemoryStateStore) Put(_ context.Context, state, verifier string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[state] = stateEntry{verifier: verifier, expiresAt: expiresAt}
	return nil
}

func (s *MemoryStateStore) Take(_ context.Context, state string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[state]
	delete(s.states, state)
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false, nil
	}
	return entry.verifier, true, nil
}

// Client performs authorization-code exchanges for configured providers.
type Client struct {
	providers map[string]Provider
	states    StateStore
	http      *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithStateStore replaces the state store.
func WithStateStore(store StateStore) Option {
	return func(c *Client) {
		if store != nil {
			c.states = store
		}
	}
}

// WithHTTPClient replaces the HTTP client used for userinfo requests.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.http = client
		}
	}
}

// New returns a client for the providers.
func New(providers ...Provider) (*Client, error) {
	client := &Client{providers: map[string]Provider{}, states: NewMemoryStateStore(), http: http.DefaultClient}
	for _, provider := range providers {
		if strings.TrimSpace(provider.Name) == "" {
			return nil, fmt.Errorf("oauthclient: provider name is required")
		}
		if provider.AuthURL == "" || provider.TokenURL == "" {
			return nil, fmt.Errorf("oauthclient: provider %s needs auth and token URLs", provider.Name)
		}
		client.providers[provider.Name] = provider
	}
	return client, nil
}

// Providers returns the configured provider names.
func (c *Client) Providers() []string {
	names := make([]string, 0, len(c.providers))
	for name := range c.providers {
		names = append(names, name)
	}
	return names
}

// AuthCodeURL returns the provider authorization URL and the PKCE verifier.
// The caller keeps the verifier out of the browser (the state store already
// holds it) and receives it here for tests or custom transports.
func (c *Client) AuthCodeURL(ctx context.Context, name, state string) (string, string, error) {
	provider, ok := c.providers[name]
	if !ok {
		return "", "", fmt.Errorf("oauthclient: unknown provider %q", name)
	}
	verifier := oauth2.GenerateVerifier()
	if err := c.states.Put(ctx, state, verifier, time.Now().Add(10*time.Minute)); err != nil {
		return "", "", err
	}
	config := c.config(provider)
	return config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), verifier, nil
}

// Exchange swaps the authorization code for a token and loads the user profile.
func (c *Client) Exchange(ctx context.Context, name, state, code string) (*UserInfo, error) {
	provider, ok := c.providers[name]
	if !ok {
		return nil, fmt.Errorf("oauthclient: unknown provider %q", name)
	}
	verifier, ok, err := c.states.Take(ctx, state)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("oauthclient: invalid or expired state")
	}
	config := c.config(provider)
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("oauthclient: exchange: %w", err)
	}
	return c.userInfo(ctx, provider, token)
}

func (c *Client) config(provider Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: provider.AuthURL, TokenURL: provider.TokenURL},
		RedirectURL:  provider.RedirectURL,
		Scopes:       provider.Scopes,
	}
}

func (c *Client) userInfo(ctx context.Context, provider Provider, token *oauth2.Token) (*UserInfo, error) {
	if provider.UserInfoURL == "" {
		return &UserInfo{}, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("oauthclient: userinfo: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("oauthclient: userinfo status %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("oauthclient: decode userinfo: %w", err)
	}
	if provider.MapUser != nil {
		info, err := provider.MapUser(payload)
		if err != nil {
			return nil, err
		}
		return &info, nil
	}
	return DefaultMapUser(payload), nil
}

// DefaultMapUser reads the common subject/username/email/name fields.
func DefaultMapUser(payload map[string]any) *UserInfo {
	info := &UserInfo{Raw: payload}
	for _, key := range []string{"sub", "id", "user_id"} {
		if value, ok := payload[key]; ok {
			info.Subject = fmt.Sprint(value)
			break
		}
	}
	for _, key := range []string{"login", "username", "preferred_username", "nickname"} {
		if value, ok := payload[key]; ok {
			info.Username = fmt.Sprint(value)
			break
		}
	}
	if value, ok := payload["email"]; ok {
		info.Email = fmt.Sprint(value)
	}
	if value, ok := payload["name"]; ok {
		info.Name = fmt.Sprint(value)
	}
	return info
}
