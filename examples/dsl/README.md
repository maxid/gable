# DSL example: model + table + form + page

The resources describe a tenant-scoped task board. They are ordinary YAML, so
an AI or a human edits the same artifact; gable lints it, renders the low-code
IR and writes the normalized output.

```bash
cd examples/dsl

# validate every resource and the references between them
go run ../../cmd/gable dsl lint --dir resources

# model -> DDL (mysql | postgres)
go run ../../cmd/gable dsl preview Model/task --dir resources --dialect postgres

# table/form/page -> lcdp.ir/v1
go run ../../cmd/gable dsl preview Table/task.table --dir resources
go run ../../cmd/gable dsl preview Form/task.form --dir resources
go run ../../cmd/gable dsl preview Page/task.page --dir resources --format yaml

# normalize resources into the runtime directory (idempotent)
go run ../../cmd/gable dsl apply --dir resources --out .gable/dsl
```

The page preview is a `UiPage`; a vamis runtime can render it directly. The
table/form previews are `TableDef`/`FormDef`, the shapes vamis already
compiles. Model preview includes the `CREATE TABLE` statement.

## Runtime data API

With a real MySQL/PostgreSQL database, `dsl serve` migrates the models and
serves dynamic CRUD. The table's `runtimeApi` points at `/dsl/data/task`.

```bash
GABLE_DSN='user:pass@tcp(127.0.0.1:3306)/app?parseTime=true' go run ../../cmd/gable dsl serve   --dir resources --dsn "$GABLE_DSN" --addr :8088

curl -s -X POST localhost:8088/dsl/data/task -H 'Content-Type: application/json' -d '{"title":"write docs"}'
curl -s 'localhost:8088/dsl/data/task?page=1&perPage=10&orderBy=title&orderDir=desc'
```

Single-tenant mode is the default (`management`, id 1). A real multi-tenant
app installs its auth source and `tenant.SaaSResolver` before mounting the same
`Runtime.Routes`.

## MCP tools

```bash
go run ../../cmd/gable dsl mcp --dir resources
```

The stdio server exposes `dsl.schema`, `dsl.lint`, `dsl.preview` and
`dsl.apply`; that is the loop an AI agent drives (generate → lint → preview →
apply). DB store, diff/revision and Api/Mcp kinds are the next slice.
