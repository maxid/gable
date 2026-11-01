package ai

import (
	"sync"
	"time"

	"github.com/maxid/gai"
)

// history keeps one conversation per session.
//
// It is bounded twice on purpose: an unbounded message list is a slow memory leak, and
// an unbounded session map is a fast one. Expired sessions are reclaimed on access and
// by a sweep, so a process that has been up for weeks does not carry dead
// conversations.
type history struct {
	maxMessages int
	ttl         time.Duration
	now         func() time.Time

	mu       sync.Mutex
	sessions map[string]*conversation
}

type conversation struct {
	messages []gai.Message
	updated  time.Time
}

func newHistory(maxMessages int, ttl time.Duration) *history {
	return &history{
		maxMessages: maxMessages,
		ttl:         ttl,
		now:         time.Now,
		sessions:    map[string]*conversation{},
	}
}

// get returns a copy of the session's messages.
func (h *history) get(sessionID string) []gai.Message {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.sessions[sessionID]
	if !ok || h.expired(entry) {
		delete(h.sessions, sessionID)
		return nil
	}
	out := make([]gai.Message, len(entry.messages))
	copy(out, entry.messages)
	return out
}

// set replaces the session's messages, trimming the oldest so the bound holds.
//
// Trimming from the front can drop a tool call while keeping its tool result, which
// some providers reject. The window is therefore aligned to a user message: history
// starts at the first user turn inside the bound.
func (h *history) set(sessionID string, messages []gai.Message) {
	if len(messages) > h.maxMessages {
		start := len(messages) - h.maxMessages
		for start < len(messages) && messages[start].Role != gai.RoleUser {
			start++
		}
		if start < len(messages) {
			messages = messages[start:]
		} else {
			messages = messages[len(messages)-h.maxMessages:]
		}
	}

	trimmed := make([]gai.Message, len(messages))
	copy(trimmed, messages)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[sessionID] = &conversation{messages: trimmed, updated: h.now()}
}

// reset forgets one session.
func (h *history) reset(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, sessionID)
}

// sweep drops expired sessions; a caller may run it periodically.
func (h *history) sweep() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	removed := 0
	for id, entry := range h.sessions {
		if h.expired(entry) {
			delete(h.sessions, id)
			removed++
		}
	}
	return removed
}

// size reports the live session count.
func (h *history) size() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

func (h *history) expired(entry *conversation) bool {
	return h.now().Sub(entry.updated) > h.ttl
}
