package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

type publicController struct {
	login *tenant.SaaSLoginResolver
}

func (c publicController) Routes(r *web.Router) {
	r.GET("/login/options", func(ctx *web.Context) error {
		resolved, err := c.login.ResolveLogin(ctx.Request().Context(), ctx.Request().Host, ctx.Query("tenantCode"))
		if err != nil {
			return err
		}
		return ctx.JSON(200, map[string]any{"tenantId": resolved.ID, "tenantCode": resolved.Code})
	}).Doc("resolve the login tenant from the host")
}

type tenantController struct{}

func (tenantController) Routes(r *web.Router) {
	r.GET("/whoami", func(ctx *web.Context) error {
		resolved, ok := ctx.Tenant()
		if !ok {
			return tenant.ErrRequired
		}
		return ctx.JSON(200, map[string]any{"tenantId": resolved.ID, "tenantCode": resolved.Code})
	}).Doc("current tenant")
}

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	store := tenant.NewMemoryStore(
		tenant.Tenant{ID: 1, Code: "management"},
		tenant.Tenant{ID: 2, Code: "acme"},
		tenant.Tenant{ID: 3, Code: "beta"},
	)
	login, err := tenant.NewSaaSLoginResolver(cfg.Tenant, store)
	if err != nil {
		log.Fatal(err)
	}

	// Demo-only source: a real application reads the principal from its auth
	// middleware or token, never from client-controlled headers.
	source := tenant.SourceFunc(func(_ context.Context, request *http.Request) (tenant.Principal, bool, error) {
		raw := request.Header.Get("X-User-Tenant")
		if raw == "" {
			return tenant.Principal{}, false, nil
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return tenant.Principal{}, false, nil
		}
		return tenant.Principal{TenantID: id, Operator: request.Header.Get("X-Operator") == "1"}, true, nil
	})
	resolver, err := tenant.NewSaaSResolver(cfg.Tenant, store, source)
	if err != nil {
		log.Fatal(err)
	}

	server := web.New(
		web.WithTitle("tenant-example"),
		web.WithVersion(gable.Version),
		web.WithDescription("single and multi-tenant examples"),
	)
	server.Register(publicController{login: login})
	server.UseTenant(resolver)
	server.Register(tenantController{})

	app := gable.New(gable.WithConfig(cfg))
	if err := app.Register(web.NewModule(server, "")); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
