package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The provider name chooses the wire format; an unknown name fails at construction instead of
// silently falling back to a vendor the caller did not ask for.
func TestProviderSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic sends x-api-key; the OpenAI adapter sends a bearer token.
		if r.Header.Get("x-api-key") == "" {
			t.Errorf("expected the Anthropic header, got %v", r.Header)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"hi from claude"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":4}}`)
	}))
	defer server.Close()

	service, err := New(Config{
		Provider: "anthropic",
		BaseURL:  server.URL,
		APIKey:   "key",
		Model:    "claude-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Ask(context.Background(), "session", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hi from claude" || result.Usage.TotalTokens != 7 {
		t.Fatalf("result = %+v", result)
	}

	if _, err := New(Config{Provider: "nobody", Model: "x"}); err == nil ||
		!strings.Contains(err.Error(), "unknown ai provider") {
		t.Fatalf("err = %v", err)
	}
}

// Streaming works through the same provider selection: the service asks the adapter for a
// stream, not for a chat response it would then pretend to stream.
func TestStreamThroughAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`event: content_block_start
data: {"index":0,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta
data: {"index":0,"delta":{"type":"text_delta","text":"streamed"}}`,
			`event: content_block_stop
data: {"index":0}`,
			`event: message_stop
data: {"type":"message_stop"}`,
		} {
			_, _ = io.WriteString(w, event+"\n\n")
		}
	}))
	defer server.Close()

	service, err := New(Config{Provider: "anthropic", BaseURL: server.URL, APIKey: "k", Model: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := service.Stream(context.Background(), "s", "hi")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var text strings.Builder
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(event.Delta)
		if event.Done {
			break
		}
	}
	if text.String() != "streamed" {
		t.Fatalf("text = %q", text.String())
	}

	// The tool schema is passed through unchanged; a provider that rejects it would be caught
	// here rather than in production.
	var payload map[string]any
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &payload)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server2.Close()

	toolService, err := New(Config{Provider: "anthropic", BaseURL: server2.URL, APIKey: "k", Model: "claude-test"},
		WithTools(Tool{Name: "echo", Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			Handler: func(context.Context, json.RawMessage) (any, error) { return "ok", nil }}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toolService.Ask(context.Background(), "s", "hi"); err != nil {
		t.Fatal(err)
	}
	tools := payload["tools"].([]any)
	first := tools[0].(map[string]any)
	if first["name"] != "echo" || first["input_schema"] == nil {
		t.Fatalf("tools = %v", tools)
	}
	if payload["max_tokens"] == nil {
		t.Fatalf("max_tokens missing: %v", payload)
	}
}
