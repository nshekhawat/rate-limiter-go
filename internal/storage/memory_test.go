package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStorage_GetSet(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	// Get non-existent key
	state, err := ms.Get(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, state)

	// Set a value
	originalState := &BucketState{
		Tokens:         50.5,
		LastRefillTime: time.Now(),
		Capacity:       100,
		RefillRate:     10.0,
	}
	err = ms.Set(ctx, "test-key", originalState, 0)
	require.NoError(t, err)

	// Get the value back
	retrieved, err := ms.Get(ctx, "test-key")
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, originalState.Tokens, retrieved.Tokens)
	assert.Equal(t, originalState.Capacity, retrieved.Capacity)
	assert.Equal(t, originalState.RefillRate, retrieved.RefillRate)
}

func TestMemoryStorage_SetReturnsClone(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	originalState := &BucketState{
		Tokens:         50.0,
		LastRefillTime: time.Now(),
		Capacity:       100,
		RefillRate:     10.0,
	}
	err := ms.Set(ctx, "test-key", originalState, 0)
	require.NoError(t, err)

	// Modify the original
	originalState.Tokens = 0

	// Retrieved value should be unaffected
	retrieved, err := ms.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Equal(t, 50.0, retrieved.Tokens)
}

func TestMemoryStorage_GetReturnsClone(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	originalState := &BucketState{
		Tokens:         50.0,
		LastRefillTime: time.Now(),
		Capacity:       100,
		RefillRate:     10.0,
	}
	err := ms.Set(ctx, "test-key", originalState, 0)
	require.NoError(t, err)

	// Get and modify
	retrieved1, err := ms.Get(ctx, "test-key")
	require.NoError(t, err)
	retrieved1.Tokens = 0

	// Get again - should be unaffected
	retrieved2, err := ms.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Equal(t, 50.0, retrieved2.Tokens)
}

func TestMemoryStorage_Delete(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	// Set a value
	state := &BucketState{Tokens: 50.0, Capacity: 100, RefillRate: 10.0}
	err := ms.Set(ctx, "test-key", state, 0)
	require.NoError(t, err)

	// Delete it
	err = ms.Delete(ctx, "test-key")
	require.NoError(t, err)

	// Should be gone
	retrieved, err := ms.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestMemoryStorage_DeleteNonexistent(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	// Delete non-existent key should not error
	err := ms.Delete(ctx, "nonexistent")
	require.NoError(t, err)
}

func TestMemoryStorage_TTL(t *testing.T) {
	ms := NewMemoryStorage(50 * time.Millisecond) // Short cleanup interval
	defer ms.Close()

	ctx := context.Background()

	// Set with short TTL
	state := &BucketState{Tokens: 50.0, Capacity: 100, RefillRate: 10.0}
	err := ms.Set(ctx, "test-key", state, 100*time.Millisecond)
	require.NoError(t, err)

	// Should exist initially
	retrieved, err := ms.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.NotNil(t, retrieved)

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	retrieved, err = ms.Get(ctx, "test-key")
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestMemoryStorage_Ping(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)

	ctx := context.Background()
	err := ms.Ping(ctx)
	require.NoError(t, err)

	// Close and try again
	ms.Close()

	err = ms.Ping(ctx)
	assert.ErrorIs(t, err, ErrStorageClosed)
}

func TestMemoryStorage_ContextCancellation(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// All operations should return context error
	_, err := ms.Get(ctx, "key")
	assert.Error(t, err)

	err = ms.Set(ctx, "key", &BucketState{}, 0)
	assert.Error(t, err)

	err = ms.Delete(ctx, "key")
	assert.Error(t, err)
}

func TestMemoryStorage_ConcurrentAccess(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	iterations := 100

	// Concurrent writes
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state := &BucketState{Tokens: float64(i), Capacity: 100, RefillRate: 10.0}
			err := ms.Set(ctx, "concurrent-key", state, 0)
			assert.NoError(t, err)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ms.Get(ctx, "concurrent-key")
			assert.NoError(t, err)
		}()
	}

	wg.Wait()
}

func TestMemoryStorage_CheckAndConsume(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	key := "test-bucket"
	capacity := int64(10)
	refillRate := 0.0 // No refill to avoid timing issues

	// First consume should succeed (bucket starts full)
	result, err := ms.CheckAndConsume(ctx, key, 5, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.InDelta(t, 5.0, result.CurrentTokens, 0.1)

	// Second consume of 5 should succeed
	result, err = ms.CheckAndConsume(ctx, key, 5, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.InDelta(t, 0.0, result.CurrentTokens, 0.1)

	// Third consume should fail (no tokens)
	result, err = ms.CheckAndConsume(ctx, key, 1, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.False(t, result.Allowed)
	assert.InDelta(t, 0.0, result.CurrentTokens, 0.1)
}

func TestMemoryStorage_CheckAndConsume_Refill(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	key := "test-bucket"
	capacity := int64(10)
	refillRate := 100.0 // 100 tokens per second

	// Consume all tokens
	result, err := ms.CheckAndConsume(ctx, key, 10, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
	assert.InDelta(t, 0.0, result.CurrentTokens, 0.1)

	// Wait for refill (60ms = 6 tokens at 100/s)
	time.Sleep(70 * time.Millisecond)

	// Should have ~6-7 tokens now, requesting 5
	result, err = ms.CheckAndConsume(ctx, key, 5, capacity, refillRate, time.Minute)
	require.NoError(t, err)
	assert.True(t, result.Allowed)
}

func TestMemoryStorage_CheckAndConsume_Concurrent(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	key := "test-bucket"
	capacity := int64(100)
	refillRate := 0.0 // No refill

	var wg sync.WaitGroup
	results := make(chan bool, 200)

	// 200 concurrent requests for 1 token each
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := ms.CheckAndConsume(ctx, key, 1, capacity, refillRate, time.Minute)
			assert.NoError(t, err)
			results <- result.Allowed
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

func TestMemoryStorage_CloseIdempotent(t *testing.T) {
	ms := NewMemoryStorage(time.Minute)

	// Multiple closes should not panic
	err := ms.Close()
	assert.NoError(t, err)

	err = ms.Close()
	assert.NoError(t, err)
}

// Benchmarks

func BenchmarkMemoryStorage_Get(b *testing.B) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	state := &BucketState{Tokens: 50.0, Capacity: 100, RefillRate: 10.0}
	ms.Set(ctx, "bench-key", state, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ms.Get(ctx, "bench-key")
	}
}

func BenchmarkMemoryStorage_Set(b *testing.B) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	state := &BucketState{Tokens: 50.0, Capacity: 100, RefillRate: 10.0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ms.Set(ctx, "bench-key", state, 0)
	}
}

func BenchmarkMemoryStorage_CheckAndConsume(b *testing.B) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ms.CheckAndConsume(ctx, "bench-key", 1, 1000000, 1000000.0, time.Minute)
	}
}

func BenchmarkMemoryStorage_CheckAndConsume_Parallel(b *testing.B) {
	ms := NewMemoryStorage(time.Minute)
	defer ms.Close()

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ms.CheckAndConsume(ctx, "bench-key", 1, 1000000, 1000000.0, time.Minute)
		}
	})
}
