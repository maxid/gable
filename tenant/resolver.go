package tenant

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	gableerrors "github.com/maxid/gable/errors"
)

// Resolver resolves the tenant of an authenticated request.
type Resolver interface {
	Resolve(context.Context, *http.Request) (Tenant, error)
}

// Principal is the authenticated caller as the tenant layer needs it.
type Principal struct {
	TenantID int64
	Operator bool // may impersonate another tenant with the tenant header
}

// Source supplies the authenticated principal. The application's auth
// middleware implements it (for example, by reading a token).
type Source interface {
	Principal(context.Context, *http.Request) (Principal, bool, error)
}

// SourceFunc adapts a function to Source.
type SourceFunc func(context.Context, *http.Request) (Principal, bool, error)

// Principal implements Source.
func (f SourceFunc) Principal(ctx context.Context, r *http.Request) (Principal, bool, error) {
	return f(ctx, r)
}

// SingleResolver always resolves the root tenant.
type SingleResolver struct {
	root Tenant
}

// NewSingleResolver returns the single-tenant resolver.
func NewSingleResolver(cfg Config) *SingleResolver {
	cfg = cfg.Normalize()
	return &SingleResolver{root: Tenant{ID: cfg.RootID, Code: cfg.RootCode}}
}

// Resolve implements Resolver.
func (r *SingleResolver) Resolve(context.Context, *http.Request) (Tenant, error) {
	return r.root, nil
}

// SaaSResolver resolves the tenant from the principal and the impersonation
// header. A header on a non-operator principal is forbidden; impersonation is
// validated against the store and a disabled target is rejected.
type SaaSResolver struct {
	cfg    Config
	store  Store
	source Source
}

// NewSaaSResolver returns a SaaS resolver.
func NewSaaSResolver(cfg Config, store Store, source Source) (*SaaSResolver, error) {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Mode != ModeSaaS {
		return nil, errors.New("tenant: SaaSResolver requires mode saas")
	}
	if store == nil {
		return nil, errors.New("tenant: SaaSResolver requires a store")
	}
	if source == nil {
		return nil, errors.New("tenant: SaaSResolver requires a principal source")
	}
	return &SaaSResolver{cfg: cfg, store: store, source: source}, nil
}

// Resolve implements Resolver.
func (r *SaaSResolver) Resolve(ctx context.Context, request *http.Request) (Tenant, error) {
	principal, ok, err := r.source.Principal(ctx, request)
	if err != nil {
		return Tenant{}, err
	}
	if !ok || principal.TenantID <= 0 {
		return Tenant{}, ErrRequired
	}

	target := principal.TenantID
	if raw := strings.TrimSpace(request.Header.Get(r.cfg.Header)); raw != "" {
		if !principal.Operator {
			return Tenant{}, ErrForbidden
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return Tenant{}, ErrInvalid
		}
		if id != principal.TenantID {
			target = id
		}
	}

	t, err := r.store.ByID(ctx, target)
	if err != nil {
		if gableerrors.IsCode(err, ErrNotFound.Code) {
			return Tenant{}, ErrInvalid
		}
		return Tenant{}, err
	}
	if t.Disabled {
		return Tenant{}, ErrInvalid
	}
	return t, nil
}
