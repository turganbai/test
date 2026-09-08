package jira

import (
	"fmt"
	"time"
)

// APIError is a non-2xx response from Jira. The response body is truncated and
// never contains credentials (the token only ever travels in the request).
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Messages   []string
	Body       string
}

func (e *APIError) Error() string {
	msg := e.Body
	if len(e.Messages) > 0 {
		msg = fmt.Sprintf("%v", e.Messages)
	}
	return fmt.Sprintf("jira: %s %s: status %d: %s", e.Method, e.Path, e.StatusCode, msg)
}

// NotFoundError reports a missing (or invisible) issue. Jira answers 404 both
// for "does not exist" and for "you may not see it"; we cannot tell them apart.
type NotFoundError struct {
	Resource string
	ID       string
	Err      error
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("jira: %s %q not found or not visible", e.Resource, e.ID)
}

func (e *NotFoundError) Unwrap() error { return e.Err }

// RateLimitedError reports that retries were exhausted while being throttled.
type RateLimitedError struct {
	RetryAfter time.Duration
	Attempts   int
	Err        error
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("jira: rate limited after %d attempts (last Retry-After %s)", e.Attempts, e.RetryAfter)
}

func (e *RateLimitedError) Unwrap() error { return e.Err }
