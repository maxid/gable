package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maxid/gai"

	"github.com/maxid/gable/rpc"
)

// scriptedProvider replays prepared responses so the tool loop can be tested without a
// model.
type scriptedProvider struct {
	responses []gai.Response
	calls     []gai.Request
	err       error
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Chat(_ context.Context, request gai.Request) (gai.Response, error) {
	p.calls = append(p.calls, request)
	if p.err != nil {
		return gai.Response{}, p.err
	}
	if len(p.responses) == 0 {
		return gai.Response{Message: gai.Message{Role: gai.RoleAssistant, Content: "done"}}, nil
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}

func (p *scriptedProvider) Stream(context.Context, gai.Request) (gai.Stream, error) {
	return nil, errors.New("not implemented")
}

func newServiceWith(t *testing.T, provider gai.Provider, cfg Config, opts ...Option) *Service {
	t.Helper()
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	service, err := newService(cfg, func(Config) (gai.Provider, error) { return provider, nil }, opts...)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return service
}

// A tool call is executed and its result is fed back; the model's next answer is the
// one returned.
func TestAskRunsToolCalls(t *testing.T) {
	provider := &scriptedProvider{responses: []gai.Response{
		{
			Message: gai.Message{Role: gai.RoleAssistant, ToolCalls: []gai.ToolCall{{
				ID:        "call-1",
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"42"}`),
			}}},
		},
		{Message: gai.Message{Role: gai.RoleAssistant, Content: "the answer is 42"}},
	}}

	var gotArguments string
	service := newServiceWith(t, provider, Config{},
		WithTools(Tool{
			Name:        "lookup",
			Description: "look something up",
			Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			Handler: func(_ context.Context, arguments json.RawMessage) (any, error) {
				gotArguments = string(arguments)
				return map[string]string{"value": "42"}, nil
			},
		}),
	)

	result, err := service.Ask(context.Background(), "session-1", "what is the answer?")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}

	if result.Text != "the answer is 42" {
		t.Fatalf("text = %q", result.Text)
	}
	if gotArguments != `{"q":"42"}` {
		t.Fatalf("tool arguments = %q", gotArguments)
	}
	if len(result.ToolsUsed) != 1 || result.ToolsUsed[0] != "lookup" {
		t.Fatalf("tools used = %v", result.ToolsUsed)
	}
	if result.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", result.Iterations)
	}

	// The second model call must carry the tool result, otherwise the model cannot
	// answer from it.
	second := provider.calls[1]
	last := second.Messages[len(second.Messages)-1]
	if last.Role != gai.RoleTool || last.ToolCallID != "call-1" || !strings.Contains(last.Content, "42") {
		t.Fatalf("tool message = %+v", last)
	}
}

// A failing tool is reported to the model rather than aborting the conversation.
func TestAskFeedsToolErrorsBack(t *testing.T) {
	provider := &scriptedProvider{responses: []gai.Response{
		{Message: gai.Message{Role: gai.RoleAssistant, ToolCalls: []gai.ToolCall{{ID: "c", Name: "broken"}}}},
		{Message: gai.Message{Role: gai.RoleAssistant, Content: "the tool failed"}},
	}}

	service := newServiceWith(t, provider, Config{}, WithTools(Tool{
		Name: "broken",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, errors.New("disk on fire")
		},
	}))

	result, err := service.Ask(context.Background(), "s", "go")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if result.Text != "the tool failed" {
		t.Fatalf("text = %q", result.Text)
	}
	last := provider.calls[1].Messages[len(provider.calls[1].Messages)-1]
	if !strings.Contains(last.Content, "disk on fire") {
		t.Fatalf("tool message = %q, want the error text", last.Content)
	}
}

// A model that keeps asking for tools must not loop forever.
func TestAskBoundsToolRounds(t *testing.T) {
	provider := &scriptedProvider{}
	for i := 0; i < 20; i++ {
		provider.responses = append(provider.responses, gai.Response{
			Message: gai.Message{Role: gai.RoleAssistant, ToolCalls: []gai.ToolCall{{ID: fmt.Sprint(i), Name: "noop"}}},
		})
	}

	service := newServiceWith(t, provider, Config{MaxIterations: 3}, WithTools(Tool{
		Name:    "noop",
		Handler: func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
	}))

	_, err := service.Ask(context.Background(), "s", "loop")
	if !errors.Is(err, rpc.ErrPermanent) || !strings.Contains(err.Error(), "tool rounds") {
		t.Fatalf("err = %v, want the iteration bound", err)
	}
	if len(provider.calls) != 3 {
		t.Fatalf("model calls = %d, want 3", len(provider.calls))
	}
}

// History is carried across turns, so the model sees the conversation.
func TestAskKeepsHistory(t *testing.T) {
	provider := &scriptedProvider{}
	service := newServiceWith(t, provider, Config{})

	if _, err := service.Ask(context.Background(), "s", "first"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := service.Ask(context.Background(), "s", "second"); err != nil {
		t.Fatalf("second: %v", err)
	}

	second := provider.calls[1]
	if len(second.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, assistant, user)", len(second.Messages))
	}
	if second.Messages[0].Content != "first" || second.Messages[2].Content != "second" {
		t.Fatalf("messages = %+v", second.Messages)
	}

	// Resetting drops it.
	service.Reset("s")
	if _, err := service.Ask(context.Background(), "s", "third"); err != nil {
		t.Fatalf("third: %v", err)
	}
	if got := len(provider.calls[2].Messages); got != 1 {
		t.Fatalf("messages after reset = %d, want 1", got)
	}
}

func TestHistoryIsBoundedAndStartsAtAUserTurn(t *testing.T) {
	h := newHistory(4, time.Minute)

	messages := []gai.Message{
		{Role: gai.RoleUser, Content: "u1"},
		{Role: gai.RoleAssistant, ToolCalls: []gai.ToolCall{{ID: "c", Name: "t"}}},
		{Role: gai.RoleTool, Content: "r", ToolCallID: "c"},
		{Role: gai.RoleAssistant, Content: "a1"},
		{Role: gai.RoleUser, Content: "u2"},
		{Role: gai.RoleAssistant, Content: "a2"},
	}
	h.set("s", messages)

	stored := h.get("s")
	if len(stored) > 4 {
		t.Fatalf("messages = %d, want at most 4", len(stored))
	}
	// Trimming must not start mid tool exchange.
	if stored[0].Role != gai.RoleUser {
		t.Fatalf("first message = %+v, want a user turn", stored[0])
	}
}

func TestHistoryExpires(t *testing.T) {
	h := newHistory(10, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	h.now = func() time.Time { return now }

	h.set("s", []gai.Message{{Role: gai.RoleUser, Content: "hi"}})
	if h.size() != 1 {
		t.Fatalf("sessions = %d", h.size())
	}

	now = now.Add(2 * time.Minute)
	if got := h.get("s"); got != nil {
		t.Fatalf("expired session returned %v", got)
	}
	if h.size() != 0 {
		t.Fatalf("sessions after sweep = %d", h.size())
	}
}

func TestSystemPromptIsPrepended(t *testing.T) {
	provider := &scriptedProvider{}
	service := newServiceWith(t, provider, Config{SystemPrompt: "be terse"})

	if _, err := service.Ask(context.Background(), "s", "hi"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	first := provider.calls[0].Messages[0]
	if first.Role != gai.RoleSystem || first.Content != "be terse" {
		t.Fatalf("first message = %+v", first)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, rpc.ErrPermanent) {
		t.Fatalf("missing model err = %v", err)
	}
	if _, err := New(Config{Model: "m", Provider: "nope"}); !errors.Is(err, rpc.ErrPermanent) {
		t.Fatalf("unknown provider err = %v", err)
	}
	if _, err := New(Config{Model: "m"}); err != nil {
		t.Fatalf("openai default must build: %v", err)
	}
}

func TestAskRequiresSession(t *testing.T) {
	service := newServiceWith(t, &scriptedProvider{}, Config{})
	if _, err := service.Ask(context.Background(), "", "hi"); !errors.Is(err, rpc.ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
}
