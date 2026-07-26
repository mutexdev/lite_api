package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/scalar"
)

// TestSwagger2IsDetectedAndImported is the end-to-end claim: the conversion is
// consumed by the real importer, not merely well-formed. Before this story the
// same input returned "imports from Swagger 2 are not supported".
func TestSwagger2IsDetectedAndImported(t *testing.T) {
	kind, collection, _, err := detectCollectionImport(swagger2Fixture(t), "swagger2.json", "")
	if err != nil {
		t.Fatalf("detectCollectionImport: %v", err)
	}
	if kind != "swagger-2" {
		t.Errorf("detected kind = %q, want swagger-2", kind)
	}
	if len(collection.Items) != 4 {
		var names []string
		for _, item := range collection.Items {
			names = append(names, item.Method+" "+item.Name)
		}
		t.Fatalf("got %d items %v, want 4", len(collection.Items), names)
	}

	byName := map[string]RequestItem{}
	for _, item := range collection.Items {
		byName[item.Name] = item
	}

	list, ok := byName["List users"]
	if !ok {
		t.Fatalf("the listUsers operation was not imported: %v", byName)
	}
	// The importer parameterises the server as a {{baseUrl}} collection
	// variable rather than inlining it, so that is where host and basePath have
	// to land. Without them the variable is empty and every request resolves to
	// a bare path — an import that looks perfect and 404s.
	if !strings.Contains(list.URL, "/users") {
		t.Errorf("imported URL %q does not carry the path", list.URL)
	}
	var baseURL string
	for _, variable := range collection.Variables {
		if variable.Name == "baseUrl" {
			baseURL = scalar.YAMLString(variable.Value)
		}
	}
	if baseURL != "https://api.example.test/v2" {
		t.Errorf("baseUrl = %q, want https://api.example.test/v2 — host and basePath did not survive", baseURL)
	}

	create, ok := byName["Create a user"]
	if !ok {
		t.Fatal("the createUser operation was not imported")
	}
	if create.Method != "POST" {
		t.Errorf("method = %q, want POST", create.Method)
	}
	if create.Body.Mode == "none" || strings.TrimSpace(create.Body.JSON) == "" {
		t.Errorf("the body parameter did not become a request body: mode=%q json=%q", create.Body.Mode, create.Body.JSON)
	}
}

// TestSwagger2ManualOverrideImports covers the explicit-kind path, which is a
// separate branch from detection.
func TestSwagger2ManualOverrideImports(t *testing.T) {
	collection, err := collectionFromImport(ImportPayload{
		Kind:    "swagger-2",
		Name:    "manual",
		Content: swagger2Fixture(t),
	})
	if err != nil {
		t.Fatalf("collectionFromImport: %v", err)
	}
	if len(collection.Items) != 4 {
		t.Errorf("got %d items, want 4", len(collection.Items))
	}
}

// Fixture helpers local to package main. The importers package has its own
// copies with ../../ paths; these read from the repo root, which is this
// package's working directory during tests.
func swagger2Fixture(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("docs", "qa", "import-fixtures", "swagger2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(content)
}
