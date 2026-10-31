English | [简体中文](README.zh-CN.md)

# gable

A code-first application framework for Go services: an explicit dependency
container, module lifecycle, typed configuration, `glogs` logging and stable
error codes. It composes the g\* library family instead of hiding it.

> Status: **experimental scaffold** (v0.0.0-dev). The API is not stable yet.

## Why another framework

- **No package-level registry.** Dependencies live in an `App` container and are
  wired by modules, so tests can build an app without global state.
- **Lifecycle is explicit.** Modules register, start in dependency order and stop
  in reverse order; a failed start rolls back what already started.
- **Configuration is typed.** YAML with `${ENV}` expansion and `GABLE_*`
  overrides, validated before anything starts.
- **Errors carry a stable code.** Transport layers can map a code to an HTTP
  status without importing business packages.
- **Web and docs are included.** Gin-backed routing, a six-field response
  envelope, binding/validation, request ids, panic recovery and access logs;
  springdoc-shaped OpenAPI 3.0.1 at `/v3/api-docs` with grouped documents and
  a Swagger UI.

## Quick start

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

func (hello) ID() string                      { return "hello" }
func (hello) Register(c *gable.Container) error { c.Set("hello.greeting", "world"); return nil }
func (hello) Start(context.Context) error      { return nil }
func (hello) Stop(context.Context) error       { return nil }

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

## Packages

| Package | Purpose |
|---|---|
| `gable` | `App`, `Container`, module lifecycle |
| `config` | typed YAML + environment configuration |
| `log` | `glogs` logger construction |
| `errors` | stable error codes and HTTP status |
| `web` | Gin-backed MVC, envelope, binding/validation, OpenAPI + Swagger UI |

## HTTP in five lines

```go
server := web.New(web.WithTitle("demo"))
server.Register(myController{}) // Routes(r *web.Router)
app.Register(web.NewModule(server, "")) // listens on config.http.addr
```

`GET /v3/api-docs` serves the OpenAPI document, `GET /v3/api-docs/{group}` a
group, and `GET /swagger-ui/index.html` the UI.

## Planned

- `orm`: dialect-aware models, query builder and migrations (MySQL/PostgreSQL first).
- `dsl`: runtime model/page resources with file or database storage.
- `ai` / `mcp`: adapters over the `gai` and `gmcp` libraries.
- `lowcode`: page/table/form resources compiled to the LCDP IR.
- `cli`: `gable gen`, `gable migrate` (in addition to `new`/`dev`).

## Install

```bash
go get github.com/maxid/gable
```

## License

MIT. See [LICENSE](LICENSE).
