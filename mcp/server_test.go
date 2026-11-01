package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maxid/gable/mcp"
	"github.com/maxid/gable/rpc"
	"github.com/maxid/gmcp"
)

func newServer(t *testing.T, cfg mcp.Config) *mcp.Server {
	t.Helper()
	if cfg.Name == "" {
		cfg.Name = "app"
	}
	server, err := mcp.New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	err = server.RegisterTool("echo", "echo the text", json.RawMessage(`{
		"type":"object","properties":{"text":{"type":"string"}},"required":["text"]
	}`), func(_ context.Context, arguments json.RawMessage) (any, error) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(arguments, &payload); err != nil {
			return nil, err
		}
		return map[string]any{"text": payload.Text}, nil
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return server
}

func post(t *testing.T, handler http.Handler, body string, headers map[string]string) (*httptest.ResponseRecorder, gmcp.Response) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	var response gmcp.Response
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return recorder, response
}

// A registered operation becomes a tool an agent can list and call.
func TestToolIsListedAndCallable(t *testing.T) {
	server := newServer(t, mcp.Config{})
	handler := server.Handler()

	_, listed := post(t, handler, `{"jsonrpc":"2.0","id":"1","method":"tools/list"}`, nil)
	var tools gmcp.ListToolsResult
	if err := json.Unmarshal(listed.Result, &tools); err != nil {
		t.Fatalf("decode tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools.Tools)
	}
	// The schema travels with the tool, so an agent can build the arguments.
	if !strings.Contains(string(tools.Tools[0].InputSchema), `"text"`) {
		t.Errorf("schema = %s", tools.Tools[0].InputSchema)
	}

	_, called := post(t, handler,
		`{"jsonrpc":"2.0","id":"2","method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`, nil)
	var result gmcp.CallToolResult
	if err := json.Unmarshal(called.Result, &result); err != nil {
		t.Fatalf("decode call: %v", err)
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "hi") {
		t.Fatalf("content = %+v", result.Content)
	}
}

func TestDuplicateToolIsRejected(t *testing.T) {
	server := newServer(t, mcp.Config{})
	err := server.RegisterTool("echo", "again", nil, func(context.Context, json.RawMessage) (any, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("err = %v, want ErrAlreadyRegistered", err)
	}
}

func TestNameIsRequired(t *testing.T) {
	if _, err := mcp.New(mcp.Config{}); err == nil {
		t.Fatal("a server without a name must fail")
	} else if !strings.Contains(err.Error(), rpc.ErrPermanent.Error()) {
		t.Fatalf("err = %v", err)
	}
}

// The shared key is the transport's only check; per-tool authority stays with the app.
func TestSharedKeyIsEnforced(t *testing.T) {
	server := newServer(t, mcp.Config{Key: "s3cret"})
	handler := server.Handler()
	body := `{"jsonrpc":"2.0","id":"1","method":"tools/list"}`

	recorder, _ := post(t, handler, body, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("no key status = %d, want 401", recorder.Code)
	}

	recorder, _ = post(t, handler, body, map[string]string{rpc.KeyHeader: "wrong"})
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want 401", recorder.Code)
	}

	recorder, response := post(t, handler, body, map[string]string{rpc.KeyHeader: "s3cret"})
	if recorder.Code != http.StatusOK || response.Error != nil {
		t.Fatalf("authorized call = %d %+v", recorder.Code, response.Error)
	}
}

func TestMountRegistersThePath(t *testing.T) {
	server := newServer(t, mcp.Config{Path: "/tools"})
	mux := http.NewServeMux()
	server.Mount(mux)

	request := httptest.NewRequest(http.MethodPost, "/tools", bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":"1","method":"tools/list"}`))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if server.Path() != "/tools" {
		t.Fatalf("path = %q", server.Path())
	}
}

func TestToolsListing(t *testing.T) {
	server := newServer(t, mcp.Config{})
	if names := server.Tools(); len(names) != 1 || names[0] != "echo" {
		t.Fatalf("tools = %v", names)
	}
}
