// Package tenant resolves and carries the tenant of a request.
//
// Two modes are supported. In single mode every request runs as the root
// tenant. In saas mode the tenant comes from a request source (a session or
// token) and an operator may impersonate another tenant with a header. Tenant
// lookup is fail-closed: a missing, unknown or disabled tenant is an error,
// never a silent fallback to the root tenant.
package tenant

import (
	"context"
	"fmt"
	"strings"
	"sync"

	gableerrors "github.com/maxid/gable/errors"
)

// Mode is the tenant assembly mode.
type Mode string

const (
	ModeSingle Mode = "single"
	ModeSaaS   Mode = "saas"
)

// Root defaults match the platform's private-deployment tenant.
const (
	DefaultRootID   int64 = 1
	DefaultRootCode       = "management"
	DefaultHeader         = "X-Tenant-Id"
)

// Config configures tenant resolution.
type Config struct {
	Mode              Mode              `yaml:"mode"`                // single | saas
	RootID            int64             `yaml:"root_id"`             // default 1
	RootCode          string            `yaml:"root_code"`           // default management
	BaseDomain        string            `yaml:"base_domain"`         // saas: acme.example.com -> acme
	Exact             map[string]string `yaml:"exact"`               // saas: full host -> tenant code
	Header            string            `yaml:"header"`              // default X-Tenant-Id
	RootFallback      bool              `yaml:"root_fallback"`       // login only; default true
	RejectUnknownHost bool              `yaml:"reject_unknown_host"` // saas login: DNS host without tenant is invalid
}

// DefaultConfig returns the single-tenant defaults.
func DefaultConfig() Config {
	return Config{
		Mode:              ModeSingle,
		RootID:            DefaultRootID,
		RootCode:          DefaultRootCode,
		Header:            DefaultHeader,
		RootFallback:      true,
		RejectUnknownHost: true,
	}
}

// Normalize fills defaults without validating.
func (c Config) Normalize() Config {
	if c.Mode == "" {
		c.Mode = ModeSingle
	}
	if c.RootID == 0 {
		c.RootID = DefaultRootID
	}
	if strings.TrimSpace(c.RootCode) == "" {
		c.RootCode = DefaultRootCode
	}
	c.RootCode = strings.ToLower(strings.TrimSpace(c.RootCode))
	if strings.TrimSpace(c.Header) == "" {
		c.Header = DefaultHeader
	}
	c.BaseDomain = strings.ToLower(strings.Trim(strings.TrimSpace(c.BaseDomain), "."))
	if c.Exact != nil {
		normalized := make(map[string]string, len(c.Exact))
		for host, code := range c.Exact {
			normalized[strings.ToLower(strings.TrimSpace(host))] = strings.ToLower(strings.TrimSpace(code))
		}
		c.Exact = normalized
	}
	return c
}

// Validate checks the values resolution depends on.
func (c Config) Validate() error {
	c = c.Normalize()
	switch c.Mode {
	case ModeSingle, ModeSaaS:
	default:
		return fmt.Errorf("tenant: mode %q is not single or saas", c.Mode)
	}
	if c.RootID <= 0 {
		return fmt.Errorf("tenant: root_id must be positive")
	}
	if c.RootCode == "" {
		return fmt.Errorf("tenant: root_code is required")
	}
	return nil
}

// Tenant is the resolved tenant of a request.
type Tenant struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Disabled bool   `json:"disabled,omitempty"`
}

// IsZero reports whether the tenant was not set.
func (t Tenant) IsZero() bool { return t.ID == 0 && t.Code == "" }

// String renders the tenant for logs.
func (t Tenant) String() string { return fmt.Sprintf("%s(%d)", t.Code, t.ID) }

// Framework errors. The web layer maps them through the envelope.
var (
	ErrRequired    = gableerrors.New("tenant_required", "tenant context is required").WithStatus(400)
	ErrInvalid     = gableerrors.New("tenant_invalid", "tenant is invalid or disabled").WithStatus(400)
	ErrForbidden   = gableerrors.New("tenant_forbidden", "tenant impersonation is not allowed").WithStatus(403)
	ErrHostInvalid = gableerrors.New("tenant_host_invalid", "host does not map to a tenant").WithStatus(400)
	ErrMismatch    = gableerrors.New("tenant_mismatch", "tenant code does not match the host").WithStatus(400)
	ErrNotFound    = gableerrors.New("tenant_not_found", "tenant not found").WithStatus(404)
)

type contextKey struct{}

// WithTenant stores the tenant in ctx.
func WithTenant(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, contextKey{}, t)
}

// FromContext returns the tenant stored in ctx.
func FromContext(ctx context.Context) (Tenant, bool) {
	if ctx == nil {
		return Tenant{}, false
	}
	t, ok := ctx.Value(contextKey{}).(Tenant)
	if !ok || t.IsZero() {
		return Tenant{}, false
	}
	return t, true
}

// MustFromContext returns the tenant or ErrRequired.
func MustFromContext(ctx context.Context) (Tenant, error) {
	t, ok := FromContext(ctx)
	if !ok {
		return Tenant{}, ErrRequired
	}
	return t, nil
}

// Store looks tenants up by code and id. Implementations decide where the
// tenant directory lives; the framework only needs the lookup.
type Store interface {
	ByCode(ctx context.Context, code string) (Tenant, error)
	ByID(ctx context.Context, id int64) (Tenant, error)
}

// MemoryStore is an in-memory Store for tests and small deployments.
type MemoryStore struct {
	mu     sync.RWMutex
	byCode map[string]Tenant
	byID   map[int64]Tenant
}

// NewMemoryStore returns a store seeded with tenants.
func NewMemoryStore(tenants ...Tenant) *MemoryStore {
	store := &MemoryStore{byCode: map[string]Tenant{}, byID: map[int64]Tenant{}}
	for _, t := range tenants {
		_ = store.Add(t)
	}
	return store
}

// Add inserts or replaces a tenant.
func (s *MemoryStore) Add(t Tenant) error {
	if t.ID <= 0 || strings.TrimSpace(t.Code) == "" {
		return fmt.Errorf("tenant: tenant id and code are required")
	}
	t.Code = strings.ToLower(strings.TrimSpace(t.Code))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byCode[t.Code] = t
	s.byID[t.ID] = t
	return nil
}

// ByCode implements Store.
func (s *MemoryStore) ByCode(_ context.Context, code string) (Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byCode[strings.ToLower(strings.TrimSpace(code))]
	if !ok {
		return Tenant{}, ErrNotFound
	}
	return t, nil
}

// ByID implements Store.
func (s *MemoryStore) ByID(_ context.Context, id int64) (Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byID[id]
	if !ok {
		return Tenant{}, ErrNotFound
	}
	return t, nil
}
