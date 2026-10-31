package cache

import (
	"testing"

	"github.com/maxid/gcache"

	"github.com/maxid/gns"
)

func TestNewUsesNamespace(t *testing.T) {
	set, err := gns.New(gns.Config{Root: "app"})
	if err != nil {
		t.Fatal(err)
	}
	cache, err := New(set, gcache.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cache.Namespace() != "app" {
		t.Fatalf("namespace = %q", cache.Namespace())
	}
}
