package analytics

import "testing"

func sampleReport() Report {
	return Report{
		Totals: Totals{SubTickets: 7, Returns: 5, ReworkRate: 0.57, Developers: 3},
		Developers: []DeveloperReport{
			{AccountID: "acc-1", DisplayName: "Турганбай С", Issues: 2, Returns: 3, IssueKeys: []string{"KAN-3", "KAN-7"}},
			{AccountID: "acc-2", DisplayName: "azamat", Issues: 4, Returns: 2, IssueKeys: []string{"KAN-11", "KAN-13", "KAN-14"}},
			{AccountID: "acc-3", DisplayName: "a1к0l", Issues: 1, Returns: 0, IssueKeys: []string{"KAN-16"}},
		},
		Stories: []StoryReport{{Key: "KAN-10", SubTicketCount: 7, Returns: 5}},
		Issues: []IssueReport{
			{Key: "KAN-3"}, {Key: "KAN-7"}, {Key: "KAN-11"},
			{Key: "KAN-13"}, {Key: "KAN-14"}, {Key: "KAN-16"},
		},
		Warnings: []Warning{{IssueKey: "KAN-3", Code: WarnChangelogTruncated}},
	}
}

func TestFilterDevelopers(t *testing.T) {
	rep := sampleReport()
	if unmatched := rep.FilterDevelopers([]string{"azamat"}); len(unmatched) != 0 {
		t.Fatalf("unmatched = %v, want none", unmatched)
	}

	if len(rep.Developers) != 1 || rep.Developers[0].AccountID != "acc-2" {
		t.Fatalf("developers = %+v, want only azamat", rep.Developers)
	}
	// Only the issues attributed to that developer survive.
	var keys []string
	for _, i := range rep.Issues {
		keys = append(keys, i.Key)
	}
	if len(keys) != 3 || keys[0] != "KAN-11" || keys[2] != "KAN-14" {
		t.Errorf("issues = %v, want azamat's three", keys)
	}
}

// The point of filtering after aggregation: one developer's numbers stay
// readable against what the whole team did.
func TestFilterDevelopers_KeepsTeamWideContext(t *testing.T) {
	rep := sampleReport()
	rep.FilterDevelopers([]string{"azamat"})

	if rep.Totals.SubTickets != 7 || rep.Totals.Returns != 5 || rep.Totals.Developers != 3 {
		t.Errorf("totals = %+v, want the team-wide figures untouched", rep.Totals)
	}
	if len(rep.Stories) != 1 || rep.Stories[0].SubTicketCount != 7 {
		t.Errorf("stories = %+v, want the story rows untouched", rep.Stories)
	}
	if len(rep.Warnings) != 1 {
		t.Errorf("warnings = %+v, want them kept", rep.Warnings)
	}
	if len(rep.Params.Developers) != 1 || rep.Params.Developers[0] != "azamat" {
		t.Errorf("params.developers = %v, want the filter recorded", rep.Params.Developers)
	}
}

func TestFilterDevelopers_Matching(t *testing.T) {
	tests := []struct {
		name      string
		selectors []string
		wantIDs   []string
		wantMiss  []string
	}{
		{"empty selector keeps everyone", nil, []string{"acc-1", "acc-2", "acc-3"}, nil},
		{"by account id", []string{"acc-3"}, []string{"acc-3"}, nil},
		{"case-insensitive", []string{"AZAMAT"}, []string{"acc-2"}, nil},
		{"substring", []string{"Турганбай"}, []string{"acc-1"}, nil},
		{"cyrillic display name", []string{"a1к0l"}, []string{"acc-3"}, nil},
		{"several at once", []string{"azamat", "a1к0l"}, []string{"acc-2", "acc-3"}, nil},
		// A typo must not silently produce an empty report.
		{"no match is reported", []string{"nobody"}, nil, []string{"nobody"}},
		{"partial match still reports the miss", []string{"azamat", "nobody"}, []string{"acc-2"}, []string{"nobody"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := sampleReport()
			miss := rep.FilterDevelopers(tt.selectors)

			var got []string
			for _, d := range rep.Developers {
				got = append(got, d.AccountID)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("developers = %v, want %v", got, tt.wantIDs)
			}
			for i := range got {
				if got[i] != tt.wantIDs[i] {
					t.Errorf("developers = %v, want %v", got, tt.wantIDs)
					break
				}
			}
			if len(miss) != len(tt.wantMiss) {
				t.Errorf("unmatched = %v, want %v", miss, tt.wantMiss)
			}
		})
	}
}
