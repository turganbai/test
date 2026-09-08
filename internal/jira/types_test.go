package jira

import (
	"encoding/json"
	"testing"
	"time"
)

func TestIssueFields_CustomFieldAccess(t *testing.T) {
	const body = `{
	  "summary": "s",
	  "assignee": {"accountId": "qa-1", "displayName": "Qa Engineer", "active": true},
	  "customfield_10050": {"accountId": "dev-gone", "displayName": "", "active": false},
	  "customfield_10051": null
	}`
	var f IssueFields
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if f.Assignee == nil || f.Assignee.AccountID != "qa-1" {
		t.Fatalf("assignee = %+v", f.Assignee)
	}
	if !f.Has("customfield_10050") || !f.Has("customfield_10051") {
		t.Error("Has: both custom fields were present in the payload")
	}
	if f.Has("customfield_99999") {
		t.Error("Has: reported a field that was not in the payload")
	}

	// A deleted account keeps its id and loses its name.
	dev, err := f.UserField("customfield_10050")
	if err != nil || dev == nil {
		t.Fatalf("UserField = %+v, %v", dev, err)
	}
	if dev.AccountID != "dev-gone" || dev.DisplayName != "" {
		t.Errorf("deleted user decoded as %+v", dev)
	}

	// Present-but-null and absent both yield no user, and neither is an error.
	for _, id := range []string{"customfield_10051", "customfield_99999"} {
		u, err := f.UserField(id)
		if err != nil || u != nil {
			t.Errorf("UserField(%s) = %+v, %v; want nil, nil", id, u, err)
		}
	}

	// A field of the wrong shape is an error, not a silent zero value.
	f.raw["customfield_10052"] = json.RawMessage(`"just a string"`)
	if _, err := f.UserField("customfield_10052"); err == nil {
		t.Error("UserField on a non-user field succeeded, want an error")
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"2026-08-02T09:00:00.000+0000", "2026-08-02T09:00:00Z"},
		{"2026-08-02T12:00:00.123+0300", "2026-08-02T09:00:00Z"},
		{"2026-08-02T09:00:00Z", "2026-08-02T09:00:00Z"},
	}
	for _, tc := range tests {
		got, err := ParseTime(tc.in)
		if err != nil {
			t.Errorf("ParseTime(%q): %v", tc.in, err)
			continue
		}
		if s := got.UTC().Format(time.RFC3339); s != tc.want {
			t.Errorf("ParseTime(%q) = %s, want %s", tc.in, s, tc.want)
		}
	}
	if _, err := ParseTime("yesterday"); err == nil {
		t.Error("ParseTime accepted nonsense")
	}
}

func TestPageOfChangelogs_Truncated(t *testing.T) {
	var nilPage *PageOfChangelogs
	if nilPage.Truncated() {
		t.Error("a nil changelog must not be reported as truncated")
	}
	full := &PageOfChangelogs{Total: 2, Histories: make([]Changelog, 2)}
	if full.Truncated() {
		t.Error("a complete changelog was reported as truncated")
	}
	short := &PageOfChangelogs{Total: 150, Histories: make([]Changelog, 100)}
	if !short.Truncated() {
		t.Error("expand=changelog truncation went undetected")
	}
}
