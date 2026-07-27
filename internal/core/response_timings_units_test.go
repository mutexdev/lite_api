// Response timing units and availability flags.
//
// Found by negative control: swapping Milliseconds() for Microseconds() failed
// no test. The field is named TotalMs and the UI renders it as milliseconds, so
// a unit slip shows a 200ms request as 200000ms — visibly wrong, but only to
// someone who looks, and nothing would fail.
//
// The availability flags matter more than they look. A zero duration and an
// unmeasured phase are the same number, so the flag is the only thing that
// distinguishes "this took no time" from "Go never reported this".
package core

import (
	"testing"
	"time"
)

func TestFinalizeReportsMilliseconds(t *testing.T) {
	start := time.Now()
	trace := newResponseTimingTrace(start)

	got := trace.finalize(start.Add(1500 * time.Millisecond))

	if got.TotalMs != 1500 {
		t.Fatalf("TotalMs = %d for a 1.5s request, want 1500 (a unit slip would give 1500000 or 1)", got.TotalMs)
	}
}

func TestFinalizeRoundsSubMillisecondToZeroNotNegative(t *testing.T) {
	start := time.Now()
	trace := newResponseTimingTrace(start)

	got := trace.finalize(start.Add(400 * time.Microsecond))

	if got.TotalMs != 0 {
		t.Fatalf("TotalMs = %d for a 400µs request, want 0", got.TotalMs)
	}
}

// Download timing is only meaningful once a first byte arrived. Reporting a
// duration without one would time from the request start instead, quietly
// attributing the whole wait to download.
func TestDownloadTimingIsUnavailableUntilAFirstByte(t *testing.T) {
	start := time.Now()
	trace := newResponseTimingTrace(start)

	got := trace.finalize(start.Add(time.Second))
	if got.DownloadAvailable {
		t.Fatal("download reported as available with no first byte recorded")
	}
	if got.DownloadMs != 0 {
		t.Fatalf("DownloadMs = %d with no first byte, want 0", got.DownloadMs)
	}
}

func TestDownloadTimingMeasuresFromTheFirstByte(t *testing.T) {
	start := time.Now()
	trace := newResponseTimingTrace(start)
	trace.firstByte = start.Add(700 * time.Millisecond)

	got := trace.finalize(start.Add(time.Second))

	if !got.DownloadAvailable {
		t.Fatal("download should be available once a first byte was seen")
	}
	// 1000ms total, first byte at 700ms, so download is the remaining 300 --
	// not the full second.
	if got.DownloadMs != 300 {
		t.Fatalf("DownloadMs = %d, want 300 (measured from the first byte, not the start)", got.DownloadMs)
	}
	if got.TotalMs != 1000 {
		t.Fatalf("TotalMs = %d, want 1000", got.TotalMs)
	}
}

func TestRedirectCountAccumulates(t *testing.T) {
	trace := newResponseTimingTrace(time.Now())
	for i := 0; i < 3; i++ {
		trace.redirect()
	}
	if got := trace.finalize(time.Now()).RedirectCount; got != 3 {
		t.Fatalf("RedirectCount = %d, want 3", got)
	}
}
