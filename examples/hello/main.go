package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

type helloResponse struct {
	Message string `json:"message"`
}

type helloController struct{}

func (helloController) Routes(r *web.Router) {
	r.GET("/hello/:name", func(c *web.Context) error {
		return c.JSON(200, helloResponse{Message: "hello " + c.Param("name")})
	}).Doc("say hello").Out(helloResponse{})

	r.GET("/whoami", func(c *web.Context) error {
		resolved, _ := c.Tenant()
		return c.JSON(200, map[string]any{"tenantId": resolved.ID, "tenantCode": resolved.Code})
	}).Doc("current tenant")
}

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	app := gable.New(gable.WithConfig(cfg))

	// One registry for probes, one for metrics: the App owns them, modules add to them,
	// and the server mounts them at /healthz, /readyz and /metrics.
	app.Health().Add("database", true, func(context.Context) error { return nil }) // replace with a real ping

	server := web.New(
		web.WithTitle("hello"),
		web.WithVersion(gable.Version),
		web.WithDescription("gable example application"),
		web.WithHealth(app.Health()),
		web.WithMetrics(app.Metrics()),
	)
	server.UseTenant(tenant.NewSingleResolver(cfg.Tenant))
	server.Register(helloController{})

	if err := app.Register(web.NewModule(server, "")); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
