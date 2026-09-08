package analytics

import (
	"context"
	"fmt"
)

// Run fetches the issues matching jql and aggregates them. It is the only
// place in this package that touches the outside world, and it does so through
// the consumer-declared IssueFetcher interface.
func Run(ctx context.Context, f IssueFetcher, jql string, opts Options) (Report, error) {
	issues, warnings, err := f.FetchIssues(ctx, jql)
	if err != nil {
		return Report{}, fmt.Errorf("fetch issues: %w", err)
	}
	rep := Compute(issues, opts, warnings)
	rep.Params.JQL = jql
	return rep, nil
}
