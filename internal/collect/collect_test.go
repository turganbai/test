package collect

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcp/internal/analytics"
	"mcp/internal/jira"
)

const devField = "customfield_10050"

// newServer serves the same fixtures the transport tests use, so this test
// exercises the whole path: search -> changelog top-up -> DTO mapping ->
// aggregation.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	read := func(name string) []byte {
		// Shared with the transport tests rather than copied: one fixture set,
		// one place to update when the API contract moves.
		b, err := os.ReadFile(filepath.Join("..", "jira", "testdata", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return b
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			var req jira.SearchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode search request: %v", err)
			}
			if req.NextPageToken == "" {
				w.Write(read("search_page1.json"))
				return
			}
			w.Write(read("search_page2.json"))
		case "/rest/api/3/issue/SUB-1/changelog":
			if r.URL.Query().Get("startAt") == "0" {
				w.Write(read("changelog_page1.json"))
				return
			}
			w.Write(read("changelog_page2.json"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestCollectorAndCompute(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	client, err := jira.New(srv.URL, "someone@example.com", "secret-token",
		jira.WithLogger(logger), jira.WithConcurrency(2))
	if err != nil {
		t.Fatalf("jira.New: %v", err)
	}

	rep, err := analytics.Run(context.Background(), New(client, devField, logger), "parent in (STORY-1, STORY-2)", analytics.Options{
		DeveloperFieldID:  devField,
		ReturnedStatusIDs: []string{"10007"},
		Mode:              analytics.ModeCurrent,
		StatusNames:       map[string]string{"10007": "Returned", "10001": "Code Review"},
		StoryKeys:         []string{"STORY-1", "STORY-2"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if rep.Totals.SubTickets != 3 {
		t.Errorf("sub-tickets = %d, want 3", rep.Totals.SubTickets)
	}
	// SUB-1: the embedded changelog held one history and claimed total=150, so
	// the full changelog was fetched over two pages and holds three returns.
	sub1 := issueByKey(t, rep, "SUB-1")
	if sub1.Returns != 3 {
		t.Errorf("SUB-1 returns = %d, want 3 from the paginated changelog", sub1.Returns)
	}
	if sub1.Developer.AccountID != "dev-a" {
		t.Errorf("SUB-1 attributed to %q, want dev-a from the Developer field", sub1.Developer.AccountID)
	}
	if sub1.ViaAssigneeFallback {
		t.Error("SUB-1 used the assignee fallback although the Developer field is set")
	}
	// The QA engineer is the changelog author on every return and must never
	// be the one the metric lands on.
	for _, ev := range sub1.Events {
		if ev.ReturnedBy.AccountID != "qa-1" {
			t.Errorf("returnedBy = %q, want qa-1", ev.ReturnedBy.AccountID)
		}
		if ev.AttributedTo.AccountID == "qa-1" {
			t.Error("a return was attributed to the QA engineer")
		}
	}
	if got := sub1.Events[0].At.Format(time.RFC3339); got != "2026-08-02T09:00:00Z" {
		t.Errorf("first return at %s, want 2026-08-02T09:00:00Z", got)
	}

	// SUB-3 has no Developer field on the response at all and falls back to the
	// assignee, and its return is only recognisable by the status id: the
	// fixture's toString is "Возвращено".
	sub3 := issueByKey(t, rep, "SUB-3")
	if sub3.Returns != 1 {
		t.Errorf("SUB-3 returns = %d, want 1 (renamed/localised status, same id)", sub3.Returns)
	}
	if sub3.Developer.AccountID != "dev-b" || !sub3.ViaAssigneeFallback {
		t.Errorf("SUB-3 attributed to %+v (fallback=%v), want dev-b via the assignee fallback",
			sub3.Developer, sub3.ViaAssigneeFallback)
	}
	if got := sub3.Events[0].ToStatusName; got != "Returned" {
		t.Errorf("status label = %q, want the catalog name rather than the changelog string", got)
	}

	// Stories must not bleed into each other.
	stories := map[string]analytics.StoryReport{}
	for _, s := range rep.Stories {
		stories[s.Key] = s
	}
	if got := stories["STORY-1"]; got.Returns != 3 || got.SubTicketCount != 2 {
		t.Errorf("STORY-1 = %d returns over %d sub-tickets, want 3 over 2", got.Returns, got.SubTicketCount)
	}
	if got := stories["STORY-2"]; got.Returns != 1 || got.SubTicketCount != 1 {
		t.Errorf("STORY-2 = %d returns over %d sub-tickets, want 1 over 1", got.Returns, got.SubTicketCount)
	}

	if !hasWarning(rep, analytics.WarnDeveloperFieldMissing) {
		t.Error("missing Developer field on SUB-3 was not surfaced as a warning")
	}
	if !hasWarning(rep, analytics.WarnAssigneeFallback) {
		t.Error("assignee fallback was not surfaced as a warning")
	}
}

// A deleted account keeps its accountId and loses its display name; the
// mapping must survive that rather than dropping the user.
func TestFlatten_DeletedUser(t *testing.T) {
	changes, warns := flatten("SUB-2", []jira.Changelog{{
		ID:      "9102",
		Author:  &jira.User{AccountID: "dev-gone", DisplayName: "", Active: false},
		Created: "2026-08-01T12:00:00.000+0000",
		Items: []jira.ChangeDetails{{
			Field: "assignee", FieldID: "assignee",
			From: "dev-b", FromString: "Bob Dev", To: "dev-gone", ToString: "",
		}},
	}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v, want none", warns)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	if changes[0].Author.AccountID != "dev-gone" {
		t.Errorf("author = %+v, want the deleted account's id", changes[0].Author)
	}
	if got := changes[0].Author.Label(); got != "dev-gone" {
		t.Errorf("label = %q, want the accountId when the display name is gone", got)
	}
}

func TestFlatten_UnparseableTimestampBecomesWarning(t *testing.T) {
	changes, warns := flatten("SUB-9", []jira.Changelog{
		{ID: "1", Created: "not a date", Items: []jira.ChangeDetails{{FieldID: "status"}}},
		{ID: "2", Created: "2026-08-01T12:00:00.000+0000", Items: []jira.ChangeDetails{{FieldID: "status"}}},
	})
	if len(changes) != 1 {
		t.Errorf("changes = %d, want the parseable entry to survive", len(changes))
	}
	if len(warns) != 1 || warns[0].IssueKey != "SUB-9" {
		t.Errorf("warnings = %+v, want one for SUB-9", warns)
	}
}

func issueByKey(t *testing.T, rep analytics.Report, key string) analytics.IssueReport {
	t.Helper()
	for _, i := range rep.Issues {
		if i.Key == key {
			return i
		}
	}
	t.Fatalf("issue %s missing from the report", key)
	return analytics.IssueReport{}
}

func hasWarning(rep analytics.Report, code string) bool {
	for _, w := range rep.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
