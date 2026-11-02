package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	gableerrors "github.com/maxid/gable/errors"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// Middleware verifies the bearer token and stores the principal in the request
// context. Install it on protected routes only.
func (h *Handlers) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := h.accessToken(c.Request)
		if token == "" {
			abortUnauthorized(c, ErrMissingToken)
			return
		}
		principal, err := h.Service.Verify(c.Request.Context(), token)
		if err != nil {
			abortUnauthorized(c, err)
			return
		}
		c.Request = c.Request.WithContext(WithPrincipal(c.Request.Context(), principal))
		c.Next()
	}
}

func abortUnauthorized(c *gin.Context, err error) {
	status := http.StatusUnauthorized
	code := "unauthorized"
	message := "unauthorized"
	var framework *gableerrors.Error
	if errors.As(err, &framework) {
		code = framework.Code
		message = framework.Message
		if framework.Status != 0 {
			status = framework.Status
		}
	}
	c.AbortWithStatusJSON(status, web.Envelope{
		Code:      code,
		Message:   message,
		Success:   false,
		Timestamp: time.Now().UnixMilli(),
	})
}

// TenantSource adapts the authenticated principal to tenant resolution.
func TenantSource() tenant.Source {
	return tenant.SourceFunc(func(ctx context.Context, _ *http.Request) (tenant.Principal, bool, error) {
		principal, ok := PrincipalFromContext(ctx)
		if !ok || principal.TenantID <= 0 {
			return tenant.Principal{}, false, nil
		}
		return tenant.Principal{TenantID: principal.TenantID, Operator: principal.Operator}, true, nil
	})
}
