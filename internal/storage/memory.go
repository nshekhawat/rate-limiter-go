package storage

import (
	"context"
	"sync"
	"time"
)

// memoryEntry stores a bucket state with its expiration time and a mutex for atomic operations.
type memoryEntry struct {
	mu        sync.Mutex
	state     *BucketState
	expiresAt time.Time
}

// MemoryStorage implements the Storage interface using in-memory storage.
// It is suitable for single-instance deployments or testing.
type MemoryStorage struct {
	data          sync.Map
	locks         sync.Map // Per-key locks for atomic operations
	cleanupTicker *time.Ticker
	done          chan struct{}
	closed        bool
	closeMu       sync.Mutex
}

// NewMemoryStorage creates a new in-memory storage instance.
// It starts a background goroutine for TTL cleanup with the specified interval.
func NewMemoryStorage(cleanupInterval time.Duration) *MemoryStorage {
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}

	ms := &MemoryStorage{
		cleanupTicker: time.NewTicker(cleanupInterval),
		done:          make(chan struct{}),
	}

	go ms.cleanupLoop()

	return ms
}

// getLock returns the mutex for a given key, creating one if it doesn't exist.
func (ms *MemoryStorage) getLock(key string) *sync.Mutex {
	lock, _ := ms.locks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex) //nolint:errcheck // type assertion is safe here
}

// cleanupLoop periodically removes expired entries.
func (ms *MemoryStorage) cleanupLoop() {
	for {
		select {
		case <-ms.cleanupTicker.C:
			ms.cleanup()
		case <-ms.done:
			return
		}
	}
}

// cleanup removes all expired entries.
func (ms *MemoryStorage) cleanup() {
	now := time.Now()
	ms.data.Range(func(key, value interface{}) bool {
		entry, ok := value.(*memoryEntry)
		if !ok {
			return true
		}
		entry.mu.Lock()
		expired := !entry.expiresAt.IsZero() && now.After(entry.expiresAt)
		entry.mu.Unlock()
		if expired {
			ms.data.Delete(key)
		}
		return true
	})
}

// Get retrieves the bucket state for the given key.
func (ms *MemoryStorage) Get(ctx context.Context, key string) (*BucketState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	value, ok := ms.data.Load(key)
	if !ok {
		return nil, nil
	}

	entry, ok := value.(*memoryEntry)
	if !ok {
		return nil, nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Check if expired
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		ms.data.Delete(key)
		return nil, nil
	}

	// Return a copy to prevent external modification
	stateCopy := *entry.state
	return &stateCopy, nil
}

// Set stores the bucket state with an optional TTL.
func (ms *MemoryStorage) Set(ctx context.Context, key string, state *BucketState, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Store a copy to prevent external modification
	stateCopy := *state
	entry := &memoryEntry{
		state: &stateCopy,
	}

	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}

	ms.data.Store(key, entry)
	return nil
}

// Delete removes the bucket state for the given key.
func (ms *MemoryStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ms.data.Delete(key)
	return nil
}

// Close stops the cleanup goroutine and releases resources.
func (ms *MemoryStorage) Close() error {
	ms.closeMu.Lock()
	defer ms.closeMu.Unlock()

	if ms.closed {
		return nil
	}

	ms.closed = true
	ms.cleanupTicker.Stop()
	close(ms.done)
	return nil
}

// Ping checks if the storage is healthy.
func (ms *MemoryStorage) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ms.closeMu.Lock()
	defer ms.closeMu.Unlock()

	if ms.closed {
		return ErrStorageClosed
	}

	return nil
}

// CheckAndConsume atomically checks if tokens are available and consumes them.
// For in-memory storage, this uses per-key locks for atomicity.
func (ms *MemoryStorage) CheckAndConsume(ctx context.Context, key string, tokens, capacity int64, refillRate float64, ttl time.Duration) (*ConsumeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Get per-key lock for atomic operation
	lock := ms.getLock(key)
	lock.Lock()
	defer lock.Unlock()

	now := time.Now()

	// Load or create entry
	var state *BucketState
	var entry *memoryEntry

	value, loaded := ms.data.Load(key)

	if loaded {
		var ok bool
		entry, ok = value.(*memoryEntry)
		if !ok {
			loaded = false
		} else {
			// Check if expired
			if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
				loaded = false
			} else {
				// Work with a copy of the state
				stateCopy := *entry.state
				state = &stateCopy
			}
		}
	}

	if !loaded {
		// Create new bucket at full capacity
		state = &BucketState{
			Tokens:         float64(capacity),
			LastRefillTime: now,
			Capacity:       capacity,
			RefillRate:     refillRate,
		}
	}

	// Calculate refill
	elapsed := now.Sub(state.LastRefillTime).Seconds()
	if elapsed > 0 {
		state.Tokens += elapsed * state.RefillRate
		if state.Tokens > float64(state.Capacity) {
			state.Tokens = float64(state.Capacity)
		}
		state.LastRefillTime = now
	}

	// Check if we can consume
	allowed := state.Tokens >= float64(tokens)
	if allowed {
		state.Tokens -= float64(tokens)
	}

	// Store updated state (create new entry to avoid races)
	newEntry := &memoryEntry{
		state: state,
	}
	if ttl > 0 {
		newEntry.expiresAt = now.Add(ttl)
	}
	ms.data.Store(key, newEntry)

	return &ConsumeResult{
		Allowed:        allowed,
		CurrentTokens:  state.Tokens,
		Capacity:       state.Capacity,
		RefillRate:     state.RefillRate,
		LastRefillTime: state.LastRefillTime,
	}, nil
}

// Ensure MemoryStorage implements AtomicStorage
var _ AtomicStorage = (*MemoryStorage)(nil)
