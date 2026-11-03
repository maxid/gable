package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maxid/gai"
)

// streamingProvider replays prepared chunk sequences, one per round.
type streamingProvider struct {
	rounds [][]gai.Chunk
	// failAt makes the given round return an error instead of a stream.
	failAt int
	round  int
	reqs   []gai.Request
}

func (p *streamingProvider) Name() string { return "streaming" }

func (p *streamingProvider) Chat(context.Context, gai.Request) (gai.Response, error) {
	return gai.Response{}, errors.New("not implemented")
}

func (p *streamingProvider) Stream(_ context.Context, request gai.Request) (gai.Stream, error) {
	p.reqs = append(p.reqs, request)
	if p.failAt != 0 && p.round == p.failAt-1 {
		p.round++
		return nil, errors.New("provider exploded")
	}
	chunks := []gai.Chunk{{Done: true}}
	if p.round < len(p.rounds) {
		chunks = p.rounds[p.round]
	}
	p.round++
	return &sliceStream{chunks: chunks}, nil
}

type sliceStream struct{ chunks []gai.Chunk }

func (s *sliceStream) Recv() (gai.Chunk, error) {
	if len(s.chunks) == 0 {
		return gai.Chunk{}, io.EOF
	}
	chunk := s.chunks[0]
	s.chunks = s.chunks[1:]
	return chunk, nil
}

func (s *sliceStream) Close() error { return nil }

func collect(t *testing.T, stream *Stream) ([]Event, error) {
	t.Helper()
	var events []Event
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
}

// A stream is the same bounded tool loop as Ask, with the text delivered as it is generated.
func TestStreamEmitsDeltasAndRunsTools(t *testing.T) {
	provider := &streamingProvider{rounds: [][]gai.Chunk{
		{
			{Delta: "let me "},
			{Delta: "check"},
			{ToolCall: &gai.ToolCall{ID: "c1", Name: "orders.summary", Arguments: json.RawMessage(`{"days":7}`)}},
			// A usage chunk carries only usage, like the real providers do.
			{Usage: &gai.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}},
			{Done: true},
		},
		{
			{Delta: "there are 3 orders"},
			{Done: true},
		},
	}}
	service := newServiceWith(t, provider, Config{}, WithTools(Tool{
		Name:   "orders.summary",
		Schema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]int{"count": 3}, nil
		},
	}))

	stream, err := service.Stream(context.Background(), "session", "how many orders?")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	events, err := collect(t, stream)
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	var toolCall, toolResult bool
	var usage *gai.Usage
	done := false
	for _, event := range events {
		// The Done event also carries the final usage, so it is checked first.
		if event.Done {
			done = true
		}
		switch {
		case event.Delta != "":
			text.WriteString(event.Delta)
		case event.ToolCall != nil:
			toolCall = event.ToolCall.Name == "orders.summary"
		case event.ToolResult != nil:
			toolResult = event.ToolResult.Name == "orders.summary" && strings.Contains(event.ToolResult.Output, `"count":3`)
		case event.Usage != nil:
			usage = event.Usage
		}
	}
	if text.String() != "let me checkthere are 3 orders" {
		t.Fatalf("text = %q", text.String())
	}
	if !toolCall || !toolResult {
		t.Fatalf("tool events missing: %+v", events)
	}
	if !done {
		t.Fatalf("no done event: %+v", events)
	}
	if usage == nil || usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", usage)
	}
	// The second round saw the tool result, which is what makes the loop a conversation.
	second := provider.reqs[1].Messages
	last := second[len(second)-1]
	if last.Role != gai.RoleTool || last.ToolCallID != "c1" || !strings.Contains(last.Content, `"count":3`) {
		t.Fatalf("tool message = %+v", last)
	}
}

// A tool that fails is reported to the model, not raised at the caller.
func TestStreamReportsToolFailureAsContent(t *testing.T) {
	provider := &streamingProvider{rounds: [][]gai.Chunk{
		{
			{ToolCall: &gai.ToolCall{ID: "c1", Name: "broken", Arguments: json.RawMessage(`{}`)}},
			{Done: true},
		},
		{{Delta: "sorry"}, {Done: true}},
	}}
	service := newServiceWith(t, provider, Config{}, WithTools(Tool{
		Name:   "broken",
		Schema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, errors.New("upstream down")
		},
	}))

	stream, err := service.Stream(context.Background(), "s", "go")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	events, err := collect(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	var result *ToolResult
	for _, event := range events {
		if event.ToolResult != nil {
			result = event.ToolResult
		}
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "upstream down") {
		t.Fatalf("result = %+v", result)
	}
	// The model's next message saw the failure and continued.
	last := provider.reqs[1].Messages[len(provider.reqs[1].Messages)-1]
	if last.Role != gai.RoleTool || !strings.Contains(last.Content, "upstream down") {
		t.Fatalf("tool message = %+v", last)
	}
}

func TestStreamProviderErrorSurfacesThroughRecv(t *testing.T) {
	provider := &streamingProvider{failAt: 1}
	service := newServiceWith(t, provider, Config{})
	stream, err := service.Stream(context.Background(), "s", "go")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := collect(t, stream); err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("err = %v", err)
	}

	if _, err := service.Stream(context.Background(), "", "go"); err == nil {
		t.Fatal("an empty session must be refused")
	}
}

// Close cancels the work: a caller that stops reading must not leave the provider call running.
func TestStreamCloseCancels(t *testing.T) {
	service := newServiceWith(t, &streamingProvider{}, Config{})
	stream, err := service.Stream(context.Background(), "s", "go")
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Recv did not stop after Close")
	}
}

// SSE needs the headers, a flush per event and a [DONE] sentinel; without the flush the answer
// arrives in one lump when the connection closes.
func TestServeSSEWritesFrames(t *testing.T) {
	provider := &streamingProvider{rounds: [][]gai.Chunk{{
		{Delta: "hello"},
		{Done: true},
	}}}
	service := newServiceWith(t, provider, Config{})
	stream, err := service.Stream(context.Background(), "s", "hi")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	recorder := httptest.NewRecorder()
	if err := stream.ServeSSE(recorder); err != nil {
		t.Fatal(err)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("content type = %q", contentType)
	}
	if buffering := recorder.Header().Get("X-Accel-Buffering"); buffering != "no" {
		t.Fatalf("buffering header = %q", buffering)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"delta":"hello"`) {
		t.Fatalf("body = %q", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("body does not end with the sentinel: %q", body)
	}
	if !recorder.Flushed {
		t.Fatal("the response was never flushed")
	}

	// Every data frame is valid JSON (or the sentinel).
	for _, frame := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		payload := strings.TrimPrefix(frame, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("frame %q is not JSON: %v", payload, err)
		}
	}
}
