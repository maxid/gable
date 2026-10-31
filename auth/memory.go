package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MemoryUserStore is an in-memory UserStore.
type MemoryUserStore struct {
	mu    sync.RWMutex
	users map[string]*User
}

// NewMemoryUserStore returns an empty store.
func NewMemoryUserStore(users ...User) *MemoryUserStore {
	store := &MemoryUserStore{users: map[string]*User{}}
	for i := range users {
		_ = store.Add(users[i])
	}
	return store
}

// Add inserts a user keyed by tenant and username.
func (s *MemoryUserStore) Add(user User) error {
	key := userKey(user.TenantID, user.Username)
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := user
	s.users[key] = &copy
	return nil
}

// FindByUsername implements UserStore.
func (s *MemoryUserStore) FindByUsername(_ context.Context, tenantID int64, username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[userKey(tenantID, username)]
	if !ok {
		return nil, ErrInvalidCredentials
	}
	copy := *user
	return &copy, nil
}

// MemoryClientStore is an in-memory ClientStore.
type MemoryClientStore struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// NewMemoryClientStore returns an empty store.
func NewMemoryClientStore(clients ...Client) *MemoryClientStore {
	store := &MemoryClientStore{clients: map[string]*Client{}}
	for i := range clients {
		_ = store.Add(clients[i])
	}
	return store
}

// Add inserts a client.
func (s *MemoryClientStore) Add(client Client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := client
	s.clients[client.ID] = &copy
	return nil
}

// FindClient implements ClientStore.
func (s *MemoryClientStore) FindClient(_ context.Context, clientID string) (*Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	client, ok := s.clients[clientID]
	if !ok {
		return nil, ErrInvalidClient
	}
	copy := *client
	return &copy, nil
}

func userKey(tenantID int64, username string) string {
	return fmt.Sprintf("%d:%s", tenantID, strings.ToLower(username))
}
