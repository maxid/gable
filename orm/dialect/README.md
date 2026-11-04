# orm/dialect

Canonical SQL dialect layer for gable applications.

It covers the constructs that differ across engines without hiding SQL:

- placeholders (`?`, `$n`)
- safe identifier quoting (rejects unsafe names)
- pagination with bound arguments (MySQL/PostgreSQL/ClickHouse/达梦)
- `INSERT ... ON DUPLICATE KEY` / `ON CONFLICT` upsert (unsupported engines return `ErrUnsupported`)
- aliases for MySQL/PostgreSQL family engines

The ORM query builder still has its own minimal dialect interface; this package
is the shared layer for hand-written repositories and the migration path for
applications that need ClickHouse or 达梦.

```go
d, err := dialect.DialectFor("dm")
clause := d.Pagination(20, 40, 0)
args := d.PaginationArgs(20, 40)
```
