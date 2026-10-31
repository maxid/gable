package web

import (
	"github.com/gin-gonic/gin"

	"github.com/maxid/gable/tenant"
)

// UseTenant installs tenant resolution middleware.
//
// Call it before registering routes and after the authentication middleware:
// the resolver needs the authenticated principal. A resolution error is
// returned as the framework envelope and stops the chain.
func (s *Server) UseTenant(resolver tenant.Resolver) *Server {
	if resolver == nil {
		return s
	}
	s.engine.Use(func(c *gin.Context) {
		resolved, err := resolver.Resolve(c.Request.Context(), c.Request)
		if err != nil {
			s.writeError(&Context{gin: c, server: s}, err)
			return
		}
		c.Request = c.Request.WithContext(tenant.WithTenant(c.Request.Context(), resolved))
		c.Next()
	})
	return s
}

// Tenant returns the tenant bound to the request.
func (c *Context) Tenant() (tenant.Tenant, bool) {
	return tenant.FromContext(c.Request().Context())
}
