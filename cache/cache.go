// Package cache builds a gcache instance with the application namespace.
package cache

import (
	"strings"

	"github.com/maxid/gcache"

	"github.com/maxid/gns"
)

// New creates a cache. An empty config namespace inherits set.CacheName().
func New(set gns.Set, cfg gcache.Config, opts ...gcache.Option) (*gcache.Cache, error) {
	if strings.TrimSpace(cfg.Namespace) == "" {
		cfg.Namespace = set.CacheName()
	}
	return gcache.New(cfg, opts...)
}
