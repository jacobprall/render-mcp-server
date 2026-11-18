package initials_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/render-oss/render-mcp-server/pkg/initials"
)

func TestOf(t *testing.T) {
	cases := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"basic", "ada lovelace", 0, "AL"},
		{"collapsed whitespace", "ada   lovelace", 0, "AL"},
		{"mixed whitespace", "\tada \n lovelace\n", 0, "AL"},
		{"hyphenated", "jean-luc picard", 0, "JLP"},
		{"surrounding hyphens and spaces", "  -mary- -jane-  ", 0, "MJ"},
		{"unicode letters", "édouard ñuño", 0, "ÉÑ"},
		{"already uppercase", "Ada Lovelace", 0, "AL"},
		{"digit leading word skipped", "3M company", 0, "C"},
		{"punctuation leading word skipped", "!bang boom", 0, "B"},
		{"all non-letter words", "42 7", 0, ""},
		{"max limits", "ada byron king lovelace", 2, "AB"},
		{"max equals word count", "ada byron king lovelace", 4, "ABKL"},
		{"max greater than word count", "ada byron king lovelace", 10, "ABKL"},
		{"max zero is unlimited", "ada byron king lovelace", 0, "ABKL"},
		{"negative max is unlimited", "ada byron king lovelace", -1, "ABKL"},
		{"empty", "", 0, ""},
		{"whitespace only", " \t\n", 0, ""},
		{"hyphens only", "---", 3, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, initials.Of(tc.input, tc.max))
		})
	}
}
