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
`db.AutoMigrate(model)` 只创建缺失表。

## 规划中
- `dsl`：模型/页面资源的运行时加载，文件或数据库存储。
- `ai` / `mcp`：对 `gai` 与 `gmcp` 的适配。
- `lowcode`：页面/表格/表单资源编译为 LCDP IR。
- ORM：关系/Preload、软删、ClickHouse/达梦/TDengine 方言。
- `cli`：`gable gen`、`gable migrate`（在 `new`/`dev` 之外）。

## 安装

```bash
go get github.com/maxid/gable
```

## 许可证

MIT。见 [LICENSE](LICENSE)。
