// Package rpc defines the inter-service RPC abstraction: one typed Client, two
// bindings (in-process and HTTP/JSON), and one wire contract shared by both.
//
// The two bindings exist because a deployment runs either one process or several.
// Business code depends on the Client interface only, so switching between them
// never changes the contract — that is what makes "IAM embedded" and "IAM
// standalone" the same code path.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Client calls one service contract.
type Client interface {
	Call(ctx context.Context, method string, request any, response any) error
}

// Binding selects how a client is implemented.
type Binding string

const (
	BindingLocal  Binding = "local"
	BindingRemote Binding = "http"
)

// Error is a transport-independent RPC failure. Code is stable across bindings;
// Status is only meaningful on the HTTP binding.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

// Error implements error.
func (e *Error) Error() string { return fmt.Sprintf("rpc %s: %s", e.Code, e.Message) }

// WithStatus sets the HTTP status used when the error crosses the wire.
func (e *Error) WithStatus(status int) *Error {
	e.Status = status
	return e
}

// NewError creates an RPC error with a 500 status, which callers override when the
// failure is the caller's fault.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message, Status: http.StatusInternalServerError}
}

// Handler executes one method on the raw request body and returns the raw response
// body. Both bindings share it, so local and remote encoding cannot drift.
type Handler func(ctx context.Context, body []byte) ([]byte, error)

// JSON adapts a typed function to a Handler.
func JSON[Req, Resp any](fn func(context.Context, Req) (Resp, error)) Handler {
	return func(ctx context.Context, body []byte) ([]byte, error) {
		var request Req
		if len(body) > 0 {
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, NewError("bad_request", "decode request: "+err.Error()).WithStatus(http.StatusBadRequest)
			}
		}

		response, err := fn(ctx, request)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return nil, NewError("internal", "encode response: "+err.Error())
		}
		return encoded, nil
	}
}

// Registry holds the methods one service exposes.
//
// Registration happens before the process starts serving; the registry is not meant
// to be mutated while calls are in flight.
type Registry struct {
	mu      sync.RWMutex
	methods map[string]Handler
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{methods: map[string]Handler{}}
}

// Register adds a method. An empty name or a nil handler is a programming error, and
// a duplicate name would silently replace a contract.
func (r *Registry) Register(method string, handler Handler) error {
	if method == "" || handler == nil {
		return fmt.Errorf("%w: method and handler are required", ErrPermanent)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.methods[method]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, method)
	}
	r.methods[method] = handler
	return nil
}

// Methods lists the registered method names, sorted.
func (r *Registry) Methods() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.methods))
	for name := range r.methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// invoke runs one method by name.
func (r *Registry) invoke(ctx context.Context, method string, body []byte) ([]byte, error) {
	r.mu.RLock()
	handler, ok := r.methods[method]
	r.mu.RUnlock()

	if !ok {
		return nil, NewError("not_found", "unknown method "+method).WithStatus(http.StatusNotFound)
	}
	return handler(ctx, body)
}

// LocalClient implements Client against a registry in the same process: no
// serialization shortcut, no special case.
type LocalClient struct {
	reg *Registry
}

// NewLocalClient binds a client to a registry.
func NewLocalClient(reg *Registry) Client {
	return &client{binding: BindingLocal, invoke: func(ctx context.Context, method string, body []byte) ([]byte, error) {
		if reg == nil {
			return nil, ErrNoRegistry
		}
		return reg.invoke(ctx, method, body)
	}}
}

// client is the shared typed wrapper over an invoke function.
type client struct {
	binding Binding
	invoke  func(ctx context.Context, method string, body []byte) ([]byte, error)
}

// Call implements Client.
func (c *client) Call(ctx context.Context, method string, request, response any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("rpc: encode request: %w", err)
	}

	raw, err := c.invoke(ctx, method, body)
	if err != nil {
		return err
	}
	if response == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, response); err != nil {
		return fmt.Errorf("rpc: decode response: %w", err)
	}
	return nil
}

// Binding reports which binding this client uses, for logs and health output.
func (c *client) Binding() Binding { return c.binding }
