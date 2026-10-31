// Package humanize provides helpers for rendering values in a short,
// human-readable form.
package humanize

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const day = 24 * time.Hour

// Duration formats d using at most the two largest non-zero units from
// days, hours, minutes and seconds (for example "1d 2h", "1m 30s", "45s").
// Smaller units are truncated rather than rounded, intermediate zero units
// are skipped ("1d 5m"), durations shorter than one second render as "0s",
// and negative durations are prefixed with "-".
func Duration(d time.Duration) string {
	neg := d < 0
	if neg {
		if d == time.Duration(math.MinInt64) {
			// Negating the smallest representable duration overflows,
			// so clamp it to the largest representable one.
			d = time.Duration(math.MaxInt64)
		} else {
			d = -d
		}
	}

	if d < time.Second {
		return "0s"
	}

	days := d / day
	d %= day
	hours := d / time.Hour
	d %= time.Hour
	mins := d / time.Minute
	d %= time.Minute
	secs := d / time.Second

	units := []struct {
		value  int64
		suffix string
	}{
		{int64(days), "d"},
		{int64(hours), "h"},
		{int64(mins), "m"},
		{int64(secs), "s"},
	}

	parts := make([]string, 0, 2)
	for _, u := range units {
		if u.value == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d%s", u.value, u.suffix))
		if len(parts) == 2 {
			break
		}
	}

	out := strings.Join(parts, " ")
	if neg {
		return "-" + out
	}
	return out
}
