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

Runtime data APIs (`/dsl/data/<model>`) and MCP tools are the next slice; the
lint/preview/apply loop is already the contract an AI drives.
