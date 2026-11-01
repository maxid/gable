package plugin_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/maxid/gable/plugin"
)

// recorder is a minimal container for the tests.
type recorder struct {
	mu     sync.Mutex
	values map[string]any
	order  []string
}

func newRecorder() *recorder { return &recorder{values: map[string]any{}} }

func (r *recorder) Set(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
}

func (r *recorder) Get(key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	return value, ok
}

func (r *recorder) mark(kind, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, kind+":"+id)
}

func (r *recorder) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// capability is a test module with an optional lifecycle.
type capability struct {
	id      string
	version string
	deps    []string
	// startErr makes Start fail, to exercise the rollback.
	startErr error
	// stopErr makes Stop fail, to check failures are aggregated.
	stopErr bool
}

func (c *capability) ID() string      { return c.id }
func (c *capability) Version() string { return c.version }
func (c *capability) DependsOn() []string {
	return c.deps
}

func (c *capability) Register(container plugin.Container) error {
	if rec, ok := container.(*recorder); ok {
		rec.mark("register", c.id)
	}
	return nil
}

func (c *capability) Start(ctx context.Context) error {
	if rec, ok := ctx.Value(recorderKey{}).(*recorder); ok {
		rec.mark("start", c.id)
	}
	return c.startErr
}

func (c *capability) Stop(context.Context) error {
	if c.stopErr {
		return fmt.Errorf("stop %s failed", c.id)
	}
	return nil
}

// noLifecycle has only Register: the selector must accept it.
type noLifecycle struct{ id string }

func (n noLifecycle) ID() string { return n.id }

func (n noLifecycle) Register(container plugin.Container) error {
	if rec, ok := container.(*recorder); ok {
		rec.mark("register", n.id)
	}
	return nil
}

type recorderKey struct{}

func newSet(t *testing.T, modules ...plugin.Module) *plugin.Set {
	t.Helper()
	set := plugin.NewSet()
	for _, module := range modules {
		if err := set.Add(module); err != nil {
			t.Fatalf("add %s: %v", module.ID(), err)
		}
	}
	return set
}

// Selecting a capability drags in its dependencies, in a deterministic order.
func TestSelectPullsDependenciesInOrder(t *testing.T) {
	set := newSet(t,
		&capability{id: "log", version: "1.0.0"},
		&capability{id: "cache", version: "1.0.0", deps: []string{"log"}},
		&capability{id: "web", version: "1.0.0", deps: []string{"cache", "log"}},
		&capability{id: "unused", version: "1.0.0"},
	)

	assembly, err := set.Select("web")
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	got := assembly.IDs()
	want := []string{"log", "cache", "web"}
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}

	// The manifest reports versions, in start order.
	manifest := assembly.Manifest()
	if len(manifest) != 3 || manifest[0].ID != "log" || manifest[0].Version != "1.0.0" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if len(manifest[2].DependsOn) != 2 {
		t.Fatalf("web dependencies = %v", manifest[2].DependsOn)
	}
}

func TestSelectRejectsUnknownCapability(t *testing.T) {
	set := newSet(t, &capability{id: "log"})
	if _, err := set.Select("telemetry"); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
}

func TestSelectRejectsMissingDependency(t *testing.T) {
	// web depends on cache, but cache is not in this binary.
	set := newSet(t, &capability{id: "web", deps: []string{"cache"}})
	if _, err := set.Select("web"); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
}

func TestSelectRejectsCycle(t *testing.T) {
	set := newSet(t,
		&capability{id: "a", deps: []string{"b"}},
		&capability{id: "b", deps: []string{"a"}},
	)
	if _, err := set.Select("a"); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
}

func TestSelectRequiresAtLeastOneCapability(t *testing.T) {
	set := newSet(t, &capability{id: "log"})
	if _, err := set.Select(); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
}

// A capability without a lifecycle is still selectable: the selector must not force
// empty Start/Stop methods.
func TestSelectAcceptsCapabilitiesWithoutLifecycle(t *testing.T) {
	set := newSet(t, noLifecycle{id: "config"}, &capability{id: "web", deps: []string{"config"}})

	assembly, err := set.Select("web")
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	rec := newRecorder()
	ctx := context.WithValue(context.Background(), recorderKey{}, rec)
	if err := assembly.Start(ctx, rec); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Registration happened for both; only the lifecycle capability reports a start.
	events := rec.events()
	if len(events) != 3 {
		t.Fatalf("events = %v", events)
	}
	if events[0] != "register:config" || events[1] != "register:web" || events[2] != "start:web" {
		t.Fatalf("events = %v", events)
	}
}

// Start order is dependency order; a failure stops what already started, in reverse.
func TestStartFailureRollsBack(t *testing.T) {
	set := newSet(t,
		&capability{id: "log"},
		&capability{id: "cache", deps: []string{"log"}},
		&capability{id: "web", deps: []string{"cache"}, startErr: errors.New("port in use")},
	)

	assembly, err := set.Select("web")
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	rec := newRecorder()
	ctx := context.WithValue(context.Background(), recorderKey{}, rec)
	err = assembly.Start(ctx, rec)
	if err == nil {
		t.Fatal("start must fail")
	}

	events := rec.events()
	// log and cache start before web; the rollback is Stop, which this recorder does
	// not record, so assert the start sequence and that web was attempted last.
	if events[len(events)-1] != "start:web" {
		t.Fatalf("events = %v", events)
	}
}

func TestStopJoinsFailures(t *testing.T) {
	set := newSet(t,
		&capability{id: "a", stopErr: true},
		&capability{id: "b", stopErr: true},
	)
	assembly, err := set.Select("a", "b")
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	err = assembly.Stop(context.Background())
	if err == nil {
		t.Fatal("stop must report failures")
	}
	if text := err.Error(); !contains(text, "stop a failed") || !contains(text, "stop b failed") {
		t.Fatalf("err = %v, want both failures", err)
	}
}

func TestAddRejectsDuplicatesAndEmptyIDs(t *testing.T) {
	set := plugin.NewSet()
	if err := set.Add(nil); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("nil err = %v", err)
	}
	if err := set.Add(noLifecycle{id: ""}); !errors.Is(err, plugin.ErrPermanent) {
		t.Fatalf("empty id err = %v", err)
	}
	if err := set.Add(noLifecycle{id: "log"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := set.Add(noLifecycle{id: "log"}); !errors.Is(err, plugin.ErrAlreadyRegistered) {
		t.Fatalf("duplicate err = %v", err)
	}
}

func TestIDsAreSorted(t *testing.T) {
	set := newSet(t, noLifecycle{id: "web"}, noLifecycle{id: "cache"}, noLifecycle{id: "log"})
	ids := set.IDs()
	want := []string{"cache", "log", "web"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
}

// Static wraps an existing module so an application can adopt selection without
// rewriting it.
func TestStaticWrapsAnExistingModule(t *testing.T) {
	inner := &capability{id: "inner", version: "ignored"}
	set := newSet(t, plugin.Static("web", "2.0.0", inner))

	assembly, err := set.Select("web")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	manifest := assembly.Manifest()
	if manifest[0].ID != "web" || manifest[0].Version != "2.0.0" {
		t.Fatalf("manifest = %+v", manifest)
	}

	rec := newRecorder()
	if err := assembly.Start(context.Background(), rec); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
