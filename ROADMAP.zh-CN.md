[English](ROADMAP.md) | 简体中文

# 路线图

- [x] 显式容器与模块生命周期
- [x] 类型化配置（YAML + 环境变量）
- [x] `glogs` logger
- [x] 稳定错误码
- [x] Web MVC、绑定/校验、响应包络
- [x] springdoc 形状的 OpenAPI 3.0.1 与 Swagger UI
- [x] Namespace：gable 前缀配置、gns context/Store SPI、gcache Store 适配器
- [x] Tenant：single/saas、Host 登录规则、fail-closed 作用域、代入
- [x] ORM 核心：模型、查询构造器、CRUD、租户守卫、gcosid 主键、AutoMigrate（MySQL/PostgreSQL）
- [x] Migrate：Flyway 兼容历史、CRC32 校验和、baseline/repair（MySQL/PostgreSQL）
- [x] ORM：关系（`belongs_to`/`has_many` + `Preload`）、软删/保留标志守卫与 `Restore`/`Purge`
- [ ] ORM：ClickHouse/达梦/TDengine 方言
- [x] Pilot：租户任务看板（single/saas、内存/ORM、OpenAPI）
- [x] DSL：Model/Table/Form/Page、lint、DDL/IR preview、幂等 apply（file store）
- [x] DSL：运行时 data API（/dsl/data/<model>），租户守卫与 vamis 参数
- [x] DSL：MCP 工具（schema/lint/preview/apply）
- [x] DSL：数据库 store（`NewDBStore` 的 migrate/load/save，一资源一行，每次写入进 revision 表）、`gable dsl push`、`serve --load db`
- [ ] DSL：基于已存 revision 的 diff/dry-run、Api/Mcp kind
- [x] Auth 核心：密码、JWT/opaque access、opaque refresh 轮换、bearer 中间件
- [x] Passport 兼容 Login/refresh/logout + OAuth2 token/introspection/revocation
- [x] OAuth2 client：authorization code + PKCE
- [x] OAuth2 授权服务器：authorize + code + PKCE + consent + metadata
- [x] 社交登录回调（/oauth2/login|callback/:provider）
- [x] OIDC id_token/JWKS/userinfo（RS256）
- [x] Cookie 会话与 MFA（TOTP + 一次性恢复码 + 登录 ticket）
- [x] `rpc`：local + HTTP/JSON binding，resolver 端口（Nacos adapter 随部署提供）
- [ ] `gai` 与 `gmcp` adapter
- [ ] 低代码资源编译到 LCDP IR
- [x] CLI：`new`、`dev`、`version`
- [ ] CLI：`gen`、`migrate`（库已存在，命令未接）
- [ ] v1.0 API 稳定化
