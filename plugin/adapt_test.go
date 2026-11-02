package plugin_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/maxid/gable/plugin"
)

// An adapted spec must carry identity, version and dependencies; nothing is inferred.
func TestFromRejectsIncompleteSpec(t *testing.T) {
	if _, err := plugin.From(plugin.Spec{Register: func(plugin.Container) error { return nil }}); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("empty id err = %v, want ErrPermanent", err)
	}
	if _, err := plugin.From(plugin.Spec{ID: "cache"}); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("nil register err = %v, want ErrPermanent", err)
	}
}

// A spec without hooks is selectable, reports its manifest entry and registers, but it
// must not look like a capability that has something to start.
func TestFromAdaptsWiringOnlyCapability(t *testing.T) {
	rec := newRecorder()
	log, err := plugin.From(plugin.Spec{ID: "log", Version: "1.2.0", Register: func(c plugin.Container) error {
		rec.mark("register", "log")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := plugin.From(plugin.Spec{ID: "cache", DependsOn: []string{"log"}, Register: func(c plugin.Container) error {
		rec.mark("register", "cache")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, module := range []plugin.Module{log, cache} {
		if _, ok := module.(plugin.Starter); ok {
			t.Fatalf("%s has no hooks but satisfies Starter", module.ID())
		}
		if _, ok := module.(plugin.Stopper); ok {
			t.Fatalf("%s has no hooks but satisfies Stopper", module.ID())
		}
	}

	assembly, err := newSet(t, log, cache).Select("cache")
	if err != nil {
		t.Fatal(err)
	}
	manifest := assembly.Manifest()
	if len(manifest) != 2 || manifest[0].ID != "log" || manifest[0].Version != "1.2.0" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if len(manifest[1].DependsOn) != 1 || manifest[1].DependsOn[0] != "log" {
		t.Fatalf("cache dependencies = %v", manifest[1].DependsOn)
	}

	if err := assembly.Start(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	events := rec.events()
	if len(events) != 2 || events[0] != "register:log" || events[1] != "register:cache" {
		t.Fatalf("events = %v, want only registrations", events)
	}
}

// A spec that declares hooks starts in dependency order and stops in reverse.
func TestFromAdaptsLifecycleCapability(t *testing.T) {
	rec := newRecorder()
	spec := func(id string, deps []string) plugin.Spec {
		return plugin.Spec{
			ID:        id,
			DependsOn: deps,
			Register: func(plugin.Container) error {
				rec.mark("register", id)
				return nil
			},
			Start: func(context.Context) error {
				rec.mark("start", id)
				return nil
			},
			Stop: func(context.Context) error {
				rec.mark("stop", id)
				return nil
			},
		}
	}

	web, err := plugin.From(spec("web", []string{"cache"}))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := plugin.From(spec("cache", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := web.(plugin.Starter); !ok {
		t.Fatal("a spec with a start hook must satisfy Starter")
	}

	assembly, err := newSet(t, web, cache).Select("web")
	if err != nil {
		t.Fatal(err)
	}
	if err := assembly.Start(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := assembly.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"register:cache", "register:web",
		"start:cache", "start:web",
		"stop:web", "stop:cache",
	}
	events := rec.events()
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

// A failing adapted start stops what already started, in reverse, and does not claim to
// have started the capability that failed.
func TestFromStartFailureRollsBack(t *testing.T) {
	rec := newRecorder()
	spec := func(id string, deps []string, startErr error) plugin.Spec {
		return plugin.Spec{
			ID:        id,
			DependsOn: deps,
			Register: func(plugin.Container) error {
				rec.mark("register", id)
				return nil
			},
			Start: func(context.Context) error {
				rec.mark("start", id)
				return startErr
			},
			Stop: func(context.Context) error {
				rec.mark("stop", id)
				return nil
			},
		}
	}

	web, err := plugin.From(spec("web", []string{"cache"}, errors.New("port in use")))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := plugin.From(spec("cache", nil, nil))
	if err != nil {
		t.Fatal(err)
	}

	assembly, err := newSet(t, web, cache).Select("web")
	if err != nil {
		t.Fatal(err)
	}
	err = assembly.Start(context.Background(), rec)
	if err == nil {
		t.Fatal("start must fail")
	}
	if !contains(err.Error(), "start web") {
		t.Fatalf("err = %v, want it to name the capability", err)
	}

	want := []string{
		"register:cache", "register:web",
		"start:cache", "start:web",
		"stop:cache",
	}
	events := rec.events()
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

// Stop may be declared without Start: the capability has cleanup but nothing to start.
func TestFromAdaptsStopOnlyCapability(t *testing.T) {
	rec := newRecorder()
	module, err := plugin.From(plugin.Spec{
		ID:       "cache",
		Register: func(plugin.Container) error { return nil },
		Stop: func(context.Context) error {
			rec.mark("stop", "cache")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := module.(plugin.Starter); !ok {
		t.Fatal("a spec with a stop hook must be started so its stop is reached")
	}

	assembly, err := newSet(t, module).Select("cache")
	if err != nil {
		t.Fatal(err)
	}
	if err := assembly.Start(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := assembly.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := rec.events()
	if len(events) != 1 || events[0] != "stop:cache" {
		t.Fatalf("events = %v", events)
	}
}

// platformContainer mirrors the platform's concrete container: values in, values out, no
// interface. The platform's modules take this type by pointer, which is exactly the
// signature mismatch that gable's Module interface cannot accept.
type platformContainer struct{ values map[string]any }

func (c *platformContainer) Set(key string, value any) { c.values[key] = value }
func (c *platformContainer) Get(key string) (any, bool) {
	value, ok := c.values[key]
	return value, ok
}

// platformModule has the platform's four methods: ID, Version, DependsOn and a Register
// that takes the concrete container.
type platformModule struct {
	id      string
	version string
	deps    []string
	started *bool
}

func (m platformModule) ID() string          { return m.id }
func (m platformModule) Version() string     { return m.version }
func (m platformModule) DependsOn() []string { return m.deps }
func (m platformModule) Register(container *platformContainer) error {
	container.Set(m.id+".registered", true)
	return nil
}

// A platform module joins selection and the manifest without changing its own
// signature: the closure does the narrowing assertion, and the lifecycle is forwarded.
func TestFromAdaptsPlatformShapedModule(t *testing.T) {
	started := false
	mod := platformModule{id: "iam", version: "0.4.0", deps: []string{"log"}}
	adapted, err := plugin.From(plugin.Spec{
		ID:        mod.ID(),
		Version:   mod.Version(),
		DependsOn: mod.DependsOn(),
		Register: func(c plugin.Container) error {
			concrete, ok := c.(*platformContainer)
			if !ok {
				return fmt.Errorf("expected the platform container, got %T", c)
			}
			return mod.Register(concrete)
		},
		Start: func(context.Context) error {
			started = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The platform's modules depend on each other by id, so the dependency has to be in
	// the set as well; it is adapted the same way.
	logModule, err := plugin.From(plugin.Spec{
		ID:       "log",
		Register: func(plugin.Container) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	assembly, err := newSet(t, logModule, adapted).Select("iam")
	if err != nil {
		t.Fatal(err)
	}
	manifest := assembly.Manifest()
	if len(manifest) != 2 || manifest[0].ID != "log" || manifest[1].ID != "iam" || manifest[1].Version != "0.4.0" {
		t.Fatalf("manifest = %+v", manifest)
	}

	container := &platformContainer{values: map[string]any{}}
	if err := assembly.Start(context.Background(), container); err != nil {
		t.Fatal(err)
	}
	if registered, ok := container.Get("iam.registered"); !ok || registered != true {
		t.Fatalf("the module's own Register did not run: %+v", container.values)
	}
	if !started {
		t.Fatal("the forwarded start hook did not run")
	}
}
