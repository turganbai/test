// Package config loads and validates the runtime configuration from the
// environment. The API token is read here and nowhere else, and never appears
// in logs or in the report.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/turganbay/mcp/internal/analytics"
)

// Defaults.
const (
	DefaultConcurrency = 5
	DefaultTimeout     = 60 * time.Second
)

// Config is the validated configuration.
type Config struct {
	BaseURL  string
	Email    string
	APIToken string

	DeveloperFieldID string

	// ReturnedStatusIDs is authoritative. ReturnedStatusNames is a convenience
	// resolved against the status catalog at startup when no ids are given.
	ReturnedStatusIDs   []string
	ReturnedStatusNames []string
	CodeReviewStatusIDs []string

	AttributionMode analytics.AttributionMode
	Concurrency     int
	Timeout         time.Duration
	LogLevel        slog.Level
}

// Getenv is the environment accessor, injectable for tests.
type Getenv func(string) string

// Load reads the configuration and reports every problem at once, so a
// misconfigured deployment is fixed in one pass rather than one variable per
// run.
func Load(getenv Getenv) (*Config, error) {
	cfg := &Config{
		BaseURL:             strings.TrimSpace(getenv("JIRA_BASE_URL")),
		Email:               strings.TrimSpace(getenv("JIRA_EMAIL")),
		APIToken:            getenv("JIRA_API_TOKEN"),
		DeveloperFieldID:    strings.TrimSpace(getenv("JIRA_DEVELOPER_FIELD_ID")),
		ReturnedStatusIDs:   splitList(getenv("JIRA_RETURNED_STATUS_IDS")),
		ReturnedStatusNames: splitList(getenv("JIRA_RETURNED_STATUS_NAMES")),
		CodeReviewStatusIDs: splitList(getenv("JIRA_CODE_REVIEW_STATUS_IDS")),
		Concurrency:         DefaultConcurrency,
		Timeout:             DefaultTimeout,
		LogLevel:            slog.LevelInfo,
	}

	var errs []error

	if cfg.BaseURL == "" {
		errs = append(errs, errors.New("JIRA_BASE_URL is required, e.g. https://example.atlassian.net"))
	} else if !strings.HasPrefix(cfg.BaseURL, "http://") && !strings.HasPrefix(cfg.BaseURL, "https://") {
		errs = append(errs, fmt.Errorf("JIRA_BASE_URL %q must include the scheme", cfg.BaseURL))
	}
	if cfg.Email == "" {
		errs = append(errs, errors.New("JIRA_EMAIL is required (basic auth for Jira Cloud is email + API token)"))
	}
	if cfg.APIToken == "" {
		errs = append(errs, errors.New("JIRA_API_TOKEN is required; create one at https://id.atlassian.com/manage-profile/security/api-tokens"))
	}
	if cfg.DeveloperFieldID == "" {
		errs = append(errs, errors.New(`JIRA_DEVELOPER_FIELD_ID is required, e.g. customfield_10050; find it with GET /rest/api/3/field`))
	} else if !strings.HasPrefix(cfg.DeveloperFieldID, "customfield_") {
		errs = append(errs, fmt.Errorf("JIRA_DEVELOPER_FIELD_ID %q does not look like a custom field id (expected customfield_NNNNN)", cfg.DeveloperFieldID))
	}
	if len(cfg.ReturnedStatusIDs) == 0 && len(cfg.ReturnedStatusNames) == 0 {
		errs = append(errs, errors.New("JIRA_RETURNED_STATUS_IDS is required (comma-separated status ids); JIRA_RETURNED_STATUS_NAMES may be used instead and is resolved at startup"))
	}
	for _, id := range append(append([]string{}, cfg.ReturnedStatusIDs...), cfg.CodeReviewStatusIDs...) {
		if _, err := strconv.Atoi(id); err != nil {
			errs = append(errs, fmt.Errorf("status id %q must be numeric (a status *name* goes in JIRA_RETURNED_STATUS_NAMES)", id))
		}
	}

	mode := strings.TrimSpace(getenv("JIRA_ATTRIBUTION_MODE"))
	if mode == "" {
		cfg.AttributionMode = analytics.ModeCurrent
	} else {
		m, err := analytics.ParseAttributionMode(mode)
		if err != nil {
			errs = append(errs, fmt.Errorf("JIRA_ATTRIBUTION_MODE: %w", err))
		}
		cfg.AttributionMode = m
	}

	if v := strings.TrimSpace(getenv("JIRA_CONCURRENCY")); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("JIRA_CONCURRENCY %q is not a number", v))
		case n < 1 || n > 50:
			errs = append(errs, fmt.Errorf("JIRA_CONCURRENCY %d out of range [1,50]", n))
		default:
			cfg.Concurrency = n
		}
	}

	if v := strings.TrimSpace(getenv("JIRA_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("JIRA_TIMEOUT %q is not a duration, e.g. 90s or 2m", v))
		case d <= 0:
			errs = append(errs, fmt.Errorf("JIRA_TIMEOUT %s must be positive", d))
		default:
			cfg.Timeout = d
		}
	}

	if v := strings.TrimSpace(getenv("LOG_LEVEL")); v != "" {
		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL %q is not one of debug|info|warn|error", v))
		} else {
			cfg.LogLevel = lvl
		}
	}

	if len(errs) > 0 {
		lines := strings.Split(errors.Join(errs...).Error(), "\n")
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(lines, "\n  "))
	}
	return cfg, nil
}

// String redacts the token so a Config can be logged safely.
func (c *Config) String() string {
	return fmt.Sprintf("Config{BaseURL:%s Email:%s APIToken:[redacted] DeveloperFieldID:%s "+
		"ReturnedStatusIDs:%v ReturnedStatusNames:%v CodeReviewStatusIDs:%v Mode:%s Concurrency:%d Timeout:%s}",
		c.BaseURL, c.Email, c.DeveloperFieldID, c.ReturnedStatusIDs, c.ReturnedStatusNames,
		c.CodeReviewStatusIDs, c.AttributionMode, c.Concurrency, c.Timeout)
}

func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
