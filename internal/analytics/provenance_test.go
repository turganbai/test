package analytics

import (
	"strings"
	"testing"
	"time"
)

// The KAN-10 shape: a return happens, and the Developer field is populated
// only afterwards — its first changelog entry has a null "from". Replayed to
// the moment of the return the field is empty, so the return is attributed to
// the assignee.
//
// The field is populated today, so the cur-based warning stays silent and the
// DEVELOPER (CURRENT) column shows a real developer. Nothing said that the
// number had come from somewhere else.
func fieldPopulatedAfterTheReturn(t *testing.T) Issue {
	t.Helper()
	returned := ts(t, "2026-09-10T12:18:00Z")
	return Issue{
		Key: "KAN-13", ParentKey: "KAN-10",
		Developer:             User{AccountID: "dev-x", DisplayName: "Dev X"},
		DeveloperFieldPresent: true,
		Assignee:              User{AccountID: "azamat", DisplayName: "azamat"},
		Changelog: []Change{
			{At: returned, FieldID: StatusFieldID, From: "10001", To: statusReturn},
			// Jira writes null for the side that did not exist yet.
			{At: returned.Add(30 * time.Minute), Field: "Developer", FieldID: devFieldID,
				From: "", FromString: "", To: "[dev-x]", ToString: "[Dev X]"},
		},
	}
}

func warningsWithCode(rep Report, code string) []Warning {
	var out []Warning
	for _, w := range rep.Warnings {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

func TestCompute_AssigneeFallbackIsReportedPerEvent(t *testing.T) {
	iss := fieldPopulatedAfterTheReturn(t)
	rep := Compute([]Issue{iss}, Options{
		Mode:              ModeAtTransition,
		DeveloperFieldID:  devFieldID,
		ReturnedStatusIDs: []string{statusReturn},
	}, nil)

	// The condition that hid this: the field reads fine today.
	if rep.Issues[0].ViaAssigneeFallback {
		t.Fatal("current developer is set; the cur-based marker should be silent")
	}
	ev := rep.Issues[0].Events[0]
	if !ev.ViaAssigneeFallback || ev.AttributedTo.AccountID != "azamat" {
		t.Fatalf("event = %+v, want the return attributed to the assignee", ev)
	}

	warns := warningsWithCode(rep, WarnAssigneeFallback)
	if len(warns) != 1 {
		t.Fatalf("assignee_fallback warnings = %d, want 1", len(warns))
	}
	for _, want := range []string{"azamat", "2026-09-10T12:18:00Z", devFieldID} {
		if !strings.Contains(warns[0].Message, want) {
			t.Errorf("warning does not name %q: %q", want, warns[0].Message)
		}
	}
	if warns[0].IssueKey != "KAN-13" {
		t.Errorf("warning issue key = %q, want KAN-13", warns[0].IssueKey)
	}
}

// One warning per return, not one per issue.
func TestCompute_AssigneeFallbackWarnsForEveryReturn(t *testing.T) {
	iss := fieldPopulatedAfterTheReturn(t)
	extra := ts(t, "2026-09-10T12:30:00Z")
	iss.Changelog = append(iss.Changelog,
		Change{At: extra, FieldID: StatusFieldID, From: "10001", To: statusReturn})

	rep := Compute([]Issue{iss}, Options{
		Mode:              ModeAtTransition,
		DeveloperFieldID:  devFieldID,
		ReturnedStatusIDs: []string{statusReturn},
	}, nil)

	if n := len(warningsWithCode(rep, WarnAssigneeFallback)); n != 2 {
		t.Errorf("warnings = %d, want one per fallback-attributed return", n)
	}
}

// ModeCurrent resolves every event from the same state as cur, so the
// per-event warning would only repeat the cur-based one. It must not fire.
func TestCompute_ModeCurrentWarningCountUnchanged(t *testing.T) {
	t.Run("field populated: no fallback at all", func(t *testing.T) {
		rep := Compute([]Issue{fieldPopulatedAfterTheReturn(t)}, Options{
			Mode:              ModeCurrent,
			DeveloperFieldID:  devFieldID,
			ReturnedStatusIDs: []string{statusReturn},
		}, nil)
		if n := len(warningsWithCode(rep, WarnAssigneeFallback)); n != 0 {
			t.Errorf("warnings = %d, want none — the field is set", n)
		}
	})

	t.Run("field empty: exactly the one cur-based warning", func(t *testing.T) {
		iss := fieldPopulatedAfterTheReturn(t)
		iss.Developer = User{}
		// Two returns: a per-event warning would make this 2, or 3 with the
		// cur-based one. It must stay at 1.
		iss.Changelog = append(iss.Changelog, Change{
			At: ts(t, "2026-09-10T12:30:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn})

		rep := Compute([]Issue{iss}, Options{
			Mode:              ModeCurrent,
			DeveloperFieldID:  devFieldID,
			ReturnedStatusIDs: []string{statusReturn},
		}, nil)
		if n := len(warningsWithCode(rep, WarnAssigneeFallback)); n != 1 {
			t.Errorf("warnings = %d, want the single cur-based one", n)
		}
	})
}

// In at_transition with the field empty today too, both warnings fire: one
// says the field is empty, the other says which assignee each return landed
// on. Assignees change over a ticket's life, so that is not a duplicate.
func TestCompute_AtTransitionKeepsBothWarningsWhenFieldIsEmptyToday(t *testing.T) {
	iss := fieldPopulatedAfterTheReturn(t)
	iss.Developer = User{}

	rep := Compute([]Issue{iss}, Options{
		Mode:              ModeAtTransition,
		DeveloperFieldID:  devFieldID,
		ReturnedStatusIDs: []string{statusReturn},
	}, nil)

	warns := warningsWithCode(rep, WarnAssigneeFallback)
	if len(warns) != 2 {
		t.Fatalf("warnings = %d, want the cur-based one and one per return", len(warns))
	}
	var timestamped int
	for _, w := range warns {
		if strings.Contains(w.Message, "2026-09-10T12:18:00Z") {
			timestamped++
		}
	}
	if timestamped != 1 {
		t.Errorf("warnings = %+v, want exactly one naming the transition instant", warns)
	}
}
