// Package clamp provides helpers for constraining values to a range.
package clamp

// Int limits v to the inclusive range [lo, hi]. If lo > hi the bounds are swapped first.
func Int(v, lo, hi int) int {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
