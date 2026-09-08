package jira

import (
	"context"
	"fmt"
	"strconv"
)

const defaultChangelogPageSize = 100

// IssueChangelog fetches an issue's complete changelog through
// GET /rest/api/3/issue/{issueIdOrKey}/changelog, following offset pagination
// until the last page.
func (c *Client) IssueChangelog(ctx context.Context, issueIDOrKey string, pageSize int) ([]Changelog, error) {
	if pageSize <= 0 {
		pageSize = defaultChangelogPageSize
	}
	path := "/rest/api/3/issue/" + issueIDOrKey + "/changelog"

	var all []Changelog
	for startAt := 0; ; {
		var page PageBeanChangelog
		q := queryOf("startAt", strconv.Itoa(startAt), "maxResults", strconv.Itoa(pageSize))
		if err := c.do(ctx, "GET", path, q, nil, &page, issueIDOrKey); err != nil {
			return nil, fmt.Errorf("changelog of %s at %d: %w", issueIDOrKey, startAt, err)
		}
		all = append(all, page.Values...)
		if page.IsLast || len(page.Values) == 0 || len(all) >= page.Total {
			return all, nil
		}
		startAt += len(page.Values)
	}
}
