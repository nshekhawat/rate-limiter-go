package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HTTPClient is an HTTP-based rate limiter client.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

// HTTPConfig holds HTTP client configuration.
type HTTPConfig struct {
	BaseURL    string
	Timeout    time.Duration
	MaxRetries int
}

// DefaultHTTPConfig returns the default HTTP client configuration.
func DefaultHTTPConfig() *HTTPConfig {
	return &HTTPConfig{
		BaseURL:    "http://localhost:8080",
		Timeout:    5 * time.Second,
		MaxRetries: 3,
	}
}

// NewHTTPClient creates a new HTTP rate limiter client.
func NewHTTPClient(config *HTTPConfig) (*HTTPClient, error) {
	if config == nil {
		config = DefaultHTTPConfig()
	}

	return &HTTPClient{
		baseURL: config.BaseURL,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

type checkRequest struct {
	Identifier string `json:"identifier"`
	Resource   string `json:"resource,omitempty"`
	Tokens     int64  `json:"tokens,omitempty"`
}

type checkResponse struct {
	Allowed           bool  `json:"allowed"`
	Limit             int64 `json:"limit"`
	Remaining         int64 `json:"remaining"`
	ResetAtUnix       int64 `json:"reset_at_unix"`
	RetryAfterSeconds int64 `json:"retry_after_seconds,omitempty"`
}

type statusResponse struct {
	Limit           int64   `json:"limit"`
	Remaining       int64   `json:"remaining"`
	ResetAtUnix     int64   `json:"reset_at_unix"`
	TokensAvailable float64 `json:"tokens_available"`
}

type resetResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Check checks if a request should be allowed.
func (c *HTTPClient) Check(ctx context.Context, identifier string, opts ...CheckOption) (*Decision, error) {
	options := &checkOptions{tokens: 1}
	for _, opt := range opts {
		opt(options)
	}

	reqBody := checkRequest{
		Identifier: identifier,
		Resource:   options.resource,
		Tokens:     options.tokens,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/check", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
		var errResp errorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("server error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var checkResp checkResponse
	if err := json.Unmarshal(respBody, &checkResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &Decision{
		Allowed:    checkResp.Allowed,
		Limit:      checkResp.Limit,
		Remaining:  checkResp.Remaining,
		ResetAt:    time.Unix(checkResp.ResetAtUnix, 0),
		RetryAfter: time.Duration(checkResp.RetryAfterSeconds) * time.Second,
	}, nil
}

// GetStatus returns the current rate limit status.
func (c *HTTPClient) GetStatus(ctx context.Context, identifier string, opts ...StatusOption) (*Status, error) {
	options := &statusOptions{}
	for _, opt := range opts {
		opt(options)
	}

	urlPath := c.baseURL + "/v1/status/" + url.PathEscape(identifier)
	if options.resource != "" {
		urlPath += "?resource=" + url.QueryEscape(options.resource)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlPath, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp errorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("server error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var statusResp statusResponse
	if err := json.Unmarshal(respBody, &statusResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &Status{
		Limit:           statusResp.Limit,
		Remaining:       statusResp.Remaining,
		ResetAt:         time.Unix(statusResp.ResetAtUnix, 0),
		TokensAvailable: statusResp.TokensAvailable,
	}, nil
}

// Reset resets the rate limit for an identifier.
func (c *HTTPClient) Reset(ctx context.Context, identifier string, opts ...ResetOption) error {
	options := &resetOptions{}
	for _, opt := range opts {
		opt(options)
	}

	urlPath := c.baseURL + "/v1/reset/" + url.PathEscape(identifier)
	if options.resource != "" {
		urlPath += "?resource=" + url.QueryEscape(options.resource)
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", urlPath, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp errorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("server error: %s", errResp.Error)
		}
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var resetResp resetResponse
	if err := json.Unmarshal(respBody, &resetResp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !resetResp.Success {
		return fmt.Errorf("reset failed: %s", resetResp.Message)
	}

	return nil
}

// Close closes the client connection.
func (c *HTTPClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Ensure HTTPClient implements Client interface.
var _ Client = (*HTTPClient)(nil)
