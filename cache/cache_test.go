package cache

import (
	"testing"

	"github.com/maxid/gcache"

	"github.com/maxid/gable/config"
)

func TestNewUsesNamespace(t *testing.T) {
	ns := config.Namespace{Root: "app"}.Normalize()
	cache, err := New(ns, gcache.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cache.Namespace() != "app" {
		t.Fatalf("namespace = %q", cache.Namespace())
	}
}
