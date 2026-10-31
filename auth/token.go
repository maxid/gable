package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenStore persists opaque access tokens and refresh tokens. Implementations
// may use memory, Redis or SQL.
type TokenStore interface {
	PutAccess(ctx context.Context, key string, principal Principal, expiresAt time.Time) error
	GetAccess(ctx context.Context, key string) (Principal, time.Time, bool, error)
	DeleteAccess(ctx context.Context, key string) error
	PutRefresh(ctx context.Context, key string, principal Principal, expiresAt time.Time) error
	GetRefresh(ctx context.Context, key string) (Principal, time.Time, bool, error)
	DeleteRefresh(ctx context.Context, key string) error
	DeleteSession(ctx context.Context, session string) error
}

type tokenEntry struct {
	principal Principal
	expiresAt time.Time
}

// MemoryTokenStore is an in-memory TokenStore.
type MemoryTokenStore struct {
	mu      sync.RWMutex
	access  map[string]tokenEntry
	refresh map[string]tokenEntry
}

// NewMemoryTokenStore returns an empty store.
func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{access: map[string]tokenEntry{}, refresh: map[string]tokenEntry{}}
}

func (s *MemoryTokenStore) PutAccess(_ context.Context, key string, principal Principal, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access[key] = tokenEntry{principal: principal, expiresAt: expiresAt}
	return nil
}

func (s *MemoryTokenStore) GetAccess(_ context.Context, key string) (Principal, time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.access[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return Principal{}, time.Time{}, false, nil
	}
	return entry.principal, entry.expiresAt, true, nil
}

func (s *MemoryTokenStore) DeleteAccess(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.access, key)
	return nil
}

func (s *MemoryTokenStore) PutRefresh(_ context.Context, key string, principal Principal, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh[key] = tokenEntry{principal: principal, expiresAt: expiresAt}
	return nil
}

func (s *MemoryTokenStore) GetRefresh(_ context.Context, key string) (Principal, time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.refresh[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return Principal{}, time.Time{}, false, nil
	}
	return entry.principal, entry.expiresAt, true, nil
}

func (s *MemoryTokenStore) DeleteRefresh(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refresh, key)
	return nil
}

func (s *MemoryTokenStore) DeleteSession(_ context.Context, session string) error {
	if session == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.access {
		if entry.principal.Session == session {
			delete(s.access, key)
		}
	}
	for key, entry := range s.refresh {
		if entry.principal.Session == session {
			delete(s.refresh, key)
		}
	}
	return nil
}

// TokenService issues, verifies and rotates tokens.
type TokenService struct {
	cfg       Config
	users     UserStore
	clients   ClientStore
	passwords PasswordHasher
	store     TokenStore
}

// NewTokenService builds a token service. A nil clients store makes every
// login a public client; a nil token store falls back to memory.
func NewTokenService(cfg Config, users UserStore, clients ClientStore, store TokenStore) (*TokenService, error) {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		store = NewMemoryTokenStore()
	}
	return &TokenService{
		cfg:       cfg,
		users:     users,
		clients:   clients,
		passwords: BcryptHasher{},
		store:     store,
	}, nil
}

// Config returns the normalized configuration.
func (s *TokenService) Config() Config { return s.cfg }

// Issue issues a token pair for an already authenticated principal.
func (s *TokenService) Issue(ctx context.Context, principal Principal) (*TokenPair, error) {
	return s.issue(ctx, principal, true)
}

// Login authenticates a username/password and issues tokens.
func (s *TokenService) Login(ctx context.Context, request LoginRequest) (*TokenPair, error) {
	if s.users == nil {
		return nil, ErrInvalidCredentials
	}
	username := strings.TrimSpace(request.Username)
	if username == "" || request.Password == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.users.FindByUsername(ctx, request.TenantID, username)
	if err != nil || user == nil {
		return nil, ErrInvalidCredentials
	}
	if user.Disabled {
		return nil, ErrUserDisabled
	}

	clientID := strings.TrimSpace(request.ClientID)
	if clientID == "" {
		clientID = user.ClientID
	}
	var client *Client
	if clientID != "" {
		if s.clients == nil {
			return nil, ErrInvalidClient
		}
		client, err = s.clients.FindClient(ctx, clientID)
		if err != nil || client == nil || client.Disabled {
			return nil, ErrInvalidClient
		}
		if client.Secret != "" && !secretEqual(client.Secret, request.ClientSecret) {
			return nil, ErrInvalidClient
		}
	}

	raw := request.Password
	if user.Salt != "" {
		raw = user.Salt + ":" + raw
	}
	if err := s.passwords.Verify(user.PasswordHash, raw); err != nil {
		return nil, ErrInvalidCredentials
	}

	principal := Principal{
		Subject:  user.ID,
		Username: user.Username,
		TenantID: user.TenantID,
		ClientID: clientID,
		UserType: user.UserType,
		Operator: user.Operator,
		Scopes:   user.Scopes,
		Roles:    user.Roles,
	}
	if client != nil {
		if principal.ClientID == "" {
			principal.ClientID = client.ID
		}
		if len(client.Scopes) > 0 {
			principal.Scopes = client.Scopes
		}
	}
	return s.issue(ctx, principal, true)
}

// ClientCredentials issues an access token for a client without a user.
func (s *TokenService) ClientCredentials(ctx context.Context, clientID, clientSecret string) (*TokenPair, error) {
	if s.clients == nil {
		return nil, ErrInvalidClient
	}
	client, err := s.clients.FindClient(ctx, clientID)
	if err != nil || client == nil || client.Disabled {
		return nil, ErrInvalidClient
	}
	if client.Secret != "" && !secretEqual(client.Secret, clientSecret) {
		return nil, ErrInvalidClient
	}
	principal := Principal{
		Subject:  client.ID,
		Username: client.ID,
		TenantID: client.TenantID,
		ClientID: client.ID,
		UserType: "client",
		Scopes:   client.Scopes,
	}
	return s.issue(ctx, principal, false)
}

// Refresh rotates a refresh token and returns a new pair.
func (s *TokenService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	token := strings.TrimSpace(refreshToken)
	if token == "" {
		return nil, ErrInvalidGrant
	}
	key := hashToken(token)
	principal, _, ok, err := s.store.GetRefresh(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidGrant
	}
	_ = s.store.DeleteRefresh(ctx, key)
	return s.issue(ctx, principal, true)
}

// Verify validates an access token.
func (s *TokenService) Verify(ctx context.Context, accessToken string) (Principal, error) {
	token := strings.TrimSpace(accessToken)
	if token == "" {
		return Principal{}, ErrMissingToken
	}
	switch s.cfg.AccessFormat {
	case AccessOpaque:
		principal, expiresAt, ok, err := s.store.GetAccess(ctx, hashToken(token))
		if err != nil {
			return Principal{}, err
		}
		if !ok || time.Now().After(expiresAt) {
			return Principal{}, ErrInvalidToken
		}
		principal.ExpiresAt = expiresAt
		return principal, nil
	default:
		return s.verifyJWT(token)
	}
}

// Introspect implements RFC 7662 semantics: an invalid token is inactive.
func (s *TokenService) Introspect(ctx context.Context, token string) (Introspection, error) {
	principal, err := s.Verify(ctx, token)
	if err != nil {
		return Introspection{Active: false}, nil
	}
	return Introspection{
		Active:    true,
		ClientID:  principal.ClientID,
		Username:  principal.Username,
		TenantID:  principal.TenantID,
		UserType:  principal.UserType,
		Scope:     scopeString(principal.Scopes),
		ExpiresAt: principal.ExpiresAt.Unix(),
		IssuedAt:  principal.IssuedAt.Unix(),
	}, nil
}

// Revoke removes the token's session: the access token when it is opaque and
// the refresh token. JWT access tokens stay valid until they expire; their
// session marker is removed so refresh cannot continue.
func (s *TokenService) Revoke(ctx context.Context, token string) error {
	key := hashToken(strings.TrimSpace(token))
	if principal, _, ok, _ := s.store.GetAccess(ctx, key); ok && principal.Session != "" {
		return s.store.DeleteSession(ctx, principal.Session)
	}
	if principal, _, ok, _ := s.store.GetRefresh(ctx, key); ok && principal.Session != "" {
		return s.store.DeleteSession(ctx, principal.Session)
	}
	_ = s.store.DeleteAccess(ctx, key)
	_ = s.store.DeleteRefresh(ctx, key)
	return nil
}

func (s *TokenService) issue(ctx context.Context, principal Principal, withRefresh bool) (*TokenPair, error) {
	now := time.Now()
	principal.IssuedAt = now
	principal.ExpiresAt = now.Add(s.cfg.AccessTTL)
	if principal.Session == "" {
		principal.Session = randomUUID()
	}

	access, err := s.accessToken(ctx, principal)
	if err != nil {
		return nil, err
	}

	// Store a marker even for JWT so logout can revoke the refresh token.
	if s.cfg.AccessFormat == AccessJWT {
		_ = s.store.PutAccess(ctx, hashToken(access), principal, principal.ExpiresAt)
	}
	pair := &TokenPair{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.cfg.AccessTTL.Seconds()),
		Scope:       scopeString(principal.Scopes),
		GlobalKey:   s.cfg.GlobalKey,
	}
	if withRefresh {
		refresh := randomToken()
		if err := s.store.PutRefresh(ctx, hashToken(refresh), principal, now.Add(s.cfg.RefreshTTL)); err != nil {
			return nil, err
		}
		pair.RefreshToken = refresh
	}
	return pair, nil
}

func (s *TokenService) accessToken(ctx context.Context, principal Principal) (string, error) {
	if s.cfg.AccessFormat == AccessOpaque {
		clientID := principal.ClientID
		if clientID == "" {
			clientID = "console"
		}
		userType := principal.UserType
		if userType == "" {
			userType = "0"
		}
		token := fmt.Sprintf(s.cfg.OpaqueFormat, clientID, principal.Username, userType, randomUUID())
		if err := s.store.PutAccess(ctx, hashToken(token), principal, principal.ExpiresAt); err != nil {
			return "", err
		}
		return token, nil
	}

	claims := jwtClaims{
		Username: principal.Username,
		ClientID: principal.ClientID,
		TenantID: principal.TenantID,
		UserType: principal.UserType,
		Scopes:   principal.Scopes,
		Roles:    principal.Roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   principal.Username,
			ID:        randomUUID(),
			IssuedAt:  jwt.NewNumericDate(principal.IssuedAt),
			ExpiresAt: jwt.NewNumericDate(principal.ExpiresAt),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.Secret))
}

type jwtClaims struct {
	Username string   `json:"username,omitempty"`
	ClientID string   `json:"client_id,omitempty"`
	TenantID int64    `json:"tenant_id,omitempty"`
	UserType string   `json:"user_type,omitempty"`
	Scopes   []string `json:"scope,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

func (s *TokenService) verifyJWT(raw string) (Principal, error) {
	claims := &jwtClaims{}
	options := []jwt.ParserOption{jwt.WithValidMethods([]string{"HS256"})}
	if s.cfg.Issuer != "" {
		options = append(options, jwt.WithIssuer(s.cfg.Issuer))
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		return []byte(s.cfg.Secret), nil
	}, options...)
	if err != nil || !token.Valid {
		return Principal{}, ErrInvalidToken
	}
	principal := Principal{
		Username: claims.Username,
		ClientID: claims.ClientID,
		TenantID: claims.TenantID,
		UserType: claims.UserType,
		Scopes:   claims.Scopes,
		Roles:    claims.Roles,
	}
	if claims.Subject != "" {
		principal.Username = claims.Subject
	}
	if claims.IssuedAt != nil {
		principal.IssuedAt = claims.IssuedAt.Time
	}
	if claims.ExpiresAt != nil {
		principal.ExpiresAt = claims.ExpiresAt.Time
	}
	return principal, nil
}

func secretEqual(want, got string) bool {
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func randomUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
