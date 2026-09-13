package analytics

import (
	"reflect"
	"testing"
)

// finish iterates a map, and Go randomises map iteration order, so the report's
// story order has to come entirely from the final sort. Same input, same output,
// every run — otherwise two runs over identical data would produce diffs.
//
// The sort is a total order (returns desc, then key asc over keys unique by
// construction), which is what makes this hold without tracking insertion
// order separately.
func TestStoryOrderStable(t *testing.T) {
	issues := []Issue{
		{Key: "A-1", ParentKey: "STORY-A", Changelog: []Change{{At: ts(t, "2026-09-01T10:00:00Z"), FieldID: StatusFieldID, To: "10008"}}},
		{Key: "B-1", ParentKey: "STORY-B", Changelog: []Change{{At: ts(t, "2026-09-01T10:00:00Z"), FieldID: StatusFieldID, To: "10008"}}},
		{Key: "B-2", ParentKey: "STORY-B"},
		{Key: "C-1", ParentKey: "STORY-C"},
		{Key: "D-1", ParentKey: "STORY-D"},
		{Key: "E-1", ParentKey: "STORY-E"},
	}
	opts := Options{ReturnedStatusIDs: []string{"10008"}, StoryKeys: []string{"STORY-Z", "STORY-Y"}}

	first := keysOf(Compute(issues, opts, nil))
	for i := 0; i < 200; i++ {
		if got := keysOf(Compute(issues, opts, nil)); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d gave %v, first run gave %v", i, got, first)
		}
	}
	t.Logf("stable across 200 runs: %v", first)
}

func keysOf(rep Report) []string {
	out := make([]string, 0, len(rep.Stories))
	for _, s := range rep.Stories {
		out = append(out, s.Key)
	}
	return out
}
