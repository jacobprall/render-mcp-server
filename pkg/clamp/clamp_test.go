package clamp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/render-oss/render-mcp-server/pkg/clamp"
)

func TestInt(t *testing.T) {
	cases := []struct {
		name            string
		v, lo, hi, want int
	}{
		{"below range", -5, 0, 10, 0},
		{"above range", 42, 0, 10, 10},
		{"inside range", 7, 0, 10, 7},
		{"equal to lo", 0, 0, 10, 0},
		{"equal to hi", 10, 0, 10, 10},
		{"equal bounds, v below", 1, 3, 3, 3},
		{"equal bounds, v above", 7, 3, 3, 3},
		{"equal bounds, v equal", 3, 3, 3, 3},
		{"swapped bounds, v inside", 5, 10, 0, 5},
		{"swapped bounds, v below", -3, 10, 0, 0},
		{"swapped bounds, v above", 42, 10, 0, 10},
		{"negative range, v below", -20, -10, -1, -10},
		{"negative range, v above", 5, -10, -1, -1},
		{"negative range, v inside", -4, -10, -1, -4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clamp.Int(tc.v, tc.lo, tc.hi))
		})
	}
}
