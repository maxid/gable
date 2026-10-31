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
- **One namespace, derived everywhere.** `gns` turns a root name into cache
  prefixes, Flyway history tables, CosID segments, Kafka topic/group names and
  cluster channels; each subsystem can override it.
- **Authentication and OAuth2.** Password login, JWT or opaque access tokens,
  rotating opaque refresh tokens, bearer middleware, passport-compatible
  responses, an OAuth2 authorization server (code + PKCE, consent callback,
  metadata), OIDC id_token/JWKS/userinfo, token/introspection endpoints and a
  PKCE client with social callbacks.
- **A validated DSL for low-code resources.** `gable/v1` Model/Table/Form/Page
  resources, lint, DDL and `lcdp.ir/v1` previews, an idempotent `apply`, a
  dynamic `/dsl/data/<model>` CRUD API and MCP tools. This is the contract an
  AI can generate, inspect, apply and drive without touching Go code.

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
| `log` | `glogs` logger construction; `glogs/bridge` captures non-slog dependencies |
| `errors` | stable error codes and HTTP status |
| `web` | Gin-backed MVC, envelope, binding/validation, OpenAPI + Swagger UI |
| `tenant` | single/saas resolution, context, host login rules, impersonation |
| `orm` | sqlx-backed repositories, tenant guard, migrations, gcosid keys |
| `rpc` | typed Client with local and HTTP/JSON bindings, shared-key auth, pluggable resolver; `rpc/nacos` resolves through the registry |
| `dsl` | gable/v1 resources, lint, DDL, low-code IR, apply |
| `auth` | password/JWT/opaque tokens, refresh rotation, passport endpoints, OAuth2 authorize/token |
| `cache` | gcache constructor that inherits the application namespace |
| `auth/oauthclient` | authorization-code + PKCE client for social providers |

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

## DSL

```bash
gable dsl lint --dir resources
gable dsl preview Model/task --dir resources --dialect postgres
gable dsl preview Page/task.page --dir resources --format yaml
gable dsl apply --dir resources --out .gable/dsl
```

The first slice covers `Model`, `Table`, `Form` and `Page`:

- `Model` → normalized fields plus a `CREATE TABLE` statement.
- `Table`/`Form` → `lcdp.ir/v1` `TableDef`/`FormDef`.
- `Page` → a `UiPage` widget tree for vamis.

`apply` validates, writes normalized resources and is idempotent. The runtime
data API and MCP tools are available too:

```bash
# dynamic CRUD: GET/POST /dsl/data/task, GET/PUT/DELETE /dsl/data/task/{id}
gable dsl serve --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# AI tool surface over MCP stdio: dsl.schema / dsl.lint / dsl.preview / dsl.apply
gable dsl mcp --dir resources
```

The data API accepts vamis CRUD parameters (`page`, `perPage`, `orderBy`,
`orderDir`) and enforces the Model's tenant guard. The database store,
diff/revision and the Api/Mcp kinds are the next slice.

## Namespaces

```yaml
namespace:
  root: app
  cache: app          # inherited when omitted
  flyway: app
  cosid: none         # keep existing segment names
  cluster: app
  timeSeries: app
```

`app.Namespace()` returns the derived `gns.Set`:

```go
ns := app.Namespace()
ns.CacheName()                 // gcache namespace
ns.FlywayTable("schema_migrations") // app_schema_migrations
ns.Segment("sys_dict")         // app.sys_dict (or sys_dict with cosid: none)
ns.Topic("iothub_raw")         // app.iothub_raw
```

`gable/cache.New` passes `CacheName` to gcache; auth token-store keys use the
same namespace; `gcache`/`gbus` use `gns` internally. Flyway, CosID and cluster
consumers receive the derived values.

## Authentication

```go
service, _ := auth.NewTokenService(auth.Config{Secret: "change-me"}, users, clients, nil)
handlers := auth.NewHandlers(service, tenant.NewSingleLoginResolver(cfg.Tenant))
server.Register(handlers)
server.Use(handlers.Middleware()) // protected routes registered after this
```

- `POST /passport/login` returns the frontend's camelCase `TokenVO`
  (`accessToken`, `refreshToken`, `expiresIn`, `key`, `needChangePassword`, …).
- `AUTH_FORMAT=opaque` (or `AccessOpaque`) issues the platform's
  `client:username:userType:uuid` access token; `JWT` is the default.
- `POST /oauth2/token` supports `authorization_code`, `password`,
  `refresh_token` and `client_credentials`; `/oauth2/introspect` and
  `/oauth2/revoke` are included.
- `GET/POST /oauth2/authorize` implements code + PKCE with a `Consent`
  (`Authorizer`) callback and exact redirect-uri matching; metadata is served at
  `/.well-known/oauth-authorization-server`.
- With an RSA key configured, the code flow also issues an RS256 `id_token`
  for the `openid` scope; `/oauth2/jwks` publishes the key and
  `/oauth2/userinfo` returns the profile.
- `auth/oauthclient.Handlers` adds `/oauth2/login/:provider` and
  `/oauth2/callback/:provider`; the app maps the external profile to a local
  principal.
- Refresh tokens rotate; logout removes the session marker and refresh token.
- `auth/oauthclient` handles authorization-code + PKCE for external providers;
  the application maps the external profile to a local user and calls
  `TokenService`.

## Examples

- `examples/hello` — minimal HTTP app with OpenAPI and Swagger UI.
- `examples/tenant` — SaaS mode: host login, principal tenant, impersonation.
- `examples/pilot` — tenant-scoped task board: full CRUD, memory/ORM stores,
  single/saas modes, OpenAPI; runnable without a database.
- `examples/dsl` — model/table/form/page resources with lint and IR preview.
- `examples/auth` — password login, protected route, OAuth2 token/introspection,
  JWT and opaque modes.

## Planned
- DSL: database store, diff/revision, Api/Mcp kinds.
- `ai` / `mcp`: adapters over the `gai` and `gmcp` libraries.
- Cookie sessions, MFA; more provider presets.
- `lowcode`: serve the IR and the vamis SDK from an app.
- ORM: relations/preload, soft delete, ClickHouse/DM/TDengine dialects.
- `cli`: `gable gen`, `gable migrate` (in addition to `new`/`dev`).

## Install

```bash
go get github.com/maxid/gable
```

## License

MIT. See [LICENSE](LICENSE).
