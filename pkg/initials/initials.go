// Package initials derives uppercase initials from a person or company name.
package initials

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Of returns the uppercase initials of name.
//
// Words are split on runs of Unicode whitespace and/or ASCII hyphens ('-').
// Each word contributes the uppercase form of its first rune; words whose
// first rune is not a letter are skipped. When max > 0 at most max initials
// are returned; max <= 0 means no limit.
func Of(name string, max int) string {
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-'
	})

	var b strings.Builder
	count := 0
	for _, field := range fields {
		if max > 0 && count == max {
			break
		}
		r, _ := utf8.DecodeRuneInString(field)
		if !unicode.IsLetter(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
		count++
	}
	return b.String()
}
