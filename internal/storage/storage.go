package storage

import (
	"context"
	"time"
)

// BucketState represents the persistent state of a token bucket.
type BucketState struct {
	Tokens         float64   `json:"tokens"`
	LastRefillTime time.Time `json:"last_refill_time"`
	Capacity       int64     `json:"capacity"`
	RefillRate     float64   `json:"refill_rate"`
}

// Storage defines the interface for rate limiter state persistence.
// Implementations must be thread-safe.
type Storage interface {
	// Get retrieves the bucket state for the given key.
	// Returns nil, nil if the key does not exist.
	Get(ctx context.Context, key string) (*BucketState, error)

	// Set stores the bucket state with an optional TTL.
	// If ttl is 0, the entry does not expire.
	Set(ctx context.Context, key string, state *BucketState, ttl time.Duration) error

	// Delete removes the bucket state for the given key.
	// Returns nil if the key does not exist.
	Delete(ctx context.Context, key string) error

	// Close closes the storage connection and releases resources.
	Close() error

	// Ping checks if the storage is healthy and reachable.
	Ping(ctx context.Context) error
}

// AtomicStorage extends Storage with atomic operations for distributed scenarios.
// This is primarily used by Redis storage for consistency.
type AtomicStorage interface {
	Storage

	// CheckAndConsume atomically checks if tokens are available and consumes them.
	// Returns the decision (allowed, remaining tokens, etc.) and any error.
	// This is the key operation for distributed rate limiting.
	CheckAndConsume(ctx context.Context, key string, tokens, capacity int64, refillRate float64, ttl time.Duration) (*ConsumeResult, error)
}

// ConsumeResult represents the result of an atomic check-and-consume operation.
type ConsumeResult struct {
	Allowed        bool
	CurrentTokens  float64
	Capacity       int64
	RefillRate     float64
	LastRefillTime time.Time
}
