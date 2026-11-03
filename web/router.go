package web

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// HandlerFunc is a framework handler. Returning an error lets the server map
// it to an envelope and HTTP status.
type HandlerFunc func(*Context) error

// Controller contributes routes to a server.
type Controller interface {
	Routes(*Router)
}

// Router registers routes and their documentation metadata.
type Router struct {
	server   *Server
	group    *gin.RouterGroup
	prefix   string
	tags     []string
	docGroup string
}

type groupOptions struct {
	tags  []string
	group string
}

// GroupOption configures a route group.
type GroupOption func(*groupOptions)

// WithTags sets the default OpenAPI tags for routes in the group.
func WithTags(tags ...string) GroupOption {
	return func(o *groupOptions) { o.tags = append(o.tags, tags...) }
}

// WithGroup sets the OpenAPI group name for routes in the group.
func WithGroup(name string) GroupOption {
	return func(o *groupOptions) {
		if strings.TrimSpace(name) != "" {
			o.group = name
		}
	}
}

// Group returns a sub-router.
func (r *Router) Group(prefix string, opts ...GroupOption) *Router {
	options := groupOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	tags := append([]string{}, r.tags...)
	tags = append(tags, options.tags...)
	group := r.docGroup
	if options.group != "" {
		group = options.group
	}
	return &Router{
		server:   r.server,
		group:    r.group.Group(prefix),
		prefix:   joinPaths(r.prefix, prefix),
		tags:     tags,
		docGroup: group,
	}
}

// Use adds gin middleware to this router.
func (r *Router) Use(middleware ...gin.HandlerFunc) { r.group.Use(middleware...) }

// GET registers a GET route.
func (r *Router) GET(path string, handler HandlerFunc) *Route {
	return r.handle("GET", path, handler)
}

// POST registers a POST route.
func (r *Router) POST(path string, handler HandlerFunc) *Route {
	return r.handle("POST", path, handler)
}

// PUT registers a PUT route.
func (r *Router) PUT(path string, handler HandlerFunc) *Route {
	return r.handle("PUT", path, handler)
}

// PATCH registers a PATCH route.
func (r *Router) PATCH(path string, handler HandlerFunc) *Route {
	return r.handle("PATCH", path, handler)
}

// DELETE registers a DELETE route.
func (r *Router) DELETE(path string, handler HandlerFunc) *Route {
	return r.handle("DELETE", path, handler)
}

// Handle registers a route with an explicit method, for a route that comes from data rather
// than code: a DSL Api resource names its verb at runtime, so there is no method-specific
// helper to call.
func (r *Router) Handle(method, path string, handler HandlerFunc) *Route {
	return r.handle(method, path, handler)
}

func (r *Router) handle(method, path string, handler HandlerFunc) *Route {
	full := joinPaths(r.prefix, path)
	route := &Route{
		server: r.server,
		method: method,
		path:   full,
		tags:   append([]string{}, r.tags...),
		group:  r.docGroup,
		status: 200,
	}
	r.group.Handle(method, ginPath(path), func(gc *gin.Context) {
		ctx := &Context{gin: gc, server: r.server}
		if err := handler(ctx); err != nil {
			r.server.writeError(ctx, err)
		}
	})
	r.server.addRoute(route)
	return route
}

// ginPath translates the OpenAPI-style "{id}" a route may be written with into Gin's ":id".
//
// Routes are documented in OpenAPI syntax because that is what the specification, the DSL and
// the frontend use; requiring handlers to spell parameters Gin's way would leak the router
// into every path string and make a DSL-declared route impossible to register.
func ginPath(path string) string {
	if !strings.Contains(path, "{") {
		return path
	}
	var builder strings.Builder
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '{':
			builder.WriteByte(':')
		case '}':
			// dropped: Gin does not delimit a parameter once it ends
		default:
			builder.WriteByte(path[i])
		}
	}
	return builder.String()
}

// Route carries the documentation metadata for one endpoint.
type Route struct {
	server *Server

	method      string
	path        string
	summary     string
	description string
	operationID string
	tags        []string
	group       string
	status      int

	request  any
	response any
	params   []oaParameter
}

// Doc sets the summary and optional description.
func (r *Route) Doc(summary string, description ...string) *Route {
	r.summary = summary
	if len(description) > 0 {
		r.description = description[0]
	}
	return r
}

// OperationID overrides the generated operation id.
func (r *Route) OperationID(id string) *Route {
	r.operationID = id
	return r
}

// Tags sets the OpenAPI tags for this route.
func (r *Route) Tags(tags ...string) *Route {
	r.tags = append([]string{}, tags...)
	return r
}

// Group sets the OpenAPI group for this route.
func (r *Route) Group(name string) *Route {
	if strings.TrimSpace(name) != "" {
		r.group = name
	}
	return r
}

// In declares the request body type.
func (r *Route) In(sample any) *Route {
	r.request = sample
	return r
}

// Out declares the success payload type (wrapped in the envelope).
func (r *Route) Out(sample any) *Route {
	r.response = sample
	return r
}

// Status sets the success HTTP status. Defaults to 200.
func (r *Route) Status(status int) *Route {
	if status > 0 {
		r.status = status
	}
	return r
}

// Query documents a query parameter. The schema is always a string.
func (r *Route) Query(name, description string, required bool) *Route {
	r.params = append(r.params, oaParameter{
		Name:        name,
		In:          "query",
		Description: description,
		Required:    required,
		Schema:      &oaSchema{Type: "string"},
	})
	return r
}

func joinPaths(prefix, path string) string {
	if prefix == "" {
		if path == "" {
			return "/"
		}
		if path[0] != '/' {
			return "/" + path
		}
		return path
	}
	if path == "" || path == "/" {
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

func openAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
			parts[i] = "{" + part[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

func pathParameters(path string) []oaParameter {
	var params []oaParameter
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
			name := part[1:]
			params = append(params, oaParameter{
				Name:     name,
				In:       "path",
				Required: true,
				Schema:   &oaSchema{Type: "string"},
			})
		}
	}
	return params
}
