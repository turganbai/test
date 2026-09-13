package analytics

import "strings"

// FilterDevelopers narrows the report to the named developers. A selector
// matches a developer's account id exactly, or its display name
// case-insensitively as a substring, so "azamat" and a full account id both
// work and nobody has to paste a uuid to see their own row.
//
// The per-issue rows are narrowed the same way the developer rows are: an
// issue survives only if a selected developer was attributed a return on it (or
// holds it with none), and its events and returns count only theirs.
//
// Totals and the per-story rows are deliberately left whole. They are the
// denominator the filtered rows are read against: a developer's four returns
// mean one thing against a team that had five and quite another against a team
// that had ninety, and recomputing them per developer would throw that away.
// Warnings are kept for the same reason — a truncated changelog still explains
// a number that looks too low.
//
// It reports the selectors that matched nobody, which is almost always a typo
// and is worth saying out loud rather than silently returning an empty report.
func (r *Report) FilterDevelopers(selectors []string) (unmatched []string) {
	if len(selectors) == 0 {
		return nil
	}

	keep := make(map[string]bool)
	for _, sel := range selectors {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		found := false
		for _, d := range r.Developers {
			if matchesDeveloper(d, sel) {
				keep[d.AccountID] = true
				found = true
			}
		}
		if !found {
			unmatched = append(unmatched, sel)
		}
	}

	devs := make([]DeveloperReport, 0, len(keep))
	issueKeys := make(map[string]bool)
	for _, d := range r.Developers {
		if !keep[d.AccountID] {
			continue
		}
		devs = append(devs, d)
		// IssueKeys is what compute attributed to this developer, so it already
		// follows the attribution mode: under at_transition these are the
		// issues they held when the return happened, not the ones they own now.
		for _, k := range d.IssueKeys {
			issueKeys[k] = true
		}
	}
	r.Developers = devs

	issues := make([]IssueReport, 0, len(issueKeys))
	for _, i := range r.Issues {
		if !issueKeys[i.Key] {
			continue
		}
		// The events are narrowed too, not just the issue list. Under
		// at_transition one sub-ticket's returns can be split across several
		// developers as the Developer field changes hands, and keeping the
		// whole list would file somebody else's returns under the selected
		// developer — and disagree with the developers[].returns printed right
		// beside it. Returns is recounted from what survives, so an issue kept
		// only because its current owner is selected can legitimately read 0.
		events := make([]ReturnEvent, 0, len(i.Events))
		for _, ev := range i.Events {
			if keep[ev.AttributedTo.AccountID] {
				events = append(events, ev)
			}
		}
		i.Events = events
		i.Returns = len(events)
		issues = append(issues, i)
	}
	r.Issues = issues

	r.Params.Developers = append([]string(nil), selectors...)
	return unmatched
}

func matchesDeveloper(d DeveloperReport, selector string) bool {
	if d.AccountID == selector {
		return true
	}
	return d.DisplayName != "" &&
		strings.Contains(strings.ToLower(d.DisplayName), strings.ToLower(selector))
}
