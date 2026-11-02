package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// MFAChallenge is the ticket a login hands back when a second factor is due. It stands
// for "the password was correct for this principal", nothing more: it carries no scopes
// and cannot be exchanged for a token without a valid code.
type MFAChallenge struct {
	Principal Principal
	ExpiresAt time.Time
}

// ChallengeStore keeps pending challenges between the two login requests.
type ChallengeStore interface {
	PutChallenge(ctx context.Context, ticket string, challenge MFAChallenge) error
	TakeChallenge(ctx context.Context, ticket string) (MFAChallenge, bool, error)
}

// MemoryChallengeStore keeps challenges in memory. A challenge lives for minutes, so a
// replica that issued it is normally still the one that verifies it; a multi-replica
// deployment needs a shared store here too.
type MemoryChallengeStore struct {
	mu      sync.Mutex
	pending map[string]MFAChallenge
}

// NewMemoryChallengeStore returns an empty store.
func NewMemoryChallengeStore() *MemoryChallengeStore {
	return &MemoryChallengeStore{pending: map[string]MFAChallenge{}}
}

// PutChallenge implements ChallengeStore.
func (s *MemoryChallengeStore) PutChallenge(_ context.Context, ticket string, challenge MFAChallenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, existing := range s.pending {
		if now.After(existing.ExpiresAt) {
			delete(s.pending, key)
		}
	}
	s.pending[ticket] = challenge
	return nil
}

// TakeChallenge implements ChallengeStore. Taking is one-time: a replayed ticket finds
// nothing, which is what makes the second request safe to retry only with a fresh login.
func (s *MemoryChallengeStore) TakeChallenge(_ context.Context, ticket string) (MFAChallenge, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.pending[ticket]
	if !ok {
		return MFAChallenge{}, false, nil
	}
	delete(s.pending, ticket)
	if time.Now().After(challenge.ExpiresAt) {
		return MFAChallenge{}, false, nil
	}
	return challenge, true, nil
}

// newTicket returns an unguessable ticket. It is a bearer value for one request, so it
// has to be long enough that guessing is pointless.
func newTicket() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("auth: generate mfa ticket: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
