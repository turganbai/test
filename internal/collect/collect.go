// Package collect adapts the Jira transport layer to the domain layer: it
// fetches issues through internal/jira and maps the DTOs onto
// analytics.Issue. It is the only package that imports both.
package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"mcp/internal/analytics"
	"mcp/internal/jira"
)

// Collector implements analytics.IssueFetcher.
type Collector struct {
	client           *jira.Client
	developerFieldID string
	log              *slog.Logger
}

// New builds a Collector. developerFieldID is the custom field id holding the
// developer, e.g. "customfield_10050".
func New(client *jira.Client, developerFieldID string, log *slog.Logger) *Collector {
	if log == nil {
		log = slog.Default()
	}
	return &Collector{client: client, developerFieldID: developerFieldID, log: log}
}

var _ analytics.IssueFetcher = (*Collector)(nil)

// FetchIssues runs the JQL search and maps the results into the domain model.
// Per-issue failures come back as warnings so that a partial report is still
// produced.
func (c *Collector) FetchIssues(ctx context.Context, jql string) ([]analytics.Issue, []analytics.Warning, error) {
	fields := []string{"summary", "assignee", "status", "parent"}
	if c.developerFieldID != "" {
		fields = append(fields, c.developerFieldID)
	}

	raw, problems, err := c.client.SearchIssues(ctx, jira.SearchOptions{
		JQL:               jql,
		Fields:            fields,
		CompleteChangelog: true,
	})
	if err != nil {
		return nil, nil, err
	}

	warnings := make([]analytics.Warning, 0, len(problems))
	for _, p := range problems {
		warnings = append(warnings, analytics.Warning{
			IssueKey: p.Key,
			Code:     analytics.WarnFetchFailed,
			Message:  p.Err.Error(),
		})
	}

	issues := make([]analytics.Issue, 0, len(raw))
	for _, ri := range raw {
		iss, warns := c.mapIssue(ri)
		warnings = append(warnings, warns...)
		issues = append(issues, iss)
	}
	return issues, warnings, nil
}

func (c *Collector) mapIssue(ri jira.Issue) (analytics.Issue, []analytics.Warning) {
	var warnings []analytics.Warning

	iss := analytics.Issue{
		ID:      ri.ID,
		Key:     ri.Key,
		Summary: ri.Fields.Summary,
	}
	if p := ri.Fields.Parent; p != nil {
		iss.ParentKey = p.Key
		iss.ParentSummary = p.Fields.Summary
	}
	if a := ri.Fields.Assignee; a != nil {
		iss.Assignee = toUser(a)
	}
	if c.developerFieldID != "" {
		iss.DeveloperFieldPresent = ri.Fields.Has(c.developerFieldID)
		devs, err := ri.Fields.UsersField(c.developerFieldID)
		switch {
		case err != nil:
			// Present but undecodable. UsersField returns no error for an
			// absent or null field, so reaching here means the field exists
			// and is the wrong shape — never that it is missing.
			warnings = append(warnings, analytics.Warning{
				IssueKey: ri.Key,
				Code:     analytics.WarnDeveloperFieldUnreadable,
				Message:  err.Error(),
			})
		case len(devs) > 0:
			// A multi-user picker can name several people. The metric counts
			// each return against exactly one developer, so the first wins and
			// the ambiguity is reported rather than hidden.
			iss.Developer = toUser(&devs[0])
			if len(devs) > 1 {
				warnings = append(warnings, analytics.Warning{
					IssueKey: ri.Key,
					Code:     analytics.WarnDeveloperFieldAmbiguous,
					Message: fmt.Sprintf("Developer field %s names %d users (%s); attributed to %s",
						c.developerFieldID, len(devs), strings.Join(displayNames(devs), ", "), iss.Developer.DisplayName),
				})
			}
		}
	}

	// Everything that reads the changelog sits under one nil check. Truncated
	// guards its own receiver, so the old order could not actually panic —
	// but it put the invariant in the callee, where this call site cannot
	// show it, and left the len() in the warning below looking unguarded.
	if ri.Changelog != nil {
		if ri.Changelog.Truncated() {
			warnings = append(warnings, analytics.Warning{
				IssueKey: ri.Key,
				Code:     analytics.WarnChangelogTruncated,
				Message: fmt.Sprintf("changelog truncated: %d of %d histories available, counts may be low",
					len(ri.Changelog.Histories), ri.Changelog.Total),
			})
		}
		changes, warns := flatten(ri.Key, c.developerFieldID, ri.Changelog.Histories)
		iss.Changelog = changes
		warnings = append(warnings, warns...)
	}
	return iss, warnings
}

// flatten turns Jira's history/items nesting into a flat, time-ordered list of
// changes, each carrying its history's author and timestamp.
//
// developerFieldID is passed rather than read off a receiver: it is the only
// thing here that varies, and a parameter says so where a method on Collector
// implied a dependency on the client and the logger that this never had.
func flatten(issueKey, developerFieldID string, histories []jira.Changelog) ([]analytics.Change, []analytics.Warning) {
	var (
		out      []analytics.Change
		warnings []analytics.Warning
	)
	for _, h := range histories {
		at, err := h.CreatedAt()
		if err != nil {
			// The fetch succeeded; one entry's timestamp did not parse. The
			// entry is dropped — a change we cannot place in time is worse
			// than no change — and the rest of the issue is still counted.
			warnings = append(warnings, analytics.Warning{
				IssueKey: issueKey,
				Code:     analytics.WarnChangelogUnparseable,
				Message:  fmt.Sprintf("changelog entry %s: %v", h.ID, err),
			})
			continue
		}
		var author analytics.User
		if h.Author != nil {
			author = toUser(h.Author)
		}
		for _, it := range h.Items {
			from, fromString := it.From, it.FromString
			to, toString := it.To, it.ToString
			// Normalize the multi-user picker's bracketed value at the
			// boundary, through the same parser the domain replays it with.
			if developerFieldID != "" && it.FieldID == developerFieldID {
				f := analytics.ParseUserValue(from, fromString)
				t := analytics.ParseUserValue(to, toString)
				from, fromString = f.AccountID, f.DisplayName
				to, toString = t.AccountID, t.DisplayName
			}
			out = append(out, analytics.Change{
				At:         at.UTC(),
				Author:     author,
				Field:      it.Field,
				FieldID:    it.FieldID,
				From:       from,
				To:         to,
				FromString: fromString,
				ToString:   toString,
			})
		}
	}
	// Ascending order is what the replay in analytics relies on; Jira usually
	// returns it that way already, but nothing in the contract promises it.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, warnings
}

func displayNames(us []jira.User) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		name := u.DisplayName
		if name == "" {
			name = u.AccountID
		}
		out = append(out, name)
	}
	return out
}

func toUser(u *jira.User) analytics.User {
	return analytics.User{
		AccountID:   u.AccountID,
		DisplayName: u.DisplayName,
		Active:      u.Active,
	}
}
