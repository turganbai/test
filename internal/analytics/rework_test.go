package analytics

import (
	"math/rand"
	"testing"
	"time"
)

const (
	inProgress = "10001"
	inReview   = "10002"
	returnedID = "10008"
)

func reworkOpts() Options {
	return Options{
		ReturnedStatusIDs:   []string{returnedID},
		CodeReviewStatusIDs: []string{inReview},
	}
}

func status(at time.Time, to string) Change {
	return Change{At: at, FieldID: StatusFieldID, From: inProgress, To: to}
}

// reworkOf returns the ReworkSeconds of each event, in event order, with -1
// standing in for "not measured".
func reworkOf(events []ReturnEvent) []float64 {
	out := make([]float64, 0, len(events))
	for _, ev := range events {
		if ev.ReworkSeconds == nil {
			out = append(out, -1)
			continue
		}
		out = append(out, *ev.ReworkSeconds)
	}
	return out
}

// Each return must be measured to the *nearest* following code-review
// transition, not to the first one the scan happens to reach.
func TestReworkAfter_EachReturnMapsToTheNearestFollowingReview(t *testing.T) {
	base := ts(t, "2026-09-10T00:00:00Z")
	h := func(n int) time.Time { return base.Add(time.Duration(n) * time.Hour) }

	iss := Issue{Key: "KAN-3", ParentKey: "KAN-10", Changelog: []Change{
		status(h(1), returnedID), // -> review at 3h  => 2h
		status(h(3), inReview),
		status(h(4), returnedID), // -> review at 9h  => 5h
		status(h(9), inReview),
		status(h(10), returnedID), // no review afterwards => not measured
	}}

	events := returnEvents(iss, reworkOpts(), idSet([]string{returnedID}), idSet([]string{inReview}))
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	want := []float64{2 * 3600, 5 * 3600, -1}
	got := reworkOf(events)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d rework = %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

// The ordering fix: a shuffled changelog must produce exactly what the sorted
// one does. The old linear scan returned whichever match it walked past first.
func TestReworkAfter_UnsortedChangelogMatchesSorted(t *testing.T) {
	base := ts(t, "2026-09-10T00:00:00Z")
	h := func(n int) time.Time { return base.Add(time.Duration(n) * time.Hour) }

	sorted := []Change{
		status(h(1), returnedID),
		status(h(2), inReview),
		status(h(5), returnedID),
		status(h(6), inReview),
		status(h(8), inReview),
	}
	opts, ret, cr := reworkOpts(), idSet([]string{returnedID}), idSet([]string{inReview})
	want := reworkOf(returnEvents(Issue{Key: "K", Changelog: sorted}, opts, ret, cr))

	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		shuffled := append([]Change(nil), sorted...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		got := reworkOf(returnEvents(Issue{Key: "K", Changelog: shuffled}, opts, ret, cr))
		if len(got) != len(want) {
			t.Fatalf("trial %d: %d events, want %d", trial, len(got), len(want))
		}
		// Events follow changelog order, so compare as multisets of durations.
		counts := map[float64]int{}
		for _, v := range want {
			counts[v]++
		}
		for _, v := range got {
			counts[v]--
		}
		for v, n := range counts {
			if n != 0 {
				t.Fatalf("trial %d: rework %v off by %d (got %v, want %v)", trial, v, n, got, want)
			}
		}
	}
}

// A review transition stamped at the same instant as the return does not
// count: the window is strictly after.
func TestReworkAfter_SimultaneousReviewDoesNotCount(t *testing.T) {
	base := ts(t, "2026-09-10T00:00:00Z")
	iss := Issue{Key: "K", Changelog: []Change{
		status(base, returnedID),
		status(base, inReview),
		status(base.Add(time.Hour), inReview),
	}}
	events := returnEvents(iss, reworkOpts(), idSet([]string{returnedID}), idSet([]string{inReview}))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if got := reworkOf(events)[0]; got != 3600 {
		t.Errorf("rework = %v, want the review an hour later (3600), not the simultaneous one", got)
	}
}

// No code-review ids configured disables the metric outright.
func TestReworkAfter_EmptyCodeReviewSetDisablesTheMetric(t *testing.T) {
	base := ts(t, "2026-09-10T00:00:00Z")
	iss := Issue{Key: "K", Changelog: []Change{
		status(base, returnedID),
		status(base.Add(time.Hour), inReview),
	}}
	opts := Options{ReturnedStatusIDs: []string{returnedID}} // no CodeReviewStatusIDs

	events := returnEvents(iss, opts, idSet([]string{returnedID}), idSet(nil))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].ReworkSeconds != nil {
		t.Errorf("ReworkSeconds = %v, want nil when no code-review statuses are configured", *events[0].ReworkSeconds)
	}
}

// An issue that never reached code review is the len(crTimes) == 0 path.
func TestReworkAfter_IssueWithNoReviewTransitions(t *testing.T) {
	base := ts(t, "2026-09-10T00:00:00Z")
	iss := Issue{Key: "K", Changelog: []Change{
		status(base, returnedID),
		{At: base.Add(time.Hour), FieldID: "assignee", To: "dev-a"},
	}}
	events := returnEvents(iss, reworkOpts(), idSet([]string{returnedID}), idSet([]string{inReview}))
	if len(events) != 1 || events[0].ReworkSeconds != nil {
		t.Errorf("events = %+v, want one return with no rework measured", events)
	}
}

// codeReviewTimes must ignore non-status changes that happen to carry a
// code-review id in their "to" field.
func TestCodeReviewTimes_OnlyStatusChanges(t *testing.T) {
	base := ts(t, "2026-09-10T00:00:00Z")
	got := codeReviewTimes([]Change{
		{At: base.Add(2 * time.Hour), FieldID: StatusFieldID, To: inReview},
		{At: base, FieldID: "customfield_10043", To: inReview}, // not a status change
		{At: base.Add(time.Hour), FieldID: StatusFieldID, To: inReview},
	}, idSet([]string{inReview}))

	if len(got) != 2 {
		t.Fatalf("times = %v, want only the two status changes", got)
	}
	if !got[0].Before(got[1]) {
		t.Errorf("times = %v, want ascending", got)
	}
}
