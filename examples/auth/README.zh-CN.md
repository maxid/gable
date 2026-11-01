[English](README.md) | 简体中文

# Auth 示例

密码登录、JWT（或 opaque）access token、可轮换 refresh token、bearer 中间件，以及 OAuth2 兼容的 token/introspection 端点。

```bash
go run .
```

登录并调用受保护路由：

```bash
TOKEN=$(curl -s -X POST localhost:8083/passport/login   -H 'Content-Type: application/json'   -d '{"username":"admin","password":"secret","clientId":"console","clientSecret":"client-secret"}'   | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["accessToken"])')

curl -s -H "Authorization: Bearer $TOKEN" localhost:8083/protected
curl -s -H "Authorization: Bearer $TOKEN" localhost:8083/passport/me
```

OAuth2 token 端点（form 编码）：

```bash
curl -s -X POST localhost:8083/oauth2/token   -d 'grant_type=password&username=admin&password=secret&client_id=console&client_secret=client-secret'
```

`AUTH_FORMAT=opaque go run .` 切换到平台 opaque access token 格式（`client:username:userType:uuid`），可在 `/oauth2/introspect` 自省。示例源码使用内存 store；生产环境注入由应用数据库支撑的 UserStore/ClientStore。OAuth2 client 配置（GitHub/Google/微信）位于 `auth/oauthclient`；provider 回调只需把外部 profile 映射到本地用户并调用 `TokenService`。
