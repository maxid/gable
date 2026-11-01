// Package plugin assembles an application's capabilities from a declared set.
//
// It is the gable answer to "manage capabilities as plugins": the application names
// what it wants (`log`, `cache`, `web`, …), the set resolves the dependency closure and
// produces a deterministic order — the same order every process start, so a rollout
// cannot change it.
//
// Deliberately different from the platform's `internal/plugin`: no package-level
// registry and no panic on duplicates. An application's wiring is explicit and must be
// testable in isolation; a duplicate id is an error the caller sees, not a crash during
// init.
//
// Not supported, by decision: dynamic plugins (no .so, no download), and a plugin that
// bypasses wiring through the container.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrPermanent marks wiring mistakes: retrying cannot help.
var ErrPermanent = errors.New("gable/plugin: permanent error")

// ErrAlreadyRegistered means an id was registered twice.
var ErrAlreadyRegistered = errors.New("gable/plugin: already registered")

// Container carries values between capabilities during Register. It is the same
// untyped store the application already exposes; keeping one type avoids two
// containers holding half the wiring each.
type Container interface {
	Set(key string, value any)
	Get(key string) (any, bool)
}

// Module is a capability: it registers its values, and may have a lifecycle.
type Module interface {
	ID() string
	Register(Container) error
}

// Versioned reports the capability version, shown in the manifest.
//
// It is optional so an existing module keeps working: a module without it simply
// reports an empty version, which is honest — the assembly never invents one.
type Versioned interface {
	Version() string
}

// Dependent declares the capabilities that must start first.
type Dependent interface {
	DependsOn() []string
}

// Starter and Stopper are optional lifecycle hooks.
type Starter interface {
	Start(context.Context) error
}

// Stopper is the reverse-order shutdown hook.
type Stopper interface {
	Stop(context.Context) error
}

// Entry is one capability in the manifest.
type Entry struct {
	ID        string
	Version   string
	DependsOn []string
}

// Set is the registry of capabilities available to this binary.
type Set struct {
	mu      sync.RWMutex
	modules map[string]Module
}

// NewSet returns an empty set.
func NewSet() *Set { return &Set{modules: map[string]Module{}} }

// Add registers a capability. A duplicate or empty id is a wiring error: silently
// replacing a capability would make the running binary differ from its manifest.
func (s *Set) Add(module Module) error {
	if module == nil {
		return fmt.Errorf("%w: nil module", ErrPermanent)
	}
	id := module.ID()
	if id == "" {
		return fmt.Errorf("%w: module id must not be empty", ErrPermanent)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modules == nil {
		s.modules = map[string]Module{}
	}
	if _, exists := s.modules[id]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, id)
	}
	s.modules[id] = module
	return nil
}

// IDs lists the available capability ids, sorted.
func (s *Set) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.modules))
	for id := range s.modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Static wraps an already-built capability under an explicit id and version.
//
// It exists so an application can adopt selection and manifests without rewriting its
// modules: the wrapped module keeps its own Register and lifecycle.
func Static(id, version string, module Module) Module {
	if module == nil {
		return staticModule{id: id, version: version}
	}
	return staticModule{id: id, version: version, inner: module}
}

type staticModule struct {
	id      string
	version string
	inner   Module
}

func (m staticModule) ID() string      { return m.id }
func (m staticModule) Version() string { return m.version }

func (m staticModule) Register(c Container) error {
	if m.inner == nil {
		return nil
	}
	return m.inner.Register(c)
}

// Start forwards to the wrapped module when it has a lifecycle.
func (m staticModule) Start(ctx context.Context) error {
	if starter, ok := m.inner.(Starter); ok {
		return starter.Start(ctx)
	}
	return nil
}

// Stop forwards to the wrapped module when it has a lifecycle.
func (m staticModule) Stop(ctx context.Context) error {
	if stopper, ok := m.inner.(Stopper); ok {
		return stopper.Stop(ctx)
	}
	return nil
}

// DependsOn forwards the wrapped module's dependencies.
func (m staticModule) DependsOn() []string {
	if dependent, ok := m.inner.(Dependent); ok {
		return dependent.DependsOn()
	}
	return nil
}
