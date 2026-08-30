package importers

// Every folder in an Insomnia export gets its own path.
//
// The Insomnia walk shared postmanFolderPath, a pure function of the parent and
// the name, which merged distinct folders three ways: two siblings with the
// same name, two whose names sanitise to the same string ("A/B" and "A-B" both
// become "A-B"), and an unnamed folder — which sanitises to "untitled" and was
// hoisted into its parent while a FolderConfig was still registered on the
// parent's own path, shadowing it. Each of those silently put requests
// somewhere other than where the export had them, and the move was invisible
// until someone went looking for a request that was no longer where they left
// it. The Postman importer grew a per-import path registry for exactly this;
// this is the same registry.

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

func folderPathsOf(collection types.Collection) []string {
	paths := make([]string, 0, len(collection.Folders))
	for _, folder := range collection.Folders {
		paths = append(paths, folder.Path)
	}
	return paths
}

func requestFolderPath(t *testing.T, collection types.Collection, name string) string {
	t.Helper()
	for _, item := range collection.Items {
		if item.Name == name {
			return item.FolderPath
		}
	}
	t.Fatalf("request %q was not imported: %#v", name, collection.Items)
	return ""
}

func uniqueStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

const insomniaV5SiblingFolderCollisions = `
type: collection.insomnia.rest/5.0
name: Colliding Folders
collection:
  - name: Reports
    children:
      - name: First
        method: GET
        url: https://api.example.test/reports/first
  - name: Reports
    children:
      - name: Second
        method: GET
        url: https://api.example.test/reports/second
  - name: A/B
    children:
      - name: Third
        method: GET
        url: https://api.example.test/ab/third
  - name: A-B
    children:
      - name: Fourth
        method: GET
        url: https://api.example.test/a-b/fourth
`

func TestInsomniaGivesCollidingSiblingFoldersDistinctPaths(t *testing.T) {
	collection, err := ImportInsomnia(insomniaV5SiblingFolderCollisions, "Colliding Folders")
	if err != nil {
		t.Fatalf("ImportInsomnia: %v", err)
	}
	paths := folderPathsOf(collection)
	if len(paths) != 4 {
		t.Fatalf("expected four folders, got %v", paths)
	}
	if !uniqueStrings(paths) {
		t.Fatalf("two folders claimed the same path, so their requests are merged into one folder: %v", paths)
	}

	first := requestFolderPath(t, collection, "First")
	second := requestFolderPath(t, collection, "Second")
	if first == second {
		t.Fatalf("requests from two different Reports folders landed in the same folder %q", first)
	}
	third := requestFolderPath(t, collection, "Third")
	fourth := requestFolderPath(t, collection, "Fourth")
	if third == fourth {
		t.Fatalf(`"A/B" and "A-B" sanitise to the same name and were merged into %q`, third)
	}
}

const insomniaV5UnnamedFolder = `
type: collection.insomnia.rest/5.0
name: Unnamed Folder
collection:
  - name: Parent
    children:
      - name: Direct
        method: GET
        url: https://api.example.test/direct
      - name: ""
        children:
          - name: Nested
            method: GET
            url: https://api.example.test/nested
`

func TestInsomniaDoesNotHoistAnUnnamedFolderIntoItsParent(t *testing.T) {
	collection, err := ImportInsomnia(insomniaV5UnnamedFolder, "Unnamed Folder")
	if err != nil {
		t.Fatalf("ImportInsomnia: %v", err)
	}
	paths := folderPathsOf(collection)
	if !uniqueStrings(paths) {
		t.Fatalf("the unnamed folder registered a second FolderConfig on its parent's path: %v", paths)
	}

	direct := requestFolderPath(t, collection, "Direct")
	nested := requestFolderPath(t, collection, "Nested")
	if direct == nested {
		t.Fatalf("the unnamed folder was hoisted into its parent, so its request now sits beside the parent's own: both in %q", direct)
	}
	if direct != "Parent" {
		t.Fatalf("the parent folder's own request moved: %q", direct)
	}
}

// The correct behaviour must not change: ordinary nested folders keep the plain
// sanitised join they have always had.
func TestInsomniaKeepsPlainNestedFolderPaths(t *testing.T) {
	const export = `
type: collection.insomnia.rest/5.0
name: Plain Folders
collection:
  - name: Admin APIs
    children:
      - name: Users
        children:
          - name: Get User
            method: GET
            url: https://api.example.test/users/1
`
	collection, err := ImportInsomnia(export, "Plain Folders")
	if err != nil {
		t.Fatalf("ImportInsomnia: %v", err)
	}
	if got := requestFolderPath(t, collection, "Get User"); got != "Admin APIs/Users" {
		t.Fatalf("nested folder path changed: %q", got)
	}
	paths := folderPathsOf(collection)
	if len(paths) != 2 || paths[0] != "Admin APIs" || paths[1] != "Admin APIs/Users" {
		t.Fatalf("folder registry changed the ordinary paths: %v", paths)
	}
}
