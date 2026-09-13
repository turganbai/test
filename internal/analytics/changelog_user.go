package analytics

import "strings"

// ParseUserValue turns one side of a changelog item for a user field into a
// User. It is the single place that knows how Jira writes a user into a
// changelog, and both layers go through it: the DTO mapping normalizes the
// values as it flattens the histories, and valueAt parses them again when it
// replays the field. That is deliberate — the function is idempotent, an
// already-normalized value having nothing left to strip — and it is what stops
// the two layers from drifting apart on a format only one of them knows.
//
// A single-user picker writes the bare value: id "6188ce54cc2d7c007153a233",
// name "azamat". A multi-user picker wraps both in brackets, one entry or
// several: "[557058:fe14ad75-…]" / "[Турганбай С]", "[id1, id2]" /
// "[Alice, Bob]".
//
// When the list names several people the first entry wins. The metric counts a
// return against exactly one developer, and the current-value path in the DTO
// mapping already reduces a multi-user field the same way, so anything else
// would make one issue resolve to a real person under ModeCurrent and to
// somebody different — or to nobody — under ModeAtTransition. The ambiguity is
// not swallowed: the DTO mapping raises developer_field_ambiguous for it.
//
// The id list is authoritative because an account id never contains a comma;
// the name is split only once the ids prove there is more than one user, so a
// lone developer called "Doe, John" survives intact. Beyond that the name is
// best-effort: every comparison downstream is on the id.
//
// Active is left unset. A changelog records who a field pointed at, never
// whether that account was enabled at the time.
func ParseUserValue(id, name string) User {
	inner, ok := unbracket(id)
	if !ok {
		return User{AccountID: id, DisplayName: name}
	}
	ids := strings.Split(inner, ",")

	if n, ok := unbracket(name); ok {
		name = n
	}
	if len(ids) > 1 {
		if i := strings.IndexByte(name, ','); i >= 0 {
			name = name[:i]
		}
	}
	return User{
		AccountID:   strings.TrimSpace(ids[0]),
		DisplayName: strings.TrimSpace(name),
	}
}

// unbracket strips one layer of [...] and says whether it was there.
func unbracket(s string) (string, bool) {
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return s[1 : len(s)-1], true
	}
	return s, false
}
