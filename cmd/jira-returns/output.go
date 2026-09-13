package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"mcp/internal/analytics"
)

// emit writes the JSON document and prints a short human summary. When the
// JSON goes to stdout the table moves to stderr, so the output stays pipeable.
func emit(rep analytics.Report, out string) error {
	table := io.Writer(os.Stdout)
	var jsonOut io.Writer

	if out == "-" {
		jsonOut = os.Stdout
		table = os.Stderr
	} else {
		f, err := os.Create(out)
		if err != nil {
			return fmt.Errorf("create %s: %w", out, err)
		}
		defer f.Close()
		jsonOut = f
	}

	enc := json.NewEncoder(jsonOut)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	writeSummary(table, rep)
	if out != "-" {
		fmt.Fprintf(table, "\nJSON report written to %s\n", out)
	}
	return nil
}

func writeSummary(w io.Writer, rep analytics.Report) {
	fmt.Fprintf(w, "Returns report  mode=%s  sub-tickets=%d  returns=%d  rework rate=%.0f%%\n",
		rep.Params.Mode, rep.Totals.SubTickets, rep.Totals.Returns, rep.Totals.ReworkRate*100)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	// The turnaround metric is off unless JIRA_CODE_REVIEW_STATUS_IDS is set,
	// and a column of dashes helps nobody — so it appears only once there is
	// something to put in it.
	rework := false
	for _, d := range rep.Developers {
		if d.AvgReworkSeconds != nil {
			rework = true
			break
		}
	}

	header := "\nDEVELOPER\tISSUES\tRETURNS\tAVG\t0\t1\t2\t3+"
	if rework {
		header += "\tREWORK"
	}
	fmt.Fprintln(tw, header)
	for _, d := range rep.Developers {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.2f\t%d\t%d\t%d\t%d",
			truncate(d.DisplayName, 28), d.Issues, d.Returns, d.AvgReturnsPerIssue,
			d.Distribution.Zero, d.Distribution.One, d.Distribution.Two, d.Distribution.ThreePlus)
		if rework {
			fmt.Fprintf(tw, "\t%s", reworkColumn(d))
		}
		fmt.Fprintln(tw)
	}

	fmt.Fprintln(tw, "\nSTORY\tSUB\tRETURNS\tREWORK\tWORST")
	for _, s := range rep.Stories {
		worst := "-"
		if s.MaxReturnsIssueKey != "" {
			worst = fmt.Sprintf("%s (%d)", s.MaxReturnsIssueKey, s.MaxReturns)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.0f%%\t%s\n", s.Key, s.SubTicketCount, s.Returns, s.ReworkRate*100, worst)
	}

	// Under at_transition the counted returns need not have been attributed
	// the way the Developer field reads today, and the DEVELOPER (CURRENT)
	// column deliberately shows today. Without this the terminal gave no hint
	// that a number came from the assignee instead — the figures were right
	// and their provenance invisible. Shown only when there is any, like the
	// turnaround column above.
	fallback := false
	for _, i := range rep.Issues {
		if viaAssigneeCount(i) > 0 {
			fallback = true
			break
		}
	}

	header = "\nSUB-TICKET\tSTORY\tRETURNS\tDEVELOPER (CURRENT)"
	if fallback {
		header += "\tVIA ASSIGNEE"
	}
	fmt.Fprintln(tw, header)
	for _, i := range rep.Issues {
		if i.Returns == 0 {
			continue
		}
		dev := i.Developer.Label()
		if i.ViaAssigneeFallback {
			dev += " (assignee)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s", i.Key, i.ParentKey, i.Returns, truncate(dev, 34))
		if fallback {
			fmt.Fprintf(tw, "\t%s", viaAssigneeColumn(i))
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()

	if len(rep.Warnings) > 0 {
		fmt.Fprintf(w, "\n%d warning(s); see the \"warnings\" section of the JSON report\n", len(rep.Warnings))
	}
	fmt.Fprintf(w, "generated %s\n", rep.GeneratedAt.Format(time.RFC3339))
}

// reworkColumn renders the average turnaround. The report stores seconds
// because that is what a machine wants; a reader wants "5h0m", so the rounding
// to whole minutes happens here rather than in the data.
//
// The sample count rides along in parentheses because it is not the developer's
// return count: a return superseded by another before the work reached review
// yields no sample. "5h0m0s (1)" is an anecdote and "5h0m0s (9)" is a trend,
// and without the number they render identically.
func reworkColumn(d analytics.DeveloperReport) string {
	if d.AvgReworkSeconds == nil {
		return "-"
	}
	avg := (time.Duration(*d.AvgReworkSeconds * float64(time.Second))).Round(time.Minute)
	return fmt.Sprintf("%s (%d)", avg, d.ReworkSamples)
}

// viaAssigneeCount is how many of an issue's counted returns were attributed
// through the assignee because the Developer field was empty at the time.
func viaAssigneeCount(i analytics.IssueReport) int {
	n := 0
	for _, e := range i.Events {
		if e.ViaAssigneeFallback {
			n++
		}
	}
	return n
}

// viaAssigneeColumn reads as a share of the RETURNS cell beside it: "2/3" is
// two of that row's three returns. A row with none gets a placeholder rather
// than "0/3", which invites being read as a measurement that came out zero.
func viaAssigneeColumn(i analytics.IssueReport) string {
	n := viaAssigneeCount(i)
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", n, i.Returns)
}

// truncate shortens s to at most n *runes*, marking the cut with an ellipsis.
//
// Counting bytes here would slice a display name mid-rune — Cyrillic and the
// ellipsis itself are multi-byte — printing U+FFFD and, because "…" costs
// three bytes against the one it replaced, a result wider than the column it
// was meant to fit. Names in this instance mix scripts, so the cut lands on a
// multi-byte rune sooner than an all-ASCII reading of the code suggests.
//
// Runes, not display width: a CJK ideograph or an emoji occupies two terminal
// cells and is counted here as one, so a column of such names renders wider
// than its budget. Out of scope deliberately — correcting it means a
// wcwidth-style table, and tabwriter measures the same way this does, so the
// two at least agree.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
