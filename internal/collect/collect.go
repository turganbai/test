// Package collect adapts the Jira transport layer to the domain layer: it
// fetches issues through internal/jira and maps the DTOs onto
// analytics.Issue. It is the only package that imports both.
package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/turganbay/mcp/internal/analytics"
	"github.com/turganbay/mcp/internal/jira"
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
		dev, err := ri.Fields.UserField(c.developerFieldID)
		switch {
		case err != nil:
			warnings = append(warnings, analytics.Warning{
				IssueKey: ri.Key,
				Code:     analytics.WarnDeveloperFieldMissing,
				Message:  err.Error(),
			})
		case dev != nil:
			iss.Developer = toUser(dev)
		}
	}

	if ri.Changelog.Truncated() {
		warnings = append(warnings, analytics.Warning{
			IssueKey: ri.Key,
			Code:     analytics.WarnChangelogTruncated,
			Message: fmt.Sprintf("changelog truncated: %d of %d histories available, counts may be low",
				len(ri.Changelog.Histories), ri.Changelog.Total),
		})
	}
	if ri.Changelog != nil {
		changes, warns := flatten(ri.Key, ri.Changelog.Histories)
		iss.Changelog = changes
		warnings = append(warnings, warns...)
	}
	return iss, warnings
}

// flatten turns Jira's history/items nesting into a flat, time-ordered list of
// changes, each carrying its history's author and timestamp.
func flatten(issueKey string, histories []jira.Changelog) ([]analytics.Change, []analytics.Warning) {
	var (
		out      []analytics.Change
		warnings []analytics.Warning
	)
	for _, h := range histories {
		at, err := h.CreatedAt()
		if err != nil {
			warnings = append(warnings, analytics.Warning{
				IssueKey: issueKey,
				Code:     analytics.WarnFetchFailed,
				Message:  fmt.Sprintf("changelog entry %s: %v", h.ID, err),
			})
			continue
		}
		var author analytics.User
		if h.Author != nil {
			author = toUser(h.Author)
		}
		for _, it := range h.Items {
			out = append(out, analytics.Change{
				At:         at.UTC(),
				Author:     author,
				Field:      it.Field,
				FieldID:    it.FieldID,
				From:       it.From,
				To:         it.To,
				FromString: it.FromString,
				ToString:   it.ToString,
			})
		}
	}
	// Ascending order is what the replay in analytics relies on; Jira usually
	// returns it that way already, but nothing in the contract promises it.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, warnings
}

func toUser(u *jira.User) analytics.User {
	return analytics.User{
		AccountID:   u.AccountID,
		DisplayName: u.DisplayName,
		Active:      u.Active,
	}
}
