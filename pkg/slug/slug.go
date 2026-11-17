// Package slug converts arbitrary strings into URL-friendly slugs.
package slug

import "strings"

// maxLen is the maximum length, in bytes, of a slug returned by Make.
const maxLen = 64

// Make converts s into a URL slug:
//
//   - ASCII uppercase letters are lowercased; ASCII lowercase letters and
//     digits are kept as-is.
//   - Every maximal run of characters that are not ASCII letters or digits
//     (including non-ASCII bytes, which are never transliterated) collapses
//     into a single "-".
//   - Leading and trailing separators are trimmed.
//   - The result is capped at 64 bytes, cut at the last "-" boundary within
//     the first 65 bytes when one exists, otherwise hard cut at 64 bytes.
//     The result never ends with "-".
//   - An input with no ASCII letters or digits yields "".
func Make(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	pendingSep := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z':
			if pendingSep && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingSep = false
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			if pendingSep && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingSep = false
			b.WriteByte(c + ('a' - 'A'))
		default:
			pendingSep = true
		}
	}

	out := b.String()
	if len(out) <= maxLen {
		return out
	}

	cut := out[:maxLen]
	if out[maxLen] == '-' {
		return cut
	}
	if idx := strings.LastIndexByte(cut, '-'); idx >= 0 {
		return cut[:idx]
	}
	return cut
}
