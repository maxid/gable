[English](README.md) | 简体中文

# gable

面向 Go 服务的代码优先应用框架：显式依赖容器、模块生命周期、类型化配置、
`glogs` 日志与稳定错误码。它组合 g\* 库族，而不是把库藏起来。

> 状态：**实验性脚手架**（v0.0.0-dev）。API 尚不稳定。

## 为什么再造一个框架

- **没有包级注册表**：依赖放在 `App` 容器里，由模块显式装配，测试可以先建一个没有全局状态的应用。
- **生命周期显式**：模块按依赖顺序启动、逆序停止；启动失败会回滚已启动的模块。
- **配置类型化**：YAML 支持 `${ENV}` 展开与 `GABLE_*` 覆盖，启动前统一校验。
- **错误带稳定码**：传输层可以只依赖错误码映射 HTTP 状态，不用 import 业务包。
- **内置 Web 与文档**：Gin 路由、六字段响应包络、绑定/校验、请求 ID、panic 恢复与访问日志；
  `/v3/api-docs` 输出 springdoc 形状的 OpenAPI 3.0.1，支持分组与 Swagger UI。
- **可配置多租户**：`single` 固定根租户；`saas` 登录按 Host/编码解析、请求按登录主体解析，
  可选运营方代入；租户守卫 fail-closed。
- **SQL 优先的轻 ORM**：`sqlx` + 小方言层、类型化 Repository、租户过滤、`gcosid` 发号，
  MySQL/PostgreSQL 的 `AutoMigrate`。
- **认证与 OAuth2**：密码登录、JWT 或 opaque access token、可轮换的 opaque refresh、
  bearer 中间件、passport 兼容响应、OAuth2 授权服务器（code + PKCE、consent、metadata）、
  token/introspection 端点与社交登录 PKCE 客户端。
- **可校验的 DSL**：`gable/v1` 的 Model/Table/Form/Page、lint、DDL 与 `lcdp.ir/v1` 预览、
  幂等 `apply`、动态 `/dsl/data/<model>` CRUD 与 MCP 工具；AI 可以不碰 Go 代码就生成、检查、
  应用并驱动。

## 快速开始

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/maxid/gable"
)

type hello struct{}

func (hello) ID() string                        { return "hello" }
func (hello) Register(c *gable.Container) error { c.Set("hello.greeting", "world"); return nil }
func (hello) Start(context.Context) error        { return nil }
func (hello) Stop(context.Context) error         { return nil }

func main() {
	app := gable.New()
	if err := app.Register(hello{}); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
```

## 包

| 包 | 职责 |
|---|---|
| `gable` | `App`、`Container`、模块生命周期 |
| `config` | 类型化 YAML + 环境变量配置 |
| `log` | `glogs` 日志构造 |
| `errors` | 稳定错误码与 HTTP 状态 |
| `web` | Gin MVC、包络、绑定/校验、OpenAPI 与 Swagger UI |
| `tenant` | single/saas 解析、上下文、登录 Host 规则、代入 |
| `orm` | sqlx Repository、租户守卫、迁移、gcosid 主键 |
| `dsl` | gable/v1 资源、lint、DDL、低码 IR、apply |
| `auth` | 密码/JWT/opaque token、refresh 轮换、passport 端点、OAuth2 authorize/token |
| `auth/oauthclient` | 社交登录的 authorization-code + PKCE 客户端 |

## 五行接入 HTTP

```go
server := web.New(web.WithTitle("demo"))
server.Register(myController{}) // Routes(r *web.Router)
app.Register(web.NewModule(server, "")) // 监听 config.http.addr
```

`GET /v3/api-docs` 输出 OpenAPI，`GET /v3/api-docs/{group}` 输出分组，
`GET /swagger-ui/index.html` 打开 Swagger UI。

## 多租户

```go
// single：所有请求固定根租户
server.UseTenant(tenant.NewSingleResolver(cfg.Tenant))

// saas：从登录主体解析请求租户
source := tenant.SourceFunc(func(ctx context.Context, r *http.Request) (tenant.Principal, bool, error) {
    return principalFromToken(r) // 你的认证中间件
})
resolver, _ := tenant.NewSaaSResolver(cfg.Tenant, store, source)
server.UseTenant(resolver)
```

`tenant.NewSaaSLoginResolver` 按「精确域名 → base_domain 下的单段子域名 → Host 最左标签 →
显式 tenantCode → 根租户」解析；显式编码与域名冲突或编码不存在时 fail-closed。运营方可用
配置的头（默认 `X-Tenant-Id`）代入，目标租户要经 Store 校验。

## ORM

```go
type User struct {
    ID       int64     `gable:"pk"`
    TenantID int64     `gable:"tenant"`
    Name     string    `gable:"size=64"`
    Created  time.Time `gable:"auto_create"`
}

repo, _ := orm.NewRepo[User](db, "sys_user", orm.WithIDGenerator(ids))
users, err := repo.Query().Where(orm.Eq("name", "alice")).All(ctx)
```

租户谓词来自 `tenant.FromContext`；缺失或不匹配 fail-closed。`repo.Unscoped()` 是显式逃生口；
`db.AutoMigrate(model)` 只创建缺失表。MySQL DSN 需带 `parseTime=true`，否则 `DATETIME`/`TIMESTAMP`
无法扫描进 `time.Time`。

## DSL

```bash
gable dsl lint --dir resources
gable dsl preview Model/task --dir resources --dialect postgres
gable dsl preview Page/task.page --dir resources --format yaml
gable dsl apply --dir resources --out .gable/dsl
```

首刀覆盖 `Model`、`Table`、`Form`、`Page`：

- `Model` → 规范化字段 + `CREATE TABLE`。
- `Table`/`Form` → `lcdp.ir/v1` 的 `TableDef`/`FormDef`。
- `Page` → vamis 可直接渲染的 `UiPage` widget 树。

`apply` 先校验、再写规范化资源，幂等。运行时数据 API 与 MCP 工具也已可用：

```bash
# 动态 CRUD：GET/POST /dsl/data/task，GET/PUT/DELETE /dsl/data/task/{id}
gable dsl serve --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# MCP stdio 工具面：dsl.schema / dsl.lint / dsl.preview / dsl.apply
gable dsl mcp --dir resources
```

数据 API 兼容 vamis CRUD 的 `page/perPage/orderBy/orderDir` 参数，并执行 Model 的租户守卫。
数据库存储、diff/revision 与 Api/Mcp kind 是下一刀。

## 认证

```go
service, _ := auth.NewTokenService(auth.Config{Secret: "change-me"}, users, clients, nil)
handlers := auth.NewHandlers(service, tenant.NewSingleLoginResolver(cfg.Tenant))
server.Register(handlers)
server.Use(handlers.Middleware()) // 之后的受保护路由
```

- `POST /passport/login` 返回前端使用的 camelCase `TokenVO`
  （`accessToken`、`refreshToken`、`expiresIn`、`key`、`needChangePassword` 等）。
- `AUTH_FORMAT=opaque`（或 `AccessOpaque`）签发平台格式
  `client:username:userType:uuid`；默认 JWT。
- `POST /oauth2/token` 支持 `authorization_code`、`password`、`refresh_token`、`client_credentials`；
  另有 `/oauth2/introspect` 与 `/oauth2/revoke`。
- `GET/POST /oauth2/authorize` 实现 code + PKCE，带 `Authorizer` consent 回调与
  redirect_uri 精确匹配；metadata 在 `/.well-known/oauth-authorization-server`。
- `auth/oauthclient.Handlers` 提供 `/oauth2/login/:provider` 与
  `/oauth2/callback/:provider`；应用把外部资料映射成本地 principal。
- refresh 轮换；logout 删除会话标记与 refresh token。
- `auth/oauthclient` 负责外部 provider 的 authorization-code + PKCE；应用把外部资料
  映射到本地用户后调用 `TokenService`。

## 示例

- `examples/hello`：最小 HTTP 应用，带 OpenAPI/Swagger。
- `examples/tenant`：SaaS 模式，Host 登录、主体租户、代入。
- `examples/pilot`：租户任务看板，完整 CRUD、内存/ORM 双存储、single/saas、OpenAPI，
  无数据库也能跑。
- `examples/dsl`：model/table/form/page 资源，lint 与 IR 预览。
- `examples/auth`：密码登录、受保护路由、OAuth2 token/introspection、JWT 与 opaque 两种模式。

## 规划中
- DSL：数据库存储、diff/revision、Api/Mcp kind。
- `ai` / `mcp`：对 `gai` 与 `gmcp` 的适配。
- OIDC id_token/JWKS、cookie 会话、MFA；更多 provider preset。
- `lowcode`：由应用托管 IR 与 vamis SDK。
- ORM：关系/Preload、软删、ClickHouse/达梦/TDengine 方言。
- `cli`：`gable gen`、`gable migrate`（在 `new`/`dev` 之外）。

## 安装

```bash
go get github.com/maxid/gable
```

## 许可证

MIT。见 [LICENSE](LICENSE)。
