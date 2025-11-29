// Package client provides client libraries for interacting with the rate limiter service.
package client

import (
	"context"
	"time"
)

// Client is the interface for rate limiter clients.
type Client interface {
	// Check checks if a request should be allowed.
	Check(ctx context.Context, identifier string, opts ...CheckOption) (*Decision, error)

	// GetStatus returns the current rate limit status.
	GetStatus(ctx context.Context, identifier string, opts ...StatusOption) (*Status, error)

	// Reset resets the rate limit for an identifier.
	Reset(ctx context.Context, identifier string, opts ...ResetOption) error

	// Close closes the client connection.
	Close() error
}

// Decision represents a rate limit decision.
type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	ResetAt    time.Time
	RetryAfter time.Duration
}

// Status represents the current rate limit status.
type Status struct {
	Limit           int64
	Remaining       int64
	ResetAt         time.Time
	TokensAvailable float64
}

// CheckOption configures a Check call.
type CheckOption func(*checkOptions)

type checkOptions struct {
	resource string
	tokens   int64
}

// WithResource sets the resource for the check.
func WithResource(resource string) CheckOption {
	return func(o *checkOptions) {
		o.resource = resource
	}
}

// WithTokens sets the number of tokens to consume.
func WithTokens(tokens int64) CheckOption {
	return func(o *checkOptions) {
		o.tokens = tokens
	}
}

// StatusOption configures a GetStatus call.
type StatusOption func(*statusOptions)

type statusOptions struct {
	resource string
}

// WithStatusResource sets the resource for the status check.
func WithStatusResource(resource string) StatusOption {
	return func(o *statusOptions) {
		o.resource = resource
	}
}

// ResetOption configures a Reset call.
type ResetOption func(*resetOptions)

type resetOptions struct {
	resource string
}

// WithResetResource sets the resource for the reset.
func WithResetResource(resource string) ResetOption {
	return func(o *resetOptions) {
		o.resource = resource
	}
}
