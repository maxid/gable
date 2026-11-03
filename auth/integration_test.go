package auth_test

import (
	"context"
	"os"
	"testing"
	"time"

	gredis "github.com/maxid/gcache/redis"
	goredis "github.com/redis/go-redis/v9"

	"github.com/maxid/gable/auth"
)

// The shared stores are only meaningful across processes, so this test needs a real Redis:
//
//	GABLE_TEST_REDIS_ADDR=127.0.0.1:6379 go test ./auth -run Integration
//
// It skips without one, which keeps `go test ./...` runnable on a laptop.
func TestIntegrationReplicasShareMFAState(t *testing.T) {
	addr := os.Getenv("GABLE_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set GABLE_TEST_REDIS_ADDR to run the shared-store integration test")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis: %v", err)
	}
	store, err := gredis.NewStore(client)
	if err != nil {
		t.Fatal(err)
	}

	// Two replicas over the same authoritative store. Deliberately not gcache.Cache: its local
	// tier answers from a copy another replica invalidates asynchronously, which is fine for a
	// cache and wrong for a security decision.
	mfaA, challengesA := auth.NewRedisStores(store, time.Minute)
	mfaB, challengesB := auth.NewRedisStores(store, time.Minute)

	serviceA, err := auth.NewMFAService(auth.DefaultTOTPConfig("smoke"), mfaA)
	if err != nil {
		t.Fatal(err)
	}
	serviceB, err := auth.NewMFAService(auth.DefaultTOTPConfig("smoke"), mfaB)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A unique account per run keeps repeated runs independent of leftover state.
	username := "integration-" + time.Now().Format("150405.000000")

	enrollment, err := serviceA.Enroll(ctx, 1, username)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(enrollment.Secret, time.Now(), serviceA.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := serviceA.Activate(ctx, 1, username, code); err != nil {
		t.Fatal(err)
	}

	// Replica B must see the factor, or the same user logs in unprotected there.
	active, err := serviceB.Active(ctx, 1, username)
	if err != nil || !active {
		t.Fatalf("replica B active = %v, %v", active, err)
	}
	if err := serviceB.Verify(ctx, 1, username, code); err != nil {
		t.Fatalf("replica B verify: %v", err)
	}
	// ... and the step is consumed: replica A rejects the same code.
	if err := serviceA.Verify(ctx, 1, username, code); err != auth.ErrMFAInvalidCode {
		t.Fatalf("replay across replicas = %v", err)
	}

	// A ticket issued by one replica is redeemed by the other, exactly once.
	ticket := "ticket-" + username
	if err := challengesA.PutChallenge(ctx, ticket, auth.MFAChallenge{ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := challengesB.TakeChallenge(ctx, ticket); err != nil || !ok {
		t.Fatalf("replica B take = %v, %v", ok, err)
	}
	if _, ok, _ := challengesA.TakeChallenge(ctx, ticket); ok {
		t.Fatal("a ticket must be usable once across replicas")
	}
}
