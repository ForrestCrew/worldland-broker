package postgres

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNonceRepository_ConsumeIfValid_ConcurrentSafety verifies atomic nonce consumption
// This test ensures that even with 10 concurrent goroutines attempting to consume
// the same nonce, exactly ONE consumption succeeds (DELETE...RETURNING atomicity)
func TestNonceRepository_ConsumeIfValid_ConcurrentSafety(t *testing.T) {
	// Skip if no PostgreSQL available (integration test)
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Note: This test requires a running PostgreSQL instance
	// In CI/CD, use testcontainers-go to spin up ephemeral PostgreSQL
	// For local testing, ensure PostgreSQL is running with schema applied

	ctx := context.Background()

	// Setup: Create connection pool and repository
	// (In real test: use testcontainers to start PostgreSQL)
	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "test",
		Password: "test",
		Database: "worldland_test",
		MaxConns: 20,
	}

	pool, err := NewPool(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}
	defer pool.Close()

	repo := NewNonceRepository(pool)

	// Create a single nonce
	nonce := "test-nonce-concurrent"
	err = repo.Save(ctx, nonce, time.Now().Add(5*time.Minute))
	require.NoError(t, err, "Failed to save nonce")

	// Attempt concurrent consumption from 10 goroutines
	var wg sync.WaitGroup
	successCount := int32(0)
	errorCount := int32(0)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consumed, err := repo.ConsumeIfValid(ctx, nonce)
			if err != nil {
				atomic.AddInt32(&errorCount, 1)
				return
			}
			if consumed {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// Verify: Exactly ONE goroutine consumed the nonce
	assert.Equal(t, int32(1), successCount, "Exactly one consume should succeed")
	assert.Equal(t, int32(0), errorCount, "No errors should occur")

	// Verify: Nonce is gone (further attempts fail)
	consumed, err := repo.ConsumeIfValid(ctx, nonce)
	require.NoError(t, err)
	assert.False(t, consumed, "Nonce should already be consumed")
}

// TestNonceRepository_ConsumeIfValid_Expired verifies expired nonces are not consumed
func TestNonceRepository_ConsumeIfValid_Expired(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "test",
		Password: "test",
		Database: "worldland_test",
		MaxConns: 10,
	}

	pool, err := NewPool(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
		return
	}
	defer pool.Close()

	repo := NewNonceRepository(pool)

	// Create an already-expired nonce
	nonce := "test-nonce-expired"
	err = repo.Save(ctx, nonce, time.Now().Add(-1*time.Minute))
	require.NoError(t, err)

	// Attempt to consume expired nonce
	consumed, err := repo.ConsumeIfValid(ctx, nonce)
	require.NoError(t, err)
	assert.False(t, consumed, "Expired nonce should not be consumed")
}
