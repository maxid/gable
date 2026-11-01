[English](README.md) | 简体中文

# Pilot：租户任务看板

一个小而完整的 gable 用例：HTTP CRUD、租户解析、ORM 租户守卫、OpenAPI 与 Swagger UI。

## 不接数据库运行

```bash
go run .
```

内存 store 让示例可直接运行。`single` 模式把每个请求绑定到根租户（`management`，id 1）。

```bash
curl -s -X POST localhost:8082/tasks -H 'Content-Type: application/json' -d '{"title":"write docs"}'
curl -s localhost:8082/tasks
curl -s localhost:8082/whoami
open http://localhost:8082/swagger-ui/index.html
```

## 接 MySQL/PostgreSQL

```bash
GABLE_DB_DRIVER=mysql GABLE_DB_DSN='user:pass@tcp(127.0.0.1:3306)/pilot?parseTime=true' go run .
```

`AutoMigrate` 在缺失时创建 `pilot_task`。ID 来自 `gcosid` snowflake；多实例部署设置 `GABLE_MACHINE_ID`。

## 多租户模式

```bash
GABLE_TENANT_MODE=saas GABLE_TENANT_BASE_DOMAIN=example.com go run .
```

Pilot 预置 `management`/`acme`/`beta`。源码读取 `X-User-Tenant` 与 `X-Operator` header **仅用于演示**；真实应用从 auth 中间件读取 principal。

```bash
curl -s -H 'Host: acme.example.com' 'localhost:8082/login/options'
curl -s -H 'X-User-Tenant: 2' localhost:8082/whoami
curl -s -H 'X-User-Tenant: 2' -H 'X-Operator: 1' -H 'X-Tenant-Id: 3' localhost:8082/whoami
```

缺少 principal 会被拒绝（`tenant_required`）；租户 2 创建的任务对租户 3 不可见。
