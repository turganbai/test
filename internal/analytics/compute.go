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
			ReturnedStatusIDs:   opts.ReturnedStatusIDs,
			CodeReviewStatusIDs: opts.CodeReviewStatusIDs,
		},
		Warnings: append([]Warning(nil), warnings...),
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
	var events []ReturnEvent
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
		if len(codeReview) > 0 {
			if d, ok := reworkAfter(iss.Changelog, ch.At, codeReview); ok {
				secs := d.Seconds()
				ev.ReworkSeconds = &secs
			}
		}
		events = append(events, ev)
	}
	return events
}

// reworkAfter measures the time from a return until the next transition into
// code-review, i.e. how long the developer took to hand the work back.
func reworkAfter(changes []Change, from time.Time, codeReview map[string]bool) (time.Duration, bool) {
	for _, ch := range changes {
		if !isStatusChange(ch) || !ch.At.After(from) {
			continue
		}
		if codeReview[ch.To] {
			return ch.At.Sub(from), true
		}
	}
	return 0, false
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
		dr := DeveloperReport{AccountID: id, DisplayName: names[id]}
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
			dr.AvgReturnsPerIssue = round2(float64(dr.Returns) / float64(dr.Issues))
		}
		if xs := rework[id]; len(xs) > 0 {
			var sum float64
			for _, x := range xs {
				sum += x
			}
			avg := round2(sum / float64(len(xs)))
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
		t.ReworkRate = round2(float64(t.SubTicketsWithReturn) / float64(t.SubTickets))
	}
	return t
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// storyIndex keeps stories in first-seen order until the final sort.
type storyIndex struct {
	byKey map[string]*StoryReport
	order []string
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
	st := &StoryReport{Key: key}
	s.byKey[key] = st
	s.order = append(s.order, key)
	return st
}

func (s *storyIndex) finish() []StoryReport {
	out := make([]StoryReport, 0, len(s.order))
	for _, k := range s.order {
		st := s.byKey[k]
		if st.SubTicketCount > 0 {
			st.ReworkRate = round2(float64(st.SubTicketsWithReturn) / float64(st.SubTicketCount))
		}
		sort.Strings(st.SubTicketKeys)
		if st.SubTicketKeys == nil {
			st.SubTicketKeys = []string{}
		}
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
