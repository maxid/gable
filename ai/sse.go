package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ServeSSE writes the stream as server-sent events.
//
// SSE is the shape every browser has a native reader for, and it needs three things the
// framework should not leave to each application: the headers, a flush per event (without one
// the answer arrives in one lump when the connection closes), and a `[DONE]` sentinel the client
// can rely on. Each event is a JSON object of Event.
func (s *Stream) ServeSSE(w http.ResponseWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("ai: the response writer does not support flushing; SSE would buffer")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Proxies that buffer responses would defeat streaming entirely.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	encoder := json.NewEncoder(w)
	for {
		event, err := s.Recv()
		if err == io.EOF {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil
		}
		if err != nil {
			// The status line is already sent, so the error travels as an event; a client that
			// only reads data frames still sees why the answer stopped.
			payload := map[string]string{"error": err.Error()}
			raw, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", raw)
			flusher.Flush()
			return err
		}
		if _, err := io.WriteString(w, "data: "); err != nil {
			return err
		}
		if err := encoder.Encode(event); err != nil {
			return err
		}
		// Encode writes the newline; the blank line ends the frame.
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		flusher.Flush()
	}
}
