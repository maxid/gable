package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/maxid/gai"

	"github.com/maxid/gable/rpc"
)

// Event is one step of a streamed answer.
//
// A streamed answer is not just text: the model may ask for tools between deltas, and the
// caller usually wants to show that ("calling orders.summary…") instead of a frozen cursor.
// Done is the last event; an error arrives through Recv, not as an event, so a caller cannot
// mistake a failure for a short answer.
type Event struct {
	// Delta is newly generated text.
	Delta string `json:"delta,omitempty"`
	// ToolCall reports a tool the model requested; ToolResult follows once it ran.
	ToolCall *gai.ToolCall `json:"toolCall,omitempty"`
	// ToolResult is the output (or the error text) of the tool named in Name.
	ToolResult *ToolResult `json:"toolResult,omitempty"`
	// Usage accumulates across the rounds of one answer.
	Usage *gai.Usage `json:"usage,omitempty"`
	// Done marks the end of the stream.
	Done bool `json:"done,omitempty"`
}

// ToolResult is one executed tool call.
type ToolResult struct {
	Name    string `json:"name"`
	Output  string `json:"output"`
	IsError bool   `json:"isError,omitempty"`
}

// Stream is a server-sent-event friendly view of an answer in progress.
//
// Recv returns events until Done, then io.EOF. Close cancels the provider request; a caller that
// stops reading must Close, or the request sits open until its timeout.
type Stream struct {
	events chan Event
	cancel context.CancelFunc
	closed chan struct{}
	err    error
}

// Stream sends one user message and streams the answer, running tools between rounds.
//
// It is Ask with the answer turned inside out: the same bounded tool loop and the same history,
// but the text arrives as it is generated instead of after the last round.
func (s *Service) Stream(ctx context.Context, sessionID, prompt string) (*Stream, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session id is required", rpc.ErrPermanent)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &Stream{events: make(chan Event, 16), cancel: cancel, closed: make(chan struct{})}
	go func() {
		defer close(stream.events)
		stream.err = s.runStream(streamCtx, sessionID, prompt, stream.events)
	}()
	return stream, nil
}

// Recv returns the next event, then io.EOF after the Done event.
//
// Done is a normal event and the next call reports io.EOF: folding "the last event" into the
// error would make a caller that checks the error first silently drop the end of the answer.
func (s *Stream) Recv() (Event, error) {
	event, ok := <-s.events
	if !ok {
		if s.err != nil {
			return Event{}, s.err
		}
		return Event{}, io.EOF
	}
	return event, nil
}

// Close cancels the provider request. It is safe to call more than once and after io.EOF.
func (s *Stream) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	s.cancel()
	return nil
}

// runStream is the producer: it runs rounds until the model stops asking for tools.
func (s *Service) runStream(ctx context.Context, sessionID, prompt string, out chan<- Event) error {
	messages := s.history.get(sessionID)
	messages = append(messages, gai.Message{Role: gai.RoleUser, Content: prompt})

	usage := gai.Usage{}
	for iteration := 1; iteration <= s.cfg.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		round, err := s.streamRound(ctx, messages, out, &usage)
		if err != nil {
			return err
		}
		messages = round.messages

		if len(round.toolCalls) == 0 {
			s.history.set(sessionID, messages)
			send(ctx, out, Event{Usage: &usage, Done: true})
			return nil
		}
	}
	s.history.set(sessionID, messages)
	return fmt.Errorf("%w: exceeded %d tool rounds", rpc.ErrPermanent, s.cfg.MaxIterations)
}

// round is one model call: its text, its tool calls, and the messages it added.
type round struct {
	messages  []gai.Message
	toolCalls []gai.ToolCall
}

// streamRound streams one model call and, when it asks for tools, runs them.
func (s *Service) streamRound(ctx context.Context, messages []gai.Message, out chan<- Event, usage *gai.Usage) (round, error) {
	stream, err := s.provider.Stream(ctx, gai.Request{
		Model:    s.cfg.Model,
		Messages: s.buildMessages(messages),
		Tools:    s.toolset.Tools(),
	})
	if err != nil {
		return round{}, err
	}
	defer stream.Close()

	var text string
	var calls []gai.ToolCall
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return round{}, err
		}
		switch {
		case chunk.ToolCall != nil:
			calls = append(calls, *chunk.ToolCall)
			send(ctx, out, Event{ToolCall: chunk.ToolCall})
		case chunk.Delta != "":
			text += chunk.Delta
			send(ctx, out, Event{Delta: chunk.Delta})
		}
		if chunk.Usage != nil {
			usage.PromptTokens += chunk.Usage.PromptTokens
			usage.CompletionTokens += chunk.Usage.CompletionTokens
			usage.TotalTokens += chunk.Usage.TotalTokens
			send(ctx, out, Event{Usage: usage})
		}
		if chunk.Done {
			break
		}
	}

	messages = append(messages, gai.Message{Role: gai.RoleAssistant, Content: text, ToolCalls: calls})
	if len(calls) == 0 {
		return round{messages: messages}, nil
	}

	for _, call := range calls {
		result := s.runTool(ctx, call)
		send(ctx, out, Event{ToolResult: &result})
		messages = append(messages, gai.Message{
			Role:       gai.RoleTool,
			Content:    result.Output,
			ToolCallID: call.ID,
			Name:       call.Name,
		})
	}
	return round{messages: messages, toolCalls: calls}, nil
}

// runTool executes one call, turning its failure into text for the model.
func (s *Service) runTool(ctx context.Context, call gai.ToolCall) ToolResult {
	output, err := s.toolset.Call(ctx, call.Name, call.Arguments)
	if err != nil {
		// A failing tool is information for the model, not a reason to abort the answer;
		// the same rule the non-streaming loop follows.
		return ToolResult{Name: call.Name, Output: "error: " + err.Error(), IsError: true}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return ToolResult{Name: call.Name, Output: "error: " + err.Error(), IsError: true}
	}
	return ToolResult{Name: call.Name, Output: string(encoded)}
}

// send delivers an event unless the consumer is gone, which is how a cancelled stream stops
// without a goroutine leak.
func send(ctx context.Context, out chan<- Event, event Event) {
	select {
	case out <- event:
	case <-ctx.Done():
	}
}
