package core

import (
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/localserver"
)

// The network log body cap is declared twice — networkLogBodyLimit in app.go
// and localserver.NetworkLogBodyLimit — and the second says it "mirrors the
// limit package main applies". Entries from both land in the SAME in-memory
// network log and the same webview, which is the whole reason one cap should
// govern both. Nothing enforced that.
func TestNetworkLogBodyLimitsAgreeAcrossPackages(t *testing.T) {
	if networkLogBodyLimit != localserver.NetworkLogBodyLimit {
		t.Errorf("package main caps network log bodies at %d but localserver caps at %d; "+
			"entries from the mock server and from real requests share one log",
			networkLogBodyLimit, localserver.NetworkLogBodyLimit)
	}
}

func TestTruncateNetworkLogBodyLeavesShortBodiesAlone(t *testing.T) {
	for _, body := range []string{"", "short", strings.Repeat("x", networkLogBodyLimit)} {
		if got := truncateNetworkLogBody(body); got != body {
			t.Errorf("a %d-byte body was altered", len(body))
		}
	}
}

// A body over the cap is cut AND MARKED. The marker is the only thing that
// distinguishes "the request really was this size" from "we stopped reading" —
// without it, someone debugging a truncated payload has no way to tell.
func TestTruncateNetworkLogBodyMarksWhatItCut(t *testing.T) {
	body := strings.Repeat("x", networkLogBodyLimit+1)
	got := truncateNetworkLogBody(body)

	if len(got) <= networkLogBodyLimit {
		t.Fatalf("truncated length %d, want more than the cap once the marker is added", len(got))
	}
	if !strings.HasSuffix(got, "... truncated") {
		t.Error("an over-length body was cut without saying so")
	}
	if !strings.HasPrefix(got, strings.Repeat("x", networkLogBodyLimit)) {
		t.Error("the retained prefix is not the first networkLogBodyLimit bytes")
	}
}

// The cut is by BYTES, and a body is arbitrary bytes rather than text, so a
// multi-byte rune straddling the boundary is expected — what must not happen is
// a panic or a silent re-encoding that changes the retained prefix.
func TestTruncateNetworkLogBodyCutsOnAByteBoundary(t *testing.T) {
	body := strings.Repeat("é", networkLogBodyLimit) // two bytes per rune
	got := truncateNetworkLogBody(body)
	if !strings.HasPrefix(got, body[:networkLogBodyLimit]) {
		t.Error("the retained prefix is not the leading networkLogBodyLimit bytes")
	}
	if !strings.HasSuffix(got, "... truncated") {
		t.Error("the marker is missing")
	}
}

// KNOWN DIVERGENCE, recorded rather than silently changed. package main cuts
// the body and appends a marker; localserver caps at READ time with an
// io.LimitReader, which cannot append one — by the time the cap is hit the
// reader has no way to know whether more was coming without reading further.
//
// So a mock-server entry that hit the cap looks, in the log, exactly like a
// request that happened to be 64 KiB. This test does not assert the marker for
// the localserver path; it pins the fact that the CAPS match, which is what
// keeps the two entry kinds comparable at all.
func TestTheTwoCapsGovernTheSameBoundary(t *testing.T) {
	atCap := strings.Repeat("x", localserver.NetworkLogBodyLimit)
	if got := truncateNetworkLogBody(atCap); got != atCap {
		t.Error("a body exactly at the localserver cap was truncated by package main, so the two boundaries differ by one")
	}
	overCap := strings.Repeat("x", localserver.NetworkLogBodyLimit+1)
	if got := truncateNetworkLogBody(overCap); got == overCap {
		t.Error("a body one byte over the localserver cap was not truncated by package main")
	}
}
