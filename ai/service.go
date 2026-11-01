// Package ai runs a conversation against a model, executing the tools it asks for.
//
// It is deliberately a thin loop over gai: the provider, the tool definitions and the
// message encoding all belong to gai; this package owns the part every application
// would otherwise rewrite — the tool-call round trip, a bound on how many rounds a
// model may take, and conversation history that cannot grow without limit.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maxid/gai"

	"github.com/maxid/gable/rpc"
)

// Config configures the service.
type Config struct {
	// Provider names the backend; only "openai" (OpenAI-compatible) exists today.
	Provider string
	// BaseURL points at the API root, e.g. https://api.openai.com/v1.
	BaseURL string
	// APIKey authenticates the provider.
	APIKey string
	// Model is the default model.
	Model string
	// SystemPrompt is prepended to every conversation.
	SystemPrompt string
	// MaxIterations bounds tool-call rounds. Zero uses DefaultMaxIterations: an
	// unbounded loop turns a confused model into an infinite bill.
	MaxIterations int
	// MaxHistory bounds the stored conversation, in messages.
	MaxHistory int
	// HistoryTTL expires idle conversations. Zero means DefaultHistoryTTL.
	HistoryTTL time.Duration
	// RequestTimeout bounds one provider call.
	RequestTimeout time.Duration
}

// Defaults applied by New.
const (
	DefaultMaxIterations = 8
	DefaultMaxHistory    = 40
	DefaultHistoryTTL    = 30 * time.Minute
)

// Tool is one callable function exposed to the model.
//
// It mirrors gai.Tool so a caller can register the same handler it exposes elsewhere
// (an MCP tool, an HTTP operation) without an adapter type.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Handler     func(ctx context.Context, arguments json.RawMessage) (any, error)
}

// Result is one completed answer.
type Result struct {
	Text string
	// ToolsUsed lists the tools that ran, in order, so a caller can show what the
	// model did rather than only what it said.
	ToolsUsed []string
	// Iterations counts model calls, including tool rounds.
	Iterations int
	Usage      gai.Usage
}

// Service runs conversations.
type Service struct {
	cfg      Config
	provider gai.Provider
	toolset  *gai.Toolset
	history  *history
}

// Option configures a Service.
type Option func(*Service)

// providerFactory lets tests substitute the backend.
type providerFactory func(Config) (gai.Provider, error)

// WithTools registers tools. Duplicate names fail at construction, not at call time.
func WithTools(tools ...Tool) Option {
	return func(s *Service) {
		for _, tool := range tools {
			_ = s.toolset.Register(gai.Tool{
				Name:        tool.Name,
				Description: tool.Description,
				Schema:      tool.Schema,
				Handler:     tool.Handler,
			})
		}
	}
}

// New builds the service. The provider is selected by name; an unknown name fails
// loudly instead of silently falling back to a default vendor.
func New(cfg Config, opts ...Option) (*Service, error) {
	return newService(cfg, nil, opts...)
}

func newService(cfg Config, factory providerFactory, opts ...Option) (*Service, error) {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = DefaultMaxIterations
	}
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = DefaultMaxHistory
	}
	if cfg.HistoryTTL <= 0 {
		cfg.HistoryTTL = DefaultHistoryTTL
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("%w: model is required", rpc.ErrPermanent)
	}

	service := &Service{
		cfg:     cfg,
		toolset: gai.NewToolset(),
		history: newHistory(cfg.MaxHistory, cfg.HistoryTTL),
	}
	for _, o := range opts {
		o(service)
	}

	if factory != nil {
		provider, err := factory(cfg)
		if err != nil {
			return nil, err
		}
		service.provider = provider
		return service, nil
	}

	provider, err := defaultProvider(cfg)
	if err != nil {
		return nil, err
	}
	service.provider = provider
	return service, nil
}

// Ask sends one user message and returns the model's final answer, running any tools
// it requested in between.
func (s *Service) Ask(ctx context.Context, sessionID, prompt string) (Result, error) {
	if sessionID == "" {
		return Result{}, fmt.Errorf("%w: session id is required", rpc.ErrPermanent)
	}

	messages := s.history.get(sessionID)
	messages = append(messages, gai.Message{Role: gai.RoleUser, Content: prompt})

	result := Result{}
	for iteration := 1; iteration <= s.cfg.MaxIterations; iteration++ {
		response, err := s.provider.Chat(ctx, gai.Request{
			Model:    s.cfg.Model,
			Messages: s.buildMessages(messages),
			Tools:    s.toolset.Tools(),
		})
		if err != nil {
			return Result{}, err
		}
		result.Iterations = iteration
		result.Usage.PromptTokens += response.Usage.PromptTokens
		result.Usage.CompletionTokens += response.Usage.CompletionTokens
		result.Usage.TotalTokens += response.Usage.TotalTokens

		// No tool calls: this is the answer.
		if len(response.Message.ToolCalls) == 0 {
			messages = append(messages, gai.Message{Role: gai.RoleAssistant, Content: response.Message.Content})
			s.history.set(sessionID, messages)
			result.Text = response.Message.Content
			return result, nil
		}

		// Record what the model asked for, then answer each call with a tool message.
		messages = append(messages, gai.Message{
			Role:      gai.RoleAssistant,
			Content:   response.Message.Content,
			ToolCalls: response.Message.ToolCalls,
		})
		for _, call := range response.Message.ToolCalls {
			result.ToolsUsed = append(result.ToolsUsed, call.Name)
			output, err := s.toolset.Call(ctx, call.Name, call.Arguments)
			content := ""
			if err != nil {
				// A failing tool is information for the model, not a reason to abort the
				// conversation: it can retry with different arguments.
				content = "error: " + err.Error()
			} else {
				encoded, marshalErr := json.Marshal(output)
				if marshalErr != nil {
					content = "error: " + marshalErr.Error()
				} else {
					content = string(encoded)
				}
			}
			messages = append(messages, gai.Message{
				Role:       gai.RoleTool,
				Content:    content,
				ToolCallID: call.ID,
				Name:       call.Name,
			})
		}
	}

	// The model kept asking for tools. Return what it has rather than looping forever;
	// the caller sees the truncation through Iterations.
	s.history.set(sessionID, messages)
	return result, fmt.Errorf("%w: exceeded %d tool rounds", rpc.ErrPermanent, s.cfg.MaxIterations)
}

// Reset forgets a conversation.
func (s *Service) Reset(sessionID string) { s.history.reset(sessionID) }

// buildMessages prepends the system prompt.
func (s *Service) buildMessages(messages []gai.Message) []gai.Message {
	if s.cfg.SystemPrompt == "" {
		return messages
	}
	out := make([]gai.Message, 0, len(messages)+1)
	out = append(out, gai.Message{Role: gai.RoleSystem, Content: s.cfg.SystemPrompt})
	return append(out, messages...)
}
