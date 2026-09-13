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
