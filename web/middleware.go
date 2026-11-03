package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maxid/glogs"

	"github.com/maxid/gable/metrics"

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

		registry := s.opts.metrics
		var inflight *metrics.Gauge
		if registry != nil {
			inflight = registry.Gauge("http_inflight_requests", "Requests currently being served.")
			inflight.Inc()
			defer inflight.Add(-1)
		}

		started := time.Now()
		c.Next()
		elapsed := time.Since(started)
		s.recordRequest(c, elapsed)

		if logger == nil {
			return
		}
		logger.Channel(glogs.ChannelAccess).Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", elapsed.Milliseconds(),
			"request_id", c.GetString(requestIDKey),
			"client_ip", c.ClientIP(),
		)
	}
}

// recordRequest counts a request and records its latency.
//
// The route is the metric label, not the path: a path with an id in it would create one
// series per row and take the scraper down with it.
func (s *Server) recordRequest(c *gin.Context, elapsed time.Duration) {
	registry := s.opts.metrics
	if registry == nil {
		return
	}
	route := c.FullPath()
	if route == "" {
		route = "unmatched"
	}
	registry.Counter("http_requests_total", "HTTP requests served.",
		"method", "route", "status").With(c.Request.Method, route, strconv.Itoa(c.Writer.Status())).Inc()
	registry.Histogram("http_request_duration_seconds", "HTTP request latency in seconds.",
		metrics.DefaultBuckets, "method", "route").With(c.Request.Method, route).Observe(elapsed.Seconds())
}
