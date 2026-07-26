// The percent-decoding fallback, and the two path CWD helpers.
//
// hexDigit backs safeDecodeURIComponent, which runs when url.PathUnescape
// REFUSES a value — a URL containing a stray % or a malformed escape. That is
// the path a real user hits: a URL pasted from a log, or one built by a script
// from an unencoded value. If the fallback mis-decodes, the request goes to a
// different URL than the one shown in the bar.
//
// The CWD helpers are environment-dependent, so the tests assert the INVARIANTS
// rather than a value: never empty, and using the separator their platform
// namespace promises. A script doing path.posix.resolve() against a Windows-
// shaped cwd builds a path that cannot be opened.
package scripting

import (
	"strings"
	"testing"
)

func TestHexDigitAcceptsBothCasesAndRejectsTheRest(t *testing.T) {
	for c := byte('0'); c <= '9'; c++ {
		got, ok := hexDigit(c)
		if !ok || got != c-'0' {
			t.Errorf("hexDigit(%q) = %d,%v", c, got, ok)
		}
	}
	for c := byte('a'); c <= 'f'; c++ {
		got, ok := hexDigit(c)
		if !ok || got != c-'a'+10 {
			t.Errorf("hexDigit(%q) = %d,%v", c, got, ok)
		}
	}
	// Upper case matters: %2F and %2f are both legal percent-escapes, and a
	// decoder handling only one silently leaves half of them encoded.
	for c := byte('A'); c <= 'F'; c++ {
		got, ok := hexDigit(c)
		if !ok || got != c-'A'+10 {
			t.Errorf("hexDigit(%q) = %d,%v", c, got, ok)
		}
	}
	for _, c := range []byte{'g', 'G', 'z', ' ', '%', 0, 0xff, '/'} {
		if _, ok := hexDigit(c); ok {
			t.Errorf("hexDigit(%q) accepted a non-hex byte", c)
		}
	}
}

// The property that matters end to end: a value url.PathUnescape refuses must
// still decode its well-formed escapes rather than being returned untouched.
func TestSafeDecodeHandlesAMalformedEscapeWithoutLosingTheGoodOnes(t *testing.T) {
	// A stray % makes PathUnescape fail on the whole string.
	got := safeDecodeURIComponent("a%20b%zzc%2Fd")
	if !strings.Contains(got, "a b") {
		t.Errorf("got %q; the well-formed %%20 must still decode", got)
	}
	if !strings.Contains(got, "%zz") {
		t.Errorf("got %q; the malformed escape must be preserved verbatim, not dropped", got)
	}
}

func TestSafeDecodeLeavesCleanValuesIntact(t *testing.T) {
	for _, value := range []string{"plain", "a-b_c.d~e", ""} {
		if got := safeDecodeURIComponent(value); got != value {
			t.Errorf("safeDecodeURIComponent(%q) = %q", value, got)
		}
	}
	if got := safeDecodeURIComponent("a%20b"); got != "a b" {
		t.Errorf("got %q, want %q", got, "a b")
	}
}

func TestPathCWDHelpersUseTheirPlatformSeparator(t *testing.T) {
	posix := scriptPosixPathCWD()
	if posix == "" {
		t.Fatal("posix cwd is empty; path.posix.resolve() would build a relative path")
	}
	if strings.Contains(posix, "\\") {
		t.Errorf("posix cwd %q contains a backslash", posix)
	}

	win := scriptWin32PathCWD()
	if win == "" {
		t.Fatal("win32 cwd is empty")
	}
	if strings.Contains(win, "/") {
		t.Errorf("win32 cwd %q contains a forward slash — path.win32 promises backslashes", win)
	}
	// The two describe the same directory in different notations.
	if strings.ReplaceAll(win, "\\", "/") != posix {
		t.Errorf("posix %q and win32 %q do not describe the same directory", posix, win)
	}
}
