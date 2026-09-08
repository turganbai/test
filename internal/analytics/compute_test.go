package analytics

import (
	"testing"
	"time"
)

const (
	statusInProgress = "3"
	statusCodeReview = "10001"
	statusStage      = "10002"
	statusReturned   = "10007"
	statusReturnedQA = "10008" // a second "returned"-like status
	devField         = "customfield_10050"
)

var (
	qa    = User{AccountID: "qa-1", DisplayName: "Qa Engineer", Active: true}
	devA  = User{AccountID: "dev-a", DisplayName: "Alice Dev", Active: true}
	devB  = User{AccountID: "dev-b", DisplayName: "Bob Dev", Active: true}
	ghost = User{AccountID: "dev-gone", DisplayName: "", Active: false} // deleted account
)

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad timestamp %q in test: %v", s, err)
	}
	return v.UTC()
}

// statusChange builds a status transition. toName is deliberately free to
// differ from the catalog name, which is the renamed-status case.
func statusChange(t *testing.T, at, from, to, fromName, toName string, author User) Change {
	return Change{
		At: ts(t, at), Author: author,
		Field: "Статус", FieldID: StatusFieldID, // localized name on purpose
		From: from, FromString: fromName, To: to, ToString: toName,
	}
}

func fieldChange(t *testing.T, at, fieldID string, from, fromName, to, toName string) Change {
	return Change{
		At: ts(t, at), Author: qa,
		Field: "Developer", FieldID: fieldID,
		From: from, FromString: fromName, To: to, ToString: toName,
	}
}

// returned is the shorthand used by most cases: QA sends the ticket back.
func returned(t *testing.T, at string) Change {
	return statusChange(t, at, statusCodeReview, statusReturned, "Code Review", "Returned", qa)
}

func baseOpts() Options {
	return Options{
		DeveloperFieldID:  devField,
		ReturnedStatusIDs: []string{statusReturned, statusReturnedQA},
		Mode:              ModeCurrent,
		StatusNames: map[string]string{
			statusReturned:   "Returned",
			statusCodeReview: "Code Review",
		},
	}
}

type want struct {
	totalReturns int
	issueReturns map[string]int
	devReturns   map[string]int
	devIssues    map[string]int
	storyReturns map[string]int
	warningCodes []string
	extra        func(t *testing.T, rep Report)
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name   string
		issues []Issue
		opts   func(o Options) Options
		want   want
	}{
		{
			name: "three returned cycles on one sub-ticket",
			issues: []Issue{{
				Key: "SUB-1", ParentKey: "STORY-1", ParentSummary: "Checkout",
				Developer: devA, DeveloperFieldPresent: true,
				Changelog: []Change{
					statusChange(t, "2026-08-01T10:00:00Z", "", statusInProgress, "", "In Progress", devA),
					statusChange(t, "2026-08-01T12:00:00Z", statusInProgress, statusCodeReview, "In Progress", "Code Review", devA),
					returned(t, "2026-08-02T09:00:00Z"),
					statusChange(t, "2026-08-02T11:00:00Z", statusReturned, statusInProgress, "Returned", "In Progress", devA),
					statusChange(t, "2026-08-02T15:00:00Z", statusInProgress, statusCodeReview, "In Progress", "Code Review", devA),
					returned(t, "2026-08-03T09:00:00Z"),
					statusChange(t, "2026-08-03T16:00:00Z", statusReturned, statusCodeReview, "Returned", "Code Review", devA),
					returned(t, "2026-08-04T09:00:00Z"),
					statusChange(t, "2026-08-05T10:00:00Z", statusReturned, statusStage, "Returned", "Stage", devA),
				},
			}},
			want: want{
				totalReturns: 3,
				issueReturns: map[string]int{"SUB-1": 3},
				devReturns:   map[string]int{"dev-a": 3},
				devIssues:    map[string]int{"dev-a": 1},
				storyReturns: map[string]int{"STORY-1": 3},
				extra: func(t *testing.T, rep Report) {
					d := rep.Developers[0]
					if d.Distribution.ThreePlus != 1 || d.Distribution.Zero != 0 {
						t.Errorf("distribution = %+v, want one issue in the 3+ bucket", d.Distribution)
					}
					if d.AvgReturnsPerIssue != 3 {
						t.Errorf("avg = %v, want 3", d.AvgReturnsPerIssue)
					}
					if rep.Stories[0].MaxReturnsIssueKey != "SUB-1" || rep.Stories[0].MaxReturns != 3 {
						t.Errorf("story max = %d on %q", rep.Stories[0].MaxReturns, rep.Stories[0].MaxReturnsIssueKey)
					}
					for _, ev := range rep.Issues[0].Events {
						if ev.ReturnedBy.AccountID != "qa-1" {
							t.Errorf("returnedBy = %q, want the QA engineer", ev.ReturnedBy.AccountID)
						}
						if ev.AttributedTo.AccountID != "dev-a" {
							t.Errorf("attributedTo = %q, want the developer, never the QA author", ev.AttributedTo.AccountID)
						}
					}
				},
			},
		},
		{
			name: "zero returns still counts the issue and the developer",
			issues: []Issue{{
				Key: "SUB-2", ParentKey: "STORY-1",
				Developer: devA, DeveloperFieldPresent: true,
				Changelog: []Change{
					statusChange(t, "2026-08-01T10:00:00Z", "", statusInProgress, "", "In Progress", devA),
					statusChange(t, "2026-08-01T12:00:00Z", statusInProgress, statusStage, "In Progress", "Stage", devA),
				},
			}},
			want: want{
				totalReturns: 0,
				issueReturns: map[string]int{"SUB-2": 0},
				devReturns:   map[string]int{"dev-a": 0},
				devIssues:    map[string]int{"dev-a": 1},
				storyReturns: map[string]int{"STORY-1": 0},
				extra: func(t *testing.T, rep Report) {
					if got := rep.Developers[0].Distribution.Zero; got != 1 {
						t.Errorf("zero bucket = %d, want 1", got)
					}
					if rep.Totals.ReworkRate != 0 {
						t.Errorf("rework rate = %v, want 0", rep.Totals.ReworkRate)
					}
				},
			},
		},
		{
			name: "empty developer field falls back to assignee and flags it",
			issues: []Issue{{
				Key: "SUB-3", ParentKey: "STORY-1",
				DeveloperFieldPresent: true, // present but empty
				Assignee:              devB,
				Changelog:             []Change{returned(t, "2026-08-02T09:00:00Z")},
			}},
			want: want{
				totalReturns: 1,
				devReturns:   map[string]int{"dev-b": 1},
				warningCodes: []string{WarnAssigneeFallback},
				extra: func(t *testing.T, rep Report) {
					ir := rep.Issues[0]
					if !ir.ViaAssigneeFallback {
						t.Error("issue ViaAssigneeFallback = false, want true")
					}
					if !ir.Events[0].ViaAssigneeFallback {
						t.Error("event ViaAssigneeFallback = false, want true")
					}
					if ir.Events[0].Unattributed {
						t.Error("event marked unattributed although an assignee exists")
					}
				},
			},
		},
		{
			name: "no developer and no assignee is surfaced, not dropped",
			issues: []Issue{{
				Key: "SUB-4", ParentKey: "STORY-1",
				Changelog: []Change{returned(t, "2026-08-02T09:00:00Z")},
			}},
			want: want{
				totalReturns: 1,
				devReturns:   map[string]int{UnattributedAccountID: 1},
				warningCodes: []string{WarnDeveloperFieldMissing, WarnNoAttribution},
			},
		},
		{
			name: "deleted user keeps its accountId as the key",
			issues: []Issue{{
				Key: "SUB-5", ParentKey: "STORY-1",
				Developer: ghost, DeveloperFieldPresent: true,
				Changelog: []Change{returned(t, "2026-08-02T09:00:00Z")},
			}},
			want: want{
				totalReturns: 1,
				devReturns:   map[string]int{"dev-gone": 1},
				extra: func(t *testing.T, rep Report) {
					if got := rep.Developers[0].DisplayName; got != "dev-gone" {
						t.Errorf("display name = %q, want the accountId as a graceful fallback", got)
					}
				},
			},
		},
		{
			name: "renamed status: different toString, same status id",
			issues: []Issue{{
				Key: "SUB-6", ParentKey: "STORY-1",
				Developer: devA, DeveloperFieldPresent: true,
				Changelog: []Change{
					// The workflow status was renamed and localized between the
					// two transitions; only the id stayed the same.
					statusChange(t, "2026-08-02T09:00:00Z", statusCodeReview, statusReturned, "Code Review", "Returned", qa),
					statusChange(t, "2026-08-03T09:00:00Z", statusCodeReview, statusReturned, "Ревью", "Возвращено", qa),
					// Same *name* as a returned status but a different id: not a return.
					statusChange(t, "2026-08-04T09:00:00Z", statusCodeReview, statusStage, "Code Review", "Returned", qa),
				},
			}},
			want: want{
				totalReturns: 2,
				issueReturns: map[string]int{"SUB-6": 2},
				devReturns:   map[string]int{"dev-a": 2},
				extra: func(t *testing.T, rep Report) {
					for _, ev := range rep.Issues[0].Events {
						if ev.ToStatusName != "Returned" {
							t.Errorf("toStatusName = %q, want the catalog name for id %s", ev.ToStatusName, statusReturned)
						}
					}
				},
			},
		},
		{
			name: "a second returned-like status id also counts",
			issues: []Issue{{
				Key: "SUB-7", ParentKey: "STORY-1",
				Developer: devA, DeveloperFieldPresent: true,
				Changelog: []Change{
					returned(t, "2026-08-02T09:00:00Z"),
					statusChange(t, "2026-08-03T09:00:00Z", statusStage, statusReturnedQA, "Stage", "Returned by QA", qa),
				},
			}},
			want: want{totalReturns: 2, devReturns: map[string]int{"dev-a": 2}},
		},
		{
			name: "period bounds are applied to the transition timestamp",
			issues: []Issue{{
				Key: "SUB-8", ParentKey: "STORY-1",
				Developer: devA, DeveloperFieldPresent: true,
				Changelog: []Change{
					returned(t, "2026-07-31T23:59:59Z"), // before
					returned(t, "2026-08-01T00:00:00Z"), // inclusive lower bound
					returned(t, "2026-08-31T23:00:00Z"),
					returned(t, "2026-09-01T00:00:00Z"), // exclusive upper bound
				},
			}},
			opts: func(o Options) Options {
				o.From = ts(t, "2026-08-01T00:00:00Z")
				o.To = ts(t, "2026-09-01T00:00:00Z")
				return o
			},
			want: want{totalReturns: 2, issueReturns: map[string]int{"SUB-8": 2}},
		},
		{
			name: "stories do not bleed into each other and an empty story survives",
			issues: []Issue{
				{Key: "A-1", ParentKey: "STORY-A", ParentSummary: "Story A", Developer: devA, DeveloperFieldPresent: true,
					Changelog: []Change{returned(t, "2026-08-02T09:00:00Z"), returned(t, "2026-08-03T09:00:00Z")}},
				{Key: "B-1", ParentKey: "STORY-B", ParentSummary: "Story B", Developer: devB, DeveloperFieldPresent: true,
					Changelog: []Change{returned(t, "2026-08-02T09:00:00Z")}},
				{Key: "B-2", ParentKey: "STORY-B", ParentSummary: "Story B", Developer: devB, DeveloperFieldPresent: true},
				// A story returned by the JQL that has no sub-tickets at all.
				{Key: "STORY-C", Summary: "Story C"},
			},
			opts: func(o Options) Options {
				o.StoryKeys = []string{"STORY-A", "STORY-B", "STORY-C", "STORY-D"}
				return o
			},
			want: want{
				totalReturns: 3,
				storyReturns: map[string]int{"STORY-A": 2, "STORY-B": 1, "STORY-C": 0, "STORY-D": 0},
				devReturns:   map[string]int{"dev-a": 2, "dev-b": 1},
				devIssues:    map[string]int{"dev-a": 1, "dev-b": 2},
				extra: func(t *testing.T, rep Report) {
					byKey := map[string]StoryReport{}
					for _, s := range rep.Stories {
						byKey[s.Key] = s
					}
					if got := byKey["STORY-B"].ReworkRate; got != 0.5 {
						t.Errorf("STORY-B rework rate = %v, want 0.5", got)
					}
					if got := byKey["STORY-C"].SubTicketCount; got != 0 {
						t.Errorf("STORY-C sub-tickets = %d, want 0", got)
					}
					if got := byKey["STORY-D"].Key; got != "STORY-D" {
						t.Error("a story with no sub-tickets vanished from the report")
					}
					if got := byKey["STORY-A"].SubTicketKeys; len(got) != 1 || got[0] != "A-1" {
						t.Errorf("STORY-A sub-tickets = %v, want [A-1]", got)
					}
				},
			},
		},
		{
			name: "rework turnaround is measured to the next code-review",
			issues: []Issue{{
				Key: "SUB-9", ParentKey: "STORY-1",
				Developer: devA, DeveloperFieldPresent: true,
				Changelog: []Change{
					returned(t, "2026-08-02T09:00:00Z"),
					statusChange(t, "2026-08-02T11:00:00Z", statusReturned, statusInProgress, "Returned", "In Progress", devA),
					statusChange(t, "2026-08-02T13:00:00Z", statusInProgress, statusCodeReview, "In Progress", "Code Review", devA),
					returned(t, "2026-08-03T09:00:00Z"), // never comes back
				},
			}},
			opts: func(o Options) Options {
				o.CodeReviewStatusIDs = []string{statusCodeReview}
				return o
			},
			want: want{
				totalReturns: 2,
				extra: func(t *testing.T, rep Report) {
					evs := rep.Issues[0].Events
					if evs[0].ReworkSeconds == nil || *evs[0].ReworkSeconds != 4*3600 {
						t.Errorf("first rework = %v, want 14400s", evs[0].ReworkSeconds)
					}
					if evs[1].ReworkSeconds != nil {
						t.Errorf("second rework = %v, want nil (never returned to review)", *evs[1].ReworkSeconds)
					}
					if got := rep.Developers[0].AvgReworkSeconds; got == nil || *got != 14400 {
						t.Errorf("avg rework = %v, want 14400", got)
					}
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := baseOpts()
			if tc.opts != nil {
				opts = tc.opts(opts)
			}
			rep := Compute(tc.issues, opts, nil)
			checkWant(t, rep, tc.want)
		})
	}
}

func checkWant(t *testing.T, rep Report, w want) {
	t.Helper()
	if rep.Totals.Returns != w.totalReturns {
		t.Errorf("total returns = %d, want %d", rep.Totals.Returns, w.totalReturns)
	}
	for key, n := range w.issueReturns {
		found := false
		for _, ir := range rep.Issues {
			if ir.Key == key {
				found = true
				if ir.Returns != n {
					t.Errorf("issue %s returns = %d, want %d", key, ir.Returns, n)
				}
			}
		}
		if !found {
			t.Errorf("issue %s missing from report", key)
		}
	}
	for id, n := range w.devReturns {
		dr, ok := developerByID(rep, id)
		if !ok {
			t.Errorf("developer %s missing from report", id)
			continue
		}
		if dr.Returns != n {
			t.Errorf("developer %s returns = %d, want %d", id, dr.Returns, n)
		}
	}
	for id, n := range w.devIssues {
		dr, ok := developerByID(rep, id)
		if !ok {
			t.Errorf("developer %s missing from report", id)
			continue
		}
		if dr.Issues != n {
			t.Errorf("developer %s issues = %d, want %d", id, dr.Issues, n)
		}
	}
	for key, n := range w.storyReturns {
		found := false
		for _, s := range rep.Stories {
			if s.Key == key {
				found = true
				if s.Returns != n {
					t.Errorf("story %s returns = %d, want %d", key, s.Returns, n)
				}
			}
		}
		if !found {
			t.Errorf("story %s missing from report", key)
		}
	}
	for _, code := range w.warningCodes {
		if !hasWarning(rep, code) {
			t.Errorf("warning %q missing, got %+v", code, rep.Warnings)
		}
	}
	if w.extra != nil {
		w.extra(t, rep)
	}
}

func developerByID(rep Report, id string) (DeveloperReport, bool) {
	for _, d := range rep.Developers {
		if d.AccountID == id {
			return d, true
		}
	}
	return DeveloperReport{}, false
}

func hasWarning(rep Report, code string) bool {
	for _, w := range rep.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
