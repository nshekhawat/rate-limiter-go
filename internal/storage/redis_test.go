package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// setupRedisContainer starts a Redis container for testing and returns
// the storage instance and a cleanup function.
func setupRedisContainer(t *testing.T) (*RedisStorage, func()) {
	t.Helper()

	ctx := context.Background()

	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err)

	connStr, err := redisContainer.ConnectionString(ctx)
	require.NoError(t, err)

	// Parse connection string to get host:port
	opts, err := redis.ParseURL(connStr)
	require.NoError(t, err)

	config := RedisConfig{
		Address:      opts.Addr,
		Password:     opts.Password,
		DB:           opts.DB,
		PoolSize:     10,
		MinIdleConns: 2,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		MaxRetries:   3,
	}

	storage, err := NewRedisStorage(config)
	require.NoError(t, err)

	cleanup := func() {
		storage.Close()
		if err := testcontainers.TerminateContainer(redisContainer); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	}

	return storage, cleanup
}

func TestRedisStorage_GetSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Get non-existent key
	state, err := rs.Get(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, state)

	// Set a value
	originalState := &BucketState{
		Tokens:         50.5,
		LastRefillTime: time.Now().Truncate(time.Millisecond), // Truncate for comparison
		Capacity:       100,
		RefillRate:     10.0,
	}
	err = rs.Set(ctx, "test-key", originalState, 0)
	require.NoError(t, err)

	// Get the value back
	retrieved, err := rs.Get(ctx, "test-key")
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, originalState.Tokens, retrieved.Tokens)
	assert.Equal(t, originalState.Capacity, retrieved.Capacity)
	assert.Equal(t, originalState.RefillRate, retrieved.RefillRate)
}

func TestRedisStorage_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Set a value
	state := &BucketState{Tokens: 50.0, Capacity: 100, RefillRate: 10.0}
	err := rs.Set(ctx, "test-key", state, 0)
	require.NoError(t, err)

	// Delete it
	err = rs.Delete(ctx, "test-key")
	require.NoError(t, err)

	// Should be gone
	retrieved, err := rs.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestRedisStorage_TTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Set with short TTL
	state := &BucketState{Tokens: 50.0, Capacity: 100, RefillRate: 10.0}
	err := rs.Set(ctx, "test-key", state, 1*time.Second)
	require.NoError(t, err)

	// Should exist initially
	retrieved, err := rs.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)

	// Wait for expiration
	time.Sleep(1500 * time.Millisecond)

	// Should be expired
	retrieved, err = rs.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestRedisStorage_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()
	err := rs.Ping(ctx)
	require.NoError(t, err)
}

func TestRedisStorage_CheckAndConsume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()
	key := "test-bucket"
	capacity := int64(10)
	refillRate := 10.0

	// First consume should succeed (bucket starts full)
	result, err := rs.CheckAndConsume(ctx, key, 5, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.InDelta(t, 5.0, result.CurrentTokens, 0.1)

	// Second consume of 5 should succeed
	result, err = rs.CheckAndConsume(ctx, key, 5, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.InDelta(t, 0.0, result.CurrentTokens, 0.1)

	// Third consume should fail (no tokens)
	result, err = rs.CheckAndConsume(ctx, key, 1, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.False(t, result.Allowed)
}

func TestRedisStorage_CheckAndConsume_Refill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()
	key := "test-bucket-refill"
	capacity := int64(10)
	refillRate := 100.0 // 100 tokens per second

	// Consume all tokens
	result, err := rs.CheckAndConsume(ctx, key, 10, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.InDelta(t, 0.0, result.CurrentTokens, 0.1)

	// Wait for refill (100ms = 10 tokens at 100/s, but capped at capacity)
	time.Sleep(120 * time.Millisecond)

	// Should have ~10 tokens now (capacity limit)
	result, err = rs.CheckAndConsume(ctx, key, 5, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
}

func TestRedisStorage_CheckAndConsume_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()
	key := "test-bucket-concurrent"
	capacity := int64(100)
	refillRate := 0.0 // No refill

	var wg sync.WaitGroup
	results := make(chan bool, 200)

	// 200 concurrent requests for 1 token each
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := rs.CheckAndConsume(ctx, key, 1, capacity, refillRate, time.Minute)
			assert.NoError(t, err)
			if result != nil {
				results <- result.Allowed
			}
		}()
	}

	wg.Wait()
	close(results)

	// Count allowed requests
	allowed := 0
	for result := range results {
		if result {
			allowed++
		}
	}

	// Exactly 100 should be allowed (bucket capacity)
	assert.Equal(t, 100, allowed)
}

func TestRedisStorage_CheckAndConsume_MultipleKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rs, cleanup := setupRedisContainer(t)
	defer cleanup()

	ctx := context.Background()
	capacity := int64(10)
	refillRate := 10.0

	// Test multiple independent buckets
	keys := []string{"bucket1", "bucket2", "bucket3"}

	for _, key := range keys {
		// Each bucket should have full capacity
		result, err := rs.CheckAndConsume(ctx, key, 10, capacity, refillRate, time.Minute)
		require.NoError(t, err)
		assert.True(t, result.Allowed)
		assert.InDelta(t, 0.0, result.CurrentTokens, 0.1)

		// Each bucket should now be empty
		result, err = rs.CheckAndConsume(ctx, key, 1, capacity, refillRate, time.Minute)
		require.NoError(t, err)
		assert.False(t, result.Allowed)
	}
}

func TestRedisStorage_ConnectionFailure(t *testing.T) {
	// Try to connect to non-existent Redis
	config := RedisConfig{
		Address:     "localhost:59999", // Non-existent port
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  1,
	}

	_, err := NewRedisStorage(config)
	assert.Error(t, err)
}

// Benchmarks (only run when not in short mode)

func BenchmarkRedisStorage_CheckAndConsume(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ctx := context.Background()

	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		b.Fatal(err)
	}
	defer testcontainers.TerminateContainer(redisContainer)

	connStr, _ := redisContainer.ConnectionString(ctx)
	opts, _ := redis.ParseURL(connStr)

	config := RedisConfig{
		Address:      opts.Addr,
		PoolSize:     50,
		MinIdleConns: 10,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		MaxRetries:   3,
	}

	rs, err := NewRedisStorage(config)
	if err != nil {
		b.Fatal(err)
	}
	defer rs.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rs.CheckAndConsume(ctx, "bench-key", 1, 1000000, 1000000.0, time.Minute)
	}
}

func BenchmarkRedisStorage_CheckAndConsume_Parallel(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ctx := context.Background()

	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		b.Fatal(err)
	}
	defer testcontainers.TerminateContainer(redisContainer)

	connStr, _ := redisContainer.ConnectionString(ctx)
	opts, _ := redis.ParseURL(connStr)

	config := RedisConfig{
		Address:      opts.Addr,
		PoolSize:     50,
		MinIdleConns: 10,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		MaxRetries:   3,
	}

	rs, err := NewRedisStorage(config)
	if err != nil {
		b.Fatal(err)
	}
	defer rs.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rs.CheckAndConsume(ctx, "bench-key", 1, 1000000, 1000000.0, time.Minute)
		}
	})
}
