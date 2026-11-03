package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/maxid/gplugin"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// The modules are written against this pilot's contract, not gable's: if one of them ever
// needed a gable import or a signature change, these assertions stop compiling.
var (
	_ Module = (*usersModule)(nil)
	_ Module = (*rolesModule)(nil)
	_ Module = (*adminModule)(nil)
)

// recorder is a platform-shaped module that records the lifecycle it sees.
type recorder struct {
	id      string
	deps    []string
	events  *[]string
	mu      *sync.Mutex
	started *int
	failAt  string // "register" or "start"
}

func (m *recorder) ID() string          { return m.id }
func (m *recorder) Version() string     { return "9.9.9" }
func (m *recorder) DependsOn() []string { return m.deps }

func (m *recorder) record(event string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.events = append(*m.events, event)
	if event == "start:"+m.id {
		*m.started++
	}
}

func (m *recorder) Register(c *Container) error {
	if m.failAt == "register" {
		return errors.New("cannot wire " + m.id)
	}
	m.record("register:" + m.id)
	return nil
}

func (m *recorder) Start(context.Context) error {
	if m.failAt == "start" {
		return errors.New("cannot start " + m.id)
	}
	m.record("start:" + m.id)
	return nil
}

func (m *recorder) Stop(context.Context) error {
	m.record("stop:" + m.id)
	*m.started--
	return nil
}

// pilot builds the same assembly the example runs, without the listener.
func pilot(t *testing.T, extra ...Module) (*gable.App, *web.Server, *[]string, *sync.Mutex) {
	t.Helper()
	cfg := config.Default()
	cfg.App.Name = "pilot-test"

	app := gable.New(gable.WithConfig(cfg))
	server := web.New(web.WithTitle("pilot"), web.WithHealth(app.Health()), web.WithMetrics(app.Metrics()))
	server.UseTenant(tenant.NewSingleResolver(cfg.Tenant))

	modules := append([]Module{NewUsersModule(), NewRolesModule(), NewAdminModule(server)}, extra...)
	var events []string
	var mu sync.Mutex
	for _, module := range modules {
		if err := app.Register(adapt(module)); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.Register(web.NewModule(server, ":0")); err != nil {
		t.Fatal(err)
	}
	return app, server, &events, &mu
}

// Criterion 1: the container is not duplicated. A platform module writes through its own
// type, a later module reads it back, and both see the values gable put there.
func TestPilotContainerIsOneStore(t *testing.T) {
	app := gable.New(gable.WithConfig(config.Default()))
	view := newContainer(app.Container())

	app.Container().Set("gable.framework", "value")
	if value, ok := view.Get("gable.framework"); !ok || value != "value" {
		t.Fatalf("the view does not see the application's values: %v, %v", value, ok)
	}

	service := &UserService{users: map[int64]*User{}}
	if err := NewUsersModule().Register(view); err != nil {
		t.Fatal(err)
	}
	readBack, err := Require[*UserService](view, "admin.users")
	if err != nil {
		t.Fatal(err)
	}
	if readBack != service && readBack.users == nil {
		t.Fatal("the module's service did not reach the shared container")
	}
	// A missing dependency is a wiring error, not a nil pointer later.
	if _, err := Require[*RoleService](view, "admin.roles"); err == nil ||
		!strings.Contains(err.Error(), "not in the container") {
		t.Fatalf("missing dependency = %v", err)
	}
}

// Criterion 2: the manifest is the product manifest. Ids, versions and dependencies come out
// in dependency order, whatever order they were registered in.
func TestPilotManifestMatchesTheDeclaredSet(t *testing.T) {
	app, _, _, _ := pilot(t)
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })

	manifest := app.Manifest()
	got := make([]string, 0, len(manifest))
	byID := map[string]gplugin.Entry{}
	for _, entry := range manifest {
		got = append(got, entry.ID)
		byID[entry.ID] = entry
	}
	// roles and users are tied, so they follow the deterministic tie-break; admin depends on
	// both, and web is a native gable module in the same assembly.
	if strings.Join(got, ",") != "roles,users,admin,web" {
		t.Fatalf("manifest order = %v", got)
	}
	if byID["users"].Version != "1.4.0" || byID["admin"].Version != "0.9.0" {
		t.Fatalf("versions = %+v", byID)
	}
	if strings.Join(byID["admin"].DependsOn, ",") != "roles,users" {
		t.Fatalf("dependencies = %v", byID["admin"].DependsOn)
	}
}

// Criterion 3: a failing module behaves as it does in the platform — a failed Start stops
// what already started, in reverse order, and a failed Register starts nothing at all.
func TestPilotFailureSemantics(t *testing.T) {
	var events []string
	var mu sync.Mutex
	started := 0
	alpha := &recorder{id: "alpha", events: &events, mu: &mu, started: &started}
	boom := &recorder{id: "boom", deps: []string{"alpha"}, events: &events, mu: &mu, started: &started, failAt: "start"}

	app, _, _, _ := pilot(t, alpha, boom)
	err := app.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot start boom") {
		t.Fatalf("start error = %v", err)
	}
	joined := strings.Join(events, ",")
	// alpha starts, boom fails before it can record its own start, and alpha is rolled back.
	want := "register:alpha,register:boom,start:alpha,stop:alpha"
	if joined != want {
		t.Fatalf("events = %q, want %q", joined, want)
	}
	// The module that failed to start is not stopped: only what actually started is.
	if strings.Contains(joined, "stop:boom") {
		t.Fatalf("a module whose Start failed must not be stopped: %q", joined)
	}

	// A failed Register never starts anything.
	events = nil
	bad := &recorder{id: "bad", events: &events, mu: &mu, started: &started, failAt: "register"}
	app, _, _, _ = pilot(t, bad)
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("a failed register must fail the start")
	}
	for _, event := range events {
		if strings.HasPrefix(event, "start:") {
			t.Fatalf("something started after a failed register: %v", events)
		}
	}
}

// Criterion 4: the wiring is the only place the two worlds meet. This test fails to compile
// if a module grows a gable dependency, because adapt takes the platform contract and
// nothing else.
func TestPilotModulesNeedNoGableTypes(t *testing.T) {
	modules := []Module{NewUsersModule(), NewRolesModule(), NewAdminModule(web.New(web.WithTitle("pilot")))}
	for _, module := range modules {
		adapted := adapt(module)
		if adapted.ID() != module.ID() {
			t.Fatalf("adapter lost the id: %q != %q", adapted.ID(), module.ID())
		}
		if versioned, ok := adapted.(gplugin.Versioned); !ok || versioned.Version() != module.Version() {
			t.Fatalf("adapter lost the version of %s", module.ID())
		}
		if dependent, ok := adapted.(gplugin.Dependent); !ok ||
			strings.Join(dependent.DependsOn(), ",") != strings.Join(module.DependsOn(), ",") {
			t.Fatalf("adapter lost the dependencies of %s", module.ID())
		}
	}
}
