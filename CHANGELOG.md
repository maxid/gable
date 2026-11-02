# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

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
