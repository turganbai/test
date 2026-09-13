package jira

import (
	"encoding/json"
	"fmt"
	"time"
)

// SearchRequest is the body of POST /rest/api/3/search/jql
// (schema SearchAndReconcileRequestBean).
type SearchRequest struct {
	JQL           string   `json:"jql"`
	Fields        []string `json:"fields,omitempty"`
	Expand        string   `json:"expand,omitempty"`
	MaxResults    int      `json:"maxResults,omitempty"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

// SearchResponse is schema SearchAndReconcileResults. Note there is no total:
// pagination is a cursor, you loop until nextPageToken is empty.
type SearchResponse struct {
	Issues        []Issue `json:"issues"`
	NextPageToken string  `json:"nextPageToken"`
	IsLast        bool    `json:"isLast"`
}

// Issue is schema IssueBean, trimmed to what we use.
type Issue struct {
	ID        string            `json:"id"`
	Key       string            `json:"key"`
	Fields    IssueFields       `json:"fields"`
	Changelog *PageOfChangelogs `json:"changelog"`
}

// IssueFields decodes the known fields and keeps the raw map so that
// instance-specific custom fields can be read by id.
type IssueFields struct {
	Summary  string  `json:"summary"`
	Assignee *User   `json:"assignee"`
	Parent   *Parent `json:"parent"`

	raw map[string]json.RawMessage
}

// UnmarshalJSON keeps the raw representation alongside the typed fields.
func (f *IssueFields) UnmarshalJSON(b []byte) error {
	type alias IssueFields
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*f = IssueFields(a)
	return json.Unmarshal(b, &f.raw)
}

// Has reports whether the field was present in the response at all, which is
// different from being present but null.
func (f *IssueFields) Has(fieldID string) bool {
	_, ok := f.raw[fieldID]
	return ok
}

// UsersField decodes a custom field holding a user. Jira offers both a
// single-user and a multi-user picker and the field id alone does not say which
// one a site configured, so both shapes are accepted: a bare object, or an
// array of them. A multi-user field that happens to hold one user therefore
// reads back as a one-element slice rather than failing to decode.
//
// An absent, null or empty field yields no users and no error — that is the
// ordinary "nobody set it" case, which the caller answers with the assignee
// fallback.
func (f *IssueFields) UsersField(fieldID string) ([]User, error) {
	rawVal, ok := f.raw[fieldID]
	if !ok || string(rawVal) == "null" {
		return nil, nil
	}
	if isJSONArray(rawVal) {
		var us []User
		if err := json.Unmarshal(rawVal, &us); err != nil {
			return nil, fmt.Errorf("decode field %s as a list of users: %w", fieldID, err)
		}
		return us, nil
	}
	var u User
	if err := json.Unmarshal(rawVal, &u); err != nil {
		return nil, fmt.Errorf("decode field %s as user: %w", fieldID, err)
	}
	return []User{u}, nil
}

// isJSONArray reports whether raw is an array, ignoring leading whitespace.
func isJSONArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b == '['
		}
	}
	return false
}

// Parent is the link a sub-ticket has to its story. We rely on this rather than
// on the issue type, because sub-tickets are not necessarily of type Sub-task.
type Parent struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
	} `json:"fields"`
}

// User is schema UserDetails. DisplayName is empty for deleted accounts.
type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

// Status is schema StatusDetails.
type Status struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PageOfChangelogs is the changelog embedded in a search result
// (schema PageOfChangelogs). It caps out at ~100 histories and truncates
// silently, which is why total/maxResults matter.
type PageOfChangelogs struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	Histories  []Changelog `json:"histories"`
}

// Truncated reports whether the embedded changelog is missing entries.
func (p *PageOfChangelogs) Truncated() bool {
	if p == nil {
		return false
	}
	return p.Total > len(p.Histories)
}

// PageBeanChangelog is the paginated response of
// GET /rest/api/3/issue/{issueIdOrKey}/changelog.
type PageBeanChangelog struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	IsLast     bool        `json:"isLast"`
	Values     []Changelog `json:"values"`
}

// Changelog is one history entry: one author, one timestamp, many items.
type Changelog struct {
	ID      string          `json:"id"`
	Author  *User           `json:"author"`
	Created string          `json:"created"`
	Items   []ChangeDetails `json:"items"`
}

// CreatedAt parses Jira's timestamp format.
func (c Changelog) CreatedAt() (time.Time, error) { return ParseTime(c.Created) }

// ChangeDetails is one changed field inside a history entry.
type ChangeDetails struct {
	Field      string `json:"field"`
	FieldID    string `json:"fieldId"`
	FieldType  string `json:"fieldtype"`
	From       string `json:"from"`
	FromString string `json:"fromString"`
	To         string `json:"to"`
	ToString   string `json:"toString"`
}

var timeLayouts = []string{
	"2006-01-02T15:04:05.999-0700",
	"2006-01-02T15:04:05.999-07:00",
	time.RFC3339Nano,
}

// ParseTime parses the timestamp format Jira Cloud uses in changelogs.
func ParseTime(s string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse jira time %q", s)
}
