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
	fmt.Fprintln(tw, "\nDEVELOPER\tISSUES\tRETURNS\tAVG\t0\t1\t2\t3+")
	for _, d := range rep.Developers {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.2f\t%d\t%d\t%d\t%d\n",
			truncate(d.DisplayName, 28), d.Issues, d.Returns, d.AvgReturnsPerIssue,
			d.Distribution.Zero, d.Distribution.One, d.Distribution.Two, d.Distribution.ThreePlus)
	}

	fmt.Fprintln(tw, "\nSTORY\tSUB\tRETURNS\tREWORK\tWORST")
	for _, s := range rep.Stories {
		worst := "-"
		if s.MaxReturnsIssueKey != "" {
			worst = fmt.Sprintf("%s (%d)", s.MaxReturnsIssueKey, s.MaxReturns)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.0f%%\t%s\n", s.Key, s.SubTicketCount, s.Returns, s.ReworkRate*100, worst)
	}

	fmt.Fprintln(tw, "\nSUB-TICKET\tSTORY\tRETURNS\tDEVELOPER (CURRENT)")
	for _, i := range rep.Issues {
		if i.Returns == 0 {
			continue
		}
		dev := i.Developer.Label()
		if i.ViaAssigneeFallback {
			dev += " (assignee)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", i.Key, i.ParentKey, i.Returns, truncate(dev, 34))
	}
	tw.Flush()

	if len(rep.Warnings) > 0 {
		fmt.Fprintf(w, "\n%d warning(s); see the \"warnings\" section of the JSON report\n", len(rep.Warnings))
	}
	fmt.Fprintf(w, "generated %s\n", rep.GeneratedAt.Format(time.RFC3339))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
