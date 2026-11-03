package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maxid/gmcp"

	"github.com/maxid/gable/mcp"
)

func echoServer(t *testing.T) *gmcp.Server {
	t.Helper()
	server := gmcp.NewServer("gable-mcp-test")
	err := server.AddTool(
		gmcp.Tool{Name: "echo", Description: "echo the text", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)},
		func(_ context.Context, args json.RawMessage) (any, error) {
			var payload struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(args, &payload)
			return map[string]any{"echoed": payload.Text}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// The two transports expose the same surface: a caller does not branch on how it connected.
func TestClientOverHTTP(t *testing.T) {
	server := httptest.NewServer(mcp.HTTPHandler(echoServer(t)))
	defer server.Close()

	client, err := mcp.Dial(context.Background(), mcp.Endpoint{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"over http"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || mcp.ResponseText(result) == "" {
		t.Fatalf("result = %+v (text %q)", result, mcp.ResponseText(result))
	}
}

// A stdio server is a child process; the client starts it, and Close must stop it, or every
// run leaks a process.
func TestClientOverStdio(t *testing.T) {
	// The test binary re-executes itself as a tiny MCP server.
	if os.Getenv("GABLE_MCP_HELPER") == "1" {
		server := gmcp.NewServer("helper")
		_ = server.AddTool(gmcp.Tool{Name: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(context.Context, json.RawMessage) (any, error) { return map[string]string{"pong": "1"}, nil })
		if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GABLE_MCP_HELPER", "1")

	client, err := mcp.Dial(context.Background(), mcp.Endpoint{Command: []string{executable, "-test.run=TestClientOverStdio"}})
	if err != nil {
		t.Fatal(err)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("tools = %+v", tools)
	}

	result, err := client.CallTool(context.Background(), "ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	// Close waits for the child (or kills it after a grace period); either way the server is
	// gone, which is what a caller can observe: the next call fails.
	closed, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.ListTools(closed); err == nil {
		t.Fatal("a closed stdio client must not answer")
	}
}

// Bad endpoints are rejected before anything is started.
func TestDialRejectsBadEndpoints(t *testing.T) {
	if _, err := mcp.Dial(context.Background(), mcp.Endpoint{}); err == nil {
		t.Fatal("an empty endpoint must be refused")
	}
	if _, err := mcp.Dial(context.Background(), mcp.Endpoint{URL: "http://x", Command: []string{"y"}}); err == nil {
		t.Fatal("an endpoint with both transports must be refused")
	}
	if _, err := mcp.Dial(context.Background(), mcp.Endpoint{Command: []string{filepath.Join(t.TempDir(), "missing-binary")}}); err == nil {
		t.Fatal("a missing command must be reported")
	}
}
