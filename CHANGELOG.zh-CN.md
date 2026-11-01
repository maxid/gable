[English](CHANGELOG.md) | 简体中文

# 变更日志

本文件记录本项目的重要变更。

## [Unreleased]

- `migrate`：Flyway 兼容的 `flyway_schema_history` store、按行 CRC32 校验和、版本化 SQL 加载、baseline/repair 与 MySQL/PostgreSQL 锁。
- 初始脚手架：`App`、`Container`、模块生命周期、类型化配置、`glogs` 日志与稳定错误码。
- `web`：Gin MVC、六字段包络、绑定/校验、request id、recovery、access log、springdoc 形状的 OpenAPI 3.0.1 与 Swagger UI。
- CLI：`gable new`、`gable dev`、`gable version`。
- Tenant：single/saas 模式、根租户、Host 登录解析、fail-closed 请求作用域与运营方代入。
- ORM：sqlx model/repository、查询构造器、租户守卫、gcosid 主键、MySQL/PostgreSQL AutoMigrate。
- Pilot 示例：租户任务看板，支持内存/ORM store、single/saas、CRUD 与 OpenAPI。
- 修复：有 `ORDER BY` 时分页在 `LIMIT` 前补空格。
- DSL 首刀：`gable/v1` Model/Table/Form/Page、lint、Model DDL、`lcdp.ir/v1` preview、幂等 file apply 与 `gable dsl` 命令。
- DSL runtime：动态 `/dsl/data/<model>` CRUD、租户守卫、vamis 分页别名与 `gable dsl serve`；stdio MCP 工具（`gable dsl mcp`）。
- Namespace：gable 保留 cache/Flyway/CosID/cluster 前缀配置；`gns` 只负责 namespace 值、context 与 Store SPI；`namespace` 用 gcache 实现 Store；gcache/gbus 不再依赖 gns。
- Auth：密码登录、JWT/opaque access token、opaque refresh 轮换、bearer 中间件、passport 兼容 Login/refresh/logout、OAuth2 token/introspection/revocation、authorization-code + PKCE server（consent/metadata）、OIDC id_token/JWKS/userinfo、社交回调 client。
