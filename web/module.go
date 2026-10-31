package web

import (
	"context"
	"fmt"

	"github.com/maxid/glogs"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
)

// Module wires a Server into a gable application.
type Module struct {
	Server *Server
	Addr   string
}

// NewModule returns a web module. An empty Addr uses config.HTTP.Addr.
func NewModule(server *Server, addr string) *Module {
	return &Module{Server: server, Addr: addr}
}

// ID implements gable.Module.
func (m *Module) ID() string { return "web" }

// Register resolves configuration and the logger from the application container.
func (m *Module) Register(c *gable.Container) error {
	if m.Server == nil {
		return fmt.Errorf("web: module requires a server")
	}
	if m.Addr == "" {
		if cfg, ok := gable.Resolve[*config.Config](c, "gable.config"); ok {
			m.Addr = cfg.HTTP.Addr
		}
	}
	if logger, ok := gable.Resolve[*glogs.Logger](c, "gable.logger"); ok {
		m.Server.SetLogger(logger)
	}
	c.Set("web.server", m.Server)
	return nil
}

// Start implements gable.Module.
func (m *Module) Start(ctx context.Context) error { return m.Server.Start(ctx, m.Addr) }

// Stop implements gable.Module.
func (m *Module) Stop(ctx context.Context) error { return m.Server.Stop(ctx) }
