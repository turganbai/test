package analytics

import (
	"fmt"
	"sort"
	"time"
)

// Compute turns issues plus their changelogs into a Report.
//
// It is a pure function: no I/O, no clock beyond GeneratedAt, no hidden state.
// Everything the tool is tested on lives here, so the tests need no mocks.
func Compute(issues []Issue, opts Options, warnings []Warning) Report {
	rep := Report{
		GeneratedAt: time.Now().UTC(),
		Params: Params{
			Mode:                opts.Mode,
			DeveloperFieldID:    opts.DeveloperFieldID,
			ReturnedStatusIDs:   append([]string{}, opts.ReturnedStatusIDs...),
			CodeReviewStatusIDs: opts.CodeReviewStatusIDs,
		},
		// Every slice the report serialises is built non-nil *where it is
		// constructed*, never patched up afterwards: encoding/json writes nil
		// as null, and a consumer iterating .warnings[] or .issues[].events[]
		// should not have to special-case "nothing happened" against "the
		// field is missing".
		Warnings: append(make([]Warning, 0, len(warnings)), warnings...),
		Issues:   make([]IssueReport, 0, len(issues)),
	}
	if !opts.From.IsZero() {
		from := opts.From
		rep.Params.From = &from
	}
	if !opts.To.IsZero() {
		to := opts.To
		rep.Params.To = &to
	}

	returned := idSet(opts.ReturnedStatusIDs)
	codeReview := idSet(opts.CodeReviewStatusIDs)

	stories := newStoryIndex(opts.StoryKeys)
	// devIssues[accountId][issueKey] = number of returns attributed there.
	devIssues := map[string]map[string]int{}
	devNames := map[string]string{}
	devRework := map[string][]float64{}

	for _, iss := range issues {
		if iss.ParentKey == "" {
			// Not a sub-ticket. Register it as a story so that a story with no
			// sub-tickets still appears in the report.
			stories.ensure(iss.Key).Summary = iss.Summary
			continue
		}
		st := stories.ensure(iss.ParentKey)
		if st.Summary == "" {
			st.Summary = iss.ParentSummary
		}
		st.SubTicketCount++
		st.SubTicketKeys = append(st.SubTicketKeys, iss.Key)

		if opts.DeveloperFieldID != "" && !iss.DeveloperFieldPresent {
			err := &FieldMissingError{IssueKey: iss.Key, FieldID: opts.DeveloperFieldID}
			rep.Warnings = append(rep.Warnings, Warning{
				IssueKey: iss.Key,
				Code:     WarnDeveloperFieldMissing,
				Message:  err.Error(),
			})
		}

		events := returnEvents(iss, opts, returned, codeReview)
		// ModeCurrent ignores the timestamp; the zero time keeps Compute pure.
		cur := resolve(iss, opts.DeveloperFieldID, ModeCurrent, time.Time{})

		ir := IssueReport{
			Key:                 iss.Key,
			Summary:             iss.Summary,
			ParentKey:           iss.ParentKey,
			Returns:             len(events),
			Developer:           cur.user,
			ViaAssigneeFallback: cur.viaAssignee,
			Events:              events,
		}
		rep.Issues = append(rep.Issues, ir)

		st.Returns += len(events)
		if len(events) > 0 {
			st.SubTicketsWithReturn++
			if len(events) > st.MaxReturns {
				st.MaxReturns = len(events)
				st.MaxReturnsIssueKey = iss.Key
			}
		}

		if cur.viaAssignee {
			rep.Warnings = append(rep.Warnings, Warning{
				IssueKey: iss.Key,
				Code:     WarnAssigneeFallback,
				Message: fmt.Sprintf("Developer field %s is empty, attributed to assignee %s",
					opts.DeveloperFieldID, cur.user.Label()),
			})
		}

		if len(events) == 0 {
			// Seed the zero bucket so the distribution has a denominator.
			touch(devIssues, cur.user.AccountID, iss.Key)
			devNames[cur.user.AccountID] = cur.user.Label()
			continue
		}
		for _, ev := range events {
			id := ev.AttributedTo.AccountID
			touch(devIssues, id, iss.Key)
			devIssues[id][iss.Key]++
			devNames[id] = ev.AttributedTo.Label()
			if ev.ReworkSeconds != nil {
				devRework[id] = append(devRework[id], *ev.ReworkSeconds)
			}
			if ev.Unattributed {
				rep.Warnings = append(rep.Warnings, Warning{
					IssueKey: iss.Key,
					Code:     WarnNoAttribution,
					Message: fmt.Sprintf("return at %s has neither Developer nor assignee",
						ev.At.Format(time.RFC3339)),
				})
			}
		}
	}

	rep.Stories = stories.finish()
	rep.Developers = developerReports(devIssues, devNames, devRework)
	rep.Totals = totals(rep)

	sort.Slice(rep.Issues, func(i, j int) bool {
		if rep.Issues[i].Returns != rep.Issues[j].Returns {
			return rep.Issues[i].Returns > rep.Issues[j].Returns
		}
		return rep.Issues[i].Key < rep.Issues[j].Key
	})
	return rep
}

// returnEvents extracts every transition into a "returned" status that falls
// inside the reporting period, and attributes each one.
func returnEvents(iss Issue, opts Options, returned, codeReview map[string]bool) []ReturnEvent {
	events := make([]ReturnEvent, 0)
	// Collected once per issue rather than rescanned per return: an issue with
	// 250 history entries and 20 returns cost 5000 iterations before.
	var crTimes []time.Time
	if len(codeReview) > 0 {
		crTimes = codeReviewTimes(iss.Changelog, codeReview)
	}
	for _, ch := range iss.Changelog {
		if !isStatusChange(ch) {
			continue
		}
		// Match on the status id, never on toString: status names are
		// localized and get renamed, ids are stable.
		if !returned[ch.To] {
			continue
		}
		if !inPeriod(ch.At, opts.From, opts.To) {
			continue
		}
		at := resolve(iss, opts.DeveloperFieldID, opts.Mode, ch.At)
		ev := ReturnEvent{
			IssueKey:            iss.Key,
			At:                  ch.At,
			FromStatusID:        ch.From,
			FromStatusName:      statusName(opts.StatusNames, ch.From, ch.FromString),
			ToStatusID:          ch.To,
			ToStatusName:        statusName(opts.StatusNames, ch.To, ch.ToString),
			ReturnedBy:          ch.Author,
			AttributedTo:        at.user,
			ViaAssigneeFallback: at.viaAssignee,
			Unattributed:        at.unattributed,
		}
		// len(crTimes) rather than len(codeReview): an issue that never
		// reached code review has nothing to measure either way, and this
		// skips the search entirely.
		if len(crTimes) > 0 {
			if d, ok := reworkAfter(crTimes, ch.At); ok {
				secs := d.Seconds()
				ev.ReworkSeconds = &secs
			}
		}
		events = append(events, ev)
	}
	return events
}

// codeReviewTimes collects, in ascending order, the instant of every
// transition into a code-review status.
//
// Sorting here is what makes the metric independent of changelog order.
// reworkAfter used to take the first match it walked past, which is the
// earliest one only if the input happens to be ordered — and nothing in the
// Issue type promises that, least of all a changelog stitched together from
// separately fetched pages. When the order broke, the rework time silently
// grew: no panic, no warning, just a larger number.
func codeReviewTimes(changes []Change, codeReview map[string]bool) []time.Time {
	var ts []time.Time
	for _, ch := range changes {
		if isStatusChange(ch) && codeReview[ch.To] {
			ts = append(ts, ch.At)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	return ts
}

// reworkAfter measures the time from a return until the next transition into
// code-review, i.e. how long the developer took to hand the work back.
//
// crTimes must be ascending, as codeReviewTimes returns it. sort.Search finds
// the first instant strictly after from, so a code-review transition stamped at
// the same moment as the return does not count — the same boundary the linear
// scan's At.After(from) drew.
func reworkAfter(crTimes []time.Time, from time.Time) (time.Duration, bool) {
	i := sort.Search(len(crTimes), func(i int) bool {
		return crTimes[i].After(from)
	})
	if i == len(crTimes) {
		return 0, false
	}
	return crTimes[i].Sub(from), true
}

// isStatusChange matches the status field by id, falling back to the field name
// only when the id is absent (older changelog entries omit fieldId).
func isStatusChange(ch Change) bool {
	if ch.FieldID != "" {
		return ch.FieldID == StatusFieldID
	}
	return ch.Field == StatusFieldID
}

func inPeriod(t, from, to time.Time) bool {
	if !from.IsZero() && t.Before(from) {
		return false
	}
	if !to.IsZero() && !t.Before(to) {
		return false
	}
	return true
}

func statusName(names map[string]string, id, fallback string) string {
	if n, ok := names[id]; ok && n != "" {
		return n
	}
	return fallback
}

func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			m[id] = true
		}
	}
	return m
}

func touch(m map[string]map[string]int, dev, issue string) {
	if m[dev] == nil {
		m[dev] = map[string]int{}
	}
	if _, ok := m[dev][issue]; !ok {
		m[dev][issue] = 0
	}
}

func developerReports(devIssues map[string]map[string]int, names map[string]string, rework map[string][]float64) []DeveloperReport {
	out := make([]DeveloperReport, 0, len(devIssues))
	for id, byIssue := range devIssues {
		dr := DeveloperReport{
			AccountID:   id,
			DisplayName: names[id],
			IssueKeys:   make([]string, 0, len(byIssue)),
		}
		for key, n := range byIssue {
			dr.Issues++
			dr.Returns += n
			dr.IssueKeys = append(dr.IssueKeys, key)
			switch {
			case n == 0:
				dr.Distribution.Zero++
			case n == 1:
				dr.Distribution.One++
			case n == 2:
				dr.Distribution.Two++
			default:
				dr.Distribution.ThreePlus++
			}
		}
		if dr.Issues > 0 {
			dr.AvgReturnsPerIssue = float64(dr.Returns) / float64(dr.Issues)
		}
		if xs := rework[id]; len(xs) > 0 {
			var sum float64
			for _, x := range xs {
				sum += x
			}
			avg := sum / float64(len(xs))
			dr.AvgReworkSeconds = &avg
		}
		sort.Strings(dr.IssueKeys)
		out = append(out, dr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Returns != out[j].Returns {
			return out[i].Returns > out[j].Returns
		}
		return out[i].AccountID < out[j].AccountID
	})
	return out
}

func totals(rep Report) Totals {
	t := Totals{Stories: len(rep.Stories), Developers: len(rep.Developers)}
	for _, ir := range rep.Issues {
		t.SubTickets++
		t.Returns += ir.Returns
		if ir.Returns > 0 {
			t.SubTicketsWithReturn++
		}
	}
	if t.SubTickets > 0 {
		t.ReworkRate = float64(t.SubTicketsWithReturn) / float64(t.SubTickets)
	}
	return t
}

// storyIndex collects stories by key. Insertion order is deliberately not
// tracked: finish sorts by returns and then by key, a total order over unique
// keys, so the output is fully determined without it.
type storyIndex struct {
	byKey map[string]*StoryReport
}

func newStoryIndex(seed []string) *storyIndex {
	si := &storyIndex{byKey: map[string]*StoryReport{}}
	for _, k := range seed {
		si.ensure(k)
	}
	return si
}

func (s *storyIndex) ensure(key string) *StoryReport {
	if st, ok := s.byKey[key]; ok {
		return st
	}
	st := &StoryReport{Key: key, SubTicketKeys: []string{}}
	s.byKey[key] = st
	return st
}

func (s *storyIndex) finish() []StoryReport {
	out := make([]StoryReport, 0, len(s.byKey))
	for _, st := range s.byKey {
		if st.SubTicketCount > 0 {
			st.ReworkRate = float64(st.SubTicketsWithReturn) / float64(st.SubTicketCount)
		}
		sort.Strings(st.SubTicketKeys)
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Returns != out[j].Returns {
			return out[i].Returns > out[j].Returns
		}
		return out[i].Key < out[j].Key
	})
	return out
}
