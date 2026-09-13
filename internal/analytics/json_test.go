package analytics

import (
	"encoding/json"
	"strings"
	"testing"
)

// nullPaths walks decoded JSON and returns the dotted path of every null.
func nullPaths(v any, path string, out *[]string) {
	switch t := v.(type) {
	case nil:
		*out = append(*out, path)
	case map[string]any:
		for k, vv := range t {
			p := k
			if path != "" {
				p = path + "." + k
			}
			nullPaths(vv, p, out)
		}
	case []any:
		for i, vv := range t {
			nullPaths(vv, path+"[]", out)
			_ = i
		}
	}
}

func marshalNulls(t *testing.T, rep Report) []string {
	t.Helper()
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var nulls []string
	nullPaths(doc, "", &nulls)
	return nulls
}

// A report over sub-tickets that were never returned, with no warnings, is the
// quiet case — and the one that used to serialise issues[].events and warnings
// as null, breaking any consumer that iterates them.
func TestReportMarshalsNoNulls_QuietReport(t *testing.T) {
	rep := Compute([]Issue{
		{Key: "KAN-11", ParentKey: "KAN-10", Assignee: User{AccountID: "dev-a", DisplayName: "azamat"}},
		{Key: "KAN-12", ParentKey: "KAN-10", Assignee: User{AccountID: "dev-a", DisplayName: "azamat"}},
	}, Options{ReturnedStatusIDs: []string{"10008"}}, nil)

	if rep.Totals.Returns != 0 {
		t.Fatalf("returns = %d, want a quiet report", rep.Totals.Returns)
	}
	if nulls := marshalNulls(t, rep); len(nulls) > 0 {
		t.Errorf("null at: %s", strings.Join(nulls, ", "))
	}
}

// The emptiest report of all: no issues matched at all.
func TestReportMarshalsNoNulls_EmptyReport(t *testing.T) {
	rep := Compute(nil, Options{}, nil)
	if nulls := marshalNulls(t, rep); len(nulls) > 0 {
		t.Errorf("null at: %s", strings.Join(nulls, ", "))
	}
}

// A story seeded by -stories that turned out to have no sub-tickets at all
// still reaches the report, and its subTicketKeys must be [] rather than null.
// That story never passes through the sub-ticket branch, so it is the case the
// construction-time initialiser in ensure exists for.
func TestReportMarshalsNoNulls_StoryWithNoSubTickets(t *testing.T) {
	rep := Compute(nil, Options{StoryKeys: []string{"KAN-10", "KAN-99"}}, nil)

	if len(rep.Stories) != 2 {
		t.Fatalf("stories = %d, want both seeded keys", len(rep.Stories))
	}
	for _, st := range rep.Stories {
		if st.SubTicketKeys == nil {
			t.Errorf("story %s: SubTicketKeys is nil", st.Key)
		}
	}
	if nulls := marshalNulls(t, rep); len(nulls) > 0 {
		t.Errorf("null at: %s", strings.Join(nulls, ", "))
	}
}

// And the populated case, so the guards are not hiding a regression.
func TestReportMarshalsNoNulls_WithReturnsAndWarnings(t *testing.T) {
	iss := Issue{
		Key: "KAN-3", ParentKey: "KAN-10",
		Assignee: User{AccountID: "dev-a", DisplayName: "azamat"},
		Changelog: []Change{
			{At: ts(t, "2026-09-10T12:18:00Z"), FieldID: StatusFieldID, From: "10001", To: "10008"},
		},
	}
	rep := Compute([]Issue{iss}, Options{ReturnedStatusIDs: []string{"10008"}},
		[]Warning{{IssueKey: "KAN-3", Code: WarnChangelogTruncated, Message: "truncated"}})

	if rep.Totals.Returns != 1 || len(rep.Warnings) == 0 {
		t.Fatalf("totals = %+v, warnings = %d", rep.Totals, len(rep.Warnings))
	}
	if nulls := marshalNulls(t, rep); len(nulls) > 0 {
		t.Errorf("null at: %s", strings.Join(nulls, ", "))
	}
}
