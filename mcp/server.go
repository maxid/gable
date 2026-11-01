// Package mcp exposes an application's operations as MCP tools.
//
// The intent is narrow: a controller that is already callable over HTTP can also be
// called by an agent, without a second implementation. What is *not* here is
// authentication of the caller's authority — the transport check below is a shared
// key, and per-tool permission stays with the application (the same decision the
// design record makes for the DSL: "DB edits need permission plus audit").
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/maxid/gable/rpc"
	"github.com/maxid/gmcp"
)

// Config configures the MCP surface.
type Config struct {
	// Name is the server name reported during initialize.
	Name string
	// Version is reported alongside it.
	Version string
	// Key requires the shared key header on every request. Empty disables the check,
	// which is only appropriate on a closed network.
	Key string
	// KeyHeader overrides the header name; defaults to rpc.KeyHeader.
	KeyHeader string
	// Path is where Mount registers the handler.
	Path string
}

// DefaultConfig returns the defaults used by New.
func DefaultConfig(name string) Config {
	return Config{Name: name, Version: "0.0.0-dev", KeyHeader: rpc.KeyHeader, Path: "/mcp"}
}

// Handler adapts one operation.
//
// arguments is the raw JSON the agent sent; returning an error surfaces as a tool
// error, which the agent can read and retry with different arguments.
type Handler func(ctx context.Context, arguments json.RawMessage) (any, error)

// Server is an MCP tool surface.
type Server struct {
	cfg   Config
	mcp   *gmcp.Server
	tools map[string]struct{}
}

// New creates the surface.
func New(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("%w: name is required", rpc.ErrPermanent)
	}
	if cfg.KeyHeader == "" {
		cfg.KeyHeader = rpc.KeyHeader
	}
	if cfg.Path == "" {
		cfg.Path = "/mcp"
	}

	server := &Server{cfg: cfg, mcp: gmcp.NewServer(cfg.Name), tools: map[string]struct{}{}}
	if cfg.Version != "" {
		server.mcp.SetVersion(cfg.Version)
	}
	return server, nil
}

// RegisterTool adds a tool. A duplicate name is a wiring bug: the agent would get
// whichever definition won the race.
func (s *Server) RegisterTool(name, description string, schema json.RawMessage, handler Handler) error {
	if _, exists := s.tools[name]; exists {
		return fmt.Errorf("%w: tool %q", rpc.ErrAlreadyRegistered, name)
	}

	tool := gmcp.Tool{Name: name, Description: description}
	if len(schema) > 0 {
		// gmcp carries the schema as part of the tool description block; keeping it
		// here avoids a second schema model.
		tool.InputSchema = schema
	}
	if err := s.mcp.AddTool(tool, func(ctx context.Context, arguments json.RawMessage) (any, error) {
		return handler(ctx, arguments)
	}); err != nil {
		return err
	}

	s.tools[name] = struct{}{}
	return nil
}

// Tools lists the registered tool names.
func (s *Server) Tools() []string {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	return names
}

// Handler returns the HTTP handler, including the shared-key check.
func (s *Server) Handler() http.Handler {
	inner := s.mcp.HTTPHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Key != "" {
			if subtleCompare(r.Header.Get(s.cfg.KeyHeader), s.cfg.Key) == false {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"unauthorized"}}`))
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
}

// Mount registers the handler on a mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.Handle(s.cfg.Path, s.Handler())
}

// Path returns the configured mount path.
func (s *Server) Path() string { return s.cfg.Path }

// subtleCompare compares two secrets in constant time.
func subtleCompare(got, want string) bool {
	if len(got) != len(want) {
		return false
	}
	var diff byte
	for i := 0; i < len(got); i++ {
		diff |= got[i] ^ want[i]
	}
	return diff == 0
}
