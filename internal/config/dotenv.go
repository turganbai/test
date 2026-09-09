package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// DefaultEnvFile is the file LoadFile looks for, in the working directory, when
// the caller does not name one.
const DefaultEnvFile = ".env"

// ParseEnvFile reads KEY=VALUE lines in the usual dotenv shape:
//
//	# a comment
//	export JIRA_BASE_URL=https://example.atlassian.net   # trailing comment
//	JIRA_API_TOKEN="quoted, so , and # stay literal"
//	JIRA_RETURNED_STATUS_NAMES='Возвращено,Returned'
//
// Values in double quotes take \n, \r, \t, \\ and \" escapes; single-quoted
// values are literal; unquoted values are trimmed and lose an inline comment.
// A later line wins over an earlier one with the same key.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var errs []error
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		text = strings.TrimPrefix(text, "\ufeff") // a BOM on the first line
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimSpace(strings.TrimPrefix(text, "export "))

		key, rawValue, ok := strings.Cut(text, "=")
		if !ok {
			errs = append(errs, fmt.Errorf("line %d: %q is not KEY=VALUE", line, sc.Text()))
			continue
		}
		key = strings.TrimSpace(key)
		if err := validKey(key); err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", line, err))
			continue
		}
		value, err := parseValue(rawValue)
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d: %s: %w", line, key, err))
			continue
		}
		out[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// parseValue takes the text after the '=' *untrimmed*: whether a '#' opens a
// comment depends on what precedes it, so the leading whitespace has to survive
// until the comment is cut.
func parseValue(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		quote := v[0]
		body, rest, ok := cutAtClosingQuote(v[1:], quote)
		if !ok {
			return "", fmt.Errorf("unterminated %c quote", quote)
		}
		if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "#") {
			return "", fmt.Errorf("unexpected %q after the closing quote", rest)
		}
		if quote == '\'' {
			return body, nil
		}
		return unescape(body), nil
	}
	// Unquoted: an inline comment needs whitespace in front of it, so a value
	// like a URL fragment or a colour is not truncated at its own '#'. The scan
	// runs over the untrimmed text, so `KEY= # note` is an empty value with a
	// comment while `KEY=#fff` is the value "#fff".
	return strings.TrimSpace(cutComment(raw)), nil
}

// cutComment drops an unquoted inline comment: a '#' preceded by whitespace.
func cutComment(v string) string {
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return v[:i]
		}
	}
	return v
}

// cutAtClosingQuote returns the body up to the unescaped closing quote and
// whatever follows it.
func cutAtClosingQuote(s string, quote byte) (body, rest string, ok bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case quote == '"' && s[i] == '\\' && i+1 < len(s):
			b.WriteByte(s[i])
			i++
			b.WriteByte(s[i])
		case s[i] == quote:
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", false
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\', '"':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func validKey(k string) error {
	if k == "" {
		return errors.New("empty variable name")
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		alnum := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !alnum || (i == 0 && c >= '0' && c <= '9') {
			return fmt.Errorf("%q is not a valid variable name", k)
		}
	}
	return nil
}

// ReadEnvFile parses path. A missing file is reported as fs.ErrNotExist so the
// caller can decide whether that is fatal: it is when the user named the file,
// and it is not for the default .env.
func ReadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vars, err := ParseEnvFile(f)
	if err != nil {
		lines := strings.Split(err.Error(), "\n")
		return nil, fmt.Errorf("%s:\n  %s", path, strings.Join(lines, "\n  "))
	}
	return vars, nil
}

// LookupEnv reports a variable's value and whether it was set at all, mirroring
// os.LookupEnv. Getenv cannot express the difference, and the difference matters
// here: exporting JIRA_API_TOKEN= is a deliberate "I have no token", which must
// not fall through to a stale value in a .env.
type LookupEnv func(string) (string, bool)

// FileEnv layers file values *under* lookup: a variable present in the real
// environment always wins — even when it was set to the empty string — so a
// .env is a convenience for local runs and never silently overrides what CI or
// a shell exported.
func FileEnv(lookup LookupEnv, vars map[string]string) Getenv {
	return func(k string) string {
		if v, ok := lookup(k); ok {
			return v
		}
		return vars[k]
	}
}

// LoadFile is Load with a dotenv file layered under the environment, validated
// to the depth the calling command needs. An empty path means DefaultEnvFile,
// resolved against the working directory and whose absence is not an error; a
// path the caller named must exist.
func LoadFile(lookup LookupEnv, path string, scope Scope) (*Config, error) {
	named := path != ""
	if !named {
		path = DefaultEnvFile
	}
	vars, err := ReadEnvFile(path)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist) && !named:
		// No .env is the normal case for a deployed run.
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("env file %s: %w", path, fs.ErrNotExist)
	default:
		return nil, err
	}
	return load(FileEnv(lookup, vars), scope)
}
