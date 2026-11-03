[English](CHANGELOG.md) | 简体中文

# 变更日志

本文件记录本项目的重要变更。

## [Unreleased]

- `migrate`：Flyway 兼容的 `flyway_schema_history` store、按行 CRC32 校验和、版本化 SQL 加载、baseline/repair 与 MySQL/PostgreSQL 锁。
- ORM 事务：`db.WithTx` 在 `Tx` 上执行回调，`tx.Repo[T]` 把同一套仓储代码（租户守卫、删除标记、Preload）绑到事务上。返回 nil 提交、错误回滚、panic 回滚后继续上抛。
- 可观测：`health` 注册表（存活 vs 就绪、关键与可选探针、panic 记为失败）与零依赖的 `metrics` 注册表（Prometheus 文本）。`web.WithHealth`/`web.WithMetrics` 挂载 `/healthz`、`/readyz`、`/metrics`，服务端记录请求数、在途请求与按**路由模板**分桶的延迟（不用路径）。`App` 持有两个注册表、发布 `gable_capabilities`，且只有 `Start` 装配完所有模块后才就绪。
- 初始脚手架：`App`、`Container`、模块生命周期、类型化配置、`glogs` 日志与稳定错误码。
- `web`：Gin MVC、六字段包络、绑定/校验、request id、recovery、access log、springdoc 形状的 OpenAPI 3.0.1 与 Swagger UI。
- CLI：`gable new`、`gable dev`、`gable version`。
- Tenant：single/saas 模式、根租户、Host 登录解析、fail-closed 请求作用域与运营方代入。
- ORM：sqlx model/repository、查询构造器、租户守卫、gcosid 主键、MySQL/PostgreSQL AutoMigrate。
- Pilot 示例：租户任务看板，支持内存/ORM store、single/saas、CRUD 与 OpenAPI。
- 修复：有 `ORDER BY` 时分页在 `LIMIT` 前补空格。
- 修复：`orm.Open` 把 `postgres` 当方言接收却当驱动名传给 `sql.Connect`；PostgreSQL 走 `pgx`，现在每个别名都会打开真正注册的驱动。
- DSL 首刀：`gable/v1` Model/Table/Form/Page、lint、Model DDL、`lcdp.ir/v1` preview、幂等 file apply 与 `gable dsl` 命令。
- DSL runtime：动态 `/dsl/data/<model>` CRUD、租户守卫、vamis 分页别名与 `gable dsl serve`；stdio MCP 工具（`gable dsl mcp`）。
- DSL diff 与历史：`dsl.DiffResources` 用 `push` 写入的同一份规范化文档比对期望集合，`gable dsl diff` 与 `gable dsl push --dry-run` 输出它（从未迁移的库按空库处理），`gable dsl history` 列出 revision，`gable dsl restore` 把旧版本写成下一个 revision 而不改写历史。
- DSL 数据库 store：`dsl.NewDBStore` 建 `gable_dsl_resource` 与 `gable_dsl_revision` 两张表，可从行里加载回集合；保存只写发生变化的文档，在一个事务内完成，每次写入进 revision 表，revision 号由数据库分配。`gable dsl push` 把目录推到 store，`gable dsl serve --load db` 从库里提供服务。
- 认证 cookie 会话：`Handlers.Cookies` 把同一对 token 放进 HttpOnly cookie，中间件先读请求头再读 cookie，logout 撤销并让其过期。
- 认证第二因子：TOTP 注册/激活，已激活账号登录改为返回 `{mfaRequired, ticket}`，ticket 一次性使用，恢复码哈希存储且一次性；关闭因子必须有有效验证码。
- ORM 关系与删除标记：`belongs_to`/`has_many` 字段通过显式 `Preload` 加载（整个结果集每个关系一次查询，并沿用父查询的租户与删除守卫）；`soft_delete` 时间戳或 `retain` 标志会把谓词加到每一次 scoped 读写删上。`Delete` 写标记、`Restore` 清除、`Purge` 真正删行。
- 插件：`plugin.From(plugin.Spec{...})` 适配保留自有容器类型或生命周期的模块，不必改名；没有钩子的 spec 保持纯接线，不会被记作已启动。
- Namespace：gable 保留 cache/Flyway/CosID/cluster 前缀配置；`gns` 只负责 namespace 值、context 与 Store SPI；`namespace` 用 gcache 实现 Store；gcache/gbus 不再依赖 gns。
- Auth：密码登录、JWT/opaque access token、opaque refresh 轮换、bearer 中间件、passport 兼容 Login/refresh/logout、OAuth2 token/introspection/revocation、authorization-code + PKCE server（consent/metadata）、OIDC id_token/JWKS/userinfo、社交回调 client。
