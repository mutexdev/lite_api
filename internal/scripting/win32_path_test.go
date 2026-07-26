// The Windows half of the script path API.
//
// path.win32.* has to behave like Node's on every platform, because a script
// written on Windows runs on the Mac of whoever opens the collection next. That
// makes these functions pure string manipulation with no filesystem behind
// them, and the only thing keeping them honest is that they agree with Node.
//
// The root is where it goes wrong. A UNC share, a drive-relative path like
// "C:foo" and an absolute "C:\foo" are three different things that look alike,
// and confusing them turns a path into one that points somewhere else entirely.
package scripting

import "testing"

func TestWin32RootSplitsDriveUNCAndRelativePaths(t *testing.T) {
	for name, tc := range map[string]struct {
		value    string
		root     string
		rest     string
		absolute bool
	}{
		"absolute drive":  {`C:\foo\bar`, `C:\`, `foo\bar`, true},
		"lowercase drive": {`c:\foo`, `c:\`, `foo`, true},
		"drive root":      {`C:\`, `C:\`, ``, true},
		// "C:foo" means "foo relative to the current directory ON C:", which is
		// not the same place as "C:\foo" and is not absolute.
		"drive relative":  {`C:foo`, `C:`, `foo`, false},
		"rooted no drive": {`\foo`, `\`, `foo`, true},
		"relative":        {`foo\bar`, ``, `foo\bar`, false},
		"UNC share":       {`\\server\share\foo`, `\\server\share\`, `foo`, true},
		"UNC share root":  {`\\server\share`, `\\server\share\`, ``, true},
		// A UNC path missing its share name is not a share; treating it as one
		// would produce a root of "\\server\" that names no reachable thing.
		"UNC server only": {`\\server`, `\`, `server`, true},
	} {
		root, rest, absolute := scriptWin32PathSplitRoot(tc.value)
		if root != tc.root || rest != tc.rest || absolute != tc.absolute {
			t.Errorf("%s (%q): got root=%q rest=%q absolute=%v, want %q %q %v",
				name, tc.value, root, rest, absolute, tc.root, tc.rest, tc.absolute)
		}
	}
}

// Forward slashes are legal separators on Windows and scripts written on other
// platforms use them constantly.
func TestWin32AcceptsForwardSlashes(t *testing.T) {
	root, rest, absolute := scriptWin32PathSplitRoot("C:/foo/bar")
	if root != `C:\` || rest != `foo\bar` || !absolute {
		t.Errorf("got root=%q rest=%q absolute=%v", root, rest, absolute)
	}
}

func TestWin32IsAbsoluteMatchesTheRootSplit(t *testing.T) {
	for value, want := range map[string]bool{
		`C:\foo`:        true,
		`C:foo`:         false,
		`\foo`:          true,
		`foo`:           false,
		`\\srv\share\a`: true,
		``:              false,
	} {
		if got := scriptWin32PathIsAbsolute(value); got != want {
			t.Errorf("%q: got %v", value, got)
		}
	}
}

// A trailing separator is dropped so "C:\foo\" and "C:\foo" name the same
// thing — but NOT when it is the whole root. Trimming "C:\" to "C:" turns an
// absolute path into a drive-relative one, which resolves against the current
// directory and points somewhere else.
func TestWin32TrimKeepsARootsOwnSeparator(t *testing.T) {
	for value, want := range map[string]string{
		`C:\foo\`:      `C:\foo`,
		`C:\foo\\`:     `C:\foo`,
		`C:\`:          `C:\`,
		`\\srv\share\`: `\\srv\share\`,
		`\`:            `\`,
		`foo\`:         `foo`,
		`foo`:          `foo`,
	} {
		if got := scriptWin32TrimTrailingSeparators(value); got != want {
			t.Errorf("%q: got %q, want %q", value, got, want)
		}
	}
}

func TestWin32NormalizeResolvesDotSegments(t *testing.T) {
	for value, want := range map[string]string{
		`C:\foo\..\bar`: `C:\bar`,
		`C:\foo\.\bar`:  `C:\foo\bar`,
		`foo\..\bar`:    `bar`,
		// A relative path cannot climb above itself into nothing, so the ".."
		// stays: dropping it would silently turn "..\x" into "x".
		`..\x`:       `..\x`,
		``:           `.`,
		`C:/foo/bar`: `C:\foo\bar`,
	} {
		if got := scriptWin32PathNormalize(value); got != want {
			t.Errorf("%q: got %q, want %q", value, got, want)
		}
	}
}

// An absolute path CANNOT climb above its root — "C:\..\x" is "C:\x" on
// Windows, and letting the .. escape would produce a path outside any drive.
func TestWin32NormalizeCannotEscapeARoot(t *testing.T) {
	for _, value := range []string{`C:\..\x`, `C:\..\..\x`, `\..\x`} {
		got := scriptWin32PathNormalize(value)
		if got != `C:\x` && got != `\x` {
			t.Errorf("%q normalised to %q, which climbed above its root", value, got)
		}
	}
}
