package main

// US-059 — the Postman parity ledger.
//
// A ledger's only value is that its claims are checkable. These tests make the
// claims checkable BY MACHINE, because the failure mode is not a wrong entry —
// it is an entry that was true when written and quietly stopped being true when
// a test was renamed or deleted. Nothing about a stale ledger looks wrong; it
// just cites evidence that no longer exists.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goTestNamesInPackage collects every Go test function declared in the package.
func goTestNamesInPackage(t *testing.T) map[string]bool {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	names := map[string]bool{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			names[match[1]] = true
		}
	}
	return names
}

// TestPostmanLedgerCitesTestsThatExist is the guard that makes the ledger
// worth reading. An entry naming a deleted or renamed test still reads as
// evidence, which is worse than naming none.
func TestPostmanLedgerCitesTestsThatExist(t *testing.T) {
	known := goTestNamesInPackage(t)

	for _, row := range postmanFeatures() {
		for _, cited := range row.Tests {
			// Only Go test names are checkable here. Frontend suites and UI
			// smokes are cited by description and verified elsewhere; asserting
			// on them would mean parsing prose.
			if !strings.HasPrefix(cited, "Test") {
				continue
			}
			if !known[cited] {
				t.Errorf("ledger row %q cites %s, which does not exist in this package", row.ID, cited)
			}
		}
	}
}

// TestPostmanLedgerRowsAreWellFormed. A row with no tests or an unknown status
// is a claim with no evidence behind it.
func TestPostmanLedgerRowsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range postmanFeatures() {
		if row.ID == "" || row.Name == "" {
			t.Errorf("row %+v has no id or name", row)
		}
		if seen[row.ID] {
			t.Errorf("duplicate ledger id %q", row.ID)
		}
		seen[row.ID] = true

		if row.Category != "Postman parity" {
			t.Errorf("row %q has category %q; Postman rows must be distinguishable from the Bruno ledger", row.ID, row.Category)
		}
		switch row.Status {
		case "done", "partial", "missing":
		default:
			t.Errorf("row %q has unknown status %q", row.ID, row.Status)
		}
		if len(row.Tests) == 0 {
			t.Errorf("row %q cites no tests at all", row.ID)
		}
		if len(strings.TrimSpace(row.Description)) < 40 {
			t.Errorf("row %q has a description too short to be evidence: %q", row.ID, row.Description)
		}
		// A "partial" row must say what is still missing, or the status is
		// unactionable — the whole point of partial over done is naming the gap.
		if row.Status == "partial" && !strings.Contains(strings.ToLower(row.Description), "gap") {
			t.Errorf("row %q is partial but never names a gap", row.ID)
		}
	}
}

// TestBrunoLedgerIsRetained. US-059 requires the Bruno ledger kept, and
// appending to a slice is exactly the operation where a stray reassignment
// silently replaces it instead.
func TestBrunoLedgerIsRetained(t *testing.T) {
	features := defaultFeatures()

	var bruno, postman int
	for _, row := range features {
		if row.Category == "Postman parity" {
			postman++
			continue
		}
		bruno++
	}

	if bruno == 0 {
		t.Fatal("the Bruno ledger is gone; US-059 requires it retained alongside the Postman rows")
	}
	if postman == 0 {
		t.Fatal("no Postman rows reached the ledger")
	}
	if postman != len(postmanFeatures()) {
		t.Errorf("%d of %d Postman rows reached the ledger", postman, len(postmanFeatures()))
	}

	// The Bruno rows must keep citing Bruno, not inherit Postman's references.
	for _, row := range features {
		if row.Category == "Postman parity" {
			continue
		}
		joined := strings.Join(row.SourceRefs, " ")
		if strings.Contains(joined, "learning.postman.com") {
			t.Errorf("Bruno row %q now cites Postman documentation", row.ID)
		}
	}
}

// TestPostmanLedgerRowsCiteTheirOwnReferences. The two ledgers exist because
// their evidence is different in kind: Bruno's is a checked-out tree that can
// be read, Postman's is public documentation that cannot. A Postman row citing
// the Bruno tree would be claiming evidence from a source that says nothing
// about Postman.
func TestPostmanLedgerRowsCiteTheirOwnReferences(t *testing.T) {
	for _, row := range postmanFeatures() {
		joined := strings.Join(row.SourceRefs, " ")
		if strings.Contains(joined, "Workspace/bruno") {
			t.Errorf("Postman row %q cites the Bruno source tree", row.ID)
		}
		if !strings.Contains(joined, "learning.postman.com") {
			t.Errorf("Postman row %q cites no Postman documentation", row.ID)
		}
		if !strings.Contains(joined, "POSTMAN-PARITY.md") {
			t.Errorf("Postman row %q does not point at the ledger document", row.ID)
		}
	}
}

// TestPostmanParityDocumentExistsAndCoversEveryRow keeps the markdown and the
// in-app ledger from drifting apart, which they otherwise do immediately: one
// is edited by hand and the other in Go, and nothing connects them.
func TestPostmanParityDocumentExistsAndCoversEveryRow(t *testing.T) {
	data, err := os.ReadFile("POSTMAN-PARITY.md")
	if err != nil {
		t.Fatalf("POSTMAN-PARITY.md is missing: %v", err)
	}
	document := string(data)

	for _, row := range postmanFeatures() {
		if !strings.Contains(document, row.Name) {
			t.Errorf("POSTMAN-PARITY.md never mentions the ledger row %q", row.Name)
		}
	}

	// The Bruno ledger stays its own document.
	if _, err := os.Stat("PARITY.md"); err != nil {
		t.Errorf("PARITY.md was removed; the Bruno ledger must be retained: %v", err)
	}
}

// TestPostmanParityDocumentCitesRealFixtures. The document points at fixture
// files as evidence; a path that does not resolve is a citation to nothing.
func TestPostmanParityDocumentCitesRealFixtures(t *testing.T) {
	data, err := os.ReadFile("POSTMAN-PARITY.md")
	if err != nil {
		t.Fatalf("read POSTMAN-PARITY.md: %v", err)
	}

	pattern := regexp.MustCompile("`(docs/qa/import-fixtures/[A-Za-z0-9._/-]+)`")
	matches := pattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatal("the document cites no fixture files, so its import claims rest on nothing checkable")
	}
	for _, match := range matches {
		if _, err := os.Stat(filepath.FromSlash(match[1])); err != nil {
			t.Errorf("POSTMAN-PARITY.md cites %s, which does not exist: %v", match[1], err)
		}
	}
}
