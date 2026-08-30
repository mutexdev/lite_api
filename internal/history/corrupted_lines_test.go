package history

// A skipped line must be counted, not just skipped.
//
// Skipping a malformed line is right — one truncated entry from a crash
// mid-write must not make the whole log unreadable — but doing it silently
// means "the send I am looking for is not in the list" and "the log lost three
// hundred entries to a corrupted file" look identical from the UI, and only one
// of them is worth clearing the file over.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func storeWithCorruptedLines(t *testing.T, lines ...string) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewStore(path)
}

func TestReadCountsTheLinesItCouldNotParse(t *testing.T) {
	store := storeWithCorruptedLines(t,
		`{"id":"history-1","method":"GET","url":"https://example.test/one"}`,
		`{"id":"history-2","method":"GET",`,
		`not json at all`,
		`{"id":"history-3","method":"GET","url":"https://example.test/three"}`,
	)

	entries, err := store.List(HistoryQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected the two readable entries, got %d", len(entries))
	}
	if got := store.CorruptedLines(); got != 2 {
		t.Fatalf("the reader skipped 2 unreadable lines and counted %d; a silently shortened log is indistinguishable from an empty one", got)
	}
}

func TestReadReportsCorruptedLinesOncePerStore(t *testing.T) {
	store := storeWithCorruptedLines(t,
		`{"id":"history-1","method":"GET","url":"https://example.test/one"}`,
		`{"broken`,
	)

	reported := []string{}
	original := logf
	logf = func(format string, args ...interface{}) {
		reported = append(reported, format)
	}
	t.Cleanup(func() { logf = original })

	// List and Get both re-read the whole file; the history panel alone would
	// otherwise log on every keystroke of its search box.
	for i := 0; i < 3; i++ {
		if _, err := store.List(HistoryQuery{}); err != nil {
			t.Fatalf("List: %v", err)
		}
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one report for the corrupted file, got %d", len(reported))
	}
}

func TestACleanLogReportsNothing(t *testing.T) {
	store := storeWithCorruptedLines(t,
		`{"id":"history-1","method":"GET","url":"https://example.test/one"}`,
	)

	reported := 0
	original := logf
	logf = func(string, ...interface{}) { reported++ }
	t.Cleanup(func() { logf = original })

	if _, err := store.List(HistoryQuery{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if reported != 0 {
		t.Fatalf("a clean log reported %d times", reported)
	}
	if got := store.CorruptedLines(); got != 0 {
		t.Fatalf("a clean log counted %d corrupted lines", got)
	}
}
