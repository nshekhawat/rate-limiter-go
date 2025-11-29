package ratelimiter

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewTokenBucket(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	assert.Equal(t, int64(100), tb.GetCapacity())
	assert.Equal(t, 10.0, tb.GetRefillRate())
	assert.Equal(t, int64(100), tb.GetAvailableTokens())
}

func TestTokenBucket_Allow(t *testing.T) {
	tb := NewTokenBucket(5, 1.0)

	// Should allow 5 requests (bucket starts full)
	for i := 0; i < 5; i++ {
		assert.True(t, tb.Allow(), "request %d should be allowed", i+1)
	}

	// 6th request should be denied
	assert.False(t, tb.Allow(), "6th request should be denied")
	assert.Equal(t, int64(0), tb.GetAvailableTokens())
}

func TestTokenBucket_AllowN(t *testing.T) {
	tb := NewTokenBucket(10, 1.0)

	// Request 5 tokens - should succeed
	assert.True(t, tb.AllowN(5))
	assert.Equal(t, int64(5), tb.GetAvailableTokens())

	// Request 6 tokens - should fail (only 5 available)
	assert.False(t, tb.AllowN(6))
	assert.Equal(t, int64(5), tb.GetAvailableTokens()) // Tokens not consumed on failure

	// Request 5 more - should succeed
	assert.True(t, tb.AllowN(5))
	assert.Equal(t, int64(0), tb.GetAvailableTokens())
}

func TestTokenBucket_AllowN_ZeroTokens(t *testing.T) {
	tb := NewTokenBucket(10, 1.0)

	// Requesting 0 tokens should always succeed
	assert.True(t, tb.AllowN(0))
	assert.Equal(t, int64(10), tb.GetAvailableTokens())
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := NewTokenBucket(10, 10.0) // 10 tokens/second

	// Consume all tokens
	assert.True(t, tb.AllowN(10))
	assert.Equal(t, int64(0), tb.GetAvailableTokens())

	// Wait for refill (100ms = 1 token at 10 tokens/second)
	time.Sleep(110 * time.Millisecond)

	// Should have approximately 1 token
	tokens := tb.GetAvailableTokens()
	assert.GreaterOrEqual(t, tokens, int64(1), "should have at least 1 token after 100ms")
	assert.LessOrEqual(t, tokens, int64(2), "should have at most 2 tokens after 100ms")
}

func TestTokenBucket_RefillDoesNotExceedCapacity(t *testing.T) {
	tb := NewTokenBucket(10, 100.0) // High refill rate

	// Even after waiting, tokens should not exceed capacity
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int64(10), tb.GetAvailableTokens())
}

func TestTokenBucket_Reset(t *testing.T) {
	tb := NewTokenBucket(100, 1.0)

	// Consume all tokens
	assert.True(t, tb.AllowN(100))
	assert.Equal(t, int64(0), tb.GetAvailableTokens())

	// Reset bucket
	tb.Reset()

	// Should be back to full capacity
	assert.Equal(t, int64(100), tb.GetAvailableTokens())
}

func TestTokenBucket_GetState(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	// Consume some tokens
	tb.AllowN(30)

	state := tb.GetState()
	assert.Equal(t, int64(100), state.Capacity)
	assert.Equal(t, 10.0, state.RefillRate)
	assert.InDelta(t, 70.0, state.Tokens, 1.0) // Allow small delta for timing
	assert.False(t, state.LastRefillTime.IsZero())
}

func TestTokenBucket_SetState(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	pastTime := time.Now().Add(-1 * time.Second)
	tb.SetState(50.0, pastTime)

	// After SetState, refill should calculate based on elapsed time
	tokens := tb.GetAvailableTokensFloat()

	// Should have ~60 tokens (50 + 10 tokens/sec * 1 second)
	assert.InDelta(t, 60.0, tokens, 2.0)
}

func TestTokenBucket_SetState_ClampsToCapacity(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	// Try to set more tokens than capacity
	tb.SetState(200.0, time.Now())

	assert.Equal(t, int64(100), tb.GetAvailableTokens())
}

func TestTokenBucket_SetState_ClampsToZero(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	// Try to set negative tokens
	tb.SetState(-50.0, time.Now())

	assert.Equal(t, int64(0), tb.GetAvailableTokens())
}

func TestTokenBucket_TimeUntilTokens(t *testing.T) {
	tb := NewTokenBucket(10, 10.0) // 10 tokens/second

	// When bucket is full, should return 0
	assert.Equal(t, time.Duration(0), tb.TimeUntilTokens(5))

	// Consume all tokens
	tb.AllowN(10)

	// Time until 5 tokens = 5/10 = 0.5 seconds
	duration := tb.TimeUntilTokens(5)
	assert.InDelta(t, 500*time.Millisecond, duration, float64(50*time.Millisecond))
}

func TestTokenBucket_ConcurrentAccess(t *testing.T) {
	tb := NewTokenBucket(1000, 100.0)

	var wg sync.WaitGroup
	allowed := make(chan bool, 1000)

	// Start 100 goroutines, each trying to consume 10 tokens
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := tb.AllowN(10)
			allowed <- result
		}()
	}

	wg.Wait()
	close(allowed)

	// Count successful requests
	successCount := 0
	for result := range allowed {
		if result {
			successCount++
		}
	}

	// All 100 requests should succeed (100 * 10 = 1000 tokens, exactly capacity)
	assert.Equal(t, 100, successCount)

	// Bucket should be empty now
	assert.Equal(t, int64(0), tb.GetAvailableTokens())
}

func TestTokenBucket_ConcurrentAccessWithContention(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	var wg sync.WaitGroup
	allowed := make(chan bool, 200)

	// Start 200 goroutines, each trying to consume 1 token
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := tb.Allow()
			allowed <- result
		}()
	}

	wg.Wait()
	close(allowed)

	// Count successful requests
	successCount := 0
	for result := range allowed {
		if result {
			successCount++
		}
	}

	// Exactly 100 should succeed (bucket capacity)
	assert.Equal(t, 100, successCount)
}

func TestTokenBucket_BurstBehavior(t *testing.T) {
	tb := NewTokenBucket(100, 10.0)

	// Consume all tokens in a burst
	for i := 0; i < 100; i++ {
		assert.True(t, tb.Allow())
	}

	// Next request should fail
	assert.False(t, tb.Allow())

	// Wait for partial refill
	time.Sleep(150 * time.Millisecond) // Should get ~1.5 tokens

	// Should allow 1 more request
	assert.True(t, tb.Allow())
}

func TestTokenBucket_EmptyBucket(t *testing.T) {
	tb := NewTokenBucket(0, 10.0)

	// Empty capacity bucket should never allow
	assert.False(t, tb.Allow())
	assert.Equal(t, int64(0), tb.GetAvailableTokens())
}

func TestTokenBucket_ZeroRefillRate(t *testing.T) {
	tb := NewTokenBucket(10, 0.0)

	// Consume all tokens
	tb.AllowN(10)

	// Wait some time
	time.Sleep(100 * time.Millisecond)

	// Should still be empty (no refill)
	assert.Equal(t, int64(0), tb.GetAvailableTokens())
}

func TestTokenBucket_HighRefillRate(t *testing.T) {
	tb := NewTokenBucket(100, 10000.0) // Very high refill rate

	// Consume all tokens
	tb.AllowN(100)

	// Wait a short time
	time.Sleep(20 * time.Millisecond)

	// Should be back to capacity (or close to it)
	tokens := tb.GetAvailableTokens()
	assert.GreaterOrEqual(t, tokens, int64(90))
}

func TestTokenBucket_FractionalTokens(t *testing.T) {
	tb := NewTokenBucket(10, 1.0) // 1 token per second

	// Consume 9 tokens
	tb.AllowN(9)

	// Should have 1 token
	assert.Equal(t, int64(1), tb.GetAvailableTokens())

	// Wait 500ms - should have ~1.5 tokens
	time.Sleep(500 * time.Millisecond)

	// Integer truncation should give 1
	tokens := tb.GetAvailableTokens()
	assert.GreaterOrEqual(t, tokens, int64(1))

	// But float should show fractional tokens
	floatTokens := tb.GetAvailableTokensFloat()
	assert.Greater(t, floatTokens, 1.0)
}

// Benchmarks

func BenchmarkTokenBucket_Allow(b *testing.B) {
	tb := NewTokenBucket(int64(b.N), 1000000.0) // High refill rate

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow()
	}
}

func BenchmarkTokenBucket_AllowN(b *testing.B) {
	tb := NewTokenBucket(int64(b.N*10), 1000000.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.AllowN(10)
	}
}

func BenchmarkTokenBucket_ConcurrentAllow(b *testing.B) {
	tb := NewTokenBucket(int64(b.N), 1000000.0)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow()
		}
	})
}

func BenchmarkTokenBucket_GetAvailableTokens(b *testing.B) {
	tb := NewTokenBucket(1000, 100.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.GetAvailableTokens()
	}
}

func BenchmarkTokenBucket_GetState(b *testing.B) {
	tb := NewTokenBucket(1000, 100.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.GetState()
	}
}
