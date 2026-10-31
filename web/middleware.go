package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maxid/glogs"

	gableerrors "github.com/maxid/gable/errors"
)

func (s *Server) requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-Id", id)
		c.Next()
	}
}

func newRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func (s *Server) recoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.mu.RLock()
				logger := s.logger
				s.mu.RUnlock()
				if logger != nil {
					logger.Channel(glogs.ChannelError).Error("panic recovered",
						"panic", recovered,
						"path", c.Request.URL.Path,
						"stack", string(debug.Stack()),
					)
				}
				ctx := &Context{gin: c, server: s}
				s.writeError(ctx, gableerrors.New(gableerrors.CodeInternal, "internal error").WithStatus(500))
			}
		}()
		c.Next()
	}
}

func (s *Server) accessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.mu.RLock()
		logger := s.logger
		s.mu.RUnlock()
		if logger == nil {
			c.Next()
			return
		}

		started := time.Now()
		c.Next()
		logger.Channel(glogs.ChannelAccess).Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(started).Milliseconds(),
			"request_id", c.GetString(requestIDKey),
			"client_ip", c.ClientIP(),
		)
	}
}
