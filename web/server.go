package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maxid/glogs"

	gableerrors "github.com/maxid/gable/errors"
)

type options struct {
	title          string
	version        string
	description    string
	servers        []string
	openapiVersion string
	envelope       EnvelopeConfig
	debug          bool
	swagger        bool
	docs           bool
	docsPath       string
	logger         *glogs.Logger
}

// Option configures a Server.
type Option func(*options)

// WithTitle sets the OpenAPI info title.
func WithTitle(title string) Option { return func(o *options) { o.title = title } }

// WithVersion sets the OpenAPI info version.
func WithVersion(version string) Option { return func(o *options) { o.version = version } }

// WithDescription sets the OpenAPI info description.
func WithDescription(description string) Option {
	return func(o *options) { o.description = description }
}

// WithServers sets the OpenAPI servers list.
func WithServers(servers ...string) Option {
	return func(o *options) { o.servers = append([]string{}, servers...) }
}

// WithOpenAPIVersion sets the emitted OpenAPI version. Defaults to 3.0.1.
func WithOpenAPIVersion(version string) Option {
	return func(o *options) {
		if version != "" {
			o.openapiVersion = version
		}
	}
}

// WithEnvelope overrides the response code mapping.
func WithEnvelope(config EnvelopeConfig) Option {
	return func(o *options) { o.envelope = config }
}

// WithDebug includes the underlying error message in 500 responses.
func WithDebug(debug bool) Option { return func(o *options) { o.debug = debug } }

// WithSwaggerUI enables or disables the Swagger UI routes.
func WithSwaggerUI(enabled bool) Option { return func(o *options) { o.swagger = enabled } }

// WithDocs enables or disables the OpenAPI and Swagger UI routes.
func WithDocs(enabled bool) Option { return func(o *options) { o.docs = enabled } }

// WithDocsPath changes the OpenAPI base path. Defaults to /v3/api-docs.
func WithDocsPath(path string) Option {
	return func(o *options) {
		if path != "" {
			o.docsPath = path
		}
	}
}

// WithLogger sets the access-log logger.
func WithLogger(logger *glogs.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// Server owns the gin engine, the route registry and the HTTP lifecycle.
type Server struct {
	engine *gin.Engine
	router *Router
	opts   options

	mu         sync.RWMutex
	routes     []*Route
	logger     *glogs.Logger
	httpServer *http.Server
	listener   net.Listener
}

// New returns a server with the framework middleware installed.
func New(opts ...Option) *Server {
	o := options{
		title:          "API",
		version:        "0.0.0-dev",
		openapiVersion: "3.0.1",
		envelope:       DefaultEnvelopeConfig(),
		swagger:        true,
		docs:           true,
		docsPath:       "/v3/api-docs",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	engine := gin.New()
	server := &Server{engine: engine, opts: o, logger: o.logger}
	server.router = &Router{server: server, group: &engine.RouterGroup, docGroup: "default"}

	engine.Use(server.requestIDMiddleware(), server.recoveryMiddleware(), server.accessLogMiddleware())
	engine.NoRoute(func(c *gin.Context) {
		server.writeError(&Context{gin: c, server: server}, gableerrors.New(gableerrors.CodeNotFound, "not found").WithStatus(http.StatusNotFound))
	})
	server.registerDocs()
	return server
}

// Register adds controllers to the server.
func (s *Server) Register(controllers ...Controller) *Server {
	for _, controller := range controllers {
		if controller != nil {
			controller.Routes(s.router)
		}
	}
	return s
}

// Router returns the root router.
func (s *Server) Router() *Router { return s.router }

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.engine }

// SetLogger sets the access-log logger.
func (s *Server) SetLogger(logger *glogs.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

// Addr returns the actual listen address after Start.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Start listens on addr and serves in the background.
func (s *Server) Start(_ context.Context, addr string) error {
	if addr == "" {
		return fmt.Errorf("web: listen address is empty")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", addr, err)
	}

	httpServer := &http.Server{
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.mu.Lock()
	if s.httpServer != nil {
		s.mu.Unlock()
		_ = listener.Close()
		return fmt.Errorf("web: server already started")
	}
	s.listener = listener
	s.httpServer = httpServer
	s.mu.Unlock()

	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The listener error is surfaced through Stop/health in a later step; a
			// background accept failure must not crash the process.
		}
	}()
	return nil
}

// Stop shuts the HTTP server down.
func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	httpServer := s.httpServer
	s.httpServer = nil
	s.listener = nil
	s.mu.Unlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

func (s *Server) addRoute(route *Route) {
	if route.group == "" {
		route.group = "default"
	}
	if len(route.tags) == 0 {
		route.tags = []string{"default"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = append(s.routes, route)
}

func (s *Server) routeSnapshot() []*Route {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*Route{}, s.routes...)
}

func (s *Server) writeSuccess(c *Context, status int, data any) {
	if status <= 0 {
		status = http.StatusOK
	}
	envelope := newEnvelope(s.opts.envelope.SuccessCode, "OK", data, c.RequestID(), true)
	c.gin.JSON(status, envelope)
}

func (s *Server) writeError(c *Context, err error) {
	status, code, message, data := s.mapError(err)
	envelope := newEnvelope(code, message, data, c.RequestID(), false)
	if c.gin.Writer.Written() {
		c.gin.Abort()
		return
	}
	c.gin.AbortWithStatusJSON(status, envelope)
}

func (s *Server) mapError(err error) (int, string, string, any) {
	if err == nil {
		return http.StatusInternalServerError, s.opts.envelope.InternalCode, "internal error", nil
	}

	var frameworkErr *gableerrors.Error
	if errors.As(err, &frameworkErr) {
		status := frameworkErr.Status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		message := frameworkErr.Message
		if message == "" {
			message = "error"
		}
		return status, s.opts.envelope.MapCode(frameworkErr.Code), message, nil
	}

	if details, ok := validationDetails(err); ok {
		return http.StatusBadRequest, s.opts.envelope.InvalidCode, "validation failed", details
	}

	if isBadJSON(err) {
		return http.StatusBadRequest, s.opts.envelope.InvalidCode, "invalid request body", nil
	}

	message := "internal error"
	if s.opts.debug {
		message = err.Error()
	}
	return http.StatusInternalServerError, s.opts.envelope.InternalCode, message, nil
}

func isBadJSON(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return true
	}
	return false
}
