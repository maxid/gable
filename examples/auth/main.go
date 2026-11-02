package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/maxid/gable"
	"github.com/maxid/gable/auth"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	hash, err := (auth.BcryptHasher{}).Hash("secret")
	if err != nil {
		log.Fatal(err)
	}
	users := auth.NewMemoryUserStore(auth.User{
		ID: "1", Username: "admin", PasswordHash: hash, UserType: "1",
		TenantID: 1, Scopes: []string{"read"}, Roles: []string{"admin"},
	})
	clients := auth.NewMemoryClientStore(auth.Client{
		ID: "console", Secret: "client-secret", Scopes: []string{"read"}, TenantID: 1,
	})

	format := auth.AccessFormat(os.Getenv("AUTH_FORMAT"))
	if format == "" {
		format = auth.AccessJWT
	}
	secret := os.Getenv("AUTH_SECRET")
	if secret == "" {
		secret = "dev-only-change-me"
	}
	service, err := auth.NewTokenService(auth.Config{
		Issuer:       "auth-example",
		Secret:       secret,
		AccessFormat: format,
		Namespace:    cfg.Namespace.Root,
	}, users, clients, auth.NewMemoryTokenStore())
	if err != nil {
		log.Fatal(err)
	}

	handlers := auth.NewHandlers(service, tenant.NewSingleLoginResolver(tenant.DefaultConfig()))
	// A browser frontend gets the tokens in HttpOnly cookies; an API client keeps reading
	// the response body.
	session := auth.DefaultCookieSession()
	handlers.Cookies = &session
	// A second factor is optional per account: it only changes login for accounts that
	// finished enrollment.
	mfa, err := auth.NewMFAService(auth.DefaultTOTPConfig("auth-example"), auth.NewMemoryMFAStore())
	if err != nil {
		log.Fatal(err)
	}
	handlers.MFA = mfa

	server := web.New(web.WithTitle("auth-example"), web.WithVersion(gable.Version))
	server.Register(handlers)
	server.Use(handlers.Middleware())
	server.Router().GET("/protected", func(ctx *web.Context) error {
		principal, ok := auth.PrincipalFromContext(ctx.Request().Context())
		if !ok {
			return auth.ErrMissingToken
		}
		return ctx.JSON(200, map[string]any{
			"username": principal.Username,
			"tenantId": principal.TenantID,
			"roles":    principal.Roles,
		})
	}).Doc("protected route")

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
