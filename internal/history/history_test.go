package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistorySearchNarrowsOnEveryTerm(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "jsonl")}
	for _, entry := range []HistoryEntry{
		{ID: "1", Name: "list users", Method: "GET", URL: "https://api.test/users", Status: 200},
		{ID: "2", Name: "create user", Method: "POST", URL: "https://api.test/users", Status: 201},
		{ID: "3", Name: "list orders", Method: "GET", URL: "https://api.test/orders", Status: 500},
	} {
		if err := store.Append(entry); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// Every term must match, so a second word narrows rather than widening the
	// way a single-substring search would.
	got, err := store.List(HistoryQuery{Text: "post users"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != "2" {
		t.Errorf("multi-term search returned %v, want just the POST", ids(got))
	}

	got, _ = store.List(HistoryQuery{Text: "users"})
	if len(got) != 2 {
		t.Errorf("single-term search returned %v, want both /users entries", ids(got))
	}

	got, _ = store.List(HistoryQuery{Method: "get"})
	if len(got) != 2 {
		t.Errorf("method filter returned %v, want the two GETs", ids(got))
	}

	got, _ = store.List(HistoryQuery{OnlyFailures: true})
	if len(got) != 1 || got[0].ID != "3" {
		t.Errorf("failure filter returned %v, want the 500", ids(got))
	}

	got, _ = store.List(HistoryQuery{Text: "nothing matches this"})
	if len(got) != 0 {
		t.Errorf("a non-matching search returned %v", ids(got))
	}
}

func TestHistoryListsNewestFirstAndAppliesTheLimitToTheNewest(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "jsonl")}
	for i := range 10 {
		if err := store.Append(HistoryEntry{ID: fmt.Sprintf("%d", i), Method: "GET", URL: "https://api.test/"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := store.List(HistoryQuery{Limit: 3})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Walking the file forwards and stopping at the limit would return the
	// OLDEST three, which is the opposite of what a history list is for.
	if len(got) != 3 || got[0].ID != "9" || got[2].ID != "7" {
		t.Errorf("limited list returned %v, want the newest three", ids(got))
	}
}

// TestHistoryCompactsPastTheCap. Without compaction the file grows without
// bound and the startup read gets slower forever.
func TestHistoryCompactsPastTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jsonl")
	store := &Store{path: path}

	for i := range CompactAt + 5 {
		if err := store.Append(HistoryEntry{ID: fmt.Sprintf("%d", i), Method: "GET", URL: "https://api.test/"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// The FILE is what compaction changes. Asserting on list() instead would
	// measure the query limit, which caps the result at Limit whether
	// or not anything was ever compacted — the check would pass against a store
	// that grows without bound. A negative control caught exactly that.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	lines := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	// The bound is the COMPACTION TRIGGER, not the cap. The file is deliberately
	// allowed to drift above Limit between compactions — that drift is
	// the entire reason the format is append-only, since compacting at exactly
	// the cap would rewrite the whole file on every send once it filled up.
	// What must hold is that it never grows past the trigger.
	if lines > CompactAt {
		t.Errorf("the file holds %d lines after %d appends; compaction never ran", lines, CompactAt+5)
	}
	if lines < Limit {
		t.Errorf("the file holds only %d lines; compaction discarded more than the cap", lines)
	}

	entries, err := store.List(HistoryQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) > Limit {
		t.Errorf("got %d entries after compaction, want at most %d", len(entries), Limit)
	}
	// The newest must survive; compaction that dropped the tail instead of the
	// head would discard exactly what the user is looking for.
	if entries[0].ID != fmt.Sprintf("%d", CompactAt+4) {
		t.Errorf("newest entry is %q; compaction kept the wrong end", entries[0].ID)
	}
}

// TestHistoryTolerablesMalformedLines. A truncated line is the likely result of
// a crash mid-write, and it must not make the whole log unreadable.
func TestHistoryToleratesMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jsonl")
	good, err := json.Marshal(HistoryEntry{ID: "good", Method: "GET", URL: "https://api.test/"})
	if err != nil {
		t.Fatal(err)
	}
	content := string(good) + "\n{\"id\":\"trunc\",\"met\n" + string(good) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	store := &Store{path: path}
	entries, err := store.List(HistoryQuery{})
	if err != nil {
		t.Fatalf("a malformed line made the whole log unreadable: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want the 2 intact ones", len(entries))
	}
}

func TestHistoryListOnAnEmptyStoreIsNotAnError(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "never-written.jsonl")}
	entries, err := store.List(HistoryQuery{})
	if err != nil {
		t.Fatalf("listing a store with no file failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries from an empty store", len(entries))
	}
}

func ids(entries []HistoryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}
