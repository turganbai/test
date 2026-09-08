package jira

import (
	"context"
	"fmt"
	"net/url"
	"sync"
)

// searchPath is the enhanced search endpoint. The legacy POST /rest/api/3/search
// is deprecated in the v3 spec; this one paginates with an opaque
// nextPageToken cursor and reports no total.
const searchPath = "/rest/api/3/search/jql"

// defaultSearchPageSize is well under the server-side cap; large field sets
// make Jira return fewer issues per page regardless.
const defaultSearchPageSize = 100

// SearchOptions parameterises SearchIssues.
type SearchOptions struct {
	JQL      string
	Fields   []string
	PageSize int
	// CompleteChangelog re-fetches the changelog of any issue whose embedded
	// changelog was truncated by the search endpoint.
	CompleteChangelog bool
}

// IssueProblem is a per-issue failure that must not abort the whole run.
type IssueProblem struct {
	Key string
	Err error
}

// SearchIssues runs a JQL search and returns every matching issue with its
// changelog attached.
//
// expand=changelog embeds at most ~100 history records per issue and truncates
// silently, which is precisely the long-lived, repeatedly-returned issue we
// care about. So the embedded changelog is used when it is complete, and
// topped up through the dedicated endpoint when it is not.
func (c *Client) SearchIssues(ctx context.Context, opts SearchOptions) ([]Issue, []IssueProblem, error) {
	if opts.JQL == "" {
		return nil, nil, fmt.Errorf("jql is required")
	}
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultSearchPageSize
	}

	var issues []Issue
	token := ""
	for page := 0; ; page++ {
		req := SearchRequest{
			JQL:           opts.JQL,
			Fields:        opts.Fields,
			Expand:        "changelog",
			MaxResults:    pageSize,
			NextPageToken: token,
		}
		var resp SearchResponse
		if err := c.do(ctx, "POST", searchPath, nil, req, &resp, ""); err != nil {
			return nil, nil, fmt.Errorf("search page %d: %w", page, err)
		}
		issues = append(issues, resp.Issues...)
		c.log.Debug("jira search page", "page", page, "issues", len(resp.Issues), "isLast", resp.IsLast)

		if resp.NextPageToken == "" || resp.IsLast || len(resp.Issues) == 0 {
			break
		}
		token = resp.NextPageToken
	}

	var problems []IssueProblem
	if opts.CompleteChangelog {
		problems = c.completeChangelogs(ctx, issues)
	}
	return issues, problems, nil
}

// completeChangelogs re-fetches truncated changelogs through a bounded worker
// pool, mutating issues in place. A failure on one issue becomes a problem, not
// an error: the report is still worth producing without it.
func (c *Client) completeChangelogs(ctx context.Context, issues []Issue) []IssueProblem {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		problems []IssueProblem
		sem      = make(chan struct{}, c.concurrency)
	)
	for i := range issues {
		iss := &issues[i]
		if !iss.Changelog.Truncated() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				problems = append(problems, IssueProblem{Key: iss.Key, Err: ctx.Err()})
				mu.Unlock()
				return
			}
			histories, err := c.IssueChangelog(ctx, iss.Key, 0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems = append(problems, IssueProblem{
					Key: iss.Key,
					Err: fmt.Errorf("complete changelog: %w", err),
				})
				return
			}
			iss.Changelog = &PageOfChangelogs{
				Total:      len(histories),
				MaxResults: len(histories),
				Histories:  histories,
			}
		}()
	}
	wg.Wait()
	return problems
}

// queryOf is a tiny helper keeping url.Values construction readable.
func queryOf(pairs ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Set(pairs[i], pairs[i+1])
	}
	return v
}
