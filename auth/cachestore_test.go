package auth

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeKV is a cache in a map, with the two behaviours the stores depend on: expiry and delete.
type fakeKV struct {
	mu      sync.Mutex
	entries map[string]fakeEntry
}

type fakeEntry struct {
	value     []byte
	expiresAt time.Time
}

func newFakeKV() *fakeKV { return &fakeKV{entries: map[string]fakeEntry{}} }

func (f *fakeKV) Get(_ context.Context, key string) (Entry, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[key]
	if !ok {
		return Entry{}, false, nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(f.entries, key)
		return Entry{}, false, nil
	}
	return Entry{Value: entry.value}, true, nil
}

func (f *fakeKV) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := fakeEntry{value: value}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}
	f.entries[key] = entry
	return nil
}

func (f *fakeKV) SetPersistent(ctx context.Context, key string, value []byte) error {
	return f.Set(ctx, key, value, 0)
}

func (f *fakeKV) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
	return nil
}

// MFA state must survive a process restart: the enrollment is a property of the account, not of
// the pod that accepted it.
func TestCacheMFAStoreRoundTrip(t *testing.T) {
	kv := newFakeKV()
	store := NewCacheMFAStore(kv)
	ctx := context.Background()

	if state, err := store.GetMFA(ctx, 1, "ada"); err != nil || state != nil {
		t.Fatalf("empty get = %+v, %v", state, err)
	}

	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	state := &MFAState{
		TenantID: 1, Username: "Ada", Secret: "SECRET",
		Active: true, RecoveryCodes: []string{"hash1"}, LastStep: 42,
		CreatedAt: now, ActivatedAt: now,
	}
	if err := store.PutMFA(ctx, state); err != nil {
		t.Fatal(err)
	}

	// A second store over the same cache sees it — that is the point of the store.
	other := NewCacheMFAStore(kv)
	loaded, err := other.GetMFA(ctx, 1, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || !loaded.Active || loaded.Secret != "SECRET" || loaded.LastStep != 42 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if len(loaded.RecoveryCodes) != 1 {
		t.Fatalf("recovery codes = %v", loaded.RecoveryCodes)
	}
	// The key is namespace-able; a prefix must not collide with another environment.
	prefixed := NewCacheMFAStore(kv).WithKeyPrefix("prod")
	if state, _ := prefixed.GetMFA(ctx, 1, "ada"); state != nil {
		t.Fatal("a prefixed store must not read the unprefixed keys")
	}
}

// A ticket is one-shot across replicas, and it expires.
func TestCacheChallengeStoreIsOneShot(t *testing.T) {
	kv := newFakeKV()
	store := NewCacheChallengeStore(kv, time.Minute)
	ctx := context.Background()

	challenge := MFAChallenge{ExpiresAt: time.Now().Add(time.Minute)}
	if err := store.PutChallenge(ctx, "ticket-1", challenge); err != nil {
		t.Fatal(err)
	}

	// Two replicas holding different stores over the same cache: exactly one take wins.
	first := NewCacheChallengeStore(kv, time.Minute)
	second := NewCacheChallengeStore(kv, time.Minute)

	var winners int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, store := range []*CacheChallengeStore{first, second} {
		wg.Add(1)
		go func(index int, s *CacheChallengeStore) {
			defer wg.Done()
			taken, ok, err := s.TakeChallenge(ctx, "ticket-1")
			if err != nil {
				t.Errorf("store %d: %v", index, err)
				return
			}
			if ok && taken.ExpiresAt.IsZero() {
				t.Errorf("store %d: empty challenge", index)
			}
			if ok {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i, store)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly one", winners)
	}

	// An expired challenge is gone, not accepted late.
	if err := store.PutChallenge(ctx, "ticket-2", MFAChallenge{ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.TakeChallenge(ctx, "ticket-2"); err != nil || ok {
		t.Fatalf("expired take = %v, %v", ok, err)
	}
}

// The TTL is what bounds a pending login: without it a stolen ticket is valid forever.
func TestCacheChallengeStoreTTL(t *testing.T) {
	kv := newFakeKV()
	store := NewCacheChallengeStore(kv, 20*time.Millisecond)
	ctx := context.Background()
	if err := store.PutChallenge(ctx, "short", MFAChallenge{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok, _ := store.TakeChallenge(ctx, "short"); ok {
		t.Fatal("an expired ticket must not be accepted")
	}
}
