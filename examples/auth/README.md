# Auth example

Password login, JWT (or opaque) access tokens, rotating refresh tokens,
bearer middleware, and the OAuth2-compatible token/introspection endpoints.

```bash
go run .
```

Login and call a protected route:

```bash
TOKEN=$(curl -s -X POST localhost:8083/passport/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret","clientId":"console","clientSecret":"client-secret"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["accessToken"])')

curl -s -H "Authorization: Bearer $TOKEN" localhost:8083/protected
curl -s -H "Authorization: Bearer $TOKEN" localhost:8083/passport/me
```

OAuth2 token endpoint (form encoded):

```bash
curl -s -X POST localhost:8083/oauth2/token \
  -d 'grant_type=password&username=admin&password=secret&client_id=console&client_secret=client-secret'
```

`AUTH_FORMAT=opaque go run .` switches to the platform's opaque access token
format (`client:username:userType:uuid`), which can be introspected at
`/oauth2/introspect`. The demo source is in-memory; production injects a
UserStore/ClientStore backed by the application database. OAuth2 client
configs (GitHub/Google/WeChat) live in `auth/oauthclient`; a provider callback
only needs to map the external profile to a local user and call
`TokenService`.
