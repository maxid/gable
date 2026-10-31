package rpc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maxid/gable/rpc"
)

type EchoRequest struct {
	Message string `json:"message"`
}

type EchoResponse struct {
	Reply string `json:"reply"`
}

func registry(t *testing.T) *rpc.Registry {
	t.Helper()
	reg := rpc.NewRegistry()
	if err := reg.Register("demo.echo", rpc.JSON(func(_ context.Context, req EchoRequest) (EchoResponse, error) {
		if req.Message == "boom" {
			return EchoResponse{}, rpc.NewError("invalid_argument", "refused").WithStatus(http.StatusBadRequest)
		}
		return EchoResponse{Reply: "echo:" + req.Message}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	return reg
}

// Both bindings must produce the same answer and the same error, because the whole
// point is that local and remote are the same contract.
func TestBindingsAgree(t *testing.T) {
	reg := registry(t)
	server := httptest.NewServer(rpc.NewServer(reg).Handler())
	defer server.Close()

	local := rpc.NewLocalClient(reg)
	remote := rpc.NewHTTPClient(server.URL)

	for name, client := range map[string]rpc.Client{"local": local, "remote": remote} {
		var response EchoResponse
		if err := client.Call(context.Background(), "demo.echo", EchoRequest{Message: "hi"}, &response); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if response.Reply != "echo:hi" {
			t.Errorf("%s: reply = %q", name, response.Reply)
		}

		err := client.Call(context.Background(), "demo.echo", EchoRequest{Message: "boom"}, &response)
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != "invalid_argument" {
			t.Errorf("%s: err = %v, want invalid_argument", name, err)
		}
	}
}

func TestSharedKeyIsEnforced(t *testing.T) {
	reg := registry(t)
	server := httptest.NewServer(rpc.NewServer(reg, rpc.WithKey("s3cret")).Handler())
	defer server.Close()

	// No key: rejected.
	var response EchoResponse
	err := rpc.NewHTTPClient(server.URL).Call(context.Background(), "demo.echo", EchoRequest{Message: "hi"}, &response)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401", err)
	}

	// Wrong key: rejected.
	err = rpc.NewHTTPClient(server.URL, rpc.WithAPIKey("nope")).Call(context.Background(), "demo.echo", EchoRequest{Message: "hi"}, &response)
	if !errors.As(err, &rpcErr) || rpcErr.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401", err)
	}

	// Correct key: accepted.
	if err := rpc.NewHTTPClient(server.URL, rpc.WithAPIKey("s3cret")).Call(
		context.Background(), "demo.echo", EchoRequest{Message: "hi"}, &response); err != nil {
		t.Fatalf("authorized call: %v", err)
	}
}

// A resolver is how the same binary reaches a service in a cluster.
func TestResolverDrivesRouting(t *testing.T) {
	reg := registry(t)
	server := httptest.NewServer(rpc.NewServer(reg).Handler())
	defer server.Close()

	client := rpc.NewServiceClient("iam", stubResolver{base: server.URL})

	var response EchoResponse
	if err := client.Call(context.Background(), "demo.echo", EchoRequest{Message: "hi"}, &response); err != nil {
		t.Fatalf("call: %v", err)
	}
	if response.Reply != "echo:hi" {
		t.Fatalf("reply = %q", response.Reply)
	}
}

type stubResolver struct {
	base string
	err  error
}

func (s stubResolver) Resolve(context.Context, string) (string, error) { return s.base, s.err }

func TestResolverFailureIsReported(t *testing.T) {
	client := rpc.NewServiceClient("iam", stubResolver{err: rpc.ErrNoEndpoint})

	var response EchoResponse
	if err := client.Call(context.Background(), "demo.echo", EchoRequest{}, &response); !errors.Is(err, rpc.ErrNoEndpoint) {
		t.Fatalf("err = %v, want ErrNoEndpoint", err)
	}
}

func TestUnknownMethodIsNotFound(t *testing.T) {
	server := httptest.NewServer(rpc.NewServer(registry(t)).Handler())
	defer server.Close()

	var response EchoResponse
	err := rpc.NewHTTPClient(server.URL).Call(context.Background(), "demo.missing", EchoRequest{}, &response)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want 404", err)
	}
}

func TestRegistryRules(t *testing.T) {
	reg := rpc.NewRegistry()
	handler := rpc.JSON(func(context.Context, EchoRequest) (EchoResponse, error) { return EchoResponse{}, nil })

	if err := reg.Register("", handler); !errors.Is(err, rpc.ErrPermanent) {
		t.Fatalf("empty name err = %v", err)
	}
	if err := reg.Register("demo", nil); !errors.Is(err, rpc.ErrPermanent) {
		t.Fatalf("nil handler err = %v", err)
	}
	if err := reg.Register("demo", handler); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := reg.Register("demo", handler); !errors.Is(err, rpc.ErrAlreadyRegistered) {
		t.Fatalf("duplicate err = %v", err)
	}
	if names := reg.Methods(); len(names) != 1 || names[0] != "demo" {
		t.Fatalf("methods = %v", names)
	}
}

func TestStaticResolverTrimsAndRejectsEmpty(t *testing.T) {
	base, err := rpc.StaticResolver{Base: " http://127.0.0.1:8080/ "}.Resolve(context.Background(), "any")
	if err != nil || base != "http://127.0.0.1:8080" {
		t.Fatalf("base = %q, %v", base, err)
	}
	if _, err := (rpc.StaticResolver{}).Resolve(context.Background(), "any"); !errors.Is(err, rpc.ErrNoEndpoint) {
		t.Fatalf("empty base err = %v", err)
	}
}
