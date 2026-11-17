package slug_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/render-oss/render-mcp-server/pkg/slug"
)

func repeatJoin(s string, n int, sep string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = s
	}
	return strings.Join(parts, sep)
}

func TestMake(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// AC-5: no ASCII letters or digits
		{"empty", "", ""},
		{"punctuation only", "!!! ???", ""},
		{"non-ascii only", "日本語", ""},

		// AC-2: lowercasing and keeping alphanumerics
		{"mixed case with digits", "Hello123", "hello123"},
		{"uppercase", "ABC", "abc"},

		// AC-3: runs of non-alphanumerics collapse to one "-"
		{"spaces and punctuation", "Hello,   World!!Again", "hello-world-again"},
		{"underscores", "a_b__c", "a-b-c"},
		{"slashes and dots", "a/b.c", "a-b-c"},
		{"control chars", "a\t\n\x00b", "a-b"},

		// AC-4: leading and trailing separators trimmed
		{"leading and trailing separators", "  --Foo Bar--  ", "foo-bar"},
		{"surrounding dashes", "-abc-", "abc"},

		// AC-8: non-ascii mixed with ascii
		{"accented", "café au lait", "caf-au-lait"},

		// AC-7: exactly 64 bytes is unchanged
		{"exactly max length", strings.Repeat("a", 64), strings.Repeat("a", 64)},

		// AC-6: truncation
		{"too long without separator", strings.Repeat("a", 70), strings.Repeat("a", 64)},
		{"too long cut at separator", strings.Repeat("abcde ", 15), repeatJoin("abcde", 10, "-")},
		{"separator exactly at cut point", strings.Repeat("a", 64) + " tail", strings.Repeat("a", 64)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slug.Make(tc.in)
			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), 64)
			assert.False(t, strings.HasSuffix(got, "-"))
			assert.False(t, strings.HasPrefix(got, "-"))
		})
	}
}
