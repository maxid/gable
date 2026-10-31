package tenant

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func normalStore() *MemoryStore {
	return NewMemoryStore(
		Tenant{ID: 1, Code: "management"},
		Tenant{ID: 2, Code: "acme"},
		Tenant{ID: 3, Code: "beta"},
		Tenant{ID: 9, Code: "off", Disabled: true},
	)
}

func saasConfig() Config {
	return Config{
		Mode:              ModeSaaS,
		RootID:            1,
		RootCode:          "management",
		BaseDomain:        "example.com",
		Exact:             map[string]string{"exact.test": "beta"},
		Header:            "X-Tenant-Id",
		RootFallback:      true,
		RejectUnknownHost: true,
	}
}

func TestSingleResolverAndLogin(t *testing.T) {
	cfg := DefaultConfig()
	resolver := NewSingleResolver(cfg)
	got, err := resolver.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 || got.Code != "management" {
		t.Fatalf("tenant = %+v", got)
	}

	login, err := NewSingleLoginResolver(cfg).ResolveLogin(context.Background(), "any.example.com", "other")
	if err != nil {
		t.Fatal(err)
	}
	if login.ID != 1 {
		t.Fatalf("login tenant = %+v", login)
	}
}

func TestSaaSLoginResolution(t *testing.T) {
	store := normalStore()
	resolver, err := NewSaaSLoginResolver(saasConfig(), store)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		host     string
		code     string
		wantCode string
		wantErr  error
	}{
		{"exact host", "exact.test", "", "beta", nil},
		{"subdomain", "acme.example.com", "", "acme", nil},
		{"subdomain with port", "acme.example.com:8080", "", "acme", nil},
		{"leftmost label", "beta.other.test", "", "beta", nil},
		{"explicit code", "127.0.0.1", "acme", "acme", nil},
		{"empty host root fallback", "", "", "management", nil},
		{"localhost root fallback", "localhost", "", "management", nil},
		{"mismatch", "acme.example.com", "beta", "", ErrMismatch},
		{"unknown explicit", "", "nope", "", ErrInvalid},
		{"disabled", "off.example.com", "", "", ErrInvalid},
		{"base domain is invalid", "example.com", "", "", ErrHostInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolver.ResolveLogin(context.Background(), tc.host, tc.code)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", got.Code, tc.wantCode)
			}
		})
	}
}

func TestSaaSLoginRootFallbackDisabled(t *testing.T) {
	cfg := saasConfig()
	cfg.RootFallback = false
	resolver, err := NewSaaSLoginResolver(cfg, normalStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveLogin(context.Background(), "localhost", ""); !errors.Is(err, ErrHostInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaaSResolver(t *testing.T) {
	cfg := saasConfig()
	source := SourceFunc(func(_ context.Context, r *http.Request) (Principal, bool, error) {
		if r.Header.Get("X-Principal") == "" {
			return Principal{}, false, nil
		}
		return Principal{TenantID: 2, Operator: r.Header.Get("X-Operator") == "1"}, true, nil
	})
	resolver, err := NewSaaSResolver(cfg, normalStore(), source)
	if err != nil {
		t.Fatal(err)
	}

	request := func(principal, operator, target string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Principal", principal)
		r.Header.Set("X-Operator", operator)
		r.Header.Set("X-Tenant-Id", target)
		return r
	}

	got, err := resolver.Resolve(context.Background(), request("1", "0", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "acme" {
		t.Fatalf("tenant = %+v", got)
	}

	got, err = resolver.Resolve(context.Background(), request("1", "1", "3"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "beta" {
		t.Fatalf("impersonated = %+v", got)
	}

	if _, err := resolver.Resolve(context.Background(), request("1", "0", "3")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-operator impersonation err = %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), request("1", "1", "9")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled target err = %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), request("", "0", "")); !errors.Is(err, ErrRequired) {
		t.Fatalf("missing principal err = %v", err)
	}
}

func TestContextAndMemoryStore(t *testing.T) {
	store := normalStore()
	tenant, err := store.ByCode(context.Background(), "ACME")
	if err != nil {
		t.Fatal(err)
	}
	if tenant.ID != 2 {
		t.Fatalf("tenant = %+v", tenant)
	}
	if _, err := store.ByID(context.Background(), 404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id err = %v", err)
	}

	ctx := WithTenant(context.Background(), tenant)
	got, ok := FromContext(ctx)
	if !ok || got.Code != "acme" {
		t.Fatalf("from context = %+v, %v", got, ok)
	}
	if _, err := MustFromContext(context.Background()); !errors.Is(err, ErrRequired) {
		t.Fatalf("missing context err = %v", err)
	}
}

func TestResolveHost(t *testing.T) {
	cfg := saasConfig()
	cases := map[string]string{
		"exact.test":           "beta",
		"acme.example.com":     "acme",
		"acme.example.com:443": "acme",
		"example.com":          "",
		"127.0.0.1":            "",
		"localhost":            "",
		"beta.other.test":      "beta",
		"unrelated":            "",
	}
	for host, want := range cases {
		if got := ResolveHost(host, cfg); got != want {
			t.Fatalf("ResolveHost(%q) = %q, want %q", host, got, want)
		}
	}
}
