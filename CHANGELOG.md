# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

- `migrate`: Flyway-compatible `flyway_schema_history` store, line-based CRC32
  checksums, versioned SQL loading, baseline/repair and MySQL/PostgreSQL locks.

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
- DSL first slice: `gable/v1` Model/Table/Form/Page, lint, Model DDL,
  `lcdp.ir/v1` previews, idempotent file apply and `gable dsl` commands.
- DSL runtime: dynamic `/dsl/data/<model>` CRUD with tenant guard, vamis
  pagination aliases and `gable dsl serve`; MCP tools over stdio
  (`gable dsl mcp`).
- Namespace: `gns` config on `Config`/`App`, cache constructor, auth token-store keys;
  gcache and gbus now compose names through `gns`.
- Auth: password login, JWT/opaque access tokens, opaque refresh rotation,
  bearer middleware, passport-compatible Login/refresh/logout, OAuth2
  token/introspection/revocation endpoints, an authorization-code + PKCE
  server with consent/metadata, OIDC id_token/JWKS/userinfo, and a client with
  social callbacks.
