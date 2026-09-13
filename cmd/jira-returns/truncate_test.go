package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short ascii is untouched", "azamat", 28, "azamat"},
		{"exact fit is untouched", "azamat", 6, "azamat"},
		{"ascii is cut", "abcdefgh", 4, "abc…"},
		// The case from the real report: a Cyrillic display name longer than
		// the column. Byte slicing cut this mid-rune and printed U+FFFD.
		{"cyrillic is cut on a rune boundary", "Турганбаи Сериков", 10, "Турганбаи…"},
		{"cyrillic short enough is untouched", "Турганбаи С", 28, "Турганбаи С"},
		{"exactly at the limit in runes, not bytes", "Турганбаи С", 11, "Турганбаи С"},
		{"one rune over", "Турганбаи Се", 11, "Турганбаи …"},
		// Mixed scripts in one name: the Cyrillic к is two bytes among
		// one-byte neighbours, so a byte-indexed cut lands inside it.
		{"mixed script is cut on a rune boundary", "a1к0l azamat", 5, "a1к0…"},
		{"mixed script short enough is untouched", "a1к0l", 5, "a1к0l"},
		{"n of 1", "азамат", 1, "…"},
		{"n of 0", "азамат", 0, ""},
		{"negative n", "азамат", -3, ""},
		{"empty string", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.in, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
			if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
				t.Errorf("truncate(%q, %d) = %q: not clean UTF-8", tt.in, tt.n, got)
			}
			if n := utf8.RuneCountInString(got); n > tt.n && tt.n >= 0 {
				t.Errorf("truncate(%q, %d) = %q: %d runes, over the limit", tt.in, tt.n, got, n)
			}
		})
	}
}

// Every prefix length of a multi-byte name must come back valid — the old
// byte-slicing version failed on most of these.
func TestTruncate_EveryPrefixOfACyrillicName(t *testing.T) {
	const name = "Турганбаи Сериков"
	for n := 1; n <= utf8.RuneCountInString(name)+2; n++ {
		got := truncate(name, n)
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Errorf("truncate(name, %d) = %q: contains U+FFFD", n, got)
		}
		if c := utf8.RuneCountInString(got); c > n {
			t.Errorf("truncate(name, %d) = %q: %d runes, over the limit", n, got, c)
		}
	}
}
