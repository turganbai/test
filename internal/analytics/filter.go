package analytics

import "strings"

// FilterDevelopers narrows the report to the named developers. A selector
// matches a developer's account id exactly, or its display name
// case-insensitively as a substring, so "azamat" and a full account id both
// work and nobody has to paste a uuid to see their own row.
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
		if issueKeys[i.Key] {
			issues = append(issues, i)
		}
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
