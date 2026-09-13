// Package analytics contains the domain model and the pure aggregation over
// Jira issues and their changelogs. It knows nothing about HTTP: everything it
// needs arrives as plain values through IssueFetcher.
package analytics

import (
	"context"
	"fmt"
	"time"
)

// AttributionMode selects how a return is attributed to a developer.
type AttributionMode string

const (
	// ModeCurrent attributes by the Developer field's value as it is today.
	ModeCurrent AttributionMode = "current"
	// ModeAtTransition reconstructs the Developer field's value at the moment
	// of each transition into "returned" by replaying the changelog backwards.
	ModeAtTransition AttributionMode = "at_transition"
)

// ParseAttributionMode validates a configured mode string.
func ParseAttributionMode(s string) (AttributionMode, error) {
	switch AttributionMode(s) {
	case ModeCurrent:
		return ModeCurrent, nil
	case ModeAtTransition:
		return ModeAtTransition, nil
	default:
		return "", fmt.Errorf("unknown attribution mode %q (want %q or %q)", s, ModeCurrent, ModeAtTransition)
	}
}

// StatusFieldID is the stable id of the status field. The localized display
// name (Change.Field) differs per language, the id does not.
const StatusFieldID = "status"

// UnattributedAccountID is the synthetic bucket for returns that could not be
// attributed to anybody (Developer field empty and no assignee).
const UnattributedAccountID = "(unattributed)"

// User is a Jira account as far as this tool cares. DisplayName is empty for
// deleted or anonymized accounts, so never key on it.
type User struct {
	AccountID   string `json:"accountId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Active      bool   `json:"active"`
}

// IsZero reports whether the user carries no identity at all.
func (u User) IsZero() bool { return u.AccountID == "" }

// Label is a human-readable name that degrades gracefully for deleted users.
func (u User) Label() string {
	switch {
	case u.DisplayName != "":
		return u.DisplayName
	case u.AccountID != "":
		return u.AccountID
	default:
		return "(unknown)"
	}
}

// Change is one item of one changelog entry, flattened: Jira nests items inside
// histories, but every item carries its history's author and timestamp anyway.
type Change struct {
	At     time.Time `json:"at"`
	Author User      `json:"author"`
	// Field is the localized display name. It is a fallback identifier, not
	// decoration: changelog rows written before Jira added fieldId carry only
	// this. It is trusted for that purpose only for the fields where the name
	// and the id are known to coincide — see isFieldChange.
	Field string `json:"field"`
	// FieldID is the stable id and the first choice for matching. Absent on
	// old rows, which is why Field exists.
	FieldID    string `json:"fieldId"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	FromString string `json:"fromString,omitempty"`
	ToString   string `json:"toString,omitempty"`
}

// Issue is a sub-ticket (or a story) with its full changelog attached.
type Issue struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Summary       string `json:"summary"`
	ParentKey     string `json:"parentKey,omitempty"`
	ParentSummary string `json:"parentSummary,omitempty"`

	// Developer is the current value of the configured Developer custom field.
	Developer User `json:"developer"`
	// DeveloperFieldPresent distinguishes "field exists but is empty" from
	// "field was not returned at all" (wrong field id, or not on the screen).
	DeveloperFieldPresent bool `json:"developerFieldPresent"`
	// Assignee is the current assignee, used only as a fallback.
	Assignee User `json:"assignee"`

	// Changelog holds every change we could retrieve.
	//
	// It MUST be ascending by At. This is a contract, not a description: the
	// replay in valueAt walks it backwards and stops at the first change at or
	// before the instant it is reconstructing, so an out-of-order entry makes
	// it stop early and return a partially unwound value — a real person, just
	// not the right one, with nothing to signal it. Whoever builds an Issue
	// honours this; collect.flatten sorts once, after the changelog top-up
	// pages are merged, and Compute normalizes defensively because it is
	// exported and cannot see where its input came from.
	Changelog []Change `json:"-"`
}

// FieldMissingError reports that a field we need was absent from an issue.
type FieldMissingError struct {
	IssueKey string
	FieldID  string
}

func (e *FieldMissingError) Error() string {
	return fmt.Sprintf("issue %s: field %q missing from response", e.IssueKey, e.FieldID)
}

// Warning is a non-fatal problem. Partial failures land here instead of
// aborting the whole report.
type Warning struct {
	IssueKey string `json:"issueKey,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Warning codes.
const (
	WarnFetchFailed = "fetch_failed"
	// WarnDeveloperFieldMissing is a Developer field the response did not
	// carry at all: a wrong field id, or a field not on the issue's screen.
	WarnDeveloperFieldMissing = "developer_field_missing"
	// WarnDeveloperFieldUnreadable is a Developer field that is present and
	// non-null but does not decode as a user — a field id pointing at a text,
	// select or number field rather than a user picker.
	//
	// It is a separate code from WarnDeveloperFieldMissing because the two can
	// never describe the same issue: the decode is only attempted once the
	// field is found present and non-null, so this code implies the field
	// exists, and WarnDeveloperFieldMissing implies it does not. Reporting
	// both under one code said "missing" about a field that demonstrably
	// exists, and left a misconfigured id and a wrong field type
	// indistinguishable in the JSON.
	WarnDeveloperFieldUnreadable = "developer_field_unreadable"
	// WarnDeveloperFieldAmbiguous is a multi-user Developer field naming more
	// than one person: the return counts against one of them, and which one is
	// not something the data can settle.
	WarnDeveloperFieldAmbiguous = "developer_field_ambiguous"
	WarnAssigneeFallback        = "assignee_fallback"
	WarnNoAttribution           = "no_attribution"
	WarnChangelogTruncated      = "changelog_truncated"
	// WarnChangelogUnparseable is one history entry whose timestamp did not
	// parse. The entry is skipped and the rest of the issue is kept, so it
	// reads like WarnChangelogTruncated — counts may be low — but the cause is
	// a malformed value in a response that arrived intact, not a response we
	// could not finish fetching. Distinct from WarnFetchFailed for the same
	// reason: nothing failed at the transport.
	WarnChangelogUnparseable = "changelog_unparseable"
)

// IssueFetcher is declared here, on the consumer side: the domain states what
// it needs, and internal/collect supplies it on top of internal/jira.
type IssueFetcher interface {
	// FetchIssues returns the issues matching jql with their changelogs.
	// Per-issue problems are returned as warnings, not errors; only a failure
	// that makes the whole result meaningless returns a non-nil error.
	FetchIssues(ctx context.Context, jql string) ([]Issue, []Warning, error)
}
