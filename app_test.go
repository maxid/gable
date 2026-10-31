package gable

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maxid/gns"

	"github.com/maxid/gable/config"
)

type testModule struct {
	id      string
	deps    []string
	events  *[]string
	fail    bool
	failReg bool
}

func (m *testModule) ID() string { return m.id }

func (m *testModule) DependsOn() []string { return m.deps }

func (m *testModule) Register(c *Container) error {
	if m.failReg {
		return errors.New("register failed")
	}
	c.Set("module:"+m.id, true)
	return nil
}

func (m *testModule) Start(context.Context) error {
	*m.events = append(*m.events, "start:"+m.id)
	if m.fail {
		return errors.New("start failed")
	}
	return nil
}

func (m *testModule) Stop(context.Context) error {
	*m.events = append(*m.events, "stop:"+m.id)
	return nil
}

func testApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Default()
	cfg.App.Name = "test"
	cfg.Log.Dir = t.TempDir()
	return New(WithConfig(cfg))
}

func TestAppStartStopOrder(t *testing.T) {
	var events []string
	app := testApp(t)
	if err := app.Register(&testModule{id: "b", deps: []string{"a"}, events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := app.Register(&testModule{id: "a", events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := Resolve[bool](app.Container(), "module:a"); !ok {
		t.Fatal("module a did not register into the container")
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{"start:a", "start:b", "stop:b", "stop:a"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAppStartFailureStopsStartedModules(t *testing.T) {
	var events []string
	app := testApp(t)
	_ = app.Register(&testModule{id: "a", events: &events})
	_ = app.Register(&testModule{id: "b", deps: []string{"a"}, events: &events, fail: true})
	_ = app.Register(&testModule{id: "c", deps: []string{"b"}, events: &events})

	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected start error")
	}
	want := []string{"start:a", "start:b", "stop:a"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAppRejectsUnknownDependency(t *testing.T) {
	var events []string
	app := testApp(t)
	_ = app.Register(&testModule{id: "a", deps: []string{"missing"}, events: &events})
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected unknown dependency error")
	}
}

func TestAppRejectsCycle(t *testing.T) {
	var events []string
	app := testApp(t)
	_ = app.Register(&testModule{id: "a", deps: []string{"b"}, events: &events})
	_ = app.Register(&testModule{id: "b", deps: []string{"a"}, events: &events})
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestAppRejectsDuplicateModuleAndDoubleStart(t *testing.T) {
	var events []string
	app := testApp(t)
	if err := app.Register(&testModule{id: "a", events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := app.Register(&testModule{id: "a", events: &events}); err == nil {
		t.Fatal("expected duplicate module error")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err == nil {
		t.Fatal("expected already started error")
	}
	_ = app.Stop(context.Background())
}

func TestAppNamespace(t *testing.T) {
	var events []string
	app := testApp(t)
	if err := app.Register(&testModule{id: "a", events: &events}); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Stop(context.Background())
	if app.Namespace().Root != "gable" || app.Namespace().Cache != "gable" {
		t.Fatalf("namespace = %+v", app.Namespace())
	}
	if _, ok := Resolve[gns.Set](app.Container(), "gable.namespace"); !ok {
		t.Fatal("namespace not in container")
	}
}

func TestContainerResolveRejectsWrongType(t *testing.T) {
	c := NewContainer()
	c.Set("n", 1)
	if _, ok := Resolve[string](c, "n"); ok {
		t.Fatal("expected wrong type to fail")
	}
	if n, ok := Resolve[int](c, "n"); !ok || n != 1 {
		t.Fatalf("Resolve = %v, %v", n, ok)
	}
}
