package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const requestIDKey = "gable.request_id"

// Context wraps gin.Context with the framework's binding, response and error
// helpers. Handlers never need to import gin directly.
type Context struct {
	gin    *gin.Context
	server *Server
}

// Gin exposes the underlying gin context for middleware that needs it.
func (c *Context) Gin() *gin.Context { return c.gin }

// Request returns the HTTP request.
func (c *Context) Request() *http.Request { return c.gin.Request }

// Writer returns the response writer.
func (c *Context) Writer() http.ResponseWriter { return c.gin.Writer }

// RequestID returns the request id injected by middleware.
func (c *Context) RequestID() string {
	if value, ok := c.gin.Get(requestIDKey); ok {
		if id, ok := value.(string); ok {
			return id
		}
	}
	return ""
}

// Bind decodes the JSON body into dst and runs validation tags.
func (c *Context) Bind(dst any) error {
	if err := c.gin.ShouldBindJSON(dst); err != nil {
		return err
	}
	return nil
}

// BindQuery decodes query parameters into dst.
func (c *Context) BindQuery(dst any) error { return c.gin.ShouldBindQuery(dst) }

// JSON writes a successful envelope with the given HTTP status.
func (c *Context) JSON(status int, data any) error {
	c.server.writeSuccess(c, status, data)
	return nil
}

// Fail writes the error as an envelope. It is safe to ignore the return value.
func (c *Context) Fail(err error) error {
	c.server.writeError(c, err)
	return nil
}

// NoContent writes an empty 204 response.
func (c *Context) NoContent() error {
	c.gin.Status(http.StatusNoContent)
	return nil
}

// Param returns a path parameter.
func (c *Context) Param(name string) string { return c.gin.Param(name) }

// Query returns a query parameter.
func (c *Context) Query(name string) string { return c.gin.Query(name) }

// DefaultQuery returns a query parameter or fallback.
func (c *Context) DefaultQuery(name, fallback string) string {
	return c.gin.DefaultQuery(name, fallback)
}

// QueryInt returns an integer query parameter.
func (c *Context) QueryInt(name string, fallback int) int {
	raw := c.gin.Query(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

// Header returns a request header.
func (c *Context) Header(name string) string { return c.gin.GetHeader(name) }

// Set stores a value on the request context.
func (c *Context) Set(key string, value any) { c.gin.Set(key, value) }

// Get reads a value stored on the request context.
func (c *Context) Get(key string) (any, bool) { return c.gin.Get(key) }

// Value implements context.Context.
func (c *Context) Value(key any) any { return c.gin.Request.Context().Value(key) }

// Deadline implements context.Context.
func (c *Context) Deadline() (deadline time.Time, ok bool) {
	return c.gin.Request.Context().Deadline()
}

// Done implements context.Context.
func (c *Context) Done() <-chan struct{} { return c.gin.Request.Context().Done() }

// Err implements context.Context.
func (c *Context) Err() error { return c.gin.Request.Context().Err() }

// Abort stops the middleware chain.
func (c *Context) Abort() { c.gin.Abort() }

// AbortWithStatus writes an empty status and aborts.
func (c *Context) AbortWithStatus(status int) { c.gin.AbortWithStatus(status) }

// DecodeJSON decodes raw JSON into dst.
func DecodeJSON(raw []byte, dst any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty JSON body")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

var _ context.Context = (*Context)(nil)
