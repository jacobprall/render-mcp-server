package wordwrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/render-oss/render-mcp-server/pkg/wordwrap"
)

func TestWrap(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"simple wrap", "aa bb cc", 5, "aa bb\ncc"},
		{"exact fit boundary", "aa bb", 5, "aa bb"},
		{"one rune short of fit", "aa bb", 4, "aa\nbb"},
		{"collapses whitespace runs", "a\t\tb\n c", 100, "a b c"},
		{"collapses whitespace runs while wrapping", "a\t\tb\n c", 3, "a b\nc"},
		{"long word alone", "abcdefg", 3, "abcdefg"},
		{"long word among short words", "a abcdefg b", 3, "a\nabcdefg\nb"},
		{"long word first", "abcdefg a b", 3, "abcdefg\na b"},
		{"width zero", "aa bb cc", 0, "aa bb cc"},
		{"width negative", "aa bb cc", -1, "aa bb cc"},
		{"width zero collapses whitespace", "a\t\tb\n c", 0, "a b c"},
		{"empty string positive width", "", 5, ""},
		{"empty string zero width", "", 0, ""},
		{"whitespace only positive width", " \n\t ", 5, ""},
		{"whitespace only zero width", " \n\t ", 0, ""},
		{"whitespace only negative width", " \n\t ", -1, ""},
		{"single short word", "hi", 10, "hi"},
		{"multi-byte word fits width", "héllo wörld", 5, "héllo\nwörld"},
		{"multi-byte words fit one line", "héllo wörld", 11, "héllo wörld"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, wordwrap.Wrap(tc.in, tc.width))
		})
	}
}
