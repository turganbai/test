package analytics

import "time"

// Options parameterises Compute. Everything here comes from configuration or
// the command line; Compute itself reads nothing else.
type Options struct {
	// DeveloperFieldID is the custom field id holding the developer, e.g.
	// "customfield_10050". Changelog entries for it are matched on this id.
	DeveloperFieldID string
	// ReturnedStatusIDs are the status *ids* that count as "returned".
	ReturnedStatusIDs []string
	// CodeReviewStatusIDs are the status ids that end a rework turnaround.
	// Empty disables the turnaround metric.
	CodeReviewStatusIDs []string
	// Mode selects current vs point-in-time developer attribution.
	Mode AttributionMode
	// From/To bound the reporting period on the transition timestamp.
	// Zero means unbounded. From is inclusive, To is exclusive.
	From time.Time
	To   time.Time
	// StoryKeys, when set, seeds the per-story section so that a story with no
	// sub-tickets still shows up (with zeroes) instead of vanishing.
	StoryKeys []string
	// StatusNames maps status id -> current name, for display only.
	StatusNames map[string]string
}

// ReturnEvent is one transition into a "returned" status.
type ReturnEvent struct {
	IssueKey string    `json:"issueKey"`
	At       time.Time `json:"at"`

	FromStatusID   string `json:"fromStatusId,omitempty"`
	FromStatusName string `json:"fromStatusName,omitempty"`
	ToStatusID     string `json:"toStatusId"`
	ToStatusName   string `json:"toStatusName,omitempty"`

	// ReturnedBy is the changelog author, i.e. the QA engineer. Informational
	// only: the metric is never counted against this person.
	ReturnedBy User `json:"returnedBy"`
	// AttributedTo is the developer the return is counted against.
	AttributedTo User `json:"attributedTo"`
	// ViaAssigneeFallback records that AttributedTo came from the assignee
	// because the Developer field was empty.
	ViaAssigneeFallback bool `json:"viaAssigneeFallback"`
	// Unattributed records that neither Developer nor assignee was set.
	Unattributed bool `json:"unattributed"`

	// ReworkSeconds is the time until the next transition into code-review,
	// nil when unknown or when the metric is disabled.
	ReworkSeconds *float64 `json:"reworkSeconds,omitempty"`
}

// IssueReport is the per sub-ticket view.
type IssueReport struct {
	Key                 string        `json:"key"`
	Summary             string        `json:"summary,omitempty"`
	ParentKey           string        `json:"parentKey,omitempty"`
	Returns             int           `json:"returns"`
	Developer           User          `json:"developer"`
	ViaAssigneeFallback bool          `json:"viaAssigneeFallback"`
	Events              []ReturnEvent `json:"events"`
}

// StoryReport aggregates the sub-tickets of one story.
type StoryReport struct {
	Key                  string   `json:"key"`
	Summary              string   `json:"summary,omitempty"`
	SubTicketCount       int      `json:"subTicketCount"`
	SubTicketsWithReturn int      `json:"subTicketsWithReturn"`
	Returns              int      `json:"returns"`
	ReworkRate           float64  `json:"reworkRate"`
	MaxReturns           int      `json:"maxReturns"`
	MaxReturnsIssueKey   string   `json:"maxReturnsIssueKey,omitempty"`
	SubTicketKeys        []string `json:"subTicketKeys"`
}

// Distribution is the histogram that the average hides.
type Distribution struct {
	Zero      int `json:"0"`
	One       int `json:"1"`
	Two       int `json:"2"`
	ThreePlus int `json:"3+"`
}

// DeveloperReport is the per-developer view — the quality metric this tool
// exists for.
type DeveloperReport struct {
	AccountID          string       `json:"accountId"`
	DisplayName        string       `json:"displayName,omitempty"`
	Issues             int          `json:"issues"`
	Returns            int          `json:"returns"`
	AvgReturnsPerIssue float64      `json:"avgReturnsPerIssue"`
	Distribution       Distribution `json:"distribution"`
	IssueKeys          []string     `json:"issueKeys"`
	// AvgReworkSeconds is nil when no turnaround could be measured.
	AvgReworkSeconds *float64 `json:"avgReworkSeconds,omitempty"`
}

// Totals is the headline summary.
type Totals struct {
	SubTickets           int     `json:"subTickets"`
	Stories              int     `json:"stories"`
	Returns              int     `json:"returns"`
	SubTicketsWithReturn int     `json:"subTicketsWithReturn"`
	ReworkRate           float64 `json:"reworkRate"`
	Developers           int     `json:"developers"`
}

// Params echoes what the report was computed with, so a stored JSON file is
// self-describing.
type Params struct {
	Mode                AttributionMode `json:"attributionMode"`
	DeveloperFieldID    string          `json:"developerFieldId"`
	ReturnedStatusIDs   []string        `json:"returnedStatusIds"`
	CodeReviewStatusIDs []string        `json:"codeReviewStatusIds,omitempty"`
	From                *time.Time      `json:"from,omitempty"`
	To                  *time.Time      `json:"to,omitempty"`
	JQL                 string          `json:"jql,omitempty"`
	// Developers echoes a -developer filter. Empty means the whole team.
	Developers []string `json:"developers,omitempty"`
}

// Report is the whole output document.
type Report struct {
	GeneratedAt time.Time         `json:"generatedAt"`
	Params      Params            `json:"params"`
	Totals      Totals            `json:"totals"`
	Developers  []DeveloperReport `json:"developers"`
	Stories     []StoryReport     `json:"stories"`
	Issues      []IssueReport     `json:"issues"`
	Warnings    []Warning         `json:"warnings"`
}
