[English](README.md) | 简体中文

# DSL 示例：model + table + form + page

资源描述一个租户任务看板。它们就是普通 YAML，AI 或人都编辑同一份 artifact；gable 负责 lint、渲染低代码 IR 并写出规范化结果。

```bash
cd examples/dsl

# 校验所有资源及其引用
go run ../../cmd/gable dsl lint --dir resources

# model -> DDL（mysql | postgres）
go run ../../cmd/gable dsl preview Model/task --dir resources --dialect postgres

# table/form/page -> lcdp.ir/v1
go run ../../cmd/gable dsl preview Table/task.table --dir resources
go run ../../cmd/gable dsl preview Form/task.form --dir resources
go run ../../cmd/gable dsl preview Page/task.page --dir resources --format yaml

# 把资源规范化到运行时目录（幂等）
go run ../../cmd/gable dsl apply --dir resources --out .gable/dsl
```

Page preview 是 `UiPage`，vamis runtime 可直接渲染；table/form preview 是 vamis 已能编译的 `TableDef`/`FormDef`；Model preview 包含 `CREATE TABLE`。

## 运行时 data API

接真实 MySQL/PostgreSQL 后，`dsl serve` 会迁移模型并提供动态 CRUD。表的 `runtimeApi` 指向 `/dsl/data/task`。

```bash
GABLE_DSN='user:pass@tcp(127.0.0.1:3306)/app?parseTime=true' go run ../../cmd/gable dsl serve --dir resources --dsn "$GABLE_DSN" --addr :8088

curl -s -X POST localhost:8088/dsl/data/task -H 'Content-Type: application/json' -d '{"title":"write docs"}'
curl -s 'localhost:8088/dsl/data/task?page=1&perPage=10&orderBy=title&orderDir=desc'
```

默认 single-tenant（`management`，id 1）。真实多租户应用在挂载同一 `Runtime.Routes` 前安装自己的 auth source 与 `tenant.SaaSResolver`。

## MCP 工具

```bash
go run ../../cmd/gable dsl mcp --dir resources
```

stdio server 暴露 `dsl.schema`、`dsl.lint`、`dsl.preview`、`dsl.apply`；这是 AI agent 驱动的循环（generate → lint → preview → apply）。DB store、diff/revision 与 Api/Mcp kind 是下一刀。
