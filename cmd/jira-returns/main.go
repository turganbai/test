// Command jira-returns reports how often issues were sent back to "returned",
// and which developer each return is counted against.
//
// Configuration comes from the environment, or from a .env file layered under
// it (see internal/config); the query and the reporting period come from flags:
//
//	jira-returns -stories PROJ-1,PROJ-2 -from 2026-08-01 -to 2026-09-01
//	jira-returns -jql 'project = PROJ AND parent is not EMPTY' -out -
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"mcp/internal/analytics"
	"mcp/internal/collect"
	"mcp/internal/config"
	"mcp/internal/jira"
)

type flags struct {
	jql      string
	stories  string
	from     string
	to       string
	out      string
	env      string
	dev      string
	deadline time.Duration
	statuses bool
}

func main() {
	err := run(os.Args[1:], os.Stderr)
	switch {
	case err == nil:
		return
	case errors.Is(err, flag.ErrHelp):
		// -h is a request, not a failure.
		return
	default:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("jira-returns", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var f flags
	fs.StringVar(&f.jql, "jql", "", "JQL selecting the sub-tickets to analyse")
	fs.StringVar(&f.stories, "stories", "", "comma-separated story keys; their sub-tickets are analysed")
	fs.StringVar(&f.from, "from", "", "start of the period, inclusive (2006-01-02 or RFC3339)")
	fs.StringVar(&f.to, "to", "", "end of the period, exclusive (2006-01-02 or RFC3339)")
	fs.StringVar(&f.out, "out", "report.json", `JSON output path, or "-" for stdout`)
	fs.StringVar(&f.env, "env", "", `env file layered under the environment (default ".env" in the working directory, when present)`)
	fs.StringVar(&f.dev, "developer", "", "comma-separated developers to report on, by display name or account id (default: everyone)")
	fs.DurationVar(&f.deadline, "deadline", 10*time.Minute, "overall deadline for the whole run")
	fs.BoolVar(&f.statuses, "statuses", false, "print this site's status ids and names, then exit")
	fs.Usage = func() { usage(fs) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !f.statuses && (f.jql == "") == (f.stories == "") {
		// The most likely way to land here is a bare invocation, so show what
		// to type rather than only what is wrong.
		fs.Usage()
		return errors.New("exactly one of -jql or -stories is required")
	}

	// -statuses is how the reader discovers their returned status ids, so it
	// must not be blocked by the configuration it exists to help write.
	scope := config.ScopeReport
	if f.statuses {
		scope = config.ScopeConnect
	}
	cfg, err := config.LoadFile(os.LookupEnv, f.env, scope)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	from, err := parseDay(f.from, false)
	if err != nil {
		return fmt.Errorf("-from: %w", err)
	}
	to, err := parseDay(f.to, true)
	if err != nil {
		return fmt.Errorf("-to: %w", err)
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		return fmt.Errorf("-to (%s) must be after -from (%s)", to, from)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, f.deadline)
	defer cancel()

	client, err := jira.New(cfg.BaseURL, cfg.Email, cfg.APIToken,
		jira.WithTimeout(cfg.Timeout),
		jira.WithConcurrency(cfg.Concurrency),
		jira.WithLogger(logger),
	)
	if err != nil {
		return err
	}

	// The status catalog is fetched once: names are localized and renamable,
	// so everything downstream compares ids.
	catalog, err := client.StatusCatalog(ctx)
	if err != nil {
		return fmt.Errorf("load status catalog: %w", err)
	}
	if f.statuses {
		printStatuses(os.Stdout, catalog)
		return nil
	}

	returnedIDs, err := resolveStatusIDs(catalog, cfg.ReturnedStatusIDs, cfg.ReturnedStatusNames)
	if err != nil {
		return err
	}
	for _, id := range cfg.CodeReviewStatusIDs {
		if !catalog.Known(id) {
			logger.Warn("configured code-review status id is unknown on this site", "statusId", id)
		}
	}
	logger.Info("resolved returned statuses", "ids", returnedIDs, "names", namesOf(catalog, returnedIDs))

	storyKeys := splitKeys(f.stories)
	jql := f.jql
	if jql == "" {
		jql = fmt.Sprintf("parent in (%s) ORDER BY created ASC", strings.Join(storyKeys, ", "))
	}

	report, err := analytics.Run(ctx, collect.New(client, cfg.DeveloperFieldID, logger), jql, analytics.Options{
		DeveloperFieldID:    cfg.DeveloperFieldID,
		ReturnedStatusIDs:   returnedIDs,
		CodeReviewStatusIDs: cfg.CodeReviewStatusIDs,
		Mode:                cfg.AttributionMode,
		From:                from,
		To:                  to,
		StoryKeys:           storyKeys,
		StatusNames:         catalog.Names(),
	})
	if err != nil {
		return err
	}

	// Filtering happens after aggregation, not in the JQL: the totals a
	// developer's numbers are read against have to come from the whole team,
	// and under at_transition a JQL filter would miss the tickets they have
	// since handed over.
	if unmatched := report.FilterDevelopers(splitKeys(f.dev)); len(unmatched) > 0 {
		logger.Warn("no developer matched", "selectors", unmatched)
	}

	return emit(report, f.out)
}

func usage(fs *flag.FlagSet) {
	w := fs.Output()
	fmt.Fprint(w, `jira-returns counts how often issues were sent back to "returned",
and which developer each return is attributed to.

Usage:
  jira-returns -stories KEY[,KEY...] [-from DATE] [-to DATE] [-out FILE]
  jira-returns -jql QUERY            [-from DATE] [-to DATE] [-out FILE]
  jira-returns -statuses             (list this site's status ids and names)

Examples:
  jira-returns -stories PROJ-101,PROJ-102 -from 2026-08-01 -to 2026-08-31
  jira-returns -jql 'project = PROJ AND parent is not EMPTY AND updated >= -30d' -out -

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Configuration comes from the environment, or from a .env file in the current
directory (-env FILE for another path); the real environment wins. See README.md.
Required: JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN, JIRA_DEVELOPER_FIELD_ID,
and one of JIRA_RETURNED_STATUS_IDS / JIRA_RETURNED_STATUS_NAMES.
-statuses needs only the first three: it is how you find the last one.
`)
}

// resolveStatusIDs prefers explicit ids and falls back to resolving configured
// names through the catalog. An unresolvable name is a startup error: silently
// counting nothing would look exactly like a team that never reworks anything.
func resolveStatusIDs(catalog *jira.StatusCatalog, ids, names []string) ([]string, error) {
	if len(ids) > 0 {
		var unknown []string
		for _, id := range ids {
			if !catalog.Known(id) {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf("JIRA_RETURNED_STATUS_IDS: unknown status id(s) %v on this site; "+
				"run with -statuses to list them", unknown)
		}
		return ids, nil
	}
	var out []string
	for _, name := range names {
		resolved := catalog.IDsByName(name)
		if len(resolved) == 0 {
			return nil, fmt.Errorf("JIRA_RETURNED_STATUS_NAMES: no status named %q on this site.%s\n"+
				"  Status names are localized and get renamed; run with -statuses to list all of them,\n"+
				"  then set JIRA_RETURNED_STATUS_IDS to the ids (ids never change).",
				name, suggestions(catalog, name))
		}
		out = append(out, resolved...)
	}
	return out, nil
}

// suggestions offers near matches so a failed lookup names the alternatives
// rather than sending the reader off to another tool.
func suggestions(catalog *jira.StatusCatalog, name string) string {
	needle := strings.ToLower(strings.TrimSpace(name))
	var close []string
	for id, n := range catalog.Names() {
		l := strings.ToLower(n)
		if strings.Contains(l, needle) || strings.Contains(needle, l) {
			close = append(close, fmt.Sprintf("%s (id %s)", n, id))
		}
	}
	if len(close) == 0 {
		return ""
	}
	sort.Strings(close)
	return " Did you mean: " + strings.Join(close, ", ") + "?"
}

// printStatuses lists the site's statuses so the ids can be copied into
// JIRA_RETURNED_STATUS_IDS.
func printStatuses(w io.Writer, catalog *jira.StatusCatalog) {
	names := catalog.Names()
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return names[ids[i]] < names[ids[j]] })

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME")
	for _, id := range ids {
		fmt.Fprintf(tw, "%s\t%s\n", id, names[id])
	}
	tw.Flush()
}

func namesOf(catalog *jira.StatusCatalog, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, catalog.NameByID(id))
	}
	return out
}

// parseDay accepts a plain date or a full RFC3339 timestamp. A plain -to date
// is treated as the end of that day, which is what "until the 31st" means to a
// human.
func parseDay(s string, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither 2006-01-02 nor RFC3339", s)
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return t.UTC(), nil
}

func splitKeys(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if k := strings.TrimSpace(p); k != "" {
			out = append(out, k)
		}
	}
	return out
}
