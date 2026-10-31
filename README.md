English | [简体中文](README.zh-CN.md)

# gable

A code-first application framework for Go services: an explicit dependency
container, module lifecycle, typed configuration, `glogs` logging and stable
error codes. It composes the g\* library family instead of hiding it.

> Status: **experimental scaffold** (v0.0.0-dev). The API is not stable yet.

## Why another framework

- **No package-level registry.** Dependencies live in an `App` container and are
  wired by modules, so tests can build an app without global state.
- **Lifecycle is explicit.** Modules register, start in dependency order and stop
  in reverse order; a failed start rolls back what already started.
- **Configuration is typed.** YAML with `${ENV}` expansion and `GABLE_*`
  overrides, validated before anything starts.
- **Errors carry a stable code.** Transport layers can map a code to an HTTP
  status without importing business packages.
- **Web and docs are included.** Gin-backed routing, a six-field response
  envelope, binding/validation, request ids, panic recovery and access logs;
  springdoc-shaped OpenAPI 3.0.1 at `/v3/api-docs` with grouped documents and
  a Swagger UI.
- **Multi-tenancy is configurable.** `single` runs as the root tenant; `saas`
  resolves the login tenant from the host and the request tenant from the
  principal, with optional operator impersonation. Tenant scope fails closed.
- **A SQL-first ORM.** `sqlx` + a small dialect layer, typed repositories,
  tenant-scoped queries, `gcosid` keys and `AutoMigrate` for MySQL and
  PostgreSQL.

## Quick start

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/maxid/gable"
)

type hello struct{}

func (hello) ID() string                      { return "hello" }
func (hello) Register(c *gable.Container) error { c.Set("hello.greeting", "world"); return nil }
func (hello) Start(context.Context) error      { return nil }
func (hello) Stop(context.Context) error       { return nil }

func main() {
	app := gable.New()
	if err := app.Register(hello{}); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
```

## Packages

| Package | Purpose |
|---|---|
| `gable` | `App`, `Container`, module lifecycle |
| `config` | typed YAML + environment configuration |
| `log` | `glogs` logger construction |
| `errors` | stable error codes and HTTP status |
| `web` | Gin-backed MVC, envelope, binding/validation, OpenAPI + Swagger UI |
| `tenant` | single/saas resolution, context, host login rules, impersonation |
| `orm` | sqlx-backed repositories, tenant guard, migrations, gcosid keys |

## HTTP in five lines

```go
server := web.New(web.WithTitle("demo"))
server.Register(myController{}) // Routes(r *web.Router)
app.Register(web.NewModule(server, "")) // listens on config.http.addr
```

`GET /v3/api-docs` serves the OpenAPI document, `GET /v3/api-docs/{group}` a
group, and `GET /swagger-ui/index.html` the UI.

## Multi-tenancy

```go
// single: every request is the root tenant
server.UseTenant(tenant.NewSingleResolver(cfg.Tenant))

// saas: resolve the request tenant from the authenticated principal
source := tenant.SourceFunc(func(ctx context.Context, r *http.Request) (tenant.Principal, bool, error) {
    return principalFromToken(r) // your auth middleware
})
resolver, _ := tenant.NewSaaSResolver(cfg.Tenant, store, source)
server.UseTenant(resolver)
```

`tenant.NewSaaSLoginResolver` implements the login order: exact host, label
under `base_domain`, leftmost DNS label, explicit `tenantCode`, then the root
tenant. A conflicting or unknown explicit code is rejected, never silently
mapped to the root. Operators may impersonate with the configured header
(`X-Tenant-Id` by default), validated against the store.

## ORM

```go
type User struct {
    ID       int64     `gable:"pk"`
    TenantID int64     `gable:"tenant"`
    Name     string    `gable:"size=64"`
    Created  time.Time `gable:"auto_create"`
}

repo, _ := orm.NewRepo[User](db, "sys_user", orm.WithIDGenerator(ids))
users, err := repo.Query().Where(orm.Eq("name", "alice")).All(ctx)
```

The tenant predicate is injected from `tenant.FromContext`; a missing or
mismatched tenant fails closed. `repo.Unscoped()` is the explicit escape hatch.
`db.AutoMigrate(model)` creates missing tables only. MySQL DSNs should set
`parseTime=true` so `DATETIME`/`TIMESTAMP` columns scan into `time.Time`.

## Planned
- `dsl`: runtime model/page resources with file or database storage.
- `ai` / `mcp`: adapters over the `gai` and `gmcp` libraries.
- `lowcode`: page/table/form resources compiled to the LCDP IR.
- ORM: relations/preload, soft delete, ClickHouse/DM/TDengine dialects.
- `cli`: `gable gen`, `gable migrate` (in addition to `new`/`dev`).

## Install

```bash
go get github.com/maxid/gable
```

## License

MIT. See [LICENSE](LICENSE).
