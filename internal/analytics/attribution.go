package analytics

import "time"

// assigneeFieldID is the stable id of the assignee field, used for the
// fallback replay.
const assigneeFieldID = "assignee"

// attribution is the outcome of deciding whom a return counts against.
type attribution struct {
	user         User
	viaAssignee  bool
	unattributed bool
}

// resolve decides which developer a return at time t is counted against.
//
// The changelog author is deliberately not consulted: the transition into
// "returned" is performed by QA, so attributing to the author would measure the
// QA engineer instead of the developer.
//
// In ModeAtTransition both the Developer field and the assignee fallback are
// replayed to t, so the two sources can never be mixed across time.
func resolve(iss Issue, developerFieldID string, mode AttributionMode, t time.Time) attribution {
	dev := iss.Developer
	assignee := iss.Assignee
	if mode == ModeAtTransition {
		dev = valueAt(iss.Changelog, developerFieldID, iss.Developer, t)
		assignee = valueAt(iss.Changelog, assigneeFieldID, iss.Assignee, t)
	}

	if !dev.IsZero() {
		return attribution{user: dev}
	}
	if !assignee.IsZero() {
		return attribution{user: assignee, viaAssignee: true}
	}
	return attribution{
		user:         User{AccountID: UnattributedAccountID, DisplayName: UnattributedAccountID},
		unattributed: true,
	}
}

// valueAt reconstructs a single-user field's value at time t by replaying the
// changelog backwards from the field's current value.
//
// changes must be ascending by time — the contract on Issue.Changelog, which
// Compute normalizes to before any of this runs. Walking backwards, every
// change that happened strictly after t is undone by taking its "from" side; the first
// change at or before t stops the walk, because it is already reflected in the
// value we are holding. A change with exactly the same timestamp as the
// transition is therefore treated as already applied.
func valueAt(changes []Change, fieldID string, current User, t time.Time) User {
	if fieldID == "" {
		return current
	}
	val := current
	for i := len(changes) - 1; i >= 0; i-- {
		ch := changes[i]
		if !isFieldChange(ch, fieldID) {
			continue
		}
		if !ch.At.After(t) {
			break
		}
		// Undo this change by taking its "from" side. The bare accountId in
		// from/to is the single-user picker's shape only — a multi-user field
		// wraps its value in brackets, so the raw strings are never used
		// directly; ParseUserValue is what both this replay and the DTO
		// mapping read them through. A cleared field yields an empty "from",
		// which correctly collapses to a zero User and triggers the assignee
		// fallback for that point in time.
		val = ParseUserValue(ch.From, ch.FromString)
	}
	return val
}
