// Package auth provides password login, JWT/opaque access tokens, rotating
// opaque refresh tokens, bearer middleware and OAuth2-compatible endpoints.
//
// Tokens are pluggable: JWT access tokens for stateless services and opaque
// access tokens for clients that need introspection or a legacy token format.
package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	gableerrors "github.com/maxid/gable/errors"
)

// AccessFormat selects the access-token representation.
type AccessFormat string

const (
	AccessJWT    AccessFormat = "jwt"
	AccessOpaque AccessFormat = "opaque"
)

// Config configures the token service.
type Config struct {
	Issuer       string        `yaml:"issuer"`
	Secret       string        `yaml:"secret"`
	AccessFormat AccessFormat  `yaml:"accessFormat"`
	AccessTTL    time.Duration `yaml:"accessTTL"`
	RefreshTTL   time.Duration `yaml:"refreshTTL"`
	OpaqueFormat string        `yaml:"opaqueFormat"` // four %s: client, user, type, uuid
	GlobalKey    string        `yaml:"globalKey"`
}

// DefaultConfig returns development defaults.
func DefaultConfig() Config {
	return Config{
		Issuer:       "gable",
		AccessFormat: AccessJWT,
		AccessTTL:    2 * time.Hour,
		RefreshTTL:   7 * 24 * time.Hour,
		OpaqueFormat: "%s:%s:%s:%s",
	}
}

// Normalize fills defaults.
func (c Config) Normalize() Config {
	if c.Issuer == "" {
		c.Issuer = "gable"
	}
	if c.AccessFormat == "" {
		c.AccessFormat = AccessJWT
	}
	if c.AccessTTL <= 0 {
		c.AccessTTL = 2 * time.Hour
	}
	if c.RefreshTTL <= 0 {
		c.RefreshTTL = 7 * 24 * time.Hour
	}
	if c.OpaqueFormat == "" {
		c.OpaqueFormat = "%s:%s:%s:%s"
	}
	return c
}

// Validate checks the configuration.
func (c Config) Validate() error {
	c = c.Normalize()
	switch c.AccessFormat {
	case AccessJWT:
		if strings.TrimSpace(c.Secret) == "" {
			return fmt.Errorf("auth: secret is required for JWT access tokens")
		}
	case AccessOpaque:
		if strings.Count(c.OpaqueFormat, "%s") != 4 {
			return fmt.Errorf("auth: opaqueFormat must contain four %%s verbs")
		}
	default:
		return fmt.Errorf("auth: unsupported access format %q", c.AccessFormat)
	}
	return nil
}

// User is the login projection.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Salt         string
	UserType     string
	ClientID     string
	TenantID     int64
	Disabled     bool
	Operator     bool
	Scopes       []string
	Roles        []string
}

// Client is an OAuth2 client.
type Client struct {
	ID       string
	Secret   string
	Name     string
	Scopes   []string
	TenantID int64
	Disabled bool
}

// UserStore loads users by tenant and username.
type UserStore interface {
	FindByUsername(ctx context.Context, tenantID int64, username string) (*User, error)
}

// ClientStore loads OAuth2 clients.
type ClientStore interface {
	FindClient(ctx context.Context, clientID string) (*Client, error)
}

// Principal is the authenticated identity.
type Principal struct {
	Subject   string
	Session   string
	Username  string
	TenantID  int64
	ClientID  string
	UserType  string
	Operator  bool
	Scopes    []string
	Roles     []string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// TokenPair is the result of a login or refresh.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	Scope        string
	GlobalKey    string
}

// LoginRequest is a password login.
type LoginRequest struct {
	TenantID     int64
	Username     string
	Password     string
	ClientID     string
	ClientSecret string
	GrantType    string
}

// Introspection is the OAuth2 introspection result.
type Introspection struct {
	Active    bool
	ClientID  string
	Username  string
	TenantID  int64
	UserType  string
	Scope     string
	ExpiresAt int64
	IssuedAt  int64
}

// Framework errors.
var (
	ErrInvalidCredentials = authError("invalid_credentials", "invalid username or password", 401)
	ErrUserDisabled       = authError("user_disabled", "user is disabled", 403)
	ErrInvalidClient      = authError("invalid_client", "invalid client", 401)
	ErrInvalidToken       = authError("invalid_token", "invalid or expired token", 401)
	ErrInvalidGrant       = authError("invalid_grant", "invalid grant", 400)
	ErrMissingToken       = authError("token_required", "access token is required", 401)
)

func authError(code, message string, status int) *gableerrors.Error {
	return gableerrors.New(code, message).WithStatus(status)
}

type principalKey struct{}

// WithPrincipal stores the authenticated principal in ctx.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFromContext returns the principal.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

func scopeString(scopes []string) string { return strings.Join(scopes, " ") }
