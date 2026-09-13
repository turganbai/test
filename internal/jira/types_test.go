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
	devs, err := f.UsersField("customfield_10050")
	if err != nil || len(devs) != 1 {
		t.Fatalf("UsersField = %+v, %v", devs, err)
	}
	if devs[0].AccountID != "dev-gone" || devs[0].DisplayName != "" {
		t.Errorf("deleted user decoded as %+v", devs[0])
	}

	// Present-but-null and absent both yield no user, and neither is an error.
	for _, id := range []string{"customfield_10051", "customfield_99999"} {
		u, err := f.UsersField(id)
		if err != nil || u != nil {
			t.Errorf("UsersField(%s) = %+v, %v; want nil, nil", id, u, err)
		}
	}

	// A field of the wrong shape is an error, not a silent zero value.
	f.raw["customfield_10052"] = json.RawMessage(`"just a string"`)
	if _, err := f.UsersField("customfield_10052"); err == nil {
		t.Error("UsersField on a non-user field succeeded, want an error")
	}
}

// Jira has both a single-user and a multi-user picker, and the field id does
// not say which one a site configured. The multi-user shape is an array, which
// used to fail to decode and silently cost the issue its developer.
func TestUsersField_MultiUserPicker(t *testing.T) {
	var f IssueFields
	if err := json.Unmarshal([]byte(`{
		"customfield_10043": [
			{"accountId": "dev-a", "displayName": "azamat", "active": true},
			{"accountId": "dev-b", "displayName": "Bob Dev", "active": true}
		],
		"customfield_10044": [{"accountId": "dev-a", "displayName": "azamat"}],
		"customfield_10045": []
	}`), &f); err != nil {
		t.Fatal(err)
	}

	devs, err := f.UsersField("customfield_10043")
	if err != nil {
		t.Fatalf("UsersField on an array: %v", err)
	}
	if len(devs) != 2 || devs[0].AccountID != "dev-a" || devs[1].AccountID != "dev-b" {
		t.Errorf("decoded %+v, want both users in order", devs)
	}

	// The common case: a multi-user field holding exactly one person.
	one, err := f.UsersField("customfield_10044")
	if err != nil || len(one) != 1 || one[0].DisplayName != "azamat" {
		t.Errorf("UsersField = %+v, %v; want the single user", one, err)
	}

	// An empty list is "nobody set it", not an error.
	none, err := f.UsersField("customfield_10045")
	if err != nil || len(none) != 0 {
		t.Errorf("UsersField on [] = %+v, %v; want no users and no error", none, err)
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
