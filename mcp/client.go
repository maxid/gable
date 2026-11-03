package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/maxid/gmcp"
)

// Endpoint says where an MCP server lives. Exactly one of URL or Command is set.
type Endpoint struct {
	// URL is the server's streamable-HTTP endpoint.
	URL string
	// Command starts a server that speaks MCP over stdio, e.g. ["gable","dsl","mcp","--dir","."].
	Command []string
}

// String renders the endpoint for logs and error messages without leaking arguments that
// might contain a token.
func (e Endpoint) String() string {
	if e.URL != "" {
		return e.URL
	}
	if len(e.Command) > 0 {
		return "stdio:" + e.Command[0]
	}
	return "unconfigured"
}

// Client talks to a remote MCP server.
//
// gable already exposes its own tools; this is the other direction, which a product needs as
// soon as it wants to use somebody else's — an internal MCP server, or one produced by another
// team. The transport choice (HTTP or a stdio subprocess) is the only difference; the tool
// surface is the same.
type Client struct {
	endpoint Endpoint
	http     *gmcp.HTTPClient
	stdio    *gmcp.Client
	process  *exec.Cmd
	extra    []io.Closer
}

// Dial connects to an MCP server.
//
// For stdio the process is started here and its lifetime is the client's: Close kills it, so a
// caller that forgets to close leaks a child process rather than nothing at all. For HTTP
// nothing is opened until the first call.
func Dial(ctx context.Context, endpoint Endpoint) (*Client, error) {
	switch {
	case endpoint.URL != "" && len(endpoint.Command) > 0:
		return nil, fmt.Errorf("mcp: endpoint has both a URL and a command")
	case endpoint.URL != "":
		return &Client{endpoint: endpoint, http: gmcp.NewHTTPClient(endpoint.URL, nil)}, nil
	case len(endpoint.Command) > 0:
		return dialStdio(ctx, endpoint)
	default:
		return nil, fmt.Errorf("mcp: endpoint needs a URL or a command")
	}
}

// dialStdio starts the server and connects to its standard streams.
func dialStdio(ctx context.Context, endpoint Endpoint) (*Client, error) {
	// Not CommandContext: the process must outlive the dial, and be stopped by Close instead.
	process := exec.Command(endpoint.Command[0], endpoint.Command[1:]...)
	stdin, err := process.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderr, err := process.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := process.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", endpoint.Command[0], err)
	}

	client := &Client{endpoint: endpoint, process: process, stdio: gmcp.NewClient(stdout, stdin)}
	// The server's stderr is drained, not printed: an MCP server that logs to stderr must not
	// block on a full pipe because nobody read it.
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := client.stdio.Connect(connectCtx, gmcp.Options{}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("mcp: connect %s: %w", endpoint, err)
	}
	return client, nil
}

// Endpoint returns where this client points.
func (c *Client) Endpoint() Endpoint { return c.endpoint }

// ListTools returns the remote tool list.
func (c *Client) ListTools(ctx context.Context) ([]gmcp.Tool, error) {
	if c.stdio != nil {
		return c.stdio.ListTools(ctx)
	}
	return c.http.ListTools(ctx)
}

// CallTool invokes a remote tool with JSON arguments.
//
// The result is returned as-is: MCP reports a tool failure as IsError on a normal result, so
// the caller keeps the difference between "the tool said no" and "the transport broke".
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (gmcp.CallToolResult, error) {
	if strings.TrimSpace(name) == "" {
		return gmcp.CallToolResult{}, fmt.Errorf("mcp: tool name is required")
	}
	if c.stdio != nil {
		return c.stdio.CallTool(ctx, name, arguments)
	}
	return c.http.CallTool(ctx, name, arguments)
}

// Ping reports whether the server answers a tool listing.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.ListTools(ctx)
	return err
}

// Close releases the connection and, for stdio, the child process.
func (c *Client) Close() error {
	var firstErr error
	if c.stdio != nil {
		if err := c.stdio.Close(); err != nil {
			firstErr = err
		}
	}
	if c.process != nil && c.process.Process != nil {
		// The client closed the streams; give the server a moment, then stop it.
		done := make(chan struct{})
		go func() {
			_ = c.process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = c.process.Process.Kill()
			<-done
		}
	}
	for _, closer := range c.extra {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// HTTPHandler is the server side of the same package, re-exported so an application can mount
// its tools and dial them with the same client in a test.
func HTTPHandler(server *gmcp.Server) http.Handler { return server.HTTPHandler() }

// ResponseText concatenates the textual content of a result, which is what a CLI prints.
func ResponseText(result gmcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if content.Text != "" {
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n")
}
