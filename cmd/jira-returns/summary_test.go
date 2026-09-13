package main

import (
	"bytes"
	"strings"
	"testing"

	"mcp/internal/analytics"
)

func summary(t *testing.T, rep analytics.Report) string {
	t.Helper()
	var buf bytes.Buffer
	writeSummary(&buf, rep)
	return buf.String()
}

func ptr(f float64) *float64 { return &f }

// developerHeader returns the DEVELOPER table's header line. The STORY table
// has a REWORK column of its own, so searching the whole output for "REWORK"
// would always match.
func developerHeader(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "DEVELOPER") {
			return line
		}
	}
	t.Fatalf("no DEVELOPER header in:\n%s", out)
	return ""
}

// The JSON keeps full precision, so the table is the only thing standing
// between a reader and 1.3333333333333333.
func TestWriteSummary_RoundsForDisplay(t *testing.T) {
	out := summary(t, analytics.Report{
		Totals: analytics.Totals{SubTickets: 7, Returns: 5, ReworkRate: 5.0 / 7.0},
		Developers: []analytics.DeveloperReport{
			{DisplayName: "azamat", Issues: 3, Returns: 4, AvgReturnsPerIssue: 4.0 / 3.0},
		},
		Stories: []analytics.StoryReport{
			{Key: "KAN-10", SubTicketCount: 7, Returns: 5, ReworkRate: 5.0 / 7.0},
		},
	})

	for _, want := range []string{"rework rate=71%", "1.33", "71%"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"1.3333", "0.7142", "71.42"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("summary leaked full precision %q:\n%s", unwanted, out)
		}
	}
}

// The turnaround column shows up only when the metric is enabled.
func TestWriteSummary_ReworkColumnHiddenWhenUnmeasured(t *testing.T) {
	out := summary(t, analytics.Report{
		Developers: []analytics.DeveloperReport{{DisplayName: "azamat", Issues: 1}},
	})
	if h := developerHeader(t, out); strings.Contains(h, "REWORK") {
		t.Errorf("REWORK column shown with no turnaround data: %q", h)
	}
}

func TestWriteSummary_ReworkColumnShownWhenMeasured(t *testing.T) {
	out := summary(t, analytics.Report{
		Developers: []analytics.DeveloperReport{
			{DisplayName: "azamat", Issues: 2, AvgReworkSeconds: ptr(18000)},
			{DisplayName: "a1к0l", Issues: 1, AvgReworkSeconds: ptr(5400)},
			// Never measured: still gets a placeholder cell.
			{DisplayName: "Турганбай С", Issues: 1, AvgReworkSeconds: nil},
		},
	})

	if h := developerHeader(t, out); !strings.Contains(h, "REWORK") {
		t.Fatalf("REWORK column missing from header: %q", h)
	}
	for _, want := range []string{"5h0m0s", "1h30m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Турганбай С") && !strings.HasSuffix(strings.TrimRight(line, " "), "-") {
			t.Errorf("unmeasured developer has no placeholder: %q", line)
		}
	}
}

func TestReworkColumn(t *testing.T) {
	if got := reworkColumn(nil); got != "-" {
		t.Errorf("reworkColumn(nil) = %q, want %q", got, "-")
	}
	for _, tt := range []struct {
		secs float64
		want string
	}{
		{18000, "5h0m0s"},
		{5400, "1h30m0s"},
		{5430, "1h31m0s"}, // 90.5 minutes rounds away from zero
		{29, "0s"},        // under half a minute
		{0, "0s"},
	} {
		if got := reworkColumn(&tt.secs); got != tt.want {
			t.Errorf("reworkColumn(%v) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}
