package discovery

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/importers"
)

// Discovery emits sources the existing importers already read -- that is the
// whole design, and the reason there is no second Insomnia parser and no second
// Thunder Client one. This test is what keeps the claim true: it runs what
// discovery produced through the real importer and checks the request count it
// advertised is the request count that arrives.
func TestDiscoveredContentImportsThroughTheRealImporters(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeInsomnia(t, roots)
	writeThunderClient(t, roots, "Code")

	for _, installation := range Detect(roots) {
		if !installation.Readable {
			continue
		}
		found, err := ReadCollections(installation)
		if err != nil {
			t.Fatalf("%s: %v", installation.Client, err)
		}
		for _, entry := range found {
			switch entry.Kind {
			case "insomnia":
				collection, err := importers.ImportInsomnia(entry.Content, entry.Name)
				if err != nil {
					t.Fatalf("insomnia round trip: %v", err)
				}
				t.Logf("insomnia %-14q -> %d requests, %d folders", collection.Name, len(collection.Items), len(collection.Folders))
				if len(collection.Items) != entry.RequestCount {
					t.Errorf("reported %d requests, importer produced %d", entry.RequestCount, len(collection.Items))
				}
			case "postman":
				collection, warnings, err := importers.ImportPostman(entry.Content, entry.Name, false)
				if err != nil {
					t.Fatalf("postman round trip: %v", err)
				}
				t.Logf("thunder  %-14q -> %d requests, %d folders, warnings=%v", collection.Name, len(collection.Items), len(collection.Folders), warnings)
				if len(collection.Items) != entry.RequestCount {
					t.Errorf("reported %d requests, importer produced %d", entry.RequestCount, len(collection.Items))
				}
				for _, item := range collection.Items {
					t.Logf("   %s %s (folder %q) body=%s", item.Method, item.URL, item.FolderPath, item.Body.Mode)
				}
			}
		}
	}
}
