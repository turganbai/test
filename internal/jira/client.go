// Package jira is the transport layer: HTTP, auth, retries and the DTOs of the
// Jira Cloud REST API v3. It contains no analytics logic.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxRetries  = 4
	defaultBaseBackoff = 500 * time.Millisecond
	defaultMaxBackoff  = 30 * time.Second
	defaultConcurrency = 5
	maxErrorBodyBytes  = 4 << 10
)

// Client talks to one Jira Cloud site.
type Client struct {
	baseURL *url.URL
	email   string
	token   string

	hc  *http.Client
	log *slog.Logger

	timeout     time.Duration
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	concurrency int

	// sleep is injectable so tests do not actually wait out a backoff.
	sleep func(ctx context.Context, d time.Duration) error
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default http.Client (which carries its own
// timeout in addition to the caller's context deadline).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithTimeout bounds a single attempt, both through the http.Client and
// through a per-attempt context deadline. The two are complementary: the
// client timeout covers a stalled connection, the context deadline also covers
// a body that trickles in forever.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.hc.Timeout = d
		c.timeout = d
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// WithMaxRetries sets how many times a 429/5xx is retried.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithBackoff sets the exponential backoff base and cap.
func WithBackoff(base, max time.Duration) Option {
	return func(c *Client) { c.baseBackoff, c.maxBackoff = base, max }
}

// WithConcurrency bounds the worker pool used for per-issue follow-up calls.
func WithConcurrency(n int) Option { return func(c *Client) { c.concurrency = n } }

// withSleep is used by tests to skip real backoff waits.
func withSleep(f func(ctx context.Context, d time.Duration) error) Option {
	return func(c *Client) { c.sleep = f }
}

// New builds a client for a Jira Cloud site. The API token is used for Basic
// auth and is never logged.
func New(baseURL, email, token string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("base url %q must be absolute, e.g. https://example.atlassian.net", baseURL)
	}
	if email == "" || token == "" {
		return nil, fmt.Errorf("email and api token are required for basic auth")
	}
	c := &Client{
		baseURL:     u,
		email:       email,
		token:       token,
		hc:          &http.Client{Timeout: 30 * time.Second},
		timeout:     30 * time.Second,
		log:         slog.Default(),
		maxRetries:  defaultMaxRetries,
		baseBackoff: defaultBaseBackoff,
		maxBackoff:  defaultMaxBackoff,
		concurrency: defaultConcurrency,
		sleep:       sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	if c.concurrency < 1 {
		c.concurrency = 1
	}
	return c, nil
}

// Concurrency reports the configured worker-pool size.
func (c *Client) Concurrency() int { return c.concurrency }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do performs a request with retries on 429 and 5xx, decoding a JSON response
// into out. notFound, when non-empty, turns a 404 into a *NotFoundError for
// that resource id.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, notFound string) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	u := *c.baseURL
	u.Path = path
	u.RawQuery = query.Encode()

	var lastErr error
	var lastRetryAfter time.Duration
	rateLimited := false

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			wait := c.backoff(attempt, lastRetryAfter)
			c.log.Warn("jira retry",
				"method", method, "path", path,
				"attempt", attempt, "wait", wait, "cause", lastErr)
			if err := c.sleep(ctx, wait); err != nil {
				return fmt.Errorf("%s %s: %w", method, path, err)
			}
		}

		res := c.attempt(ctx, method, path, u.String(), payload, out, notFound, attempt)
		if !res.retry {
			return res.err
		}
		lastErr = res.err
		lastRetryAfter = res.retryAfter
		rateLimited = res.rateLimited
		if ctx.Err() != nil {
			return fmt.Errorf("%s %s: %w", method, path, ctx.Err())
		}
	}

	if rateLimited {
		return &RateLimitedError{RetryAfter: lastRetryAfter, Attempts: c.maxRetries + 1, Err: lastErr}
	}
	return fmt.Errorf("%s %s: giving up after %d attempts: %w", method, path, c.maxRetries+1, lastErr)
}

// attemptResult says whether the caller should retry, and why not if not.
type attemptResult struct {
	err         error
	retry       bool
	rateLimited bool
	retryAfter  time.Duration
}

// attempt performs one request under its own deadline and consumes the body
// before returning, so the per-attempt context can be cancelled safely.
func (c *Client) attempt(ctx context.Context, method, path, rawURL string, payload []byte, out any, notFound string, n int) attemptResult {
	reqCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, reader)
	if err != nil {
		return attemptResult{err: fmt.Errorf("build request %s %s: %w", method, path, err)}
	}
	// Basic auth for Jira Cloud: email + API token. The token never reaches a
	// log line; only the request carries it.
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return attemptResult{err: fmt.Errorf("%s %s: %w", method, path, ctx.Err())}
		}
		// Includes the per-attempt deadline: worth another try.
		return attemptResult{err: fmt.Errorf("%s %s: %w", method, path, err), retry: true}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		return attemptResult{
			err:         apiError(method, path, resp),
			retry:       true,
			rateLimited: resp.StatusCode == http.StatusTooManyRequests,
			retryAfter:  retryAfter(resp.Header.Get("Retry-After")),
		}

	case resp.StatusCode == http.StatusNotFound && notFound != "":
		return attemptResult{err: &NotFoundError{Resource: "issue", ID: notFound, Err: apiError(method, path, resp)}}

	case resp.StatusCode >= 400:
		return attemptResult{err: apiError(method, path, resp)}
	}

	c.log.Debug("jira request", "method", method, "path", path, "status", resp.StatusCode, "attempt", n)
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return attemptResult{}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return attemptResult{err: fmt.Errorf("decode response of %s %s: %w", method, path, err)}
	}
	return attemptResult{}
}

// backoff is exponential with jitter, unless the server told us how long to
// wait, in which case Retry-After wins.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		// Honour the server unconditionally, including waits longer than our
		// own cap: the overall context deadline is what bounds the run.
		return retryAfter
	}
	d := c.baseBackoff << (attempt - 1)
	if d > c.maxBackoff || d <= 0 {
		d = c.maxBackoff
	}
	// Full jitter: a uniform draw over [d/2, d) keeps a stampede of workers
	// from retrying in lockstep.
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// retryAfter understands both forms of the header: delay-seconds and HTTP-date.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func apiError(method, path string, resp *http.Response) *APIError {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	e := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Body: string(b)}
	var payload struct {
		ErrorMessages []string `json:"errorMessages"`
	}
	if json.Unmarshal(b, &payload) == nil {
		e.Messages = payload.ErrorMessages
	}
	return e
}
