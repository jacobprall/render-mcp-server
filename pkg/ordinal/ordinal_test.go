package ordinal

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

type ordinalCase struct {
	name  string
	input int
	want  string
}

func runCases(t *testing.T, cases []ordinalCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Of(tt.input))
		})
	}
}

func TestOfBasicSuffixes(t *testing.T) {
	runCases(t, []ordinalCase{
		{name: "zero takes th", input: 0, want: "0th"},
		{name: "one takes st", input: 1, want: "1st"},
		{name: "two takes nd", input: 2, want: "2nd"},
		{name: "three takes rd", input: 3, want: "3rd"},
		{name: "four takes th", input: 4, want: "4th"},
		{name: "five takes th", input: 5, want: "5th"},
		{name: "six takes th", input: 6, want: "6th"},
		{name: "seven takes th", input: 7, want: "7th"},
		{name: "eight takes th", input: 8, want: "8th"},
		{name: "nine takes th", input: 9, want: "9th"},
		{name: "ten takes th", input: 10, want: "10th"},
	})
}

func TestOfTeensAlwaysTh(t *testing.T) {
	runCases(t, []ordinalCase{
		{name: "eleven", input: 11, want: "11th"},
		{name: "twelve", input: 12, want: "12th"},
		{name: "thirteen", input: 13, want: "13th"},
		{name: "fourteen", input: 14, want: "14th"},
		{name: "one hundred eleven", input: 111, want: "111th"},
		{name: "one hundred twelve", input: 112, want: "112th"},
		{name: "one hundred thirteen", input: 113, want: "113th"},
		{name: "one thousand eleven", input: 1011, want: "1011th"},
	})
}

func TestOfLastDigitBeyondTeens(t *testing.T) {
	runCases(t, []ordinalCase{
		{name: "twenty one", input: 21, want: "21st"},
		{name: "twenty two", input: 22, want: "22nd"},
		{name: "twenty three", input: 23, want: "23rd"},
		{name: "twenty four", input: 24, want: "24th"},
		{name: "one hundred", input: 100, want: "100th"},
		{name: "one hundred one", input: 101, want: "101st"},
		{name: "one hundred two", input: 102, want: "102nd"},
		{name: "one hundred three", input: 103, want: "103rd"},
	})
}

func TestOfNegativesKeepSign(t *testing.T) {
	runCases(t, []ordinalCase{
		{name: "minus one", input: -1, want: "-1st"},
		{name: "minus two", input: -2, want: "-2nd"},
		{name: "minus three", input: -3, want: "-3rd"},
		{name: "minus four", input: -4, want: "-4th"},
		{name: "minus eleven", input: -11, want: "-11th"},
		{name: "minus twelve", input: -12, want: "-12th"},
		{name: "minus thirteen", input: -13, want: "-13th"},
		{name: "minus twenty one", input: -21, want: "-21st"},
		{name: "minus one hundred two", input: -102, want: "-102nd"},
	})
}

func TestOfExtremes(t *testing.T) {
	runCases(t, []ordinalCase{
		{name: "max int", input: math.MaxInt, want: strconv.Itoa(math.MaxInt) + "th"},
		{name: "min int", input: math.MinInt, want: strconv.Itoa(math.MinInt) + "th"},
	})
}
