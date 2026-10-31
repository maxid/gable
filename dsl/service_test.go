package dsl

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/maxid/gmcp"
)

func TestServicePreviewAndApply(t *testing.T) {
	service, err := NewService(validSet(t))
	if err != nil {
		t.Fatal(err)
	}
	if issues := service.Lint(); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}

	preview, err := service.Preview("Model/task", "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.(map[string]any)["ddl"].(string), `"dsl_task"`) {
		t.Fatalf("model preview = %+v", preview)
	}
	table, err := service.Preview("Table/task.table", "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if table.(map[string]any)["kind"] != "TableDef" {
		t.Fatalf("table preview = %+v", table)
	}

	written, err := service.Apply(".gable/dsl")
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 {
		t.Fatalf("written = %v", written)
	}
	again, err := service.Apply(".gable/dsl")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second apply = %v", again)
	}
	if _, err := service.Apply("../escape"); err == nil {
		t.Fatal("expected path escape error")
	}
	if schema := Schema(); schema["apiVersion"] != APIVersion {
		t.Fatalf("schema = %+v", schema)
	}
}

func TestServiceMCPTools(t *testing.T) {
	service, err := NewService(validSet(t))
	if err != nil {
		t.Fatal(err)
	}
	server := gmcp.NewServer("dsl-test")
	if err := service.AddTo(server); err != nil {
		t.Fatal(err)
	}

	clientConn, serverConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = server.Serve(ctx, serverConn, serverConn)
		close(done)
	}()
	defer func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	}()

	client := gmcp.NewClient(clientConn, clientConn)
	if _, err := client.Initialize(ctx, gmcp.ClientInfo{Name: "test"}); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 4 {
		t.Fatalf("tools = %+v", tools)
	}

	lint, err := client.CallTool(ctx, "dsl.lint", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var lintResult struct {
		Valid bool `json:"valid"`
	}
	if err := json.Unmarshal([]byte(lint.Content[0].Text), &lintResult); err != nil {
		t.Fatal(err)
	}
	if !lintResult.Valid {
		t.Fatalf("lint = %s", lint.Content[0].Text)
	}

	preview, err := client.CallTool(ctx, "dsl.preview", map[string]any{"resource": "Table/task.table"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Content[0].Text, "TableDef") {
		t.Fatalf("preview = %s", preview.Content[0].Text)
	}

	apply, err := client.CallTool(ctx, "dsl.apply", map[string]any{"out": ".gable/dsl"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apply.Content[0].Text, "Model-task.yaml") {
		t.Fatalf("apply = %s", apply.Content[0].Text)
	}
}

func TestServiceStartsWithInvalidDSL(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "broken.yaml", `apiVersion: gable/v1
kind: Model
metadata: {name: broken}
spec:
  table: broken
  fields: [{name: id, type: int64}]
`)
	service, err := NewService(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.Lint()) == 0 {
		t.Fatal("expected lint issues")
	}
	if _, err := service.Apply(".gable/dsl"); err == nil {
		t.Fatal("apply must refuse invalid resources")
	}

	var lintHandler func(context.Context, json.RawMessage) (any, error)
	for _, tool := range service.MCPTools() {
		if tool.Tool.Name == "dsl.lint" {
			lintHandler = tool.Handler
		}
	}
	if lintHandler == nil {
		t.Fatal("dsl.lint tool missing")
	}
	raw, err := lintHandler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(map[string]any)
	if result["valid"] != false {
		t.Fatalf("lint = %+v", result)
	}
	if issues := result["issues"].(Issues); len(issues) == 0 {
		t.Fatal("expected issues")
	}
	server := gmcp.NewServer("invalid")
	if err := service.AddTo(server); err != nil {
		t.Fatal(err)
	}
}
