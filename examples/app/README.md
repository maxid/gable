# Admin pilot: platform modules in a gable application

This example answers one question: **what does it cost to run a module that was written for
the platform's `internal/plugin` inside a gable application?**

Nothing in the modules. `users`, `roles` and `admin` are written against the pilot's own
contract — `Register(*Container)` on a concrete container type, plus `ID`, `Version`,
`DependsOn`, `Start` and `Stop`. They do not import gable. The whole adaptation is fifteen
lines in `main.go`:

```go
type adapted struct{ module Module }

func (a adapted) Register(c *gable.Container) error { return a.module.Register(newContainer(c)) }
// ID, Version, DependsOn, Start and Stop forward verbatim
```

Run it:

```bash
go run ./examples/app
curl localhost:8085/status        # assembly manifest + readiness
curl localhost:8085/admin/users
curl localhost:8085/admin/roles
curl localhost:8085/metrics
```

## The four criteria the pilot measures

`pilot_test.go` asserts each one, so a regression breaks the build rather than the claim:

| Criterion | What the test proves |
|---|---|
| The container is substitutable | A platform module writes through its own container type, a later module reads it back, and both see what gable put there — one store, no second container |
| The manifest is equivalent | `App.Manifest()` reports the declared ids, versions and dependencies in dependency order, with the native `web` module in the same assembly |
| Failure semantics are equivalent | A module that fails to `Start` takes the already-started ones down in reverse; a module that fails to `Register` starts nothing; the module that failed is not "stopped" |
| No business code changes | The modules satisfy the pilot's `Module` interface, not gable's; only the wiring knows both |

## What this does **not** prove

- **Not a main-repo migration.** The pilot lives in gable and touches no platform code, which
  is why it is allowed during Gate 0. Moving `products/*.yaml` generation onto `plugin.Set`
  is the next step, and it is a Gate 0 decision, not a code decision.
- **No real persistence.** The services are in memory so the example runs with no database;
  a real pilot swaps them for `orm.Repo` without changing the module shape.
- **No per-module config.** A module reads `gable.config` through the container, but the
  configuration is still one file for the process.
