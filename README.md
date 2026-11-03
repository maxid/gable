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
- **One namespace, derived in the framework.** gable keeps the cache, Flyway,
  CosID and cluster prefixes; `gns` only carries the namespace value/context and
  the `Store` SPI used to persist namespace definitions.
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

## Managing capabilities

Capability selection lives in [`gplugin`](https://github.com/maxid/gplugin), shared with the
protocol libraries. It takes the container type as a parameter, so `gable.Container` is just
one of its uses:

```go
set := gplugin.NewSet[*Container]()
set.Add(gplugin.Static("log", "1.0.0", logCapability))
set.Add(gplugin.Static("cache", "1.0.0", cacheCapability))   // DependsOn: log
set.Add(gplugin.Static("web", "1.0.0", webCapability))       // DependsOn: cache

assembly, err := set.Select("web")        // -> log, cache, web (dependency order)
err = assembly.Start(ctx, container)      // register + start; a failure rolls back
defer assembly.Stop(ctx)
```

Three properties matter:

- **Deterministic order** — ties break on id, so a rolling restart cannot reorder
  capabilities;
- **Selection is validated at startup** — an unknown capability, a dependency that is not
  in the binary, or a cycle is an error, never a quietly smaller assembly;
- **Lifecycle is optional** — a wiring-only capability needs no empty `Start`/`Stop`.

Unlike the platform's `internal/plugin`, there is no package-level registry and no panic
on duplicates: wiring stays explicit so a test can build an app in isolation.

A module that already has its own shape — takes its own container type, or carries its own
lifecycle interface — joins without being rewritten, by describing it with closures:

```go
set := gplugin.NewSet[*platform.Container]()
module, err := gplugin.From(gplugin.Spec[*platform.Container]{
	ID:        "cache",
	Version:   "1.0.0",
	DependsOn: []string{"log"},
	Register:  cache.Register,
	Start:     cache.Start,
	Stop:      cache.Stop,
})
set.Add(module)
```

Everything the manifest shows comes from the `Spec` — nothing is inferred — and a spec
without hooks stays wiring-only, so it never appears as started.

A module that declared a lifecycle satisfies `gable.Module` as-is — the method sets are
identical — so an assembly can also be registered on the application directly.

## Conversations with tools

```go
service, err := ai.New(ai.Config{
    Provider: "openai", BaseURL: "https://api.deepseek.com/v1",
    APIKey: key, Model: "deepseek-chat",
    SystemPrompt: "be terse",
}, ai.WithTools(ai.Tool{
    Name: "order_status", Schema: schema,
    Handler: func(ctx context.Context, args json.RawMessage) (any, error) { /* ... */ },
}))

result, err := service.Ask(ctx, sessionID, "where is order 42?")
// result.Text, result.ToolsUsed, result.Iterations
```

The loop runs the tools the model asks for and feeds the results back. Two bounds are
not configurable into "off": `MaxIterations` (a confused model must not become an
infinite bill) and `MaxHistory` (history is trimmed at a user turn, so a tool call is
never separated from its result).

## Streamed answers

`ai.Service` is the same bounded tool loop as `Ask`, with the answer turned inside out:

```go
stream, err := service.Stream(ctx, sessionID, "how many orders this week?")
defer stream.Close()

for {
    event, err := stream.Recv()
    if err == io.EOF { break }
    if err != nil { return err }
    switch {
    case event.Delta != "":     send(event.Delta)          // text as it is generated
    case event.ToolCall != nil: show(event.ToolCall.Name)  // "calling orders.summary…"
    case event.ToolResult != nil: show(event.ToolResult)
    case event.Done:            return nil
    }
}
```

`Done` is a normal event and the next `Recv` reports `io.EOF`: folding the last event into the
error would make a caller that checks the error first silently drop the end of the answer.

For a browser, `stream.ServeSSE(w)` writes `text/event-stream` with a flush per event, a
`[DONE]` sentinel, and `X-Accel-Buffering: no` so a proxy does not hold the answer until the
connection closes. A provider failure after the headers are sent travels as an `event: error`
frame instead of being swallowed.

The provider is chosen by name: `openai` (OpenAI and every server copying that wire format) or
`anthropic` (the Messages API, where the system prompt is top-level and tool calls are content
blocks). A vendor that fits neither gets its own adapter rather than a pile of flags.

## Schema from DSL

```bash
gable migrate --config gable.yaml --dir ./dsl --dry-run   # print the DDL
gable migrate --config gable.yaml --dir ./dsl             # create missing tables
```

`migrate` lints the DSL first: a model without a primary key parses fine but would
create a table nobody designed, so the command refuses it. It only creates missing
tables — altering one is a data-loss decision that belongs to explicit migrations.

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
| `mcp` | exposes operations as MCP tools over Streamable HTTP (shared-key transport check) and dials remote servers over HTTP or stdio |
| `ai` | tool-calling conversation loop over `gai`, with bounded rounds and bounded history |
| `dsl` | gable/v1 resources, lint, DDL, low-code IR, apply, file/database store, diff/history/restore |
| `validate` | struct validation into a field list the envelope can carry |
| `i18n` | per-language message bundles with region and default fallbacks |
| `health` | liveness vs readiness, critical and optional probes |
| `metrics` | Prometheus-text counters, gauges and histograms |
| `cli` | `new`, `dev`, `dsl`, `migrate`, `inspect`, `version` |
| `auth` | password/JWT/opaque tokens, refresh rotation, passport endpoints, OAuth2 authorize/token |
| `cache` | gcache constructor that inherits the application namespace |
| `namespace` | gcache-backed `gns.Store` implementation (memory or Redis) |
| `auth/oauthclient` | authorization-code + PKCE client for social providers |

## HTTP in five lines

```go
server := web.New(web.WithTitle("demo"))
server.Register(myController{}) // Routes(r *web.Router)
app.Register(web.NewModule(server, "")) // listens on config.http.addr
```

`GET /v3/api-docs` serves the OpenAPI document, `GET /v3/api-docs/{group}` a
group, and `GET /swagger-ui/index.html` the UI.

An operation can carry vendor extensions, which is how a gateway hint or a springdoc-style
flag reaches the document:

```go
router.GET("/items", listItems).Doc("list items").
    Extension("x-springdoc-paginated", true)
```

The name must start with `x-` — anything else panics at wiring time rather than being dropped
or, worse, rendered as a field of the operation schema. Extensions are flattened into the
operation object (not nested under an `extensions` key), and an operation without any renders
exactly as before. Routes can be written with OpenAPI's `{id}` and are translated to Gin's
`:id` on registration, so the document keeps the specification's spelling.

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

A row can be hidden instead of removed, in the two spellings the platform uses:

```go
type Doc struct {
    ID        int64      `gable:"pk"`
    Title     string     `gable:"size=128"`
    DeletedAt *time.Time `gable:"soft_delete"` // hidden once it is set
}

type Flag struct { // or with a flag
    ID     int64 `gable:"pk"`
    Name   string
    Retain bool `gable:"retain"` // hidden once it is true
}
```

Declaring either one adds `deleted_at IS NULL` / `retain = false` to every
scoped read, update and delete, so no caller has to remember it. `Delete`
writes the marker, `Restore` clears it, and `Purge` is the only call that
removes the row for good. `Unscoped()` lifts the guard; `Update` never touches
the marker, so a stale entity cannot resurrect a row.

Relations are declared on the entity and loaded only when asked for:

```go
type Order struct {
    ID     int64  `gable:"pk"`
    UserID int64
    User   *User  `gable:"belongs_to=sys_user,foreign_key=user_id"`
    Items  []Item `gable:"has_many=order_item,foreign_key=order_id"`
}

orders, err := orders.Query().
    Preload("User", users).
    Preload("Items", items).
    All(ctx)
```

Transactions take a callback, and the repositories inside it are ordinary
repositories:

```go
err := db.WithTx(ctx, func(ctx context.Context, tx *orm.Tx) error {
    accounts, _ := tx.Repo[Account]("account")
    return accounts.Update(ctx, debited) // and any second statement
})
```

Returning nil commits; an error or a panic rolls back — and a panic is re-raised
rather than swallowed, so a caller's deferred cleanup never runs as if the work
succeeded. The tenant guard and delete markers work inside the transaction because
it is the same repository code, only a different executor.

A relation is metadata, not a column. `Preload` costs one query per relation
for the whole result set — never one per row — and the related read keeps the
same tenant and delete-marker guards as the query that asked for it. There is
no lazy loading, so a handler cannot accidentally issue a query per row.


Schema migrations live in the independent [`gmigrate`](https://github.com/maxid/gmigrate) library; gable only keeps `AutoMigrate` for development.

## Generating DSL from Go code

```bash
gable gen dsl --pkg ./internal/model --out resources --table-prefix app_ --dry-run
gable gen dsl --pkg ./internal/model --out resources          # writes Model-<name>.yaml
```

The generator parses the package (it does not type-check) and turns every struct with
`gable` tags into a `Model` resource:

- exported fields become columns — `gable:"-"` fields and relations (`has_many`,
  `belongs_to`) do not, because a relation is not a column;
- `pk`, `tenant`, `unique`, `auto_create`, `auto_update`, `auto` and `size=N` become flags;
- `column=x` renames the column, `table=x` names the table (otherwise the snake-cased struct
  name, prefixed by `--table-prefix`);
- a field whose Go type has no DSL equivalent is left out **with a warning on stderr**, never
  silently: `soft_delete`/`retain` markers and slice/struct fields are named there today.

Writing twice leaves identical files untouched, so running it in CI is a no-op when nothing
changed. The output is a skeleton: labels, enums, defaults and the Table/Form overlays are
still written by hand, because those are the parts the DSL exists to carry.

## DSL

```bash
gable dsl lint --dir resources
gable dsl preview Model/task --dir resources --dialect postgres
gable dsl preview Page/task.page --dir resources --format yaml
gable dsl apply --dir resources --out .gable/dsl
```

The first slice covers `Model`, `Table`, `Form` and `Page`; `Api` and `Mcp` bind actions
and tools to Go handlers you register, by name:

- `Model` → normalized fields plus a `CREATE TABLE` statement.
- `Table`/`Form` → `lcdp.ir/v1` `TableDef`/`FormDef`.
- `Page` → a `UiPage` widget tree for vamis.

```yaml
# Api: one action, bound to a registered handler, behind declared guards
apiVersion: gable/v1
kind: Api
metadata: {name: task.close, title: Close a task}
spec:
  handler: tasks.close     # a name, never a package or a file
  model: task
  method: POST
  path: task/{id}/close
  guards: [tenant]
  audit: true
---
# Mcp: the metadata a model reads, for a handler that stays in Go
apiVersion: gable/v1
kind: Mcp
metadata: {name: tasks.search}
spec:
  handler: tasks.search
  description: Find tasks by title.
  params:
    type: object
    properties: {q: {type: string}}
    required: [q]
```

```go
registry := dsl.NewRegistry().
    AddGuard("tenant", requireTenant).
    API("tasks.close", closeTask).
    Tool("tasks.search", searchTasks)

bindings, err := registry.Routes(set, server.Router(), "/dsl/api") // POST /dsl/api/task/{id}/close
tools, err := registry.Tools(set, mcpServer)                        // tasks.search
```

A resource never names Go code, only a symbolic handler name, so the "no arbitrary code in
resources" rule still holds while a low-code page can call real business logic. A name that
was never registered — handler or guard — fails binding instead of producing a route that
500s in production or, worse, runs unguarded. Guards run in the order the resource declares
them, so `[tenant, role]` means the role check sees a tenant. Routes are written in OpenAPI
syntax (`{id}`) and registered with Gin's (`:id`) by the router.

`apply` validates, writes normalized resources and is idempotent. The runtime
data API and MCP tools are available too:

```bash
# dynamic CRUD: GET/POST /dsl/data/task, GET/PUT/DELETE /dsl/data/task/{id}
gable dsl serve --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# AI tool surface over MCP stdio: dsl.schema / dsl.lint / dsl.preview / dsl.apply
gable dsl mcp --dir resources
```

The data API accepts vamis CRUD parameters (`page`, `perPage`, `orderBy`,
`orderDir`) and enforces the Model's tenant guard.

### Storing resources in a database

A directory is the GitOps path; a database is the path for several instances that
share one definition or edit resources at runtime. Both store the same normalized
YAML, so a resource looks identical whichever store produced it.

```bash
# what would change? (reads only; a database that was never migrated reads as empty)
gable dsl diff --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true' --lines
gable dsl push --dir resources --dry-run --lines --dsn '...'   # same output, inside push

# directory -> database (linted first, only changed documents are written)
gable dsl push --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# serve what the push stored, instead of the directory
gable dsl serve --load db --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# audit history and roll one resource back
gable dsl history Model/task --dsn '...'
gable dsl restore Model/task --revision 3 --dsn '...'
```

### One definition, several databases

A model can name a connector — a name for "driver + DSN + pool" — so the same definition
points at a different database without being edited:

```yaml
apiVersion: gable/v1
kind: Model
metadata: {name: revenue}
spec:
  table: revenue
  connector: reporting        # empty means the default connector
  fields:
    - {name: id, type: int64, primary: true}
    - {name: amount, type: int64}
```

```bash
gable dsl serve --load db --dsn '.../app' \
  --connector 'reporting=.../reporting' --addr :8088
```

Every query the runtime issues for that model — list, get, create, update, delete, and the
`Migrate` at startup — goes to the named database. A model that names an unregistered
connector is refused **when the set is bound** (construction and every reload), not on the
first request that touches it.

A dialect is not a connector: the dialect decides SQL syntax, the connector decides which
database receives it. One deployment usually has one dialect and several connectors.
**Cross-connector transactions are not supported** and are not pretended to be — a write that
spans two databases needs an application-level compensation, not a framework promise.

### Reloading without a restart

```bash
gable dsl serve --load db --reload 30s --dsn '...'
kill -HUP <pid>            # reload now, whatever the interval is
```

A reload swaps the resource snapshot atomically, so a request sees either the whole old set
or the whole new one; a model added to the database becomes reachable on the next tick (its
table is created the same way startup does it), and a model removed from it stops being
reachable — that is what unloading means for a data API driven by resources. A set that does
not lint is **refused** and the running snapshot keeps serving: the log names the broken
resource, and a bad edit does not become an outage.

Resources can also say what may happen to them:

```yaml
metadata:
  name: task
  readonly: true          # push and restore refuse to overwrite a stored readonly resource
  status: deprecated      # still loads and serves; "active" and empty mean the same
  tags: [admin, task]     # for UI grouping and filtered listings
  sort: 10                # order of the same kind; a smaller number comes first
```

`readonly` is enforced against the **stored** document, so a file cannot lift its own flag and
overwrite the database — it is the guard rail that lets hand-written resources live next to
generated ones.

`diff` compares the same normalized documents `push` writes, so a dry run and the real
write cannot disagree; resources that exist only in the database are reported as
`removed` (nothing is deleted implicitly). `restore` writes the old document as the
**next** revision rather than rewriting history, and does nothing when that revision is
already current.

`dsl.NewDBStore` is the library side (`Migrate`, `Load`, `Save`). Two tables back it:
`gable_dsl_resource` (one row per `Kind/name`, with the current document and revision)
and `gable_dsl_revision` (the same document at every revision it was written, for the
diff/history slice). `Save` lints the whole set, skips documents that did not change,
and writes the rest in one transaction — the revision number is assigned by the
database, so two instances saving at once cannot hand out the same one. Deleting a
resource is not implicit: dropping it from the set leaves the row in place.

The diff/dry-run over those revisions and the Api/Mcp kinds are the next slice.

## Configuration hot reload

```go
watcher, err := config.Watch("gable.yaml", config.WithOnChange(func(cfg *config.Config) error {
    return featureFlags.Rebind(cfg) // return an error to keep the previous config
}))
defer watcher.Close()
cfg := watcher.Config() // a fresh pointer after each reload, never a mutated one
```

The directory is watched, not the file: editors and ConfigMap updates replace the file,
which would end a watch on the old inode. A burst of writes collapses into one reload, an
invalid file keeps the last good configuration and reports the error through
`WithOnError` — a typo must not take a running process down — and a subscriber that returns
an error blocks the swap, so a half-applied reload cannot happen.

Hot reload replaces the *configuration value*; it does not re-register modules. Wiring that
depends on a setting at Register time (a listener address, a database pool) still needs a
restart, and that is deliberate: the alternative is modules mutating live state behind the
lifecycle's back.

## Validation and messages

```go
validator := validate.New()          // English defaults, field names from json tags
validator.SetMessage("min", "at least %s characters")
validator.Register("slug", "must be a lowercase slug", isSlug)

if issues := validator.Struct(payload); len(issues) > 0 {
    return issues                    // 400 invalid_argument, with {fields:[{field,rule,message}]}
}
```

Every failing field is reported, message text is a one-liner a log can carry, and the
envelope data lists `field`/`rule`/`message` per entry — the same shape Gin's binding errors
render, so a client parses one format whichever layer rejected the request. A custom rule is
registered once with its message; a `Translator` (or a nil translator) decides the language,
and `i18n.Bundle` is the bundle it typically reads:

```go
bundle := i18n.New("en")
_ = bundle.Load("zh", []byte("required: 必填\nemail: 邮箱格式不正确\n"))
bundle.Translate("zh-CN", "email", nil) // 邮箱格式不正确: region falls back to its base language
```

Resolution is exact language → base language (`zh-CN` → `zh`) → default language → the key
itself, so a missing message shows up as the key instead of an empty string. Placeholders are
`{name}`; an unknown one is left visible rather than blanked.

## Pagination

```go
page, err := ctx.Page("name", "created_at") // the sort whitelist
if err != nil {
    return err // 400 naming the allowed columns
}
rows, err := repo.Query().OrderBy(page.Sort, page.Desc).Limit(page.Limit()).Offset(page.Offset()).All(ctx)
```

`page`, `size`, `sort` and `order` are read together with the vamis aliases the existing
frontend sends (`perPage`, `orderBy`, `orderDir`). A sort column outside the whitelist is a
400 that lists the allowed values: ignoring it silently returns the wrong order, which looks
like a data bug. Size is clamped to 200 instead of rejected — asking for 10 000 rows is a
performance problem, not a client mistake worth an error.

## Observability

Two registries live on the `App`, one per question an operator asks; the web server mounts
them:

```go
app := gable.New(gable.WithConfig(cfg))
app.Health().Add("database", true, func(ctx context.Context) error { return db.PingContext(ctx) })
app.Metrics().Counter("orders_paid_total", "Paid orders.").Inc()

server := web.New(web.WithHealth(app.Health()), web.WithMetrics(app.Metrics()))
```

- `GET /healthz` — liveness. It answers 200 unless the process is shutting down: a failing
  dependency must not get a healthy process restarted.
- `GET /readyz` — readiness. 503 while a **critical** probe fails or before `Start` has
  finished, so a load balancer takes the instance out instead of killing it. A probe
  returning an error is reported with its message; one that panics is a failure, not a
  crash; a non-critical probe is reported but does not fail the report.
- `GET /metrics` — Prometheus text. The server records `http_requests_total` (method, route,
  status), `http_request_duration_seconds` (bucketed) and `http_inflight_requests`, and the
  App publishes `gable_capabilities`. The route *pattern* is the label, never the path, so a
  resource id cannot create one time series per row.

Both endpoints are outside the JSON envelope: a scraper and an orchestrator read status
codes and text, not the application's response contract.

## Talking to a remote MCP server

`gable/mcp` is both sides: the server your application mounts, and a client for somebody
else's.

```go
client, err := mcp.Dial(ctx, mcp.Endpoint{URL: "https://tools.internal/mcp"})
defer client.Close()

tools, err := client.ListTools(ctx)
result, err := client.CallTool(ctx, "orders.summary", json.RawMessage(`{"days":7}`))
```

The transport is the only difference between an HTTP endpoint and a stdio subprocess
(`Endpoint{Command: []string{"gable","dsl","mcp","--dir","."}}`): the same client lists and
calls either. A stdio client owns the child process — `Close` stops it, and its stderr is
drained so a chatty server cannot block on a full pipe.

A tool failure comes back as a result with `IsError` set, because that is how MCP models it;
the caller keeps the difference between "the tool said no" and "the transport broke".

```bash
gable mcp list  --url https://tools.internal/mcp
gable mcp call  orders.summary --url https://tools.internal/mcp --arguments '{"days":7}'

# the DSL tool surface is reachable the same way
gable mcp list --command gable --arg dsl --arg mcp --arg --dir --arg ./resources
```

`mcp call` exits non-zero when the tool reports an error, so a script gets the same verdict a
human reads.

## Bundling for deployment

```bash
gable pack --config gable.yaml --dir resources --assets public --out app.tar.gz --app shop
gable restore app.tar.gz --out /srv/app
gable token --config gable.yaml --secret "$SECRET" --username ada --ttl 15m
```

`pack` puts the configuration, the DSL resources and any static assets into one archive with an
index (`bundle.json`) that carries a checksum per file and a content version. Entries are
sorted with zero mtimes and no ownership, so the same inputs always yield the same version —
two bundles are comparable even when they were packed at different times.

`restore` verifies **every** file before writing any of them: a corrupt archive fails without
leaving a half-restored application, which is a harder state to diagnose than a refusal. It
refuses a non-empty target unless `--force` is given.

`token` mints a short-lived access token with the same issuer, secret and TTL the application
verifies, which is what a CI smoke test needs before the application can log in. The token is
the only thing on stdout, so `TOKEN=$(gable token ...)` works.

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

`app.Namespace()` returns gable's `config.Namespace`:

```go
ns := app.Namespace()
ns.CacheName()                 // gcache namespace
ns.FlywayTable("schema_migrations") // app_schema_migrations
ns.SegmentKey("sys_dict")      // app.sys_dict (or sys_dict with cosid: none)
ns.CosIDName()                 // app (or "" with cosid: none)
ns.Topic("iothub_raw")         // app.iothub_raw
```

`gable/cache.New` passes `CacheName` to gcache and auth token-store keys use the
same namespace. gns does not derive these values: it only carries
`gns.Namespace` in a context and exposes a `Store` SPI. The `namespace` package
implements that SPI over gcache, so the same adapter works with memory or Redis:

```go
store, _ := namespace.NewStore(cache)
manager, _ := gns.NewManager(store)
ctx, err := manager.Bind(ctx, "app")
```

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

### Cookie sessions

A browser cannot put an `Authorization` header on a page navigation, so a
frontend that the application itself serves needs the session in a cookie:

```go
handlers.Cookies = &auth.CookieSession{Secure: true} // HttpOnly, SameSite=Lax
```

The cookie carries the same access and refresh tokens the body returns — it is a
transport, not a second session format. The middleware reads the header first and
the cookie second; logout revokes the token and expires both cookies; refresh
accepts the refresh cookie when the body omits it. Note that revoking a JWT only
stops future refresh: a stateless JWT stays valid until it expires, which is why
`AccessOpaque` is the format to use when logout must be immediate.

### Second factor (TOTP)

MFA is per account, and opt-in until enrollment finishes:

```go
mfa, _ := auth.NewMFAService(auth.DefaultTOTPConfig("myapp"), auth.NewMemoryMFAStore())
handlers.MFA = mfa
```

- `POST /passport/mfa/enroll` returns the base32 secret, an `otpauth://` URL and
  ten recovery codes — shown once, because a code that can be shown again is not
  a second factor;
- `POST /passport/mfa/activate` needs a code from the authenticator, so a
  half-finished enrollment cannot lock anyone out;
- once active, `POST /passport/login` answers `{mfaRequired, ticket}` instead of
  tokens; `POST /passport/mfa/verify` exchanges the ticket plus a code for tokens;
- the ticket is unguessable, lives five minutes and is consumed by the first
  attempt even when the code is wrong, so it buys exactly one guess;
- recovery codes are stored hashed and each works once;
- `POST /passport/mfa/disable` requires a working code: a stolen session alone
  must not be able to remove the factor.

`MFAStore` and `ChallengeStore` default to memory; a deployment with more than one
replica needs shared implementations of both.

## Examples

- `examples/hello` — minimal HTTP app with OpenAPI and Swagger UI.
- `examples/tenant` — SaaS mode: host login, principal tenant, impersonation.
- `examples/pilot` — tenant-scoped task board: full CRUD, memory/ORM stores,
  single/saas modes, OpenAPI; runnable without a database.
- `examples/app` — the plugin pilot: `users`, `roles` and `admin` modules written against the
  platform's own module contract (`Register(*Container)`, its own lifecycle), joined to a
  gable application by a fifteen-line adapter in the wiring. `/status` prints the assembly
  manifest; `pilot_test.go` asserts the four adoption criteria.
- `examples/dsl` — model/table/form/page resources with lint and IR preview.
- `examples/auth` — password login, protected route, OAuth2 token/introspection,
  JWT and opaque modes.

## Planned
- DSL: database store, diff/revision, Api/Mcp kinds.
- `ai` / `mcp`: adapters over the `gai` and `gmcp` libraries.
- Cookie sessions, MFA; more provider presets.
- `lowcode`: serve the IR and the vamis SDK from an app.
- ORM: relations/preload, soft delete, ClickHouse/DM/TDengine dialects.
- `cli`: `gable gen` (in addition to `new`/`dev`).

## Install

```bash
go get github.com/maxid/gable
```

## License

MIT. See [LICENSE](LICENSE).
