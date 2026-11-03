# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

- Assembly delegates to `gplugin`: `App.Register` adds the module to a
  `gplugin.Set[*Container]` and `App.Start` lets the assembly resolve the closure,
  order it deterministically and roll back a failed start. The 90-line
  `orderModules` and the App's own started-module bookkeeping are gone; module
  signatures are unchanged (`gable.Module` is `gplugin.Module[*Container]` plus the
  optional lifecycle).

- Pilot: `examples/app` wires `users`, `roles` and `admin` modules that keep the platform's
  own contract (`Register(*Container)`, own lifecycle) into a gable application through a
  fifteen-line adapter in the wiring. `/status` exposes the assembly manifest, and
  `pilot_test.go` asserts the four adoption criteria: one container, an equivalent manifest,
  the platform's rollback semantics, and no gable type in the modules.
- CLI: `gable gen dsl` turns gable-tagged Go structs into `Model` resources (flags,
  sizes, column and table names), omitting relations and any field it cannot express —
  with a warning on stderr rather than silently. Writing twice leaves identical files
  untouched.
- OpenAPI: `Route.Extension(name, value)` attaches a vendor extension (`x-...`) to an
  operation, flattened into the operation object; a name without the prefix panics at
  wiring time. Routes may be written with OpenAPI's `{id}` and are translated to Gin's
  spelling on registration.
- Configuration hot reload: `config.Watch` loads, validates and then watches the
  directory (editors and ConfigMap updates replace the file), collapses a write burst
  into one reload, keeps the last good config when the new file is invalid, and refuses a
  swap when a subscriber rejects it.
- Validation and messages: `validate` reports every failing field as
  `{field, rule, message}` (the same shape binding errors render) with overridable and
  translatable messages and custom rules; `i18n.Bundle` resolves exact language → base
  language → default → key, with `{name}` placeholders.
- Pagination: `ctx.Page(allowedSorts...)` reads `page/size/sort/order` plus the vamis
  aliases, clamps the size to 200 and rejects a sort column outside the whitelist with a
  400 that lists the allowed values. The DSL data API now sorts only by the model's own
  columns.
- CLI: `gable inspect` prints the effective configuration (DSN password redacted),
  the derived namespace names and, with `--dir`, a DSL summary; `--format json` is the
  machine-readable form, and a missing config file falls back to the defaults unless
  `--config` was given explicitly.
- ORM transactions: `db.WithTx` runs a callback against a `Tx`, and `tx.Repo[T]`
  binds the same repository code (tenant guard, delete markers, Preload) to the
  transaction. Returning nil commits; an error rolls back; a panic rolls back and
  is re-raised instead of being swallowed.
- Observability: `health` registry (liveness vs readiness, critical and optional
  probes, panicking probes reported as failures) and a dependency-free `metrics`
  registry rendering Prometheus text. `web.WithHealth`/`web.WithMetrics` mount
  `/healthz`, `/readyz` and `/metrics`, and the server records request counts,
  in-flight requests and latency bucketed by route pattern — never by path.
  `App` owns both registries, publishes `gable_capabilities`, and only becomes
  ready once `Start` has wired every module.
- Initial scaffold: `App`, `Container`, module lifecycle, typed configuration,
  `glogs` logging and stable error codes.
- `web`: Gin-backed MVC, six-field envelope, binding/validation, request ids,
  recovery, access logs, springdoc-shaped OpenAPI 3.0.1 and Swagger UI.
- CLI: `gable new`, `gable dev`, `gable version`.
- Tenant: single/saas modes, root tenant, host login resolution, fail-closed
  request scope and operator impersonation.
- ORM: sqlx-backed models and repositories, query builder, tenant guard,
  gcosid primary keys, AutoMigrate for MySQL/PostgreSQL.
- Pilot example: tenant-scoped task board with memory/ORM stores, single/saas
  modes, CRUD and OpenAPI.
- Fix: pagination now inserts a space before `LIMIT` when an `ORDER BY` is present.
- Fix: `orm.Open` accepted `postgres` as a dialect but handed it to `sql.Connect`
  as a driver name; PostgreSQL is reached through `pgx`, so every alias now opens
  the driver that actually registered itself.
- DSL first slice: `gable/v1` Model/Table/Form/Page, lint, Model DDL,
  `lcdp.ir/v1` previews, idempotent file apply and `gable dsl` commands.
- DSL runtime: dynamic `/dsl/data/<model>` CRUD with tenant guard, vamis
  pagination aliases and `gable dsl serve`; MCP tools over stdio
  (`gable dsl mcp`).
- MCP client: `mcp.Dial` reaches a remote server over streamable HTTP or a stdio
  subprocess (`gable/mcp` was server-only), and `gable mcp list/call` drives it from the
  shell. `CallTool` returns an `IsError` result as data, so a caller keeps the difference
  between a tool refusing and the transport failing; `mcp call` exits non-zero on the
  former. gmcp's HTTP client gained the `ListTools`/`CallTool` helpers its stdio client
  already had.
- DSL connectors: `spec.connector` names a database (driver + DSN + pool) registered with
  `dsl.Connectors`, and every statement for that model — including `Migrate` — goes there.
  A model naming an unregistered connector is refused when the set is bound, not on the first
  request. `gable dsl serve --connector name=dsn` adds one; cross-connector transactions are
  explicitly unsupported.
- DSL runtime reload: `dsl.Runtime.Reload` swaps the resource snapshot atomically, so a
  request sees one whole set; `gable dsl serve --reload 30s` polls and SIGHUP reloads on
  demand, creating the tables of newly added models the way startup does. A set that does not
  lint is refused and the running snapshot keeps serving. Metadata gains `readonly`
  (push/restore refuse to overwrite a stored readonly resource), `status` (`active` /
  `deprecated`), `tags` and `sort`.
- DSL `Api` and `Mcp` kinds: an Api binds one action to a handler registered with
  `dsl.NewRegistry().API(name, fn)`, behind guards registered by name; an Mcp declares the
  metadata a model reads for a registered tool handler. A name that was never registered —
  handler or guard — fails binding instead of producing an unguarded route. Routes are
  written in OpenAPI syntax (`{id}`) and registered with Gin's spelling by the router.
- DSL diff and history: `dsl.DiffResources` compares a desired set with the stored
  documents using the same normalization `push` writes, `gable dsl diff` and
  `gable dsl push --dry-run` print it (a never-migrated database reads as empty),
  `gable dsl history` lists revisions and `gable dsl restore` writes an old one as the
  next revision instead of rewriting history.
- DSL database store: `dsl.NewDBStore` migrates `gable_dsl_resource` and
  `gable_dsl_revision`, loads a set back from the rows and saves only the
  documents that changed, in one transaction, keeping every revision; the
  revision number is assigned by the database. `gable dsl push` moves a
  directory into the store, `gable dsl serve --load db` serves it back.
- Auth cookie sessions: `Handlers.Cookies` carries the same tokens in HttpOnly
  cookies, the middleware reads header first and cookie second, and logout
  revokes and expires them.
- Auth second factor: TOTP enrollment/activation, `{mfaRequired, ticket}` login
  for active accounts, single-use tickets and hashed one-time recovery codes;
  the factor can only be disabled with a working code.
- ORM relations and delete markers: `belongs_to`/`has_many` fields are loaded by an
  explicit `Preload` (one query per relation for the whole result set, keeping the
  parent's tenant and delete guards), and a `soft_delete` timestamp or `retain` flag
  adds its predicate to every scoped read, write and delete. `Delete` writes the marker,
  `Restore` clears it, `Purge` removes the row.
- Plugins: `plugin.From(plugin.Spec{...})` adapts a module that keeps its own
  container type or lifecycle, without renaming it; a spec without hooks stays
  wiring-only, so it is never counted as started.
- Namespace: gable keeps the cache/Flyway/CosID/cluster prefix config; `gns` is
  limited to the namespace value, context and Store SPI. `namespace` adapts a
  gcache instance as the Store implementation; gcache/gbus no longer depend on
  gns.
- Auth: password login, JWT/opaque access tokens, opaque refresh rotation,
  bearer middleware, passport-compatible Login/refresh/logout, OAuth2
  token/introspection/revocation endpoints, an authorization-code + PKCE
  server with consent/metadata, OIDC id_token/JWKS/userinfo, and a client with
  social callbacks.
