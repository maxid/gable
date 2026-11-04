# orm/dialect

gable 应用的规范 SQL 方言层。

它覆盖各引擎真正不同的构造，但不隐藏 SQL：

- 占位符（`?`、`$n`）
- 安全标识符引用（拒绝不安全名称）
- 带绑定参数的分页（MySQL/PostgreSQL/ClickHouse/达梦）
- `INSERT ... ON DUPLICATE KEY` / `ON CONFLICT` upsert（不支持的引擎返回 `ErrUnsupported`）
- MySQL/PostgreSQL 协议族别名

ORM 查询构建器仍保留自己的最小方言接口；本包是手写 Repository 的共享层，也是需要
ClickHouse 或达梦的应用的迁移路径。

```go
d, err := dialect.DialectFor("dm")
clause := d.Pagination(20, 40, 0)
args := d.PaginationArgs(20, 40)
```
