package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// KV is the slice of a shared cache these stores need.
//
// It matches gcache's Get/Set/Delete/SetPersistent so an application passes the cache it
// already has; keeping it an interface is what lets the stores be tested without Redis and
// keeps `auth` free of a cache dependency.
type KV interface {
	Get(ctx context.Context, key string) (Entry, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	SetPersistent(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// Entry is one cached value: a byte slice with an expiry.
type Entry struct {
	Value []byte
}

// CacheMFAStore keeps MFA state in a shared cache.
//
// The memory store in this package is fine for one process; a second replica would simply not
// see an enrollment, so a user who enrolled on one pod could log in on another without a second
// factor. This store is the one that makes MFA a property of the account rather than of the pod.
type CacheMFAStore struct {
	kv    KV
	keyFn func(tenantID int64, username string) string
}

// NewCacheMFAStore returns a store over kv. A nil key function uses "mfa:<tenant>:<user>".
func NewCacheMFAStore(kv KV) *CacheMFAStore {
	return &CacheMFAStore{kv: kv, keyFn: func(tenantID int64, username string) string {
		return fmt.Sprintf("mfa:%d:%s", tenantID, normalizeUser(username))
	}}
}

// WithKeyPrefix puts the keys under a namespace, which is how one shared cache can host several
// environments without collisions.
func (s *CacheMFAStore) WithKeyPrefix(prefix string) *CacheMFAStore {
	if prefix == "" {
		return s
	}
	s.keyFn = func(tenantID int64, username string) string {
		return fmt.Sprintf("%s:mfa:%d:%s", prefix, tenantID, normalizeUser(username))
	}
	return s
}

// GetMFA implements MFAStore.
func (s *CacheMFAStore) GetMFA(ctx context.Context, tenantID int64, username string) (*MFAState, error) {
	entry, ok, err := s.kv.Get(ctx, s.keyFn(tenantID, username))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var state MFAState
	if err := json.Unmarshal(entry.Value, &state); err != nil {
		return nil, fmt.Errorf("auth: decode mfa state: %w", err)
	}
	return &state, nil
}

// PutMFA implements MFAStore.
//
// The state is stored without a TTL: an enrollment that expires is an account silently losing
// its second factor.
func (s *CacheMFAStore) PutMFA(ctx context.Context, state *MFAState) error {
	if state == nil {
		return fmt.Errorf("auth: nil mfa state")
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("auth: encode mfa state: %w", err)
	}
	return s.kv.SetPersistent(ctx, s.keyFn(state.TenantID, state.Username), encoded)
}

// DeleteMFA removes an account's second factor entirely, for an account that is being deleted.
func (s *CacheMFAStore) DeleteMFA(ctx context.Context, tenantID int64, username string) error {
	return s.kv.Delete(ctx, s.keyFn(tenantID, username))
}

// CacheChallengeStore keeps pending login tickets in a shared cache.
//
// A challenge lives for minutes, and a load balancer will happily send the second request to
// another replica; with the memory store that request finds nothing and the login fails in a way
// that looks like a wrong code.
type CacheChallengeStore struct {
	kv    KV
	ttl   time.Duration
	keyFn func(ticket string) string
}

// NewCacheChallengeStore returns a store over kv with the given TTL.
func NewCacheChallengeStore(kv KV, ttl time.Duration) *CacheChallengeStore {
	if ttl <= 0 {
		ttl = challengeTTL
	}
	return &CacheChallengeStore{
		kv:    kv,
		ttl:   ttl,
		keyFn: func(ticket string) string { return "mfa-ticket:" + ticket },
	}
}

// WithKeyPrefix puts the keys under a namespace.
func (s *CacheChallengeStore) WithKeyPrefix(prefix string) *CacheChallengeStore {
	if prefix == "" {
		return s
	}
	s.keyFn = func(ticket string) string { return prefix + ":mfa-ticket:" + ticket }
	return s
}

// PutChallenge implements ChallengeStore.
func (s *CacheChallengeStore) PutChallenge(ctx context.Context, ticket string, challenge MFAChallenge) error {
	encoded, err := json.Marshal(challenge)
	if err != nil {
		return fmt.Errorf("auth: encode mfa challenge: %w", err)
	}
	return s.kv.Set(ctx, s.keyFn(ticket), encoded, s.ttl)
}

// TakeChallenge implements ChallengeStore.
//
// Taking is delete-then-read: the entry is removed first, so two requests racing on one ticket
// cannot both find it. A cache without atomic delete-and-return still gives one winner, because
// only the request whose Delete reported success continues.
func (s *CacheChallengeStore) TakeChallenge(ctx context.Context, ticket string) (MFAChallenge, bool, error) {
	entry, ok, err := s.kv.Get(ctx, s.keyFn(ticket))
	if err != nil || !ok {
		return MFAChallenge{}, false, err
	}
	if err := s.kv.Delete(ctx, s.keyFn(ticket)); err != nil && !errors.Is(err, ErrNotFound) {
		return MFAChallenge{}, false, err
	}
	var challenge MFAChallenge
	if err := json.Unmarshal(entry.Value, &challenge); err != nil {
		return MFAChallenge{}, false, fmt.Errorf("auth: decode mfa challenge: %w", err)
	}
	if !challenge.ExpiresAt.IsZero() && time.Now().After(challenge.ExpiresAt) {
		return MFAChallenge{}, false, nil
	}
	return challenge, true, nil
}

// ErrNotFound reports that a key was not there, so a caller can tell "gone" from "broken".
var ErrNotFound = errors.New("auth: not found")

func normalizeUser(username string) string {
	// The store is case-insensitive about usernames, matching the login that produced them.
	return strings.ToLower(strings.TrimSpace(username))
}
