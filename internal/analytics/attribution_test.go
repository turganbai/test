package analytics

import "testing"

// The developer handed the ticket over mid-flight: the field was Alice for the
// first return and Bob for the second. The two modes must disagree, and each
// must disagree in a specific way.
func TestAttributionModes_DeveloperChangedMidFlight(t *testing.T) {
	iss := Issue{
		Key: "SUB-10", ParentKey: "STORY-1",
		Developer:             devB, // current value
		DeveloperFieldPresent: true,
		Changelog: []Change{
			returned(t, "2026-08-02T09:00:00Z"),
			fieldChange(t, "2026-08-05T10:00:00Z", devField, devA.AccountID, devA.DisplayName, devB.AccountID, devB.DisplayName),
			returned(t, "2026-08-07T09:00:00Z"),
		},
	}

	t.Run("current", func(t *testing.T) {
		rep := Compute([]Issue{iss}, baseOpts(), nil)
		if got := len(rep.Developers); got != 1 {
			t.Fatalf("developers = %d, want 1 (everything on the current value)", got)
		}
		d := rep.Developers[0]
		if d.AccountID != devB.AccountID || d.Returns != 2 {
			t.Errorf("got %s with %d returns, want %s with 2", d.AccountID, d.Returns, devB.AccountID)
		}
	})

	t.Run("at_transition", func(t *testing.T) {
		opts := baseOpts()
		opts.Mode = ModeAtTransition
		rep := Compute([]Issue{iss}, opts, nil)
		if got := len(rep.Developers); got != 2 {
			t.Fatalf("developers = %d, want 2 (one return each)", got)
		}
		for _, id := range []string{devA.AccountID, devB.AccountID} {
			d, ok := developerByID(rep, id)
			if !ok {
				t.Fatalf("developer %s missing", id)
			}
			if d.Returns != 1 || d.Issues != 1 {
				t.Errorf("developer %s: %d returns over %d issues, want 1 over 1", id, d.Returns, d.Issues)
			}
		}
		// The issue-level developer stays the current value; only the events
		// carry point-in-time attribution.
		if rep.Issues[0].Developer.AccountID != devB.AccountID {
			t.Errorf("issue developer = %s, want the current value %s", rep.Issues[0].Developer.AccountID, devB.AccountID)
		}
	})
}

// A field change stamped at exactly the transition time counts as already
// applied, so the new developer owns that return.
func TestAttributionAtTransition_SameTimestamp(t *testing.T) {
	iss := Issue{
		Key: "SUB-11", ParentKey: "STORY-1",
		Developer: devB, DeveloperFieldPresent: true,
		Changelog: []Change{
			fieldChange(t, "2026-08-02T09:00:00Z", devField, devA.AccountID, devA.DisplayName, devB.AccountID, devB.DisplayName),
			returned(t, "2026-08-02T09:00:00Z"),
		},
	}
	opts := baseOpts()
	opts.Mode = ModeAtTransition
	rep := Compute([]Issue{iss}, opts, nil)
	if got := rep.Issues[0].Events[0].AttributedTo.AccountID; got != devB.AccountID {
		t.Errorf("attributed to %s, want %s", got, devB.AccountID)
	}
}

// When the Developer field was empty at the time of the return, the fallback
// assignee is replayed to that same instant rather than read from today.
func TestAttributionAtTransition_AssigneeFallbackIsAlsoReplayed(t *testing.T) {
	iss := Issue{
		Key: "SUB-12", ParentKey: "STORY-1",
		DeveloperFieldPresent: true,
		Assignee:              qa, // today the QA engineer happens to be assigned
		Changelog: []Change{
			returned(t, "2026-08-02T09:00:00Z"),
			// After the return, the ticket moved from Alice to the QA engineer.
			fieldChange(t, "2026-08-04T10:00:00Z", assigneeFieldID, devA.AccountID, devA.DisplayName, qa.AccountID, qa.DisplayName),
		},
	}
	opts := baseOpts()
	opts.Mode = ModeAtTransition
	rep := Compute([]Issue{iss}, opts, nil)

	ev := rep.Issues[0].Events[0]
	if ev.AttributedTo.AccountID != devA.AccountID {
		t.Errorf("attributed to %s, want the assignee at that time (%s)", ev.AttributedTo.AccountID, devA.AccountID)
	}
	if !ev.ViaAssigneeFallback {
		t.Error("ViaAssigneeFallback = false, want true")
	}
}

func TestValueAt(t *testing.T) {
	changes := []Change{
		fieldChange(t, "2026-08-05T10:00:00Z", devField, devA.AccountID, devA.DisplayName, devB.AccountID, devB.DisplayName),
		// Noise on another field must be ignored entirely.
		fieldChange(t, "2026-08-06T10:00:00Z", "customfield_99999", "x", "X", "y", "Y"),
		fieldChange(t, "2026-08-09T10:00:00Z", devField, devB.AccountID, devB.DisplayName, "", ""),
	}

	tests := []struct {
		name    string
		at      string
		current User
		want    string
	}{
		{"before any change", "2026-08-01T00:00:00Z", User{}, devA.AccountID},
		{"between changes", "2026-08-06T00:00:00Z", User{}, devB.AccountID},
		{"after the field was cleared", "2026-08-10T00:00:00Z", User{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := valueAt(changes, devField, tc.current, ts(t, tc.at))
			if got.AccountID != tc.want {
				t.Errorf("valueAt = %q, want %q", got.AccountID, tc.want)
			}
		})
	}
}
