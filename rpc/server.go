package rpc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// KeyHeader carries the shared secret between services.
const KeyHeader = "X-RPC-Key"

// MaxBodyBytes bounds one RPC body.
const MaxBodyBytes = 8 << 20

// Server exposes a registry over POST /rpc/{method}.
//
// It is a separate listener from the public API on purpose: the inner surface must
// not be reachable from the edge.
type Server struct {
	reg *Registry
	key string
	mux *http.ServeMux
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithKey requires the shared key on every request. An empty key disables the check,
// which is only appropriate inside a trusted network.
func WithKey(key string) ServerOption {
	return func(s *Server) { s.key = key }
}

// NewServer creates the remote binding.
func NewServer(reg *Registry, opts ...ServerOption) *Server {
	s := &Server{reg: reg, mux: http.NewServeMux()}
	for _, o := range opts {
		o(s)
	}
	s.mux.HandleFunc("POST /rpc/{method}", s.serve)
	return s
}

// Handler returns the HTTP handler for mounting on any server.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if s.key != "" {
		got := r.Header.Get(KeyHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.key)) != 1 {
			writeError(w, NewError("unauthorized", "invalid rpc key").WithStatus(http.StatusUnauthorized))
			return
		}
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		writeError(w, NewError("bad_request", "read body: "+err.Error()).WithStatus(http.StatusBadRequest))
		return
	}

	// The registry answers unknown methods itself; a missing registry is a wiring bug.
	if s.reg == nil {
		writeError(w, ErrNoRegistry)
		return
	}

	response, err := s.reg.invoke(r.Context(), r.PathValue("method"), body)
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response)
}

// writeError renders an error with the status the handler chose, defaulting to 500.
func writeError(w http.ResponseWriter, err error) {
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			rpcErr = NewError("deadline_exceeded", err.Error()).WithStatus(http.StatusGatewayTimeout)
		case errors.Is(err, context.Canceled):
			rpcErr = NewError("canceled", err.Error()).WithStatus(http.StatusGatewayTimeout)
		default:
			rpcErr = NewError("internal", err.Error())
		}
	}

	status := rpcErr.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	// The key is never echoed: an error body is the last place to leak it.
	rpcErr.Message = redactKey(rpcErr.Message)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcErr)
}

// redactKey keeps a secret out of an error message even if a driver put it there.
func redactKey(message string) string {
	if strings.Contains(strings.ToLower(message), KeyHeader) || strings.Contains(message, "password=") {
		return "internal error"
	}
	return message
}
