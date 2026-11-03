// Package main is the framework pilot: a small admin domain wired with gable while its
// modules keep the platform's own shape.
//
// The point is not the CRUD. It is proving that a module written for `internal/plugin` —
// `Register(*Container)` on a concrete container type, plus its own Start/Stop — joins a
// gable application through an adapter in the wiring only. Nothing in the modules knows that
// gable exists.
package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/maxid/gable"
)

// Container is this pilot's concrete container, deliberately shaped like the platform's:
// Set/Get, no interface, passed by pointer.
//
// It is a *view* over gable's container rather than a second store, so a value a platform
// module writes is visible to every later module and to the application itself. Two
// containers holding half the wiring each is the failure mode this avoids.
type Container struct {
	app *gable.Container
}

func newContainer(app *gable.Container) *Container { return &Container{app: app} }

// Set stores a value.
func (c *Container) Set(key string, value any) { c.app.Set(key, value) }

// Get reads a value.
func (c *Container) Get(key string) (any, bool) { return c.app.Get(key) }

// Require reads a value and fails loudly when it is missing: a module that silently runs
// without its dependency is how a nil pointer becomes a production incident.
func Require[T any](c *Container, key string) (T, error) {
	var zero T
	value, ok := c.Get(key)
	if !ok {
		return zero, fmt.Errorf("pilot: %q is not in the container", key)
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("pilot: %q holds %T, not %T", key, value, zero)
	}
	return typed, nil
}

// Module is the platform's module contract: a concrete container, its own lifecycle.
type Module interface {
	ID() string
	Version() string
	DependsOn() []string
	Register(*Container) error
	Start(context.Context) error
	Stop(context.Context) error
}

// User is the pilot's record.
type User struct {
	ID       int64
	Username string
	Roles    []string
}

// UserService is what the users module publishes.
type UserService struct {
	mu    sync.RWMutex
	users map[int64]*User
}

// Create stores a user.
func (s *UserService) Create(user User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := user
	s.users[user.ID] = &copied
}

// Get reads one user.
func (s *UserService) Get(id int64) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	return user, ok
}

// List returns the users ordered by id.
func (s *UserService) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]int64, 0, len(s.users))
	for id := range s.users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]User, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.users[id])
	}
	return out
}

// RoleService is what the roles module publishes.
type RoleService struct {
	mu    sync.RWMutex
	roles map[string]string // code -> display name
}

// Add registers a role.
func (s *RoleService) Add(code, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roles[code] = name
}

// All returns the roles in code order.
func (s *RoleService) All() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.roles))
	for code, name := range s.roles {
		out[code] = name
	}
	return out
}
