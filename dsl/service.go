package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maxid/gmcp"

	"github.com/maxid/gable/orm"
)

// Service owns a resource directory for the CLI and the MCP tool surface.
type Service struct {
	Root   string
	set    *Set
	issues Issues
}

// NewService loads and lints a directory. Lint issues are reported by
// Service.Lint rather than failing construction, so an agent can see them.
func NewService(root string) (*Service, error) {
	set, loadIssues, err := LoadDir(root)
	if err != nil {
		return nil, err
	}
	issues := append(loadIssues, set.Lint()...)
	return &Service{Root: root, set: set, issues: issues}, nil
}

// Reload re-reads the directory.
func (s *Service) Reload() (Issues, error) {
	set, loadIssues, err := LoadDir(s.Root)
	if err != nil {
		return nil, err
	}
	s.set = set
	s.issues = append(loadIssues, set.Lint()...)
	return s.Lint(), nil
}

// Set returns the current resources.
func (s *Service) Set() *Set { return s.set }

// Lint returns the last known issues.
func (s *Service) Lint() Issues { return append(Issues{}, s.issues...) }

// Preview renders a resource by "Kind/name".
func (s *Service) Preview(resource, dialectName string) (any, error) {
	kind, name, err := splitResource(resource)
	if err != nil {
		return nil, err
	}
	dialect, err := orm.DialectFor(dialectName)
	if err != nil {
		return nil, err
	}
	return s.set.Preview(kind, name, dialect)
}

// Apply writes normalized resources below the service root.
func (s *Service) Apply(out string) ([]string, error) {
	if issues := s.Lint(); len(issues) > 0 {
		return nil, issues
	}
	if out == "" {
		out = ".gable/dsl"
	}
	target := out
	if !filepath.IsAbs(target) {
		target = filepath.Join(s.Root, target)
	}
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("dsl: output must stay inside %s", s.Root)
	}
	return s.set.ApplyDir(absolute)
}

// MCPTool pairs a tool description with its handler.
type MCPTool struct {
	Tool    gmcp.Tool
	Handler gmcp.ToolHandler
}

// AddTo registers every DSL tool on an MCP server.
func (s *Service) AddTo(server *gmcp.Server) error {
	for _, tool := range s.MCPTools() {
		if err := server.AddTool(tool.Tool, tool.Handler); err != nil {
			return err
		}
	}
	return nil
}

// MCPTools returns the AI-facing tool surface for the DSL loop.
func (s *Service) MCPTools() []MCPTool {
	return []MCPTool{
		{
			Tool: gmcp.Tool{
				Name:        "dsl.schema",
				Description: "Describe the gable/v1 envelope and the Model/Table/Form/Page shapes.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Handler: func(context.Context, json.RawMessage) (any, error) {
				return Schema(), nil
			},
		},
		{
			Tool: gmcp.Tool{
				Name:        "dsl.lint",
				Description: "Validate the loaded DSL directory and return all issues.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Handler: func(context.Context, json.RawMessage) (any, error) {
				issues := s.issues
				return map[string]any{
					"issues": issues,
					"valid":  len(issues) == 0,
					"models": len(s.set.Models),
					"tables": len(s.set.Tables),
					"forms":  len(s.set.Forms),
					"pages":  len(s.set.Pages),
				}, nil
			},
		},
		{
			Tool: gmcp.Tool{
				Name:        "dsl.preview",
				Description: "Render Model DDL or Table/Form/Page lcdp.ir/v1 for a resource (Kind/name).",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"resource":{"type":"string"},"dialect":{"type":"string","enum":["mysql","postgres"]}},"required":["resource"]}`),
			},
			Handler: func(_ context.Context, args json.RawMessage) (any, error) {
				var request struct {
					Resource string `json:"resource"`
					Dialect  string `json:"dialect"`
				}
				if err := json.Unmarshal(args, &request); err != nil {
					return nil, err
				}
				if request.Dialect == "" {
					request.Dialect = "mysql"
				}
				return s.Preview(request.Resource, request.Dialect)
			},
		},
		{
			Tool: gmcp.Tool{
				Name:        "dsl.apply",
				Description: "Validate and write normalized resources below the service root.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"out":{"type":"string"}}}`),
			},
			Handler: func(_ context.Context, args json.RawMessage) (any, error) {
				var request struct {
					Out string `json:"out"`
				}
				if err := json.Unmarshal(args, &request); err != nil {
					return nil, err
				}
				written, err := s.Apply(request.Out)
				if err != nil {
					return nil, err
				}
				return map[string]any{"written": written}, nil
			},
		},
	}
}

// Schema describes the envelope and the four resource kinds for an agent.
func Schema() map[string]any {
	return map[string]any{
		"apiVersion": APIVersion,
		"envelope": map[string]any{
			"required": []string{"apiVersion", "kind", "metadata", "spec"},
			"metadata": map[string]any{"required": []string{"name"}, "properties": map[string]any{
				"name": "string (^[a-z][a-z0-9._-]*$)", "title": "string", "description": "string",
			}},
		},
		"kinds": map[string]any{
			"Api": map[string]any{
				"required": []string{"spec.handler", "spec.path"},
				"fields":   "handler, model, method (GET/POST/PUT/PATCH/DELETE), path (relative), guards, audit",
				"rules":    []string{"handler names a registered Go handler", "guards name registered guards"},
			},
			"Mcp": map[string]any{
				"required": []string{"spec.handler"},
				"fields":   "handler, name, description, params (JSON Schema object), guards",
				"rules":    []string{"handler names a registered Go handler", "params is an object schema"},
			},
			"Model": map[string]any{
				"required": []string{"spec.fields"},
				"fields":   "name, type, size, primary, tenant, required, unique, autoCreate, autoUpdate, generator, default, enum, label",
				"types":    []string{"int64", "int32", "string", "text", "bool", "float64", "time", "json"},
			},
			"Table": map[string]any{"required": []string{"spec.model"}, "fields": "model, columns, filters, runtimeApi"},
			"Form":  map[string]any{"required": []string{"spec.model"}, "fields": "model, fields, layout"},
			"Page":  map[string]any{"fields": "table, form, title"},
		},
		"rules": []string{
			"exactly one primary field; primary must be int64",
			"at most one tenant field; tenant must be int64",
			"Table/Form reference an existing Model; Page references existing Table/Form",
			"Api/Mcp reference registered handlers by name; a resource never names a Go package or file",
			"no arbitrary code in resources",
		},
	}
}

func splitResource(value string) (Kind, string, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", fmt.Errorf("dsl: expected Kind/name, got %q", value)
	}
	kind := Kind(parts[0])
	switch kind {
	case KindModel, KindTable, KindForm, KindPage, KindApi, KindMcp:
		return kind, parts[1], nil
	default:
		return "", "", fmt.Errorf("dsl: unsupported kind %q", kind)
	}
}
