package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// adapted joins a platform-shaped module to gable.
//
// It is the whole cost of adoption: narrow the container back to the pilot's type, forward the
// four methods and the optional dependencies. Nothing in the module changes, which is the claim
// the pilot exists to test — and the reason this lives in the wiring, once, instead of in every
// module.
type adapted struct{ module Module }

func (a adapted) ID() string                        { return a.module.ID() }
func (a adapted) Version() string                   { return a.module.Version() }
func (a adapted) DependsOn() []string               { return a.module.DependsOn() }
func (a adapted) Register(c *gable.Container) error { return a.module.Register(newContainer(c)) }
func (a adapted) Start(ctx context.Context) error   { return a.module.Start(ctx) }
func (a adapted) Stop(ctx context.Context) error    { return a.module.Stop(ctx) }

func adapt(module Module) gable.Module { return adapted{module: module} }

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	app := gable.New(gable.WithConfig(cfg))

	server := web.New(
		web.WithTitle("gable admin pilot"),
		web.WithVersion(gable.Version),
		web.WithDescription("admin users and roles, wired by modules that keep the platform shape"),
		// The process-wide registries reach the web server right here: one probe list, one
		// scrape target, mounted at /healthz, /readyz and /metrics.
		web.WithHealth(app.Health()),
		web.WithMetrics(app.Metrics()),
	)
	server.UseTenant(tenant.NewSingleResolver(cfg.Tenant))

	modules := []Module{
		NewUsersModule(),
		NewRolesModule(),
		NewAdminModule(server),
	}
	for _, module := range modules {
		if err := app.Register(adapt(module)); err != nil {
			log.Fatal(err)
		}
	}
	// The web server itself is a native gable module; mixing the two kinds in one assembly
	// is the normal case, not a special one.
	if err := app.Register(web.NewModule(server, "")); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// /status is registered before Start so it exists as soon as the listener does; the
	// manifest it returns is filled by Start.
	server.Router().GET("/status", func(c *web.Context) error {
		report := app.Health().Ready(c.Request().Context())
		return c.JSON(http.StatusOK, map[string]any{
			"status":   report.Status,
			"probes":   report.Checks,
			"manifest": app.Manifest(),
		})
	}).Doc("assembly manifest and readiness").Tags("ops")

	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
