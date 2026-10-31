package tenant

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// LoginResolver resolves the tenant before authentication, when no principal
// exists yet. It is separate from Resolver on purpose: login may use the host
// and an explicit tenant code, while authenticated requests use the principal.
type LoginResolver interface {
	ResolveLogin(ctx context.Context, host, tenantCode string) (Tenant, error)
}

// SingleLoginResolver always returns the root tenant.
type SingleLoginResolver struct {
	root Tenant
}

// NewSingleLoginResolver returns the single-tenant login resolver.
func NewSingleLoginResolver(cfg Config) *SingleLoginResolver {
	cfg = cfg.Normalize()
	return &SingleLoginResolver{root: Tenant{ID: cfg.RootID, Code: cfg.RootCode}}
}

// ResolveLogin implements LoginResolver.
func (r *SingleLoginResolver) ResolveLogin(context.Context, string, string) (Tenant, error) {
	return r.root, nil
}

// SaaSLoginResolver resolves the login tenant from the host, then an explicit
// tenant code, then the root tenant. An explicit code that conflicts with the
// host, or an unknown/disabled code, is rejected rather than falling back.
type SaaSLoginResolver struct {
	cfg   Config
	store Store
	root  Tenant
}

// NewSaaSLoginResolver returns the SaaS login resolver.
func NewSaaSLoginResolver(cfg Config, store Store) (*SaaSLoginResolver, error) {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Mode != ModeSaaS {
		return nil, fmt.Errorf("tenant: SaaSLoginResolver requires mode saas")
	}
	if store == nil {
		return nil, fmt.Errorf("tenant: SaaSLoginResolver requires a store")
	}
	return &SaaSLoginResolver{
		cfg:   cfg,
		store: store,
		root:  Tenant{ID: cfg.RootID, Code: cfg.RootCode},
	}, nil
}

// ResolveLogin implements LoginResolver.
func (r *SaaSLoginResolver) ResolveLogin(ctx context.Context, host, tenantCode string) (Tenant, error) {
	hostCode := ResolveHost(host, r.cfg)
	explicit := strings.ToLower(strings.TrimSpace(tenantCode))

	if hostCode != "" && explicit != "" && hostCode != explicit {
		return Tenant{}, ErrMismatch
	}

	code := hostCode
	if code == "" {
		code = explicit
	}

	if code == "" {
		if r.cfg.RootFallback && !(r.cfg.RejectUnknownHost && isDNSHost(host)) {
			return r.root, nil
		}
		return Tenant{}, ErrHostInvalid
	}

	t, err := r.store.ByCode(ctx, code)
	if err != nil {
		return Tenant{}, ErrInvalid
	}
	if t.Disabled {
		return Tenant{}, ErrInvalid
	}
	return t, nil
}

// ResolveHost maps a request host to a tenant code:
//
//  1. an exact host in Config.Exact;
//  2. a single label under Config.BaseDomain;
//  3. the leftmost DNS label of the host.
//
// IP addresses, localhost and empty hosts return "".
func ResolveHost(host string, cfg Config) string {
	cfg = cfg.Normalize()
	host = NormalizeHost(host)
	if host == "" || net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ""
	}
	if code, ok := cfg.Exact[host]; ok {
		return code
	}
	if base := cfg.BaseDomain; base != "" {
		if host == base {
			return ""
		}
		if strings.HasSuffix(host, "."+base) {
			label := strings.TrimSuffix(host, "."+base)
			if label != "" && !strings.Contains(label, ".") {
				return label
			}
		}
	}
	if !strings.Contains(host, ".") {
		return ""
	}
	return strings.Split(host, ".")[0]
}

// NormalizeHost lowercases a host and strips a port.
func NormalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(strings.ToLower(host), ".")
}

func isDNSHost(host string) bool {
	host = NormalizeHost(host)
	if host == "" || net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	return strings.Contains(host, ".")
}
