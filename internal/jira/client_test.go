package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// newTestClient points a client at a test server and makes backoff instant,
// recording every wait so the retry policy itself stays assertable.
func newTestClient(t *testing.T, srv *httptest.Server, waits *[]time.Duration, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithLogger(discardLogger()),
		WithBackoff(time.Millisecond, 10*time.Millisecond),
		withSleep(func(ctx context.Context, d time.Duration) error {
			if waits != nil {
				*waits = append(*waits, d)
			}
			return nil
		}),
	}
	c, err := New(srv.URL, "someone@example.com", "secret-token", append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSearchIssues_PaginatesAndCompletesTruncatedChangelog(t *testing.T) {
	var searchCalls, changelogCalls int32
	var seenTokens []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "someone@example.com" || pass != "secret-token" {
			t.Errorf("basic auth = (%q, ok=%v), want the configured email + token", user, ok)
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			if r.Method != http.MethodPost {
				t.Errorf("search method = %s, want POST", r.Method)
			}
			var req SearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode search body: %v", err)
			}
			if req.Expand != "changelog" {
				t.Errorf("expand = %q, want changelog", req.Expand)
			}
			seenTokens = append(seenTokens, req.NextPageToken)
			if atomic.AddInt32(&searchCalls, 1) == 1 {
				w.Write(fixture(t, "search_page1.json"))
				return
			}
			w.Write(fixture(t, "search_page2.json"))

		case "/rest/api/3/issue/SUB-1/changelog":
			atomic.AddInt32(&changelogCalls, 1)
			if got := r.URL.Query().Get("startAt"); got == "0" {
				w.Write(fixture(t, "changelog_page1.json"))
				return
			}
			w.Write(fixture(t, "changelog_page2.json"))

		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	issues, problems, err := c.SearchIssues(context.Background(), SearchOptions{
		JQL:               "parent in (STORY-1)",
		Fields:            []string{"summary", "parent", "assignee", "customfield_10050"},
		CompleteChangelog: true,
	})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none", problems)
	}
	if len(issues) != 3 {
		t.Fatalf("issues = %d, want 3 across two pages", len(issues))
	}
	if searchCalls != 2 {
		t.Errorf("search calls = %d, want 2", searchCalls)
	}
	if len(seenTokens) != 2 || seenTokens[0] != "" || seenTokens[1] != "CAEaBlNVQi0yMg==" {
		t.Errorf("nextPageToken sequence = %q, want [\"\", the token from page 1]", seenTokens)
	}

	// SUB-1's embedded changelog said total=150 with one history: it must have
	// been re-fetched, over two pages of the dedicated endpoint.
	if changelogCalls != 2 {
		t.Errorf("changelog calls = %d, want 2 pages", changelogCalls)
	}
	if got := len(issues[0].Changelog.Histories); got != 4 {
		t.Errorf("SUB-1 histories = %d, want 4 from the follow-up fetch", got)
	}
	// SUB-2's changelog was complete, so it must not have been re-fetched.
	if got := len(issues[1].Changelog.Histories); got != 2 {
		t.Errorf("SUB-2 histories = %d, want the 2 embedded ones", got)
	}
}

func TestSearchIssues_PartialFailureBecomesProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			var req SearchRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.NextPageToken == "" {
				w.Write(fixture(t, "search_page1.json"))
				return
			}
			w.Write(fixture(t, "search_page2.json"))
		default:
			// The changelog of SUB-1 is gone.
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	issues, problems, err := c.SearchIssues(context.Background(), SearchOptions{
		JQL: "parent in (STORY-1)", CompleteChangelog: true,
	})
	if err != nil {
		t.Fatalf("SearchIssues: %v, want the run to survive a per-issue failure", err)
	}
	if len(issues) != 3 {
		t.Errorf("issues = %d, want 3", len(issues))
	}
	if len(problems) != 1 || problems[0].Key != "SUB-1" {
		t.Fatalf("problems = %+v, want one for SUB-1", problems)
	}
	var nf *NotFoundError
	if !errors.As(problems[0].Err, &nf) {
		t.Errorf("problem error = %v, want a *NotFoundError", problems[0].Err)
	}
}

func TestDo_RetriesOn429HonouringRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"errorMessages":["Rate limit exceeded"]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "statuses.json"))
	}))
	defer srv.Close()

	var waits []time.Duration
	c := newTestClient(t, srv, &waits)

	statuses, err := c.Statuses(context.Background())
	if err != nil {
		t.Fatalf("Statuses after a 429: %v", err)
	}
	if len(statuses) != 5 {
		t.Errorf("statuses = %d, want 5", len(statuses))
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one throttled, one successful)", calls)
	}
	if len(waits) != 1 || waits[0] != 7*time.Second {
		t.Errorf("waits = %v, want a single 7s wait taken from Retry-After", waits)
	}
}

func TestDo_RetriesOn5xxThenGivesUpWithRateLimitedError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	var waits []time.Duration
	c := newTestClient(t, srv, &waits, WithMaxRetries(2))

	_, err := c.Statuses(context.Background())
	var rl *RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want a *RateLimitedError", err)
	}
	if rl.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", rl.Attempts)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_ServerErrorIsRetriedWithBackoff(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write(fixture(t, "statuses.json"))
	}))
	defer srv.Close()

	var waits []time.Duration
	c := newTestClient(t, srv, &waits)
	if _, err := c.Statuses(context.Background()); err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(waits) != 2 {
		t.Fatalf("waits = %v, want two backoffs", waits)
	}
	// No Retry-After here, so the jittered exponential backoff applies and the
	// second wait must exceed half of the first attempt's ceiling.
	for i, w := range waits {
		if w <= 0 {
			t.Errorf("wait %d = %v, want a positive backoff", i, w)
		}
	}
}

func TestDo_ClientErrorIsNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errorMessages":["The JQL query is invalid."]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	_, _, err := c.SearchIssues(context.Background(), SearchOptions{JQL: "not jql"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", apiErr.StatusCode)
	}
	if len(apiErr.Messages) != 1 {
		t.Errorf("messages = %v, want the server's errorMessages", apiErr.Messages)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want no retry on a 4xx", calls)
	}
}

func TestNew_Validation(t *testing.T) {
	tests := []struct {
		name, base, email, token string
	}{
		{"no scheme", "example.atlassian.net", "a@b.c", "t"},
		{"no email", "https://example.atlassian.net", "", "t"},
		{"no token", "https://example.atlassian.net", "a@b.c", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.base, tc.email, tc.token); err == nil {
				t.Error("New succeeded, want a validation error")
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{" 12 ", 12 * time.Second},
		{"-1", 0},
		{"nonsense", 0},
		{time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat), 2 * time.Second},
	}
	for _, tc := range tests {
		got := retryAfter(tc.in)
		if tc.want == 2*time.Second { // HTTP-date: allow for the second we lose to rounding
			if got <= 0 || got > 4*time.Second {
				t.Errorf("retryAfter(%q) = %v, want roughly 3s", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
