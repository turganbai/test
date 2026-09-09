package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lookup is the LookupEnv counterpart of env(): a key present in the map is
// "set", even when its value is empty.
func lookup(m map[string]string) LookupEnv {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestParseEnvFile(t *testing.T) {
	const file = "" +
		"# a comment\n" +
		"\n" +
		"JIRA_BASE_URL=https://example.atlassian.net\n" +
		"  export JIRA_EMAIL = someone@example.com   \n" +
		"JIRA_API_TOKEN=\"a token with spaces, a # and an \\\"escape\\\"\"\n" +
		"JIRA_RETURNED_STATUS_NAMES='Возвращено,Returned'\n" +
		"JIRA_TIMEOUT=90s   # trailing comment\n" +
		"EMPTY=\n" +
		"EMPTY_THEN_COMMENT=   # left at the default\n" +
		"HASH_IN_VALUE=abc#def\n" +
		"MULTILINE=\"one\\ntwo\"\n" +
		"JIRA_CONCURRENCY=1\n" +
		"JIRA_CONCURRENCY=12\n"

	got, err := ParseEnvFile(strings.NewReader(file))
	if err != nil {
		t.Fatalf("ParseEnvFile: %v", err)
	}
	want := map[string]string{
		"JIRA_BASE_URL":              "https://example.atlassian.net",
		"JIRA_EMAIL":                 "someone@example.com",
		"JIRA_API_TOKEN":             `a token with spaces, a # and an "escape"`,
		"JIRA_RETURNED_STATUS_NAMES": "Возвращено,Returned",
		"JIRA_TIMEOUT":               "90s",
		"EMPTY":                      "",
		"EMPTY_THEN_COMMENT":         "",
		"HASH_IN_VALUE":              "abc#def",
		"MULTILINE":                  "one\ntwo",
		"JIRA_CONCURRENCY":           "12", // the later line wins
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parsed %d keys, want %d: %v", len(got), len(want), got)
	}
}

func TestParseEnvFile_ReportsEveryBadLineAtOnce(t *testing.T) {
	_, err := ParseEnvFile(strings.NewReader("JIRA_BASE_URL\n1BAD=x\nOK=\"unterminated\n"))
	if err == nil {
		t.Fatal("ParseEnvFile accepted a malformed file")
	}
	for _, want := range []string{"line 1", "line 2", "line 3", "unterminated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestFileEnv_RealEnvironmentWins(t *testing.T) {
	getenv := FileEnv(lookup(map[string]string{"JIRA_EMAIL": "from-shell"}),
		map[string]string{"JIRA_EMAIL": "from-file", "JIRA_BASE_URL": "from-file"})
	if v := getenv("JIRA_EMAIL"); v != "from-shell" {
		t.Errorf("JIRA_EMAIL = %q, want the exported value to win", v)
	}
	if v := getenv("JIRA_BASE_URL"); v != "from-file" {
		t.Errorf("JIRA_BASE_URL = %q, want the file value", v)
	}
}

// Exporting FOO= is a deliberate "there is no value here" — a CI job without
// the secret, say. Falling through to a stale .env would run with the wrong
// credentials instead of failing the required-variable check.
func TestFileEnv_ExplicitlyEmptyVariableStillWins(t *testing.T) {
	getenv := FileEnv(lookup(map[string]string{"JIRA_API_TOKEN": ""}),
		map[string]string{"JIRA_API_TOKEN": "stale-file-token"})
	if v := getenv("JIRA_API_TOKEN"); v != "" {
		t.Errorf("JIRA_API_TOKEN = %q, want the exported empty value to win", v)
	}
	if _, err := Load(FileEnv(lookup(map[string]string{"JIRA_API_TOKEN": ""}), nil)); err == nil ||
		!strings.Contains(err.Error(), "JIRA_API_TOKEN is required") {
		t.Errorf("err = %v, want the required-variable complaint", err)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jira.env")
	body := "JIRA_BASE_URL=https://example.atlassian.net\n" +
		"JIRA_EMAIL=someone@example.com\n" +
		"JIRA_API_TOKEN=secret-token\n" +
		"JIRA_DEVELOPER_FIELD_ID=customfield_10050\n" +
		"JIRA_RETURNED_STATUS_IDS=10007,10008\n" +
		"JIRA_CONCURRENCY=7\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(lookup(nil), path, ScopeReport)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.BaseURL != "https://example.atlassian.net" || cfg.Concurrency != 7 {
		t.Errorf("cfg = %s, want the file values", cfg)
	}
	if len(cfg.ReturnedStatusIDs) != 2 {
		t.Errorf("returned status ids = %v", cfg.ReturnedStatusIDs)
	}
}

// A named file that is not there is a typo, not a deployment without a .env.
func TestLoadFile_NamedFileMustExist(t *testing.T) {
	_, err := LoadFile(lookup(valid()), filepath.Join(t.TempDir(), "absent.env"), ScopeReport)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadFile_MissingDefaultIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // no .env here
	cfg, err := LoadFile(lookup(valid()), "", ScopeReport)
	if err != nil {
		t.Fatalf("LoadFile with no .env present: %v", err)
	}
	if cfg.Email != "someone@example.com" {
		t.Errorf("cfg = %s, want the environment values", cfg)
	}
}

func TestLoadFile_DefaultFileIsPickedUp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultEnvFile),
		[]byte("JIRA_TIMEOUT=2m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg, err := LoadFile(lookup(valid()), "", ScopeReport)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Timeout.String() != "2m0s" {
		t.Errorf("timeout = %s, want the .env value 2m", cfg.Timeout)
	}
}

// -statuses is the command that tells the reader which returned status ids
// exist, so ScopeConnect must load with none of them configured — otherwise the
// only way to discover the ids is to already know them.
func TestLoadFile_ConnectScopeSkipsReportVariables(t *testing.T) {
	t.Chdir(t.TempDir()) // no .env here
	env := lookup(map[string]string{
		"JIRA_BASE_URL":  "https://example.atlassian.net",
		"JIRA_EMAIL":     "someone@example.com",
		"JIRA_API_TOKEN": "secret-token",
	})

	cfg, err := LoadFile(env, "", ScopeConnect)
	if err != nil {
		t.Fatalf("LoadFile(ScopeConnect): %v", err)
	}
	if cfg.BaseURL != "https://example.atlassian.net" || cfg.Timeout != DefaultTimeout {
		t.Errorf("cfg = %s, want the credentials and the transport defaults", cfg)
	}

	// The same environment is still incomplete for a report.
	if _, err := LoadFile(env, "", ScopeReport); err == nil ||
		!strings.Contains(err.Error(), "JIRA_RETURNED_STATUS_IDS is required") {
		t.Errorf("err = %v, want ScopeReport to still demand the returned statuses", err)
	}
}
