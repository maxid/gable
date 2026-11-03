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
- **一个 namespace，在框架内派生**：缓存、Flyway、CosID 与集群前缀由 gable 负责；
  `gns` 只承载 namespace 值/context 与持久化 namespace 定义的 `Store` SPI。
- **认证与 OAuth2**：密码登录、JWT 或 opaque access token、可轮换的 opaque refresh、
  bearer 中间件、passport 兼容响应、OAuth2 授权服务器（code + PKCE、consent、metadata）、
  OIDC id_token/JWKS/userinfo、token/introspection 端点与社交登录 PKCE 客户端。
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
| `dsl` | gable/v1 资源、lint、DDL、低码 IR、apply、文件/数据库存储、diff/history/restore |
| `validate` | 结构体校验，输出可进包络的字段列表 |
| `i18n` | 多语言消息包，支持地区与默认回落 |
| `health` | 存活/就绪探针，支持关键与可选依赖 |
| `metrics` | Prometheus 文本格式的计数器、gauge 与直方图 |
| `cli` | `new`、`dev`、`dsl`、`migrate`、`inspect`、`version` |
| `auth` | 密码/JWT/opaque token、refresh 轮换、passport 端点、OAuth2 authorize/token |
| `cache` | 继承应用 namespace 的 gcache 构造器 |
| `namespace` | gcache 驱动的 `gns.Store` 实现（内存或 Redis） |
| `auth/oauthclient` | 社交登录的 authorization-code + PKCE 客户端 |

能力选择（按 id 选、解析依赖、确定性顺序、版本清单、可选生命周期）不在本库：它由
[`gplugin`](https://github.com/maxid/gplugin) 提供，容器类型作为参数传入，`gable.Container`
只是其中一种用法。声明了生命周期的模块的方法集与 `gable.Module` 完全一致，因此装配结果可以直接
注册到应用上。

## 五行接入 HTTP

```go
server := web.New(web.WithTitle("demo"))
server.Register(myController{}) // Routes(r *web.Router)
app.Register(web.NewModule(server, "")) // 监听 config.http.addr
```

`GET /v3/api-docs` 输出 OpenAPI，`GET /v3/api-docs/{group}` 输出分组，
`GET /swagger-ui/index.html` 打开 Swagger UI。

操作可以挂 vendor extension，网关提示或 springdoc 风格的标记就是这样进文档的：

```go
router.GET("/items", listItems).Doc("列出条目").
    Extension("x-springdoc-paginated", true)
```

名字必须以 `x-` 开头——否则在接线期直接 panic，而不是被工具丢弃、或被渲染成 operation schema 的字段。
扩展是**平铺**进 operation 对象的（不会嵌在 `extensions` 键下），没有扩展的操作渲染结果与以前完全相同。
路由可以用 OpenAPI 的 `{id}` 写法，注册时翻译成 Gin 的 `:id`，文档保留规范写法。

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

逻辑删除有两种写法，任选其一，守卫自动生效：

```go
type Doc struct {
    ID        int64      `gable:"pk"`
    Title     string     `gable:"size=128"`
    DeletedAt *time.Time `gable:"soft_delete"` // 一旦非 NULL 即隐藏
}

type Flag struct { // 或者用标志位
    ID     int64 `gable:"pk"`
    Name   string
    Retain bool `gable:"retain"` // 一旦为 true 即隐藏
}
```

声明的列会出现在每一次 scoped 查询/更新/删除里（`deleted_at IS NULL` / `retain = false`），
调用方无需记着加。`Delete` 写标记，`Restore` 清除，`Purge` 是唯一真正删行的入口；
`Unscoped()` 解除守卫；`Update` 不碰标记列，陈旧的实体无法把行“复活”。

关系在实体上声明，只在显式请求时加载：

```go
type Order struct {
    ID     int64  `gable:"pk"`
    UserID int64
    User   *User  `gable:"belongs_to=sys_user,foreign_key=user_id"`
    Items  []Item `gable:"has_many=order_item,foreign_key=order_id"`
}

orders, err := orders.Query().
    Preload("User", users).
    Preload("Items", items).
    All(ctx)
```

事务用回调表达，回调里的仓储和外面的是同一套代码：

```go
err := db.WithTx(ctx, func(ctx context.Context, tx *orm.Tx) error {
    accounts, _ := tx.Repo[Account]("account")
    return accounts.Update(ctx, debited) // 以及后续任意语句
})
```

返回 nil 即提交；返回错误或 panic 都回滚——panic 会继续向上抛，不会被吞掉，
调用方的 defer 清理不会误以为事务成功。租户守卫与删除标记在事务内同样生效，
因为只是换了一个执行器，仓储代码没变。

关系是元数据不是列。`Preload` 对**整个结果集**每个关系只查一次，绝不按行查；
被加载的关联读取沿用同一套租户与逻辑删除守卫。没有懒加载，handler 不会意外打出 N+1。

Schema 迁移在独立库 [`gmigrate`](https://github.com/maxid/gmigrate)；gable 只保留开发用的 `AutoMigrate`。

## 从 Go 代码生成 DSL

```bash
gable gen dsl --pkg ./internal/model --out resources --table-prefix app_ --dry-run
gable gen dsl --pkg ./internal/model --out resources          # 写入 Model-<name>.yaml
```

生成器只解析语法（不做类型检查），把带 `gable` tag 的结构体转成 `Model` 资源：

- 导出字段成为列——`gable:"-"` 与关系字段（`has_many`、`belongs_to`）不会，因为关系不是列；
- `pk`、`tenant`、`unique`、`auto_create`、`auto_update`、`auto`、`size=N` 转成对应标志；
- `column=x` 改名，`table=x` 指定表名（否则用蛇形结构体名，加 `--table-prefix` 前缀）；
- Go 类型在 DSL 里没有对应物的字段会被**省略并在 stderr 给出警告**，绝不静默丢弃——
  目前包括 `soft_delete`/`retain` 标记与切片/结构体字段。

同一份输入写两次不会改动文件，所以放进 CI 时“没变化”就是真的没变化。产物是骨架：
label、enum、default 与 Table/Form 叠加层仍需手写，因为那才是 DSL 存在的意义。

## DSL

```bash
gable dsl lint --dir resources
gable dsl preview Model/task --dir resources --dialect postgres
gable dsl preview Page/task.page --dir resources --format yaml
gable dsl apply --dir resources --out .gable/dsl
```

首刀覆盖 `Model`、`Table`、`Form`、`Page`；`Api` 与 `Mcp` 则把动作与工具按**名字**绑到你注册的 Go handler 上：

- `Model` → 规范化字段 + `CREATE TABLE`。
- `Table`/`Form` → `lcdp.ir/v1` 的 `TableDef`/`FormDef`。
- `Page` → vamis 可直接渲染的 `UiPage` widget 树。

```yaml
# Api：一个动作，绑定已注册的 handler，并声明所需守卫
apiVersion: gable/v1
kind: Api
metadata: {name: task.close, title: 关闭任务}
spec:
  handler: tasks.close     # 只是一个名字，绝不是包名或文件路径
  model: task
  method: POST
  path: task/{id}/close
  guards: [tenant]
  audit: true
---
# Mcp：模型看到的那部分元数据，实现仍在 Go
apiVersion: gable/v1
kind: Mcp
metadata: {name: tasks.search}
spec:
  handler: tasks.search
  description: 按标题搜索任务。
  params:
    type: object
    properties: {q: {type: string}}
    required: [q]
```

```go
registry := dsl.NewRegistry().
    AddGuard("tenant", requireTenant).
    API("tasks.close", closeTask).
    Tool("tasks.search", searchTasks)

bindings, err := registry.Routes(set, server.Router(), "/dsl/api") // POST /dsl/api/task/{id}/close
tools, err := registry.Tools(set, mcpServer)                        // tasks.search
```

资源里永远不写 Go 代码，只有符号化的 handler 名，所以「资源里没有任意代码」这条规则仍然成立，
同时低代码页面又能调到真实业务逻辑。名字没注册过——handler 或 guard——会在绑定期直接失败，
而不是生成一条生产上 500 的路由，或更糟：无守卫地跑。守卫按资源声明顺序执行，所以 `[tenant, role]`
意味着角色检查能看到租户。路由用 OpenAPI 写法（`{id}`），由 router 注册成 Gin 的 `:id`。

`apply` 先校验、再写规范化资源，幂等。运行时数据 API 与 MCP 工具也已可用：

```bash
# 动态 CRUD：GET/POST /dsl/data/task，GET/PUT/DELETE /dsl/data/task/{id}
gable dsl serve --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# MCP stdio 工具面：dsl.schema / dsl.lint / dsl.preview / dsl.apply
gable dsl mcp --dir resources
```

数据 API 兼容 vamis CRUD 的 `page/perPage/orderBy/orderDir` 参数，并执行 Model 的租户守卫。

### 用数据库存储资源

目录是 GitOps 路径；多个实例共用一份定义、或需要在运行时编辑时走数据库。两种存储写的是
同一份规范化 YAML，所以同一资源不论来自哪种存储，内容都一样。

```bash
# 先看会改什么（只读；从未迁移过的库按空库处理）
gable dsl diff --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true' --lines
gable dsl push --dir resources --dry-run --lines --dsn '...'   # 输出相同，但入口在 push

# 目录 -> 数据库（先 lint，只写发生变化的文档）
gable dsl push --dir resources --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# 服务读取 push 写入的内容，而不是目录
gable dsl serve --load db --dsn 'user:pass@tcp(127.0.0.1:3306)/app?parseTime=true'

# 审计历史并回滚单个资源
gable dsl history Model/task --dsn '...'
gable dsl restore Model/task --revision 3 --dsn '...'
```

### 一套定义，多个数据库

Model 可以声明 connector——「驱动 + DSN + 连接池」的名字——于是同一份定义换个库不用改：

```yaml
apiVersion: gable/v1
kind: Model
metadata: {name: revenue}
spec:
  table: revenue
  connector: reporting        # 留空即默认 connector
  fields:
    - {name: id, type: int64, primary: true}
    - {name: amount, type: int64}
```

```bash
gable dsl serve --load db --dsn '.../app' \
  --connector 'reporting=.../reporting' --addr :8088
```

该模型的所有语句——列表、单条、增删改，以及启动时的 `Migrate`——都发往它声明的库。
声明了未注册 connector 的 Model 会在**集合绑定时**（构造与每次重载）被拒绝，而不是第一次请求才 500。

方言不是 connector：方言决定 SQL 语法，connector 决定语句发往哪个库。一个部署通常只有一种方言、多个 connector。
**跨 connector 事务不支持**，也不假装支持——跨库写入需要应用层补偿，而不是框架的承诺。

### 不重启地重载

```bash
gable dsl serve --load db --reload 30s --dsn '...'
kill -HUP <pid>            # 立即重载，不必等轮询
```

重载是**原子替换**资源快照：一个请求要么看到完整旧集，要么看到完整新集。库里新增的 Model 在下一个周期就可访问
（建表方式与启动时一致），从库里移除的 Model 随之不可访问——这就是「由资源驱动的数据 API」里 unload 的含义。
不通过 lint 的集合会被**拒绝**，运行中的快照继续服务：日志会指出坏资源，一次错误编辑不会变成一次故障。

资源还可以声明「能对它做什么」：

```yaml
metadata:
  name: task
  readonly: true          # 已存的 readonly 资源：push 与 restore 都拒绝覆盖
  status: deprecated      # 仍会加载与提供服务；"active" 与留空等价
  tags: [admin, task]     # 供 UI 分组与筛选列表
  sort: 10                # 同类资源排序，数字小的在前
```

`readonly` 是对**库中已存文档**判定的，所以文件无法先给自己去掉标记再覆盖库——这条护栏正是让手写资源能与生成资源共存的东西。

`diff` 比较的正是 `push` 会写的那份规范化文档，所以 dry-run 与真实写入不可能得出不同结论；
只存在于库里的资源报 `removed`（不会隐式删除）。`restore` 把旧文档写成**下一个** revision 而不是改写历史，
若该版本已是当前内容则什么都不做。

库侧接口是 `dsl.NewDBStore`（`Migrate`/`Load`/`Save`），背后两张表：
`gable_dsl_resource`（每个 `Kind/name` 一行，存当前文档与 revision）与
`gable_dsl_revision`（每次写入的文档，供下一刀的 diff/历史使用）。`Save` 先校验整个集合，
跳过没有变化的文档，其余在一个事务里写入——revision 由数据库分配，两个实例同时保存不会撞号。
删除不是隐式的：从集合里去掉一个资源，库里的行仍在。

基于这些 revision 的 diff/dry-run 与 Api/Mcp kind 是下一刀。

## 配置热更新

```go
watcher, err := config.Watch("gable.yaml", config.WithOnChange(func(cfg *config.Config) error {
    return featureFlags.Rebind(cfg) // 返回错误则保留旧配置
}))
defer watcher.Close()
cfg := watcher.Config() // 每次重载换一个新指针，不原地修改
```

监听的是目录而不是文件：编辑器与 ConfigMap 更新都是替换文件，监听文件会随旧 inode 失效。
一次保存产生的多次写事件会合并成一次重载；文件不合法时保留上一份可用配置并通过 `WithOnError`
报错——一个拼写错误不该把运行中的进程弄挂；订阅者返回错误会阻止切换，避免“半应用”的重载。

热更新替换的是**配置值**，不会重新注册模块。依赖 Register 期设置的接线（监听地址、连接池）仍需重启，
这是刻意的：否则模块就得绕过生命周期去改活状态。

## 校验与文案

```go
validator := validate.New()          // 英文默认消息，字段名取自 json tag
validator.SetMessage("min", "至少 %s 个字符")
validator.Register("slug", "必须是小写 slug", isSlug)

if issues := validator.Struct(payload); len(issues) > 0 {
    return issues                    // 400 invalid_argument，data 为 {fields:[{field,rule,message}]}
}
```

每个失败字段都会报出来，错误文本是一行可直接进日志；包络里的 data 逐条给出
`field`/`rule`/`message`，与 Gin 绑定错误同形状——无论哪一层拒绝，客户端只解析一种格式。
自定义规则注册一次即带消息；`Translator`（或 nil）决定语言，`i18n.Bundle` 是它通常读取的包：

```go
bundle := i18n.New("en")
_ = bundle.Load("zh", []byte("required: 必填\nemail: 邮箱格式不正确\n"))
bundle.Translate("zh-CN", "email", nil) // 邮箱格式不正确：地区回落到基础语言
```

解析顺序是精确语言 → 基础语言（`zh-CN` → `zh`）→ 默认语言 → 键本身，所以缺文案会显示键而不是空串。
占位符写作 `{name}`，未知占位符保持原样而不是被清空。

## 分页

```go
page, err := ctx.Page("name", "created_at") // 排序白名单
if err != nil {
    return err // 400，并列出允许的列
}
rows, err := repo.Query().OrderBy(page.Sort, page.Desc).Limit(page.Limit()).Offset(page.Offset()).All(ctx)
```

`page`、`size`、`sort`、`order` 与现网前端发送的 vamis 别名（`perPage`、`orderBy`、`orderDir`）一起读取。
排序列不在白名单内返回 400 并列出允许值——静默忽略会返回错误顺序，看起来像数据 bug。
`size` 超过 200 是**截断**而不是报错：要一万行是性能问题，不是值得 400 的客户端错误。

## 可观测

两个注册表挂在 `App` 上，正好对应运维要问的两个问题；Web 服务负责挂载：

```go
app := gable.New(gable.WithConfig(cfg))
app.Health().Add("database", true, func(ctx context.Context) error { return db.PingContext(ctx) })
app.Metrics().Counter("orders_paid_total", "已支付订单。").Inc()

server := web.New(web.WithHealth(app.Health()), web.WithMetrics(app.Metrics()))
```

- `GET /healthz` —— 存活。除进程正在关停外都返回 200：依赖挂了不该让健康的进程被重启。
- `GET /readyz` —— 就绪。**关键**探针失败或 `Start` 尚未完成时返回 503，让负载均衡摘流量而不是杀进程；
  探针报错会带上原因，panic 记为失败而不拖垮进程，非关键探针只上报不改变结论。
- `GET /metrics` —— Prometheus 文本。服务端记录 `http_requests_total`（方法/路由/状态）、
  `http_request_duration_seconds`（分桶）与 `http_inflight_requests`，App 发布 `gable_capabilities`。
  标签用**路由模板**而不是路径，避免一个资源 id 造出一条时间序列。

两个端点都在六字段包络之外：抓取器和编排器读的是状态码与文本，不是应用的响应契约。

## 连接远端 MCP 服务器

`gable/mcp` 是双向的：既是你挂载的服务端，也是连别人服务端的客户端。

```go
client, err := mcp.Dial(ctx, mcp.Endpoint{URL: "https://tools.internal/mcp"})
defer client.Close()

tools, err := client.ListTools(ctx)
result, err := client.CallTool(ctx, "orders.summary", json.RawMessage(`{"days":7}`))
```

HTTP 端点与 stdio 子进程（`Endpoint{Command: []string{"gable","dsl","mcp","--dir","."}}`）只差在传输方式：
同一个客户端都能列工具、调工具。stdio 客户端**拥有**那个子进程——`Close` 会停掉它，并且持续读它的 stderr，
以免一个爱写日志的服务器写满管道后卡死。

工具失败会以带 `IsError` 的结果返回，因为 MCP 就是这么建模的；调用方能区分「工具说不行」和「传输断了」。

```bash
gable mcp list  --url https://tools.internal/mcp
gable mcp call  orders.summary --url https://tools.internal/mcp --arguments '{"days":7}'

# DSL 工具面同样可以这样访问
gable mcp list --command gable --arg dsl --arg mcp --arg --dir --arg ./resources
```

`mcp call` 在工具报错时以非零码退出，脚本与人看到同一个结论。

## 流式回答

`ai.Service` 与非流式的 `Ask` 是同一套有界工具循环，只是把结果翻过来：

```go
stream, err := service.Stream(ctx, sessionID, "本周有多少订单？")
defer stream.Close()

for {
    event, err := stream.Recv()
    if err == io.EOF { break }
    if err != nil { return err }
    switch {
    case event.Delta != "":      send(event.Delta)          // 边生成边发
    case event.ToolCall != nil:  show(event.ToolCall.Name)  // “正在调用 orders.summary…”
    case event.ToolResult != nil: show(event.ToolResult)
    case event.Done:             return nil
    }
}
```

`Done` 是普通事件，下一次 `Recv` 才返回 `io.EOF`——把「最后一个事件」塞进 error 里，
会让先判错的调用方悄悄丢掉回答的结尾。

给浏览器用时 `stream.ServeSSE(w)` 会写好 `text/event-stream`、每个事件 flush、`[DONE]` 结束标记，
并带上 `X-Accel-Buffering: no`，避免代理把回答攒到连接关闭才发。响应头发出去之后再出错，
会以 `event: error` 帧送出，而不是被吞掉。

provider 按名字选择：`openai`（OpenAI 以及所有复制该线的服务）或 `anthropic`（Messages API：
system 在顶层、工具调用是 content block）。两边都不像的供应商应该有自己的适配器，而不是一堆开关。

## 交付打包

```bash
gable pack --config gable.yaml --dir resources --assets public --out app.tar.gz --app shop
gable restore app.tar.gz --out /srv/app
gable token --config gable.yaml --secret "$SECRET" --username ada --ttl 15m
```

`pack` 把配置、DSL 资源与静态资产打进一个包，并附一份索引（`bundle.json`）：每个文件的校验和 + 内容版本号。
条目排序、mtime 归零、丢弃属主，所以同样的输入永远得到同样的版本号——两个包即使在不同时间打，也能直接比较。

`restore` **先校验全部文件再写任何一个**：坏包直接失败，不会留下「半个应用」这种更难排查的状态；
目标目录非空时拒绝，除非给 `--force`。

`token` 用与应用相同的 issuer/secret/TTL 签一枚短期令牌，正是 CI 冒烟在应用能登录之前需要的东西。
stdout 上只有令牌本身，所以 `TOKEN=$(gable token ...)` 可用。

## 命名空间

```yaml
namespace:
  root: app
  cache: app          # 省略时继承 root
  flyway: app
  cosid: none         # 保留既有段名
  cluster: app
  timeSeries: app
```

`app.Namespace()` 返回 gable 自己的 `config.Namespace`：

```go
ns := app.Namespace()
ns.CacheName()                 // gcache namespace
ns.FlywayTable("schema_migrations") // app_schema_migrations
ns.SegmentKey("sys_dict")      // app.sys_dict（cosid: none 时仍是 sys_dict）
ns.CosIDName()                 // app（cosid: none 时为空串）
ns.Topic("iothub_raw")         // app.iothub_raw
```

`gable/cache.New` 把 `CacheName` 传给 gcache，auth 的 token store key 使用同一 namespace。
gns 不再派生这些值：它只把 `gns.Namespace` 放进 context，并提供 `Store` SPI。
`namespace` 包用 gcache 实现该 SPI，同一适配器可跑内存或 Redis：

```go
store, _ := namespace.NewStore(cache)
manager, _ := gns.NewManager(store)
ctx, err := manager.Bind(ctx, "app")
```

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
- 配置 RSA 私钥后，code 流在 `openid` scope 下额外签发 RS256 `id_token`；
  `/oauth2/jwks` 发布公钥，`/oauth2/userinfo` 返回资料。
- `auth/oauthclient.Handlers` 提供 `/oauth2/login/:provider` 与
  `/oauth2/callback/:provider`；应用把外部资料映射成本地 principal。
- refresh 轮换；logout 删除会话标记与 refresh token。
- `auth/oauthclient` 负责外部 provider 的 authorization-code + PKCE；应用把外部资料
  映射到本地用户后调用 `TokenService`。

### Cookie 会话

浏览器无法在页面导航时带 `Authorization` 头，所以由应用自己托管前端时，会话要放进 cookie：

```go
handlers.Cookies = &auth.CookieSession{Secure: true} // HttpOnly、SameSite=Lax
```

cookie 里就是响应体里同一对 token——它只是传输方式，不是第二套会话格式。中间件先看请求头、
再看 cookie；logout 撤销 token 并让两个 cookie 过期；refresh 在请求体没带 token 时读 cookie。
注意：撤销 JWT 只影响后续 refresh，无状态 JWT 在过期前仍然有效——需要“登出即刻失效”时用
`AccessOpaque`。

### 第二因子（TOTP）

MFA 按账号开启，且只有走完激活才生效：

```go
mfa, _ := auth.NewMFAService(auth.DefaultTOTPConfig("myapp"), auth.NewMemoryMFAStore())
handlers.MFA = mfa
```

- `POST /passport/mfa/enroll` 返回 base32 secret、`otpauth://` URL 与 10 个恢复码——只显示一次，
  能被再次展示的码就不是第二因子了；
- `POST /passport/mfa/activate` 必须用验证器里的码确认，所以“ enrolling 到一半”不会把任何人锁在外面；
- 激活后 `POST /passport/login` 不再返回 token，而是 `{mfaRequired, ticket}`；
  `POST /passport/mfa/verify` 用 ticket + 验证码换 token；
- ticket 不可猜、有效期 5 分钟，且**第一次尝试即消耗**（即使验证码是错的），给不了暴力试错空间；
- 恢复码落库为哈希，每个只能用一次；
- `POST /passport/mfa/disable` 需要有效验证码：光有被盗会话不能把因子关掉。

### 多副本的共享状态

MFA 是账号的属性，不是接受注册的那个 pod 的属性。内存实现只适合单进程；多副本部署要换成共享实现：

```go
store, _ := gredis.NewStore(redisClient)          // github.com/maxid/gcache/redis
mfaStore, challengeStore := auth.NewRedisStores(store, 5*time.Minute)

mfa, _ := auth.NewMFAService(auth.DefaultTOTPConfig("myapp"), mfaStore)
handlers.MFA = mfa
handlers.Challenges = challengeStore
```

它们接收的是 gcache 的**共享存储**而不是 `gcache.Cache`：缓存的本地层会被其他副本的写入异步失效，
对缓存无所谓，对认证判定则是错的。集成测试正是抓到了这一点——一个副本接受了另一个副本已经消费过的验证码。

TOTP 验证码是一次性的：`MFAService` 记录已接受的时间步，只接受**更新**的步，所以同一个码在它的
30 秒窗口内无法重放，跨副本（共享存储时）不行，时钟回拨后也不行。读改写在本进程内按账号串行；
跨副本的那半条保证由共享存储提供。

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
- cookie 会话、MFA；更多 provider preset。
- `lowcode`：由应用托管 IR 与 vamis SDK。
- ORM：关系/Preload、软删、ClickHouse/达梦/TDengine 方言。
- `cli`：`gable gen`（在 `new`/`dev` 之外）。

## 安装

```bash
go get github.com/maxid/gable
```

## 许可证

MIT。见 [LICENSE](LICENSE)。
