package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/maxid/gmcp"

	"github.com/maxid/gable/mcp"
)

func mcpServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := gmcp.NewServer("cli-mcp-test")
	err := server.AddTool(
		gmcp.Tool{Name: "echo", Description: "echo the text", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(_ context.Context, args json.RawMessage) (any, error) {
			return map[string]any{"args": string(args)}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(mcp.HTTPHandler(server))
}

// list and call are what a script uses to check a server before wiring it into a product.
func TestMCPCLIListAndCall(t *testing.T) {
	server := mcpServer(t)
	defer server.Close()

	cmd := newMCPCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list", "--url", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v (%s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "echo") || !strings.Contains(out.String(), "echo the text") {
		t.Fatalf("list output = %q", out.String())
	}

	cmd = newMCPCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list", "--url", server.URL, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var tools []gmcp.Tool
	if err := json.Unmarshal(out.Bytes(), &tools); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	cmd = newMCPCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"call", "echo", "--url", server.URL, "--arguments", `{"text":"hi"}`})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Fatalf("call output = %q", out.String())
	}

	// Invalid JSON is refused before connecting.
	cmd = newMCPCommand()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"call", "echo", "--url", server.URL, "--arguments", "{"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestMCPCLIEndpointValidation(t *testing.T) {
	if _, err := mcpEndpoint("http://x", "cmd", nil); err == nil {
		t.Fatal("both transports must be refused")
	}
	if _, err := mcpEndpoint("", "", nil); err == nil {
		t.Fatal("an empty endpoint must be refused")
	}
	endpoint, err := mcpEndpoint("", "gable", []string{"dsl", "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(endpoint.Command, " ") != "gable dsl mcp" {
		t.Fatalf("endpoint = %+v", endpoint)
	}
}

// A stdio server through the CLI: the same command shape a product would use to drive
// `gable dsl mcp`.
func TestMCPCLIOverStdio(t *testing.T) {
	if os.Getenv("GABLE_MCP_CLI_HELPER") == "1" {
		server := gmcp.NewServer("helper")
		_ = server.AddTool(gmcp.Tool{Name: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
		if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GABLE_MCP_CLI_HELPER", "1")

	cmd := newMCPCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"call", "ping", "--command", executable, "--arg", "-test.run=TestMCPCLIOverStdio"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(out.String(), "pong") {
		t.Fatalf("output = %q", out.String())
	}
}
