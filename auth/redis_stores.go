package auth

import (
	"context"
	"time"

	"github.com/maxid/gcache"
)

// remoteKV adapts a gcache shared store to the KV these stores need.
//
// It deliberately takes the *store*, not the cache: MFA state is security state, and a cache
// answers from a local tier that a write on another replica invalidates asynchronously. Reading a
// stale enrollment is not a slower answer, it is an authentication decision made on old data — the
// replay test proved it by accepting a code another replica had already consumed.
type remoteKV struct{ store gcache.RemoteStore }

// NewRedisStores returns MFA and challenge stores backed by a shared store such as
// `gcache/redis.NewStore(client)`.
//
// This is what turns MFA from a property of one process into a property of the account: an
// enrollment made on one replica is seen by the next, and a login ticket issued by a load
// balancer's first pick can be redeemed on its second.
func NewRedisStores(store gcache.RemoteStore, challengeTTL time.Duration) (*CacheMFAStore, *CacheChallengeStore) {
	kv := remoteKV{store: store}
	return NewCacheMFAStore(kv), NewCacheChallengeStore(kv, challengeTTL)
}

// Get implements KV.
func (r remoteKV) Get(ctx context.Context, key string) (Entry, bool, error) {
	entry, ok, err := r.store.Get(ctx, key)
	if err != nil || !ok {
		return Entry{}, ok, err
	}
	return Entry{Value: entry.Value}, true, nil
}

// Set implements KV.
func (r remoteKV) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.store.Set(ctx, key, value, ttl)
}

// SetPersistent implements KV. The shared store has no "no expiry" call, so a long TTL stands in
// for one: an enrollment must outlive any deployment, and 100 years is that in practice.
func (r remoteKV) SetPersistent(ctx context.Context, key string, value []byte) error {
	return r.store.Set(ctx, key, value, persistentTTL)
}

// Delete implements KV.
func (r remoteKV) Delete(ctx context.Context, key string) error {
	return r.store.Delete(ctx, key)
}

// persistentTTL is how "no expiry" is spelled on a store that only takes TTLs.
const persistentTTL = 100 * 365 * 24 * time.Hour
