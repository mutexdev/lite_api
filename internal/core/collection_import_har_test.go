package core

import (
	"os"
	"testing"
)

// TestHARIsDetectedByShapeNotOnlyExtension. Browsers routinely save a HAR as
// plain .json from the network panel; one that reached the generic fallbacks
// would be reported as "ambiguous" rather than imported.
func TestHARIsDetectedByShapeNotOnlyExtension(t *testing.T) {
	content := harFixture(t)

	for _, name := range []string{"session.har", "network-log.json"} {
		t.Run(name, func(t *testing.T) {
			kind, collection, warnings, err := detectCollectionImport(content, name, "")
			if err != nil {
				t.Fatalf("detectCollectionImport: %v", err)
			}
			if kind != "har" {
				t.Errorf("detected kind = %q, want har", kind)
			}
			if len(collection.Items) != 4 {
				t.Errorf("got %d items, want 4", len(collection.Items))
			}
			if len(warnings) == 0 {
				t.Error("detection dropped the importer's warnings; the credential notice would never reach the user")
			}
		})
	}
}

// TestHARManualOverrideCarriesWarnings. The override path is a separate branch
// from detection, and warnings are easy to drop there — the import would work
// and the credential notice would silently vanish.
func TestHARManualOverrideCarriesWarnings(t *testing.T) {
	kind, collection, warnings, err := detectCollectionImport(harFixture(t), "anything.txt", "har")
	if err != nil {
		t.Fatalf("detectCollectionImport: %v", err)
	}
	if kind != "har" {
		t.Errorf("kind = %q, want har", kind)
	}
	if len(collection.Items) != 4 {
		t.Errorf("got %d items, want 4", len(collection.Items))
	}
	if len(warnings) == 0 {
		t.Error("the manual override path dropped the warnings")
	}
}

func harFixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(repoPath(t, "docs", "qa", "import-fixtures", "session.har"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}
