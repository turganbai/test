package analytics

import (
	"testing"
	"time"
)

// The real KAN-3 shape: two returns thirty minutes apart, one In Progress
// between them that never reached review, and a single transition into review
// three days later.
//
// Both returns used to measure against that one endpoint — 83.7h and 83.2h —
// so one stretch of calendar was averaged in twice and the developer's mean
// described a period they had only lived through once.
func kan3Overlap(t *testing.T) Issue {
	t.Helper()
	return Issue{
		Key: "KAN-3", ParentKey: "KAN-10",
		Developer: User{AccountID: "turganbai", DisplayName: "Turganbai"},
		Changelog: []Change{
			{At: ts(t, "2026-09-10T12:18:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			{At: ts(t, "2026-09-10T12:30:00Z"), FieldID: StatusFieldID, From: statusReturn, To: "10001"},
			{At: ts(t, "2026-09-10T12:48:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			{At: ts(t, "2026-09-13T23:59:00Z"), FieldID: StatusFieldID, From: statusReturn, To: inReview},
		},
	}
}

func overlapOpts() Options {
	return Options{
		ReturnedStatusIDs:   []string{statusReturn},
		CodeReviewStatusIDs: []string{inReview},
	}
}

// A round of feedback superseded by a further return never ended in a
// handback, so it yields no sample rather than a duration.
func TestRework_SupersededReturnYieldsNoSample(t *testing.T) {
	rep := Compute([]Issue{kan3Overlap(t)}, overlapOpts(), nil)

	evs := rep.Issues[0].Events
	if len(evs) != 2 {
		t.Fatalf("events = %d, want both returns counted", len(evs))
	}
	if evs[0].ReworkSeconds != nil {
		t.Errorf("first return measured %v; it was superseded before reaching review",
			time.Duration(*evs[0].ReworkSeconds)*time.Second)
	}
	if evs[1].ReworkSeconds == nil {
		t.Fatal("second return has no turnaround; it did reach review")
	}
	if want := (3*24 + 11) * time.Hour; time.Duration(*evs[1].ReworkSeconds)*time.Second < want {
		t.Errorf("second return = %vs, want the full stretch to review", *evs[1].ReworkSeconds)
	}

	// Both returns still count against the developer — only the turnaround
	// sample is withheld.
	if rep.Totals.Returns != 2 {
		t.Errorf("returns = %d, want both", rep.Totals.Returns)
	}
	d := rep.Developers[0]
	if d.Returns != 2 {
		t.Errorf("developer returns = %d, want 2", d.Returns)
	}
	if d.ReworkSamples != 1 {
		t.Errorf("rework samples = %d, want 1 — one round ended in a handback", d.ReworkSamples)
	}
	if *d.AvgReworkSeconds != *evs[1].ReworkSeconds {
		t.Errorf("average = %v, want the single sample %v", *d.AvgReworkSeconds, *evs[1].ReworkSeconds)
	}
}

// The ordinary case is untouched: one return, one review, one sample.
func TestRework_UnsupersededReturnIsUnchanged(t *testing.T) {
	iss := Issue{
		Key: "KAN-11", ParentKey: "KAN-10",
		Developer: User{AccountID: "azamat"},
		Changelog: []Change{
			{At: ts(t, "2026-09-10T12:00:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			{At: ts(t, "2026-09-10T17:00:00Z"), FieldID: StatusFieldID, From: statusReturn, To: inReview},
		},
	}
	rep := Compute([]Issue{iss}, overlapOpts(), nil)

	ev := rep.Issues[0].Events[0]
	if ev.ReworkSeconds == nil || *ev.ReworkSeconds != (5*time.Hour).Seconds() {
		t.Fatalf("rework = %+v, want 5h", ev.ReworkSeconds)
	}
	if rep.Developers[0].ReworkSamples != 1 {
		t.Errorf("samples = %d, want 1", rep.Developers[0].ReworkSamples)
	}
}

// A return that reaches review before the next return keeps its sample: the
// bound is the next return, not merely the existence of one.
func TestRework_ReviewBeforeTheNextReturnStillCounts(t *testing.T) {
	iss := Issue{
		Key: "KAN-12", ParentKey: "KAN-10",
		Developer: User{AccountID: "azamat"},
		Changelog: []Change{
			{At: ts(t, "2026-09-10T12:00:00Z"), FieldID: StatusFieldID, From: "10001", To: statusReturn},
			{At: ts(t, "2026-09-10T14:00:00Z"), FieldID: StatusFieldID, From: statusReturn, To: inReview},
			{At: ts(t, "2026-09-11T09:00:00Z"), FieldID: StatusFieldID, From: inReview, To: statusReturn},
			{At: ts(t, "2026-09-11T12:00:00Z"), FieldID: StatusFieldID, From: statusReturn, To: inReview},
		},
	}
	rep := Compute([]Issue{iss}, overlapOpts(), nil)

	evs := rep.Issues[0].Events
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2", len(evs))
	}
	for i, want := range []time.Duration{2 * time.Hour, 3 * time.Hour} {
		if evs[i].ReworkSeconds == nil {
			t.Fatalf("event %d has no turnaround, want %v", i, want)
		}
		if *evs[i].ReworkSeconds != want.Seconds() {
			t.Errorf("event %d = %vs, want %v", i, *evs[i].ReworkSeconds, want)
		}
	}
	if rep.Developers[0].ReworkSamples != 2 {
		t.Errorf("samples = %d, want both rounds measured", rep.Developers[0].ReworkSamples)
	}
}
