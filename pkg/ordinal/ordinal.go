// Package ordinal formats integers with English ordinal suffixes.
package ordinal

import "strconv"

// Of returns n written as an English ordinal, e.g. Of(1) == "1st",
// Of(11) == "11th", Of(102) == "102nd". Negative numbers keep their sign:
// Of(-1) == "-1st".
func Of(n int) string {
	return strconv.Itoa(n) + suffix(n)
}

// suffix returns the English ordinal suffix for n, ignoring its sign.
func suffix(n int) string {
	// Work on the last two digits, ignoring the sign. Using remainders
	// avoids overflow for math.MinInt, where negating n is not representable.
	hundreds := n % 100
	if hundreds < 0 {
		hundreds = -hundreds
	}
	switch {
	case hundreds >= 11 && hundreds <= 13:
		return "th"
	case hundreds%10 == 1:
		return "st"
	case hundreds%10 == 2:
		return "nd"
	case hundreds%10 == 3:
		return "rd"
	default:
		return "th"
	}
}
