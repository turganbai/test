package analytics

import "testing"

// shuffled returns changes in an order no consumer may rely on. Issue.Changelog
// is contractually ascending; these tests are what makes the contract testable
// from outside, and what would catch the loader silently ceasing to honour it.
func shuffled(changes []Change) []Change {
	out := append([]Change(nil), changes...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

const (
	devFieldID   = "customfield_10043"
	statusReturn = "10008"
	statusReview = "10011"
)

// kan3 is the shape the replay actually has to unwind: a Developer handover
// sitting between two returns, so the developer differs either side of it.
func kan3(t *testing.T) Issue {
	t.Helper()
	return Issue{
		Key: "KAN-3", ParentKey: "KAN-10",
		Developer: User{AccountID: "azamat"},
		Changelog: []Change{
			{At: ts(t, "2026-09-01T09:00:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			{At: ts(t, "2026-09-02T09:00:00Z"), FieldID: StatusFieldID, From: statusReturn, To: statusReview},
			{At: ts(t, "2026-09-03T09:00:00Z"), Field: "Developer", FieldID: devFieldID,
				From: "[turganbai]", FromString: "[Турганбай С]", To: "[azamat]", ToString: "[azamat]"},
			{At: ts(t, "2026-09-04T09:00:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			{At: ts(t, "2026-09-06T09:00:00Z"), FieldID: StatusFieldID, From: statusReturn, To: statusReview},
		},
	}
}

func opts() Options {
	return Options{
		Mode:                ModeAtTransition,
		DeveloperFieldID:    devFieldID,
		ReturnedStatusIDs:   []string{statusReturn},
		CodeReviewStatusIDs: []string{statusReview},
	}
}

// An out-of-order changelog makes valueAt's backwards walk break early and
// return a partially unwound value: a real person, just not the right one. That
// is the headline number of the report, so it must not depend on input order.
func TestCompute_AttributionIsIndependentOfChangelogOrder(t *testing.T) {
	sorted := Compute([]Issue{kan3(t)}, opts(), nil)

	iss := kan3(t)
	iss.Changelog = shuffled(iss.Changelog)
	mixed := Compute([]Issue{iss}, opts(), nil)

	if len(sorted.Developers) != len(mixed.Developers) {
		t.Fatalf("developers: %d sorted vs %d shuffled", len(sorted.Developers), len(mixed.Developers))
	}
	for i := range sorted.Developers {
		a, b := sorted.Developers[i], mixed.Developers[i]
		if a.AccountID != b.AccountID || a.Returns != b.Returns || a.Issues != b.Issues {
			t.Errorf("developer %d: sorted %+v, shuffled %+v", i, a, b)
		}
	}
	// The whole point: the two returns belong to different people.
	if len(sorted.Developers) != 2 {
		t.Fatalf("developers = %d, want the handover to split the two returns", len(sorted.Developers))
	}
}

// Compute is pure: normalizing must not reorder a slice the caller still holds.
func TestCompute_DoesNotMutateCallerChangelog(t *testing.T) {
	iss := kan3(t)
	iss.Changelog = shuffled(iss.Changelog)
	before := append([]Change(nil), iss.Changelog...)

	Compute([]Issue{iss}, opts(), nil)

	for i := range before {
		if !iss.Changelog[i].At.Equal(before[i].At) {
			t.Fatalf("Compute reordered the caller's changelog at %d", i)
		}
	}
}

// Old changelog rows carry no fieldId, so the field name is all there is to
// match on. The status scan always accepted that fallback; the field replay
// did not, and silently skipped the same rows.
func TestIsFieldChange_NameFallback(t *testing.T) {
	tests := []struct {
		name    string
		ch      Change
		fieldID string
		want    bool
	}{
		{"id wins", Change{FieldID: StatusFieldID, Field: "Статус"}, StatusFieldID, true},
		{"id mismatches despite the name", Change{FieldID: "customfield_1", Field: "status"}, StatusFieldID, false},
		{"no id, name matches a system field", Change{Field: "status"}, StatusFieldID, true},
		{"no id, assignee by name", Change{Field: "assignee"}, assigneeFieldID, true},
		// The Developer field is "customfield_10043" by id and "Developer" by
		// name: the two never coincide, so the fallback must not fire.
		{"no id, custom field never matches by name", Change{Field: "Developer"}, "customfield_10043", false},
		{"no id, wrong name", Change{Field: "priority"}, StatusFieldID, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFieldChange(tt.ch, tt.fieldID); got != tt.want {
				t.Errorf("isFieldChange(%+v, %q) = %v, want %v", tt.ch, tt.fieldID, got, tt.want)
			}
		})
	}
}

// The replay now honours the same fallback the status scan always did.
func TestValueAt_ReplaysAssigneeRowsWithoutAFieldID(t *testing.T) {
	iss := Issue{
		Key: "KAN-3", ParentKey: "KAN-10",
		Assignee: User{AccountID: "dev-b", DisplayName: "Bob Dev"},
		Changelog: []Change{
			{At: ts(t, "2026-09-01T09:00:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			// No fieldId, as Jira wrote it before the field existed.
			{At: ts(t, "2026-09-02T09:00:00Z"), Field: "assignee",
				From: "dev-a", FromString: "Alice Dev", To: "dev-b", ToString: "Bob Dev"},
		},
	}
	rep := Compute([]Issue{iss}, Options{
		Mode:              ModeAtTransition,
		ReturnedStatusIDs: []string{statusReturn},
	}, nil)

	if len(rep.Issues) != 1 || len(rep.Issues[0].Events) != 1 {
		t.Fatalf("issues = %+v, want one return", rep.Issues)
	}
	if got := rep.Issues[0].Events[0].AttributedTo.AccountID; got != "dev-a" {
		t.Errorf("attributed to %q, want dev-a — the handover was after the return", got)
	}
}
