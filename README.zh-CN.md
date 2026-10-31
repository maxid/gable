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

## 五行接入 HTTP

```go
server := web.New(web.WithTitle("demo"))
server.Register(myController{}) // Routes(r *web.Router)
app.Register(web.NewModule(server, "")) // 监听 config.http.addr
```

`GET /v3/api-docs` 输出 OpenAPI，`GET /v3/api-docs/{group}` 输出分组，
`GET /swagger-ui/index.html` 打开 Swagger UI。

## 规划中

- `orm`：方言感知的模型、查询构造器与迁移（先支持 MySQL/PostgreSQL）。
- `dsl`：模型/页面资源的运行时加载，文件或数据库存储。
- `ai` / `mcp`：对 `gai` 与 `gmcp` 的适配。
- `lowcode`：页面/表格/表单资源编译为 LCDP IR。
- `cli`：`gable gen`、`gable migrate`（在 `new`/`dev` 之外）。

## 安装

```bash
go get github.com/maxid/gable
```

## 许可证

MIT。见 [LICENSE](LICENSE)。
