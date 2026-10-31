package gable

import (
	"fmt"
	"sort"
	"sync"
)

// Container is an explicit dependency container.
//
// Dependencies are registered by modules during Register and resolved by
// consumers. Keys are namespaced by convention (for example "service.user")
// so that two modules cannot silently overwrite each other's values.
type Container struct {
	mu     sync.RWMutex
	values map[string]any
}

// NewContainer returns an empty container.
func NewContainer() *Container {
	return &Container{values: make(map[string]any)}
}

// Set stores value under key. It panics on an empty key because an unnamed
// dependency cannot be resolved or diagnosed.
func (c *Container) Set(key string, value any) {
	if key == "" {
		panic("gable: container key must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values == nil {
		c.values = make(map[string]any)
	}
	c.values[key] = value
}

// Get returns the value stored under key.
func (c *Container) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.values[key]
	return v, ok
}

// Delete removes key from the container.
func (c *Container) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
}

// Keys returns the registered keys in sorted order.
func (c *Container) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := make([]string, 0, len(c.values))
	for key := range c.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Resolve returns the value under key asserted to T.
//
// The second result is false when the key is missing or holds another type.
func Resolve[T any](c *Container, key string) (T, bool) {
	var zero T
	v, ok := c.Get(key)
	if !ok {
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}

// MustResolve returns the value under key asserted to T and panics otherwise.
//
// It is meant for wiring code that runs during Register, where a missing
// dependency is a programming error rather than a runtime condition.
func MustResolve[T any](c *Container, key string) T {
	value, ok := Resolve[T](c, key)
	if !ok {
		var zero T
		panic(fmt.Sprintf("gable: cannot resolve %q as %T", key, zero))
	}
	return value
}
