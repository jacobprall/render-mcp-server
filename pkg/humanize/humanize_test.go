package humanize

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "zero", in: 0, want: "0s"},
		{name: "sub second", in: 999 * time.Millisecond, want: "0s"},
		{name: "negative sub second", in: -999 * time.Millisecond, want: "0s"},
		{name: "seconds only", in: 45 * time.Second, want: "45s"},
		{name: "minutes and seconds", in: 90 * time.Second, want: "1m 30s"},
		{name: "truncates smaller units", in: 2*time.Hour + 5*time.Minute + 9*time.Second, want: "2h 5m"},
		{name: "days and hours", in: 26 * time.Hour, want: "1d 2h"},
		{name: "negative minutes and seconds", in: -90 * time.Second, want: "-1m 30s"},
		{name: "hour only", in: time.Hour, want: "1h"},
		{name: "minutes only", in: 5 * time.Minute, want: "5m"},
		{name: "day only", in: 24 * time.Hour, want: "1d"},
		{name: "non adjacent days and minutes", in: 24*time.Hour + 5*time.Minute, want: "1d 5m"},
		{name: "non adjacent hours and seconds", in: time.Hour + 30*time.Second, want: "1h 30s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Duration(tt.in); got != tt.want {
				t.Errorf("Duration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
