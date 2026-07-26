// The PAC date/time predicates.
//
// Coverage measurement found these at 0% — ten functions, none exercised by any
// test in the repo. They are what a proxy auto-config script calls to decide
// where traffic goes: a script saying "use the corporate proxy on weekdays" or
// "direct between 18:00 and 06:00" is asking these questions.
//
// Getting one wrong routes traffic the wrong way, and the symptom is not an
// error. It is a request that quietly went direct when policy said proxy, or
// through a proxy that should not have seen it.
//
// pacWeekdayRange and pacDateRange read time.Now(), so the tests below assert
// the properties that hold at any instant — a range covering every day must
// always be true, an empty or malformed one always false — rather than pinning
// a value that depends on when the suite runs. pacTimeRange takes its clock as
// a parameter, so it is tested exactly.
package transport

import (
	"testing"
	"time"
)

func TestPacWeekdayIndexMapsTheThreeLetterNames(t *testing.T) {
	for name, want := range map[string]int{
		"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
		"sun": 0, " Fri ": 5,
	} {
		if got := pacWeekdayIndex(name); got != want {
			t.Errorf("pacWeekdayIndex(%q) = %d, want %d", name, got, want)
		}
	}
	// An unknown day must be rejected, not silently treated as Sunday — a PAC
	// script with a typo would otherwise get Sunday's routing all week.
	for _, bad := range []string{"", "XXX", "MONDAY", "1"} {
		if got := pacWeekdayIndex(bad); got >= 0 {
			t.Errorf("pacWeekdayIndex(%q) = %d, want a negative sentinel", bad, got)
		}
	}
}

func TestPacMonthIndexIsOneBased(t *testing.T) {
	for name, want := range map[string]int{
		"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
		"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
		"dec": 12, " Jan ": 1,
	} {
		if got := pacMonthIndex(name); got != want {
			t.Errorf("pacMonthIndex(%q) = %d, want %d", name, got, want)
		}
	}
	for _, bad := range []string{"", "XXX", "JANUARY"} {
		if got := pacMonthIndex(bad); got > 0 {
			t.Errorf("pacMonthIndex(%q) = %d, want 0 or negative", bad, got)
		}
	}
}

func TestPacWeekdayRangeCoveringEveryDayIsAlwaysTrue(t *testing.T) {
	// SUN..SAT spans the week, so it holds whenever the suite runs.
	if !pacWeekdayRange("SUN", "SAT") {
		t.Error("SUN..SAT must match every day")
	}
	// A wrapping range covering the whole week too: MON..SUN wraps through
	// Sunday and back, which the start>end branch has to handle.
	if !pacWeekdayRange("MON", "SUN") {
		t.Error("MON..SUN wraps and must still match every day")
	}
}

func TestPacWeekdayRangeRejectsMalformedInput(t *testing.T) {
	if pacWeekdayRange() {
		t.Error("no arguments must not match")
	}
	for _, args := range [][]string{{"XXX"}, {"XXX", "SAT"}, {"SUN", "XXX"}} {
		if pacWeekdayRange(args...) {
			t.Errorf("%v contains an unknown day and must not match", args)
		}
	}
}

// The trailing "GMT" argument switches the comparison to UTC and must not be
// read as a day name — doing so would make every GMT-qualified rule fail.
func TestPacWeekdayRangeAcceptsATrailingGMT(t *testing.T) {
	if !pacWeekdayRange("SUN", "SAT", "GMT") {
		t.Error("SUN..SAT GMT must match every day")
	}
	if !pacWeekdayRange("SUN", "SAT", "gmt") {
		t.Error("the GMT marker must be case-insensitive")
	}
}

func TestPacTimeRangeWithAnExactClock(t *testing.T) {
	at := func(h, m, s int) time.Time { return time.Date(2026, 3, 4, h, m, s, 0, time.UTC) }

	for _, tc := range []struct {
		name string
		now  time.Time
		args []int
		want bool
	}{
		{"hour range, inside", at(10, 30, 0), []int{9, 17}, true},
		{"hour range, before", at(8, 59, 59), []int{9, 17}, false},
		// The end hour is INCLUSIVE of its whole hour: timeRange(9, 17) runs to
		// 17:59:59. My first draft asserted it ended at 17:00:00 and the code was
		// right, not the test — 9-to-17 naming an instant would make the form
		// useless. Same rule one unit down for the 4-argument form.
		{"hour range, late in the end hour", at(17, 59, 59), []int{9, 17}, true},
		{"hour range, after the end hour", at(18, 0, 0), []int{9, 17}, false},
		{"single hour covers the whole hour", at(9, 45, 0), []int{9}, true},
		{"single hour ends with the hour", at(10, 0, 0), []int{9}, false},
		{"hour:minute range, inside", at(9, 30, 0), []int{9, 0, 10, 0}, true},
		{"hour:minute range, within the end minute", at(10, 0, 59), []int{9, 0, 10, 0}, true},
		{"hour:minute range, past the end minute", at(10, 1, 0), []int{9, 0, 10, 0}, false},
	} {
		if got := pacTimeRange(tc.now, tc.args...); got != tc.want {
			t.Errorf("%s: pacTimeRange(%v, %v) = %v, want %v", tc.name, tc.now.Format("15:04:05"), tc.args, got, tc.want)
		}
	}
}

func TestPacTimeRangeRejectsMalformedArity(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	if pacTimeRange(now) {
		t.Error("no arguments must not match")
	}
	if pacTimeRange(now, 1, 2, 3, 4, 5, 6, 7) {
		t.Error("more than six arguments is not a valid PAC time range")
	}
}

func TestPacDateComponentMatchesDayAndMonth(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	if !pacDateComponentMatches(now, 26) {
		t.Error("an int must be read as the day of month")
	}
	if pacDateComponentMatches(now, 25) {
		t.Error("a different day must not match")
	}
	if !pacDateComponentMatches(now, int64(26)) {
		t.Error("an int64 day must be accepted — goja hands numbers over as int64")
	}
	if !pacDateComponentMatches(now, "JUL") {
		t.Error("a month name must be read as the month")
	}
	if pacDateComponentMatches(now, "JUN") {
		t.Error("a different month must not match")
	}
}
