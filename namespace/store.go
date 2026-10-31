// Package namespace adapts gcache to the gns.Store SPI.
//
// gns owns the namespace value, context and store contract. This package is the
// framework-side driver: gcache can be memory-only or Redis-backed, so the
// deployment chooses persistence without gns depending on Redis.
package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maxid/gcache"
	"github.com/maxid/gns"
)

// Store persists namespace definitions through gcache.
type Store struct {
	cache *gcache.Cache
}

var _ gns.Store = (*Store)(nil)

// NewStore wraps a gcache instance.
func NewStore(cache *gcache.Cache) (*Store, error) {
	if cache == nil {
		return nil, errors.New("gable/namespace: nil cache")
	}
	return &Store{cache: cache}, nil
}

// Load implements gns.Store.
func (s *Store) Load(ctx context.Context, name string) (gns.Namespace, error) {
	entry, ok, err := s.cache.Get(ctx, key(name))
	if err != nil {
		return gns.Namespace{}, err
	}
	if !ok {
		return gns.Namespace{}, fmt.Errorf("%w: %s", gns.ErrNotFound, name)
	}
	var ns gns.Namespace
	if err := json.Unmarshal(entry.Value, &ns); err != nil {
		return gns.Namespace{}, fmt.Errorf("gable/namespace: decode %s: %w", name, err)
	}
	if err := ns.Validate(); err != nil {
		return gns.Namespace{}, err
	}
	return ns, nil
}

// Save implements gns.Store.
func (s *Store) Save(ctx context.Context, ns gns.Namespace) error {
	if err := ns.Validate(); err != nil {
		return err
	}
	value, err := json.Marshal(ns)
	if err != nil {
		return fmt.Errorf("gable/namespace: encode %s: %w", ns.Name, err)
	}
	return s.cache.Set(ctx, key(ns.Name), value, 0)
}

// Delete implements gns.Store.
func (s *Store) Delete(ctx context.Context, name string) error {
	return s.cache.Delete(ctx, key(name))
}

func key(name string) string { return "namespace:" + name }
