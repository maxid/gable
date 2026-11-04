package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Resolver turns a service name into a base URL. Standalone deployments use a fixed
// endpoint; cluster deployments resolve through the registry.
type Resolver interface {
	Resolve(ctx context.Context, service string) (baseURL string, err error)
}

// StaticResolver always returns one endpoint.
type StaticResolver struct {
	Base string
}

// Resolve implements Resolver.
func (s StaticResolver) Resolve(_ context.Context, _ string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(s.Base), "/")
	if base == "" {
		return "", fmt.Errorf("%w: empty static endpoint", ErrNoEndpoint)
	}
	return base, nil
}

// HTTPClient is the remote binding.
type HTTPClient struct {
	base     string
	service  string
	resolver Resolver
	key      string
	hc       *http.Client
}

// ClientOption configures an HTTP client.
type ClientOption func(*HTTPClient)

// WithHTTPClient sets the underlying client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *HTTPClient) {
		if hc != nil {
			c.hc = hc
		}
	}
}

// WithAPIKey sets the shared key sent with every call.
func WithAPIKey(key string) ClientOption { return func(c *HTTPClient) { c.key = key } }

// WithResolver resolves the endpoint per call instead of using a fixed base: the
// service name is stable, the instances are not.
func WithResolver(resolver Resolver) ClientOption {
	return func(c *HTTPClient) {
		if resolver != nil {
			c.resolver = resolver
		}
	}
}

// NewHTTPClient creates a client for a fixed endpoint.
func NewHTTPClient(baseURL string, opts ...ClientOption) Client {
	c := &HTTPClient{
		base: strings.TrimRight(baseURL, "/"),
		hc:   &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return &client{binding: BindingRemote, invoke: c.invoke}
}

// NewServiceClient creates a client that resolves `service` on every call.
func NewServiceClient(service string, resolver Resolver, opts ...ClientOption) Client {
	c := &HTTPClient{
		service:  service,
		resolver: resolver,
		hc:       &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return &client{binding: BindingRemote, invoke: c.invoke}
}

func (c *HTTPClient) invoke(ctx context.Context, method string, body []byte) ([]byte, error) {
	base := c.base
	if c.resolver != nil {
		resolved, err := c.resolver.Resolve(ctx, c.service)
		if err != nil {
			return nil, err
		}
		base = resolved
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/rpc/"+url.PathEscape(method), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		request.Header.Set(KeyHeader, c.key)
	}

	response, err := c.hc.Do(request)
	if err != nil {
		// Preserve the underlying error as well as ErrNoEndpoint: a caller
		// must still be able to detect context.DeadlineExceeded/Canceled with
		// errors.Is across the transport boundary.
		return nil, fmt.Errorf("%w: %w", ErrNoEndpoint, err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxBodyBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return raw, nil
	}

	var rpcErr Error
	if err := json.Unmarshal(raw, &rpcErr); err == nil && rpcErr.Code != "" {
		rpcErr.Status = response.StatusCode
		return nil, &rpcErr
	}
	return nil, NewError("internal", string(raw)).WithStatus(response.StatusCode)
}

var _ = errors.Is
