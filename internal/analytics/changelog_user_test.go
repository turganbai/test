package analytics

import "testing"

func TestParseUserValue(t *testing.T) {
	tests := []struct {
		name            string
		id, displayName string
		want            User
	}{
		{"single user", "[dev-a]", "[azamat]", User{AccountID: "dev-a", DisplayName: "azamat"}},
		{"several users", "[dev-a, dev-b]", "[azamat, Bob Dev]", User{AccountID: "dev-a", DisplayName: "azamat"}},
		{"cleared field", "[]", "[]", User{}},
		// A single-user picker writes the bare value; nothing to unwrap.
		{"not a list", "dev-a", "azamat", User{AccountID: "dev-a", DisplayName: "azamat"}},
		// One developer whose name contains a comma must survive: the id list
		// is what says how many people there are.
		{"comma in a name", "[dev-a]", "[Doe, John]", User{AccountID: "dev-a", DisplayName: "Doe, John"}},
		{"empty", "", "", User{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseUserValue(tt.id, tt.displayName)
			if got != tt.want {
				t.Errorf("ParseUserValue(%q, %q) = %+v, want %+v", tt.id, tt.displayName, got, tt.want)
			}
		})
	}
}

// The DTO mapping normalizes changelog values on the way in and valueAt parses
// them again on the way out. Both go through ParseUserValue precisely so that
// the second pass is a no-op rather than a second chance to get it wrong.
func TestParseUserValue_Idempotent(t *testing.T) {
	for _, raw := range [][2]string{
		{"[557058:fe14ad75-de24-4423-8a64-b05945bbbbdb]", "[Турганбай С]"},
		{"[dev-a, dev-b]", "[azamat, Bob Dev]"},
		{"6188ce54cc2d7c007153a233", "azamat"},
	} {
		once := ParseUserValue(raw[0], raw[1])
		twice := ParseUserValue(once.AccountID, once.DisplayName)
		if once != twice {
			t.Errorf("ParseUserValue not idempotent on %q/%q: %+v then %+v", raw[0], raw[1], once, twice)
		}
	}
}

// The real KAN-3 payload: a multi-user Developer field, whose changelog values
// Jira wraps in brackets while the field's own value comes through clean.
//
// Replaying the raw string would key the same person as "[557058:fe14…]" under
// ModeAtTransition and as "557058:fe14…" everywhere else, splitting their
// issues and returns across two DeveloperReport rows that a reader has no way
// to recognise as one person.
func TestCompute_BracketedDeveloperValueIsTheSamePersonAsTheField(t *testing.T) {
	const (
		devField = "customfield_10043"
		turganID = "557058:fe14ad75-de24-4423-8a64-b05945bbbbdb"
		azamatID = "6188ce54cc2d7c007153a233"
	)
	handover := ts(t, "2026-09-10T12:00:00Z")

	// KAN-3 changed hands, and was returned once before the handover — so the
	// replay has to unwind the Developer change to reach Турганбай.
	kan3 := Issue{
		Key: "KAN-3", ParentKey: "KAN-10",
		Developer: User{AccountID: azamatID, DisplayName: "azamat"},
		Changelog: []Change{
			{At: ts(t, "2026-09-09T09:00:00Z"), FieldID: StatusFieldID, From: "10001", To: "10008"},
			{
				At: handover, Field: "Developer", FieldID: devField,
				From: "[" + turganID + "]", FromString: "[Турганбай С]",
				To: "[" + azamatID + "]", ToString: "[azamat]",
			},
		},
	}
	// KAN-4 never changed hands: its Developer field arrives from the DTO
	// mapping already stripped. Both issues must land on one row.
	kan4 := Issue{
		Key: "KAN-4", ParentKey: "KAN-10",
		Developer: User{AccountID: turganID, DisplayName: "Турганбай С"},
		Changelog: []Change{
			{At: ts(t, "2026-09-11T09:00:00Z"), FieldID: StatusFieldID, From: "10001", To: "10008"},
		},
	}

	rep := Compute([]Issue{kan3, kan4}, Options{
		Mode:              ModeAtTransition,
		DeveloperFieldID:  devField,
		ReturnedStatusIDs: []string{"10008"},
	}, nil)

	if len(rep.Developers) != 1 {
		for _, d := range rep.Developers {
			t.Logf("developer %q: issues=%d returns=%d", d.AccountID, d.Issues, d.Returns)
		}
		t.Fatalf("developers = %d, want 1 — the bracketed id split one person in two", len(rep.Developers))
	}
	got := rep.Developers[0]
	if got.AccountID != turganID {
		t.Errorf("accountId = %q, want the unbracketed %q", got.AccountID, turganID)
	}
	if got.Issues != 2 || got.Returns != 2 {
		t.Errorf("issues = %d, returns = %d; want both returns on one row", got.Issues, got.Returns)
	}
}
