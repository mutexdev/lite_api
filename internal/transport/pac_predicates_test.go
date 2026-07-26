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

	"github.com/dop251/goja"
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

// The fallback path, which coverage found at 0%.
//
// When a PAC script cannot be evaluated — a syntax error, or a feature the goja
// runtime does not provide — pacDirectivesForURL falls back to scraping the
// `return "..."` statements out of the source text. That net is the difference
// between a broken PAC file routing by its stated intent and routing by nothing
// at all, and nothing is the dangerous outcome: traffic goes direct.
func TestPacReturnDirectivesScrapesReturnStatements(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		want    []string
	}{
		"double quotes": {`function FindProxyForURL(u,h){ return "PROXY 10.0.0.1:8080"; }`, []string{"PROXY 10.0.0.1:8080"}},
		"single quotes": {`function FindProxyForURL(u,h){ return 'PROXY 10.0.0.1:8080'; }`, []string{"PROXY 10.0.0.1:8080"}},
		"semicolon list is split": {
			`function FindProxyForURL(u,h){ return "PROXY a:1; DIRECT"; }`,
			[]string{"PROXY a:1", "DIRECT"},
		},
		"several returns in order": {
			`function FindProxyForURL(u,h){ if (x) return "PROXY a:1"; return "DIRECT"; }`,
			[]string{"PROXY a:1", "DIRECT"},
		},
		"no return yields nothing": {`function FindProxyForURL(u,h){ var x = 1; }`, nil},
	} {
		got := pacReturnDirectives(tc.content)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", name, got, tc.want)
				break
			}
		}
	}
}

// The property that actually matters: a script the runtime cannot execute still
// produces the directive its source states.
func TestPacDirectivesForURLFallsBackWhenTheScriptCannotRun(t *testing.T) {
	broken := `function FindProxyForURL(url, host) { this is not valid javascript return "PROXY 10.0.0.1:8080"; }`

	got, err := pacDirectivesForURL(broken, "https://example.test/x")
	if err != nil && len(got) == 0 {
		t.Fatalf("a broken script yielded nothing: %v", err)
	}
	if len(got) == 0 || got[0] != "PROXY 10.0.0.1:8080" {
		t.Fatalf("fallback did not recover the stated directive: %v", got)
	}
}

// And evaluation must win when it works, or a script whose logic returns DIRECT
// would be overridden by an earlier PROXY line in its own source.
func TestPacDirectivesForURLPrefersEvaluationOverScraping(t *testing.T) {
	script := `function FindProxyForURL(url, host) {
		if (host === "never.test") { return "PROXY wrong:1"; }
		return "DIRECT";
	}`
	got, err := pacDirectivesForURL(script, "https://example.test/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0] != "DIRECT" {
		t.Fatalf("got %v; evaluation must win over scraping the source", got)
	}
}

// dateRange, which coverage found at 0% and which had a dead branch.
//
// PAC gives no type information, so days and years are told apart only by
// magnitude. The day comparison used to return for ANY two integers, making the
// year branch below it unreachable: dateRange(2020, 2030) was evaluated as
// "day >= 2020" and was false on every date there has ever been. A script
// gating on a year range never matched, and said nothing about it.
func TestPacDateRangeDistinguishesDaysFromYears(t *testing.T) {
	vm := goja.New()
	v := func(x interface{}) goja.Value { return vm.ToValue(x) }
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		args []goja.Value
		want bool
	}{
		"day of month, single":      {[]goja.Value{v(26)}, true},
		"day of month, wrong day":   {[]goja.Value{v(25)}, false},
		"day range covering today":  {[]goja.Value{v(20), v(28)}, true},
		"day range excluding today": {[]goja.Value{v(1), v(10)}, false},

		"month name, current":   {[]goja.Value{v("JUL")}, true},
		"month name, other":     {[]goja.Value{v("JUN")}, false},
		"month range covering":  {[]goja.Value{v("JUN"), v("AUG")}, true},
		"month range excluding": {[]goja.Value{v("JAN"), v("MAR")}, false},

		// The branch that was unreachable.
		"year range covering today": {[]goja.Value{v(2020), v(2030)}, true},
		"year range before today":   {[]goja.Value{v(2000), v(2010)}, false},
		"year range after today":    {[]goja.Value{v(2030), v(2040)}, false},

		"no arguments": {nil, false},
	} {
		if got := pacDateRange(now, tc.args...); got != tc.want {
			t.Errorf("%s: pacDateRange = %v, want %v", name, got, tc.want)
		}
	}
}

// A trailing GMT switches the comparison to UTC and must not be read as a date
// component, or every GMT-qualified rule would compare against the string.
func TestPacDateRangeAcceptsATrailingGMT(t *testing.T) {
	vm := goja.New()
	v := func(x interface{}) goja.Value { return vm.ToValue(x) }
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	if !pacDateRange(now, v("JUL"), v("GMT")) {
		t.Error(`dateRange("JUL", "GMT") must match in July`)
	}
	if !pacDateRange(now, v(20), v(28), v("gmt")) {
		t.Error("the GMT marker must be case-insensitive and not consume a bound")
	}
}
