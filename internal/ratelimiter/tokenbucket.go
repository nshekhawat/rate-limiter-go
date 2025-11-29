package ratelimiter

import (
	"sync"
	"time"
)

// TokenBucket implements the token bucket algorithm for rate limiting.
// It is thread-safe and uses lazy refill to calculate tokens on-demand.
type TokenBucket struct {
	capacity       int64     // Maximum number of tokens
	tokens         float64   // Current available tokens (float for precise refill calculations)
	refillRate     float64   // Tokens added per second
	lastRefillTime time.Time // Timestamp of last refill calculation
	mu             sync.Mutex
}

// NewTokenBucket creates a new token bucket with the specified capacity and refill rate.
// The bucket starts full (tokens = capacity).
func NewTokenBucket(capacity int64, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:       capacity,
		tokens:         float64(capacity),
		refillRate:     refillRate,
		lastRefillTime: time.Now(),
	}
}

// Allow checks if a single token can be consumed.
// Returns true if the request is allowed (token consumed), false otherwise.
func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

// AllowN checks if n tokens can be consumed.
// Returns true if the request is allowed (tokens consumed), false otherwise.
func (tb *TokenBucket) AllowN(n int64) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

// refill calculates and adds tokens based on elapsed time since last refill.
// This implements lazy refill - tokens are calculated on-demand rather than
// using background goroutines.
// Must be called with mu held.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefillTime).Seconds()

	if elapsed > 0 {
		// Calculate tokens to add based on elapsed time
		tokensToAdd := elapsed * tb.refillRate

		// Add tokens, but don't exceed capacity
		tb.tokens += tokensToAdd
		if tb.tokens > float64(tb.capacity) {
			tb.tokens = float64(tb.capacity)
		}

		tb.lastRefillTime = now
	}
}

// Reset resets the bucket to full capacity.
func (tb *TokenBucket) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.tokens = float64(tb.capacity)
	tb.lastRefillTime = time.Now()
}

// GetAvailableTokens returns the current number of available tokens.
// This also triggers a refill calculation.
func (tb *TokenBucket) GetAvailableTokens() int64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return int64(tb.tokens)
}

// GetAvailableTokensFloat returns the current number of available tokens as a float.
// This also triggers a refill calculation.
func (tb *TokenBucket) GetAvailableTokensFloat() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return tb.tokens
}

// GetCapacity returns the bucket's maximum capacity.
func (tb *TokenBucket) GetCapacity() int64 {
	return tb.capacity
}

// GetRefillRate returns the bucket's refill rate (tokens per second).
func (tb *TokenBucket) GetRefillRate() float64 {
	return tb.refillRate
}

// TimeUntilTokens returns the duration until n tokens will be available.
// Returns 0 if n tokens are already available.
func (tb *TokenBucket) TimeUntilTokens(n int64) time.Duration {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(n) {
		return 0
	}

	// Calculate time needed to accumulate required tokens
	tokensNeeded := float64(n) - tb.tokens
	secondsNeeded := tokensNeeded / tb.refillRate

	return time.Duration(secondsNeeded * float64(time.Second))
}

// State returns a snapshot of the current bucket state.
type BucketSnapshot struct {
	Capacity       int64
	Tokens         float64
	RefillRate     float64
	LastRefillTime time.Time
}

// GetState returns a snapshot of the current bucket state.
func (tb *TokenBucket) GetState() BucketSnapshot {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	return BucketSnapshot{
		Capacity:       tb.capacity,
		Tokens:         tb.tokens,
		RefillRate:     tb.refillRate,
		LastRefillTime: tb.lastRefillTime,
	}
}

// SetState restores the bucket to a previous state.
// This is useful for distributed scenarios where state is loaded from storage.
func (tb *TokenBucket) SetState(tokens float64, lastRefillTime time.Time) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.tokens = tokens
	if tb.tokens > float64(tb.capacity) {
		tb.tokens = float64(tb.capacity)
	}
	if tb.tokens < 0 {
		tb.tokens = 0
	}
	tb.lastRefillTime = lastRefillTime
}
