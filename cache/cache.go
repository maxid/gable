// Package cache builds a gcache instance with the application namespace.
package cache

import (
	"strings"

	"github.com/maxid/gcache"

	"github.com/maxid/gable/config"
)

// New creates a cache. An empty config namespace inherits the gable cache
// prefix (namespace.cache).
func New(ns config.Namespace, cfg gcache.Config, opts ...gcache.Option) (*gcache.Cache, error) {
	if strings.TrimSpace(cfg.Namespace) == "" {
		cfg.Namespace = ns.CacheName()
	}
	return gcache.New(cfg, opts...)
}
