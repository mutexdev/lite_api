package gitignore

import (
	"strings"
	"testing"
)

// The managed block is a region of the USER'S .gitignore that LiteAPI rewrites.
// Everything outside it belongs to them, and the markers are the only thing
// separating the two. Two packages edit this block — the collection writer and
// the recovery store — which is why it is its own package.

const (
	begin = "# LiteAPI managed Git-backed collections"
	end   = "# End LiteAPI managed Git-backed collections"
)

func TestEntriesReadsOnlyTheManagedBlock(t *testing.T) {
	content := strings.Join([]string{
		"node_modules/",
		"*.log",
		begin,
		"Collections/API",
		"Collections/Other",
		end,
		"dist/",
	}, "\n")

	got := Entries(content)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
	for _, want := range []string{"Collections/API", "Collections/Other"} {
		if !got[want] {
			t.Errorf("%q is missing from %v", want, got)
		}
	}
	for _, outside := range []string{"node_modules/", "*.log", "dist/"} {
		if got[outside] {
			t.Errorf("%q is outside the managed block but was read as an entry", outside)
		}
	}
}

func TestEntriesOnContentWithNoBlock(t *testing.T) {
	if got := Entries("node_modules/\n*.log\n"); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
	if got := Entries(""); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// Comments and blank lines inside the block are not entries. Treating a comment
// as a path would write it back as one on the next rewrite.
func TestEntriesSkipsCommentsAndBlanks(t *testing.T) {
	content := strings.Join([]string{begin, "", "# a note", "Collections/API", "  ", end}, "\n")
	got := Entries(content)
	if len(got) != 1 || !got["Collections/API"] {
		t.Errorf("got %v, want only Collections/API", got)
	}
}

// THE PROPERTY THE USER CARES ABOUT: their own lines survive a rewrite, in
// order, and only the managed region changes.
func TestReplaceBlockLeavesTheUsersLinesAlone(t *testing.T) {
	content := strings.Join([]string{
		"node_modules/",
		"*.log",
		begin,
		"Collections/Old",
		end,
		"dist/",
	}, "\n")

	result := ReplaceBlock(content, map[string]bool{"Collections/New": true})

	for _, want := range []string{"node_modules/", "*.log", "dist/"} {
		if !strings.Contains(result, want) {
			t.Errorf("the user's line %q was lost:\n%s", want, result)
		}
	}
	if strings.Contains(result, "Collections/Old") {
		t.Errorf("the previous managed entry survived:\n%s", result)
	}
	if !strings.Contains(result, "Collections/New") {
		t.Errorf("the new entry was not written:\n%s", result)
	}
	if strings.Index(result, "node_modules/") > strings.Index(result, begin) {
		t.Errorf("the user's lines were moved below the managed block:\n%s", result)
	}
}

// Writing then reading must return what was written — the two halves of this
// package are only useful as a pair.
func TestReplaceBlockAndEntriesRoundTrip(t *testing.T) {
	want := map[string]bool{"a/b": true, "c/d": true, "e": true}
	got := Entries(ReplaceBlock("existing\n", want))
	if len(got) != len(want) {
		t.Fatalf("round trip gave %v, want %v", got, want)
	}
	for entry := range want {
		if !got[entry] {
			t.Errorf("%q did not survive the round trip", entry)
		}
	}
}

// Entries are sorted, so two runs over the same set produce identical bytes.
// An unstable order would show up as a spurious diff in the user's repository
// every time the app touched the file.
func TestReplaceBlockIsDeterministic(t *testing.T) {
	entries := map[string]bool{"z": true, "a": true, "m": true}
	first := ReplaceBlock("base\n", entries)
	for i := 0; i < 5; i++ {
		if got := ReplaceBlock("base\n", entries); got != first {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i+2, got, first)
		}
	}
	ai, mi, zi := strings.Index(first, "\na"), strings.Index(first, "\nm"), strings.Index(first, "\nz")
	if ai >= mi || mi >= zi {
		t.Errorf("entries are not sorted:\n%s", first)
	}
}

// An empty set removes the block entirely rather than leaving naked markers,
// which would otherwise accumulate in the file forever.
func TestReplaceBlockWithNoEntriesRemovesTheMarkers(t *testing.T) {
	content := strings.Join([]string{"keep me", begin, "Collections/API", end}, "\n")
	result := ReplaceBlock(content, map[string]bool{})

	if strings.Contains(result, begin) || strings.Contains(result, end) {
		t.Errorf("the markers were left behind:\n%s", result)
	}
	if !strings.Contains(result, "keep me") {
		t.Errorf("the user's line was lost:\n%s", result)
	}
}

// CRLF input must not double the line endings or strand a marker with a
// trailing \r, which would stop it matching on the next read.
func TestReplaceBlockNormalisesWindowsLineEndings(t *testing.T) {
	content := "node_modules/\r\n" + begin + "\r\nCollections/API\r\n" + end + "\r\n"
	result := ReplaceBlock(content, map[string]bool{"Collections/API": true})

	if strings.Contains(result, "\r") {
		t.Errorf("a carriage return survived:\n%q", result)
	}
	if got := Entries(result); !got["Collections/API"] {
		t.Errorf("the entry did not survive a CRLF round trip: %v", got)
	}
}
