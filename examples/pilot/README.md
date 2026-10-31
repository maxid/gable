# Pilot: tenant-scoped task board

A small but complete use case for gable: HTTP CRUD, tenant resolution, tenant
guards in the ORM, OpenAPI and Swagger UI.

## Run without a database

```bash
go run .
```

The in-memory store keeps the example runnable. `single` mode binds every
request to the root tenant (`management`, id 1).

```bash
curl -s -X POST localhost:8082/tasks -H 'Content-Type: application/json' -d '{"title":"write docs"}'
curl -s localhost:8082/tasks
curl -s localhost:8082/whoami
open http://localhost:8082/swagger-ui/index.html
```

## Run with MySQL/PostgreSQL

```bash
GABLE_DB_DRIVER=mysql \
GABLE_DB_DSN='user:pass@tcp(127.0.0.1:3306)/pilot?parseTime=true' \
go run .
```

`AutoMigrate` creates `pilot_task` if it is missing. IDs come from `gcosid`
snowflake; set `GABLE_MACHINE_ID` for multi-instance deployments.

## Multi-tenant mode

```bash
GABLE_TENANT_MODE=saas GABLE_TENANT_BASE_DOMAIN=example.com go run .
```

The pilot seeds `management`/`acme`/`beta`. The source reads `X-User-Tenant`
and `X-Operator` headers **for demonstration only**; a real app reads the
principal from its auth middleware.

```bash
curl -s -H 'Host: acme.example.com' 'localhost:8082/login/options'
curl -s -H 'X-User-Tenant: 2' localhost:8082/whoami
curl -s -H 'X-User-Tenant: 2' -H 'X-Operator: 1' -H 'X-Tenant-Id: 3' localhost:8082/whoami
```

A missing principal is rejected (`tenant_required`); a task created by tenant 2
is invisible to tenant 3.
