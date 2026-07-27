package transport

import (
	"testing"
	"time"
)

// sweepIfDue sat at 20%. The existing cache tests reach evictLocked through
// TransportFor's miss path; none of them exercises the sweep itself, which is
// the thing that keeps a HIT from scanning the whole map.
//
// Its entire purpose is rate limiting. Without it every cache hit would walk
// every entry looking for expiries — on the hot path of every request.

// The cache deliberately TOLERATES A STALE ENTRY between sweeps rather than
// scanning on each hit. That is the trade the function exists to make, and it
// is only visible from a hit that happens before the interval elapses.
func TestSweepIsSkippedBeforeItsIntervalElapses(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &Cache{idleTTL: time.Minute, now: func() time.Time { return now }}

	if _, err := cache.TransportFor(Spec{VerifyTLS: false, ProxyMode: ProxyOff}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.TransportFor(Spec{VerifyTLS: true, ProxyMode: ProxyOff}); err != nil {
		t.Fatal(err)
	}
	if cache.Size() != 2 {
		t.Fatalf("setup: size=%d, want 2", cache.Size())
	}

	// Both entries are now well past the TTL, but the sweep is not due.
	now = now.Add(10 * time.Minute)
	cache.nextSweep.Store(now.Add(time.Hour).UnixNano())

	// A HIT on one of them. It must not trigger a scan.
	if _, err := cache.TransportFor(Spec{VerifyTLS: true, ProxyMode: ProxyOff}); err != nil {
		t.Fatal(err)
	}
	if cache.Size() != 2 {
		t.Errorf("a hit swept the cache before the interval elapsed, size=%d", cache.Size())
	}
}

// And when it IS due, the same hit evicts. Without this half the cache would
// only ever shrink on a miss, so a workspace that settles into one posture
// would hold every transport it ever built.
func TestSweepRunsOnAHitOnceItIsDue(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &Cache{idleTTL: time.Minute, now: func() time.Time { return now }}

	if _, err := cache.TransportFor(Spec{VerifyTLS: false, ProxyMode: ProxyOff}); err != nil {
		t.Fatal(err)
	}
	kept, err := cache.TransportFor(Spec{VerifyTLS: true, ProxyMode: ProxyOff})
	if err != nil {
		t.Fatal(err)
	}

	// Move past both the TTL and the sweep interval, then hit the fresh entry.
	now = now.Add(10 * time.Minute)
	cache.nextSweep.Store(now.Add(-time.Second).UnixNano())

	again, err := cache.TransportFor(Spec{VerifyTLS: true, ProxyMode: ProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	if again != kept {
		t.Error("the entry being used was itself evicted")
	}
	if cache.Size() != 1 {
		t.Errorf("the idle entry survived a due sweep, size=%d", cache.Size())
	}
}

// After a sweep the next one is pushed out by a full interval, so a burst of
// requests scans once rather than once each.
func TestASweepPushesTheNextOneOut(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &Cache{idleTTL: time.Minute, now: func() time.Time { return now }}
	cache.nextSweep.Store(now.Add(-time.Second).UnixNano())

	cache.sweepIfDue(now)

	next := cache.nextSweep.Load()
	if next <= now.UnixNano() {
		t.Fatalf("nextSweep was not advanced past now")
	}
	if want := now.Add(transportCacheSweepInterval).UnixNano(); next != want {
		t.Errorf("nextSweep = %d, want %d — the interval was not applied", next, want)
	}

	// A second call in the same instant must be a no-op, which is the whole
	// point of storing the deadline.
	before := cache.nextSweep.Load()
	cache.sweepIfDue(now)
	if cache.nextSweep.Load() != before {
		t.Error("a second sweep in the same instant ran anyway")
	}
}

// Sweeping an empty cache must be harmless — it is reached on the first hit
// after a Flush.
func TestSweepingAnEmptyCacheIsHarmless(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	cache := &Cache{idleTTL: time.Minute, now: func() time.Time { return now }}
	cache.nextSweep.Store(now.Add(-time.Second).UnixNano())

	cache.sweepIfDue(now)

	if cache.Size() != 0 {
		t.Errorf("size=%d", cache.Size())
	}
}
