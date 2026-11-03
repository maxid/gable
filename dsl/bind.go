package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/maxid/gmcp"

	"github.com/maxid/gable/web"
)

// APIHandler is the Go side of an Api resource. It is web.HandlerFunc so a bound route can
// be registered without an adapter.
type APIHandler = web.HandlerFunc

// ToolHandler is the Go side of an Mcp resource. It receives the tool arguments and returns
// whatever the tool produced.
type ToolHandler func(ctx context.Context, arguments json.RawMessage) (any, error)

// Guard is a function that runs before a bound action or tool.
//
// Guards are registered by name and referenced from resources by that name, so a resource
// can require authentication without naming a Go package.
type Guard func(ctx context.Context) error

// Registry collects the Go implementations an application offers to its DSL.
//
// It is the other half of an Api/Mcp resource: the resource says "call handler order.close,
// behind the tenant guard, at POST task/{id}/close", and the registry is where those names
// exist. Nothing is global — the application builds one registry in main, which is what makes
// binding testable and a misnamed handler a startup error instead of a runtime 500.
type Registry struct {
	apis   map[string]APIHandler
	tools  map[string]ToolHandler
	guards map[string]Guard
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		apis:   map[string]APIHandler{},
		tools:  map[string]ToolHandler{},
		guards: map[string]Guard{},
	}
}

// API registers a handler name. It returns the registry so registrations chain.
func (r *Registry) API(name string, handler APIHandler) *Registry {
	if strings.TrimSpace(name) != "" && handler != nil {
		r.apis[name] = handler
	}
	return r
}

// Tool registers a tool handler name.
func (r *Registry) Tool(name string, handler ToolHandler) *Registry {
	if strings.TrimSpace(name) != "" && handler != nil {
		r.tools[name] = handler
	}
	return r
}

// AddGuard registers a guard name. A resource that names a guard that was never registered
// fails binding rather than running without it.
func (r *Registry) AddGuard(name string, guard Guard) *Registry {
	if strings.TrimSpace(name) != "" && guard != nil {
		r.guards[name] = guard
	}
	return r
}

// Names lists the registered handlers and guards, sorted. It is what an error message and
// `gable inspect` can show when a resource names something that does not exist.
func (r *Registry) Names() (apis, tools, guards []string) {
	for name := range r.apis {
		apis = append(apis, name)
	}
	for name := range r.tools {
		tools = append(tools, name)
	}
	for name := range r.guards {
		guards = append(guards, name)
	}
	sort.Strings(apis)
	sort.Strings(tools)
	sort.Strings(guards)
	return apis, tools, guards
}

// Binding is a validated Api resource together with its handler and guards.
type Binding struct {
	Doc     *ApiDoc
	Handler APIHandler
	Guards  []Guard
}

// Routes registers every bound Api resource on the router under prefix.
//
// The guards run first, in the order the resource declared them, so "tenant then role" means
// the role check sees a tenant. A handler that never got bound never gets a route: a resource
// pointing at a missing handler is a Bind error, not a 500 at request time.
func (r *Registry) Routes(set *Set, router *web.Router, prefix string) ([]Binding, error) {
	if set == nil {
		return nil, fmt.Errorf("dsl: nil set")
	}
	if router == nil {
		return nil, fmt.Errorf("dsl: nil router")
	}
	if prefix == "" {
		prefix = "/dsl/api"
	}

	var bindings []Binding
	for _, name := range sortedKeys(set.APIs) {
		doc := set.APIs[name]
		handler, ok := r.apis[doc.Spec.Handler]
		if !ok {
			return nil, fmt.Errorf("dsl: %s names handler %q, which is not registered", resourceKey(KindApi, name), doc.Spec.Handler)
		}
		guards := make([]Guard, 0, len(doc.Spec.Guards))
		for _, guardName := range doc.Spec.Guards {
			guard, ok := r.guards[guardName]
			if !ok {
				return nil, fmt.Errorf("dsl: %s names guard %q, which is not registered", resourceKey(KindApi, name), guardName)
			}
			guards = append(guards, guard)
		}

		wrapped := handler
		if len(guards) > 0 {
			wrapped = guardChain(guards, handler)
		}
		route := strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(doc.Spec.Path, "/")
		summary := doc.Metadata.Title
		if summary == "" {
			summary = doc.Spec.Handler
		}
		router.Handle(doc.MethodOrDefault(), route, wrapped).Doc(summary).Tags("dsl-api")
		bindings = append(bindings, Binding{Doc: doc, Handler: handler, Guards: guards})
	}
	return bindings, nil
}

// guardChain composes guards in declaration order.
func guardChain(guards []Guard, handler APIHandler) APIHandler {
	return func(ctx *web.Context) error {
		requestContext := ctx.Request().Context()
		for _, guard := range guards {
			if err := guard(requestContext); err != nil {
				return err
			}
		}
		return handler(ctx)
	}
}

// Tools adds every Mcp resource to an MCP server and returns the registered tools.
func (r *Registry) Tools(set *Set, server *gmcp.Server) ([]string, error) {
	if set == nil {
		return nil, fmt.Errorf("dsl: nil set")
	}
	if server == nil {
		return nil, fmt.Errorf("dsl: nil mcp server")
	}

	var names []string
	for _, name := range sortedKeys(set.MCPs) {
		doc := set.MCPs[name]
		handler, ok := r.tools[doc.Spec.Handler]
		if !ok {
			return nil, fmt.Errorf("dsl: %s names handler %q, which is not registered", resourceKey(KindMcp, name), doc.Spec.Handler)
		}
		guards := make([]Guard, 0, len(doc.Spec.Guards))
		for _, guardName := range doc.Spec.Guards {
			guard, ok := r.guards[guardName]
			if !ok {
				return nil, fmt.Errorf("dsl: %s names guard %q, which is not registered", resourceKey(KindMcp, name), guardName)
			}
			guards = append(guards, guard)
		}

		toolName := doc.ToolName()
		tool := gmcp.Tool{
			Name:        toolName,
			Description: doc.Spec.Description,
			InputSchema: schemaOf(doc),
		}
		bound := handler
		err := server.AddTool(tool, func(ctx context.Context, arguments json.RawMessage) (any, error) {
			for _, guard := range guards {
				if err := guard(ctx); err != nil {
					return nil, err
				}
			}
			return bound(ctx, arguments)
		})
		if err != nil {
			return nil, fmt.Errorf("dsl: %s: %w", resourceKey(KindMcp, name), err)
		}
		names = append(names, toolName)
	}
	return names, nil
}

// schemaOf renders the tool's input schema. A resource without params advertises an empty
// object schema, because a tool with no declared input must still be callable.
func schemaOf(doc *McpDoc) json.RawMessage {
	schema := doc.Spec.Params
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		// params come from YAML and may hold values JSON cannot express; an empty schema is
		// the honest fallback, and lint has already complained about the shape.
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return raw
}

// previewAPI renders what binding this action would register, without an application: the
// route, the verb, the handler name and the guards it requires.
func (s *Set) previewAPI(name string) (any, error) {
	doc, ok := s.APIs[name]
	if !ok {
		return nil, fmt.Errorf("dsl: api %q not found", name)
	}
	route := "/dsl/api/" + strings.TrimPrefix(doc.Spec.Path, "/")
	return map[string]any{
		"apiVersion": doc.APIVersion,
		"kind":       doc.Kind,
		"metadata":   doc.Metadata,
		"spec":       doc.Spec,
		"route":      doc.MethodOrDefault() + " " + route,
		"handler":    doc.Spec.Handler,
		"guards":     doc.Spec.Guards,
		"audit":      doc.Spec.Audit,
	}, nil
}

// previewMCP renders the tool a model would see.
func (s *Set) previewMCP(name string) (any, error) {
	doc, ok := s.MCPs[name]
	if !ok {
		return nil, fmt.Errorf("dsl: mcp %q not found", name)
	}
	return map[string]any{
		"apiVersion":  doc.APIVersion,
		"kind":        doc.Kind,
		"metadata":    doc.Metadata,
		"spec":        doc.Spec,
		"tool":        doc.ToolName(),
		"handler":     doc.Spec.Handler,
		"guards":      doc.Spec.Guards,
		"inputSchema": doc.Spec.Params,
	}, nil
}
