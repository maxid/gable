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
