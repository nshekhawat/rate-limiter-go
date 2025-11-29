package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig holds the configuration for Redis connection.
type RedisConfig struct {
	Address      string
	Password     string
	DB           int
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxRetries   int
}

// DefaultRedisConfig returns a RedisConfig with sensible defaults.
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Address:      "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 5,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		MaxRetries:   3,
	}
}

// RedisStorage implements the Storage and AtomicStorage interfaces using Redis.
// It provides distributed rate limiting with atomic operations using Lua scripts.
type RedisStorage struct {
	client *redis.Client
	config RedisConfig
}

// NewRedisStorage creates a new Redis storage instance.
func NewRedisStorage(config RedisConfig) (*RedisStorage, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         config.Address,
		Password:     config.Password,
		DB:           config.DB,
		PoolSize:     config.PoolSize,
		MinIdleConns: config.MinIdleConns,
		DialTimeout:  config.DialTimeout,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		MaxRetries:   config.MaxRetries,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), config.DialTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisStorage{
		client: client,
		config: config,
	}, nil
}

// NewRedisStorageFromClient creates a RedisStorage from an existing Redis client.
// This is useful for testing or when you want to manage the client lifecycle externally.
func NewRedisStorageFromClient(client *redis.Client) *RedisStorage {
	return &RedisStorage{
		client: client,
		config: DefaultRedisConfig(),
	}
}

// Get retrieves the bucket state for the given key.
func (rs *RedisStorage) Get(ctx context.Context, key string) (*BucketState, error) {
	data, err := rs.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("redis get failed: %w", err)
	}

	var state BucketState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bucket state: %w", err)
	}

	return &state, nil
}

// Set stores the bucket state with an optional TTL.
func (rs *RedisStorage) Set(ctx context.Context, key string, state *BucketState, ttl time.Duration) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal bucket state: %w", err)
	}

	if ttl > 0 {
		err = rs.client.Set(ctx, key, data, ttl).Err()
	} else {
		err = rs.client.Set(ctx, key, data, 0).Err()
	}

	if err != nil {
		return fmt.Errorf("redis set failed: %w", err)
	}

	return nil
}

// Delete removes the bucket state for the given key.
func (rs *RedisStorage) Delete(ctx context.Context, key string) error {
	err := rs.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("redis delete failed: %w", err)
	}
	return nil
}

// Close closes the Redis connection.
func (rs *RedisStorage) Close() error {
	return rs.client.Close()
}

// Ping checks if Redis is healthy and reachable.
func (rs *RedisStorage) Ping(ctx context.Context) error {
	return rs.client.Ping(ctx).Err()
}

// Lua script for atomic token bucket operations.
// This script performs the following atomically:
// 1. Get or create bucket state
// 2. Calculate token refill based on elapsed time
// 3. Check if tokens are available
// 4. Consume tokens if allowed
// 5. Store updated state
//
// KEYS[1] = bucket key
// ARGV[1] = tokens to consume
// ARGV[2] = bucket capacity
// ARGV[3] = refill rate (tokens per second)
// ARGV[4] = current timestamp (Unix nanoseconds)
// ARGV[5] = TTL in seconds (0 for no expiry)
//
// Returns: [allowed (0/1), current_tokens, last_refill_time_ns]
var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local tokens_to_consume = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local refill_rate = tonumber(ARGV[3])
local now_ns = tonumber(ARGV[4])
local ttl_seconds = tonumber(ARGV[5])

-- Get current state
local data = redis.call('GET', key)
local current_tokens
local last_refill_ns

if data then
    -- Parse existing state (format: "tokens:last_refill_ns")
    local separator = string.find(data, ':')
    if separator then
        current_tokens = tonumber(string.sub(data, 1, separator - 1))
        last_refill_ns = tonumber(string.sub(data, separator + 1))
    else
        -- Invalid format, reset
        current_tokens = capacity
        last_refill_ns = now_ns
    end
else
    -- New bucket, start full
    current_tokens = capacity
    last_refill_ns = now_ns
end

-- Calculate refill
local elapsed_seconds = (now_ns - last_refill_ns) / 1000000000
if elapsed_seconds > 0 then
    current_tokens = current_tokens + (elapsed_seconds * refill_rate)
    if current_tokens > capacity then
        current_tokens = capacity
    end
    last_refill_ns = now_ns
end

-- Check and consume
local allowed = 0
if current_tokens >= tokens_to_consume then
    current_tokens = current_tokens - tokens_to_consume
    allowed = 1
end

-- Store updated state
local new_data = string.format("%.6f:%d", current_tokens, last_refill_ns)
if ttl_seconds > 0 then
    redis.call('SETEX', key, ttl_seconds, new_data)
else
    redis.call('SET', key, new_data)
end

return {allowed, tostring(current_tokens), tostring(last_refill_ns)}
`)

// CheckAndConsume atomically checks if tokens are available and consumes them.
// This uses a Lua script for distributed consistency.
func (rs *RedisStorage) CheckAndConsume(ctx context.Context, key string, tokens int64, capacity int64, refillRate float64, ttl time.Duration) (*ConsumeResult, error) {
	nowNs := time.Now().UnixNano()
	ttlSeconds := int64(0)
	if ttl > 0 {
		ttlSeconds = int64(ttl.Seconds())
		if ttlSeconds < 1 {
			ttlSeconds = 1
		}
	}

	result, err := tokenBucketScript.Run(ctx, rs.client, []string{key},
		tokens,
		capacity,
		refillRate,
		nowNs,
		ttlSeconds,
	).Slice()

	if err != nil {
		return nil, fmt.Errorf("redis script failed: %w", err)
	}

	if len(result) != 3 {
		return nil, fmt.Errorf("unexpected script result length: %d", len(result))
	}

	allowed := result[0].(int64) == 1

	var currentTokens float64
	switch v := result[1].(type) {
	case string:
		fmt.Sscanf(v, "%f", &currentTokens)
	case float64:
		currentTokens = v
	case int64:
		currentTokens = float64(v)
	}

	var lastRefillNs int64
	switch v := result[2].(type) {
	case string:
		fmt.Sscanf(v, "%d", &lastRefillNs)
	case int64:
		lastRefillNs = v
	}

	return &ConsumeResult{
		Allowed:        allowed,
		CurrentTokens:  currentTokens,
		Capacity:       capacity,
		RefillRate:     refillRate,
		LastRefillTime: time.Unix(0, lastRefillNs),
	}, nil
}

// GetClient returns the underlying Redis client for advanced operations.
func (rs *RedisStorage) GetClient() *redis.Client {
	return rs.client
}

// Ensure RedisStorage implements AtomicStorage
var _ AtomicStorage = (*RedisStorage)(nil)
