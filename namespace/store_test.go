package namespace

import (
	"context"
	"errors"
	"testing"

	"github.com/maxid/gcache"
	"github.com/maxid/gcache/memory"
	"github.com/maxid/gns"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	cache, err := gcache.New(gcache.Config{Namespace: "test"}, gcache.WithRemote(memory.NewStore()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	store, err := NewStore(cache)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	ns, _ := gns.New("app-dev")
	ns.Labels = map[string]string{"env": "dev"}
	if err := store.Save(ctx, ns); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx, "app-dev")
	if err != nil || got.Labels["env"] != "dev" {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	if err := store.Delete(ctx, "app-dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, "app-dev"); !errors.Is(err, gns.ErrNotFound) {
		t.Fatalf("Load after delete = %v", err)
	}
}

func TestStoreSatisfiesGNSSPI(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	ns, _ := gns.New("app-dev")
	if err := store.Save(ctx, ns); err != nil {
		t.Fatal(err)
	}
	manager, err := gns.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := manager.Bind(ctx, "app-dev")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := gns.From(bound); !ok || got.Name != "app-dev" {
		t.Fatalf("bound = %+v, %v", got, ok)
	}
}

func TestStoreRejectsInvalidNamespace(t *testing.T) {
	if err := newStore(t).Save(context.Background(), gns.Namespace{}); !errors.Is(err, gns.ErrInvalidNamespace) {
		t.Fatalf("Save = %v", err)
	}
}
