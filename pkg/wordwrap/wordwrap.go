// Package wordwrap provides a small, dependency-free helper for greedy word
// wrapping of plain text.
package wordwrap

import (
	"strings"
	"unicode/utf8"
)

// Wrap greedily wraps s so that no output line exceeds width runes.
//
// Words are the whitespace-separated fields of s (runs of any whitespace,
// including tabs and newlines, act as a single separator) and are joined by
// single spaces. Lines are joined with "\n" and the result has no trailing
// newline. A word longer than width occupies its own line and is never broken.
// If width <= 0, all words are joined by single spaces on a single line. Empty
// or whitespace-only input returns "".
func Wrap(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	if width <= 0 {
		return strings.Join(words, " ")
	}

	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		n := utf8.RuneCountInString(w)
		switch {
		case i == 0:
			b.WriteString(w)
			lineLen = n
		case lineLen+1+n <= width:
			b.WriteByte(' ')
			b.WriteString(w)
			lineLen += 1 + n
		default:
			b.WriteByte('\n')
			b.WriteString(w)
			lineLen = n
		}
	}
	return b.String()
}
