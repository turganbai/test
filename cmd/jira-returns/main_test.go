package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
	"time"

	"mcp/internal/jira"
)

func catalog() *jira.StatusCatalog {
	return jira.NewStatusCatalog([]jira.Status{
		{ID: "3", Name: "In Progress"},
		{ID: "10001", Name: "Code Review"},
		{ID: "10007", Name: "Returned"},
		{ID: "10008", Name: "Returned by QA"},
	})
}

func TestResolveStatusIDs(t *testing.T) {
	tests := []struct {
		name    string
		ids     []string
		names   []string
		want    []string
		wantErr string
	}{
		{name: "explicit ids win", ids: []string{"10007"}, names: []string{"Code Review"}, want: []string{"10007"}},
		{name: "names are resolved", names: []string{"returned", "Returned by QA"}, want: []string{"10007", "10008"}},
		{name: "unknown id is fatal", ids: []string{"99999"}, wantErr: "unknown status id"},
		{name: "unknown name is fatal", names: []string{"Rejected"}, wantErr: `no status named "Rejected"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveStatusIDs(catalog(), tc.ids, tc.names)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveStatusIDs: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveStatusIDs_ErrorSuggestsNearMatches(t *testing.T) {
	cat := jira.NewStatusCatalog([]jira.Status{
		{ID: "10007", Name: "Returned to dev"},
		{ID: "10009", Name: "Возвращено"},
		{ID: "3", Name: "In Progress"},
	})
	_, err := resolveStatusIDs(cat, nil, []string{"Returned"})
	if err == nil {
		t.Fatal("resolveStatusIDs succeeded on a name the site does not have")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Returned to dev (id 10007)") {
		t.Errorf("error does not suggest the near match:\n%s", msg)
	}
	if strings.Contains(msg, "In Progress") {
		t.Errorf("error suggests an unrelated status:\n%s", msg)
	}
	if !strings.Contains(msg, "-statuses") {
		t.Errorf("error does not point at -statuses:\n%s", msg)
	}

	// Nothing similar: the message must still say what to do next.
	_, err = resolveStatusIDs(cat, nil, []string{"Rejected"})
	if err == nil || strings.Contains(err.Error(), "Did you mean") {
		t.Errorf("err = %v, want no bogus suggestion", err)
	}
}

func TestPrintStatuses(t *testing.T) {
	var buf bytes.Buffer
	printStatuses(&buf, jira.NewStatusCatalog([]jira.Status{
		{ID: "10007", Name: "Returned"},
		{ID: "3", Name: "In Progress"},
	}))
	out := buf.String()
	if !strings.Contains(out, "10007") || !strings.Contains(out, "Returned") {
		t.Errorf("listing is missing a status:\n%s", out)
	}
	// Sorted by name, so In Progress comes before Returned.
	if strings.Index(out, "In Progress") > strings.Index(out, "Returned") {
		t.Errorf("listing is not sorted by name:\n%s", out)
	}
}

func TestParseDay(t *testing.T) {
	tests := []struct {
		in       string
		endOfDay bool
		want     string
	}{
		{in: "", want: ""},
		{in: "2026-08-01", want: "2026-08-01T00:00:00Z"},
		// A plain -to date means "through that day", so the exclusive bound
		// lands on the next midnight.
		{in: "2026-08-31", endOfDay: true, want: "2026-09-01T00:00:00Z"},
		{in: "2026-08-01T06:30:00+03:00", want: "2026-08-01T03:30:00Z"},
	}
	for _, tc := range tests {
		got, err := parseDay(tc.in, tc.endOfDay)
		if err != nil {
			t.Errorf("parseDay(%q): %v", tc.in, err)
			continue
		}
		var s string
		if !got.IsZero() {
			s = got.Format(time.RFC3339)
		}
		if s != tc.want {
			t.Errorf("parseDay(%q) = %q, want %q", tc.in, s, tc.want)
		}
	}
	if _, err := parseDay("last tuesday", false); err == nil {
		t.Error("parseDay accepted nonsense")
	}
}

func TestRun_RequiresExactlyOneQuerySource(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-jql", "project = X", "-stories", "X-1"},
	} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("run(%v) succeeded, want an error", args)
		}
	}
}

// -h is a request for help, not a failure: it must not reach the error path
// that exits non-zero.
func TestRun_HelpIsNotAnError(t *testing.T) {
	var buf bytes.Buffer
	err := run([]string{"-h"}, &buf)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run(-h) = %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{"-stories", "-jql", "JIRA_BASE_URL"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("usage does not mention %q:\n%s", want, buf.String())
		}
	}
}
