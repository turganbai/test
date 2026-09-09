package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"mcp/internal/analytics"
)

func env(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func valid() map[string]string {
	return map[string]string{
		"JIRA_BASE_URL":            "https://example.atlassian.net",
		"JIRA_EMAIL":               "someone@example.com",
		"JIRA_API_TOKEN":           "secret-token",
		"JIRA_DEVELOPER_FIELD_ID":  "customfield_10050",
		"JIRA_RETURNED_STATUS_IDS": "10007, 10008",
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AttributionMode != analytics.ModeCurrent {
		t.Errorf("mode = %q, want the %q default", cfg.AttributionMode, analytics.ModeCurrent)
	}
	if cfg.Concurrency != DefaultConcurrency || cfg.Timeout != DefaultTimeout {
		t.Errorf("concurrency/timeout = %d/%s, want %d/%s", cfg.Concurrency, cfg.Timeout, DefaultConcurrency, DefaultTimeout)
	}
	if len(cfg.ReturnedStatusIDs) != 2 || cfg.ReturnedStatusIDs[1] != "10008" {
		t.Errorf("returned status ids = %v, want the list trimmed and split", cfg.ReturnedStatusIDs)
	}
}

func TestLoad_Overrides(t *testing.T) {
	m := valid()
	m["JIRA_ATTRIBUTION_MODE"] = "at_transition"
	m["JIRA_CONCURRENCY"] = "12"
	m["JIRA_TIMEOUT"] = "90s"
	m["JIRA_CODE_REVIEW_STATUS_IDS"] = "10001"
	m["LOG_LEVEL"] = "debug"

	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AttributionMode != analytics.ModeAtTransition {
		t.Errorf("mode = %q", cfg.AttributionMode)
	}
	if cfg.Concurrency != 12 || cfg.Timeout != 90*time.Second {
		t.Errorf("concurrency/timeout = %d/%s", cfg.Concurrency, cfg.Timeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("log level = %v", cfg.LogLevel)
	}
	if len(cfg.CodeReviewStatusIDs) != 1 {
		t.Errorf("code review ids = %v", cfg.CodeReviewStatusIDs)
	}
}

func TestLoad_StatusNamesAcceptedInsteadOfIDs(t *testing.T) {
	m := valid()
	delete(m, "JIRA_RETURNED_STATUS_IDS")
	m["JIRA_RETURNED_STATUS_NAMES"] = "Returned, Возвращено"

	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.ReturnedStatusNames) != 2 {
		t.Errorf("names = %v, want both", cfg.ReturnedStatusNames)
	}
}

func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Load(env(map[string]string{
		"JIRA_ATTRIBUTION_MODE":   "whatever",
		"JIRA_DEVELOPER_FIELD_ID": "Developer",
		"JIRA_CONCURRENCY":        "0",
	}))
	if err == nil {
		t.Fatal("Load succeeded on an empty environment")
	}
	msg := err.Error()
	for _, want := range []string{
		"JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN",
		"customfield_NNNNN", "JIRA_RETURNED_STATUS_IDS",
		"JIRA_ATTRIBUTION_MODE", "JIRA_CONCURRENCY",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %q:\n%s", want, msg)
		}
	}
}

func TestLoad_RejectsStatusNameInTheIDsVariable(t *testing.T) {
	m := valid()
	m["JIRA_RETURNED_STATUS_IDS"] = "Returned"
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "must be numeric") {
		t.Errorf("err = %v, want a complaint that a name was put in the ids variable", err)
	}
}

// The token must never leak through the obvious accident: logging the config.
func TestConfig_StringRedactsToken(t *testing.T) {
	cfg, err := Load(env(valid()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.String()
	if strings.Contains(s, "secret-token") {
		t.Errorf("String() leaked the API token:\n%s", s)
	}
	if !strings.Contains(s, "[redacted]") {
		t.Errorf("String() = %s, want the token redacted", s)
	}
}
