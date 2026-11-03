package dsl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxid/gmcp"

	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

const apiYAML = `apiVersion: gable/v1
kind: Api
metadata: {name: task.close, title: Close a task}
spec:
  handler: tasks.close
  model: task
  method: POST
  path: task/{id}/close
  guards: [tenant]
  audit: true
`

const mcpYAML = `apiVersion: gable/v1
kind: Mcp
metadata: {name: tasks.search}
spec:
  handler: tasks.search
  name: tasks.search
  description: Find tasks by title.
  params:
    type: object
    properties:
      q: {type: string}
    required: [q]
`

// The new kinds load, lint and land in the set like the original four.
func TestLoadApiAndMcp(t *testing.T) {
	dir := validSet(t)
	write(t, dir, "task.api.yaml", apiYAML)
	write(t, dir, "task.mcp.yaml", mcpYAML)

	set, issues, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("load issues = %v", issues)
	}
	if len(set.APIs) != 1 || len(set.MCPs) != 1 {
		t.Fatalf("set = %d apis, %d mcps", len(set.APIs), len(set.MCPs))
	}
	if lint := set.Lint(); len(lint) != 0 {
		t.Fatalf("lint = %v", lint)
	}
	if api := set.APIs["task.close"]; api.MethodOrDefault() != "POST" {
		t.Fatalf("method = %q", api.MethodOrDefault())
	}
	if tool := set.MCPs["tasks.search"]; tool.ToolName() != "tasks.search" {
		t.Fatalf("tool name = %q", tool.ToolName())
	}
	// ApplyDir writes the new kinds through the same shared iteration.
	out := t.TempDir()
	written, err := set.ApplyDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 6 {
		t.Fatalf("written = %v", written)
	}
}

func TestLintRejectsBrokenApiAndMcp(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"api-no-handler", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {path: task/close}
`, "handler"},
		{"api-no-path", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {handler: x}
`, "path"},
		{"api-absolute-path", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {handler: x, path: /task/close}
`, "must not start with /"},
		{"api-traversal", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {handler: x, path: ../task}
`, "clean relative path"},
		{"api-bad-method", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {handler: x, method: FETCH, path: task}
`, "unsupported method"},
		{"api-unknown-model", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {handler: x, model: nope, path: task}
`, "unknown model"},
		{"api-bad-guard", `apiVersion: gable/v1
kind: Api
metadata: {name: broken.api}
spec: {handler: x, path: task, guards: [Tenant Guard]}
`, "guard"},
		{"mcp-no-handler", `apiVersion: gable/v1
kind: Mcp
metadata: {name: broken.mcp}
spec: {}
`, "handler"},
		{"mcp-bad-name", `apiVersion: gable/v1
kind: Mcp
metadata: {name: broken.mcp}
spec: {handler: x, name: Not A Tool}
`, "must match"},
		{"mcp-params-not-object", `apiVersion: gable/v1
kind: Mcp
metadata: {name: broken.mcp}
spec:
  handler: x
  params: {type: string}
`, "type must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := validSet(t)
			write(t, dir, tc.name+".yaml", tc.content)
			set, issues, err := LoadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			issues = append(issues, set.Lint()...)
			if len(issues) == 0 || !strings.Contains(issues.Error(), tc.want) {
				t.Fatalf("issues = %v, want %q", issues, tc.want)
			}
		})
	}
}

func bindSet(t *testing.T) *Set {
	t.Helper()
	dir := validSet(t)
	write(t, dir, "task.api.yaml", apiYAML)
	write(t, dir, "task.mcp.yaml", mcpYAML)
	set, issues, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if lint := set.Lint(); len(lint) > 0 {
		t.Fatal(lint)
	}
	return set
}

// A bound route runs its guards in order, then the handler — and appears in OpenAPI.
func TestRegistryBindsRoutesAndTools(t *testing.T) {
	set := bindSet(t)
	var events []string
	registry := NewRegistry().
		AddGuard("tenant", func(ctx context.Context) error {
			if _, err := tenant.MustFromContext(ctx); err != nil {
				return err
			}
			events = append(events, "guard")
			return nil
		}).
		API("tasks.close", func(ctx *web.Context) error {
			events = append(events, "handler")
			return ctx.JSON(http.StatusOK, map[string]bool{"closed": true})
		}).
		Tool("tasks.search", func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"found": 0}, nil
		})

	server := web.New(web.WithTitle("bind"))
	server.UseTenant(tenant.NewSingleResolver(tenant.DefaultConfig()))
	bindings, err := registry.Routes(set, server.Router(), "/dsl/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || len(bindings[0].Guards) != 1 {
		t.Fatalf("bindings = %+v", bindings)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/dsl/api/task/7/close", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	if strings.Join(events, ",") != "guard,handler" {
		t.Fatalf("events = %v, want the guard before the handler", events)
	}

	mcpServer := gmcp.NewServer("dsl-bind")
	names, err := registry.Tools(set, mcpServer)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "tasks.search" {
		t.Fatalf("tools = %v", names)
	}
}

// A resource naming something the application never registered fails at bind time: the
// alternative is a route that 500s in production or, worse, runs unguarded.
func TestRegistryRejectsUnknownNames(t *testing.T) {
	set := bindSet(t)

	server := web.New(web.WithTitle("bind"))
	if _, err := NewRegistry().Routes(set, server.Router(), "/dsl/api"); err == nil ||
		!strings.Contains(err.Error(), "not registered") {
		t.Fatalf("missing handler = %v", err)
	}

	registry := NewRegistry().API("tasks.close", func(ctx *web.Context) error { return nil })
	if _, err := registry.Routes(set, server.Router(), "/dsl/api"); err == nil ||
		!strings.Contains(err.Error(), `guard "tenant"`) {
		t.Fatalf("missing guard = %v", err)
	}

	if _, err := NewRegistry().Tools(set, gmcp.NewServer("x")); err == nil ||
		!strings.Contains(err.Error(), "not registered") {
		t.Fatalf("missing tool = %v", err)
	}
}

// A guard failure stops the request before the handler runs.
func TestRegistryGuardFailureStopsTheHandler(t *testing.T) {
	set := bindSet(t)
	ran := false
	registry := NewRegistry().
		AddGuard("tenant", func(ctx context.Context) error {
			_, err := tenant.MustFromContext(ctx)
			return err
		})
	registry.API("tasks.close", func(ctx *web.Context) error {
		ran = true
		return ctx.JSON(http.StatusOK, map[string]bool{"closed": true})
	})

	server := web.New(web.WithTitle("bind"))
	if _, err := registry.Routes(set, server.Router(), "/dsl/api"); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/dsl/api/task/7/close", nil))
	if ran {
		t.Fatal("the handler ran after a guard failed")
	}
	if recorder.Code < 400 {
		t.Fatalf("status = %d", recorder.Code)
	}
}

// Preview is the read-only half: it shows what binding would register, so an operator can
// check a route and its guards without an application.
func TestPreviewApiAndMcp(t *testing.T) {
	set := bindSet(t)
	api, err := set.Preview(KindApi, "task.close", nil)
	if err != nil {
		t.Fatal(err)
	}
	preview := api.(map[string]any)
	if preview["route"] != "POST /dsl/api/task/{id}/close" {
		t.Fatalf("route = %v", preview["route"])
	}
	if preview["handler"] != "tasks.close" {
		t.Fatalf("handler = %v", preview["handler"])
	}

	tool, err := set.Preview(KindMcp, "tasks.search", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tool.(map[string]any)["tool"] != "tasks.search" {
		t.Fatalf("tool = %+v", tool)
	}

	if _, err := set.Preview(KindApi, "missing", nil); err == nil {
		t.Fatal("an unknown api must be an error")
	}
}
