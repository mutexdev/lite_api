// The deep-copy contract.
//
// Found by negative control: making CloneVariables return its input directly --
// aliasing instead of copying -- failed no test at all. Every function here
// exists so a caller can edit a copy without the original changing underneath
// it, and nothing was checking that.
//
// The failure this guards against does not look like a failure. An aliased
// clone works perfectly until someone edits the copy, at which point a request
// the user never touched changes, usually in a different pane, and nothing
// reports an error.
package types

import "testing"

func TestCloneVariablesDoesNotAliasTheOriginal(t *testing.T) {
	original := []Variable{{Name: "host", Value: "a"}, {Name: "token", Value: "b"}}
	cloned := CloneVariables(original)

	cloned[0].Value = "MUTATED"
	if original[0].Value != "a" {
		t.Fatal("editing the clone changed the original: the slice is aliased, not copied")
	}
}

func TestCloneKeyValuesDoesNotAliasTheOriginal(t *testing.T) {
	original := []KeyValue{{Name: "Accept", Value: "application/json", Enabled: true}}
	cloned := CloneKeyValues(original)

	cloned[0].Value = "MUTATED"
	cloned[0].Enabled = false
	if original[0].Value != "application/json" || !original[0].Enabled {
		t.Fatal("editing the clone changed the original")
	}
}

func TestCloneTagsDoesNotAliasTheOriginal(t *testing.T) {
	original := []string{"smoke", "auth"}
	cloned := CloneTags(original)

	cloned[0] = "MUTATED"
	if original[0] != "smoke" {
		t.Fatal("editing the clone changed the original")
	}
}

func TestCloneAuthConfigCopiesItsNestedSlices(t *testing.T) {
	original := AuthConfig{
		Mode:   "oauth2",
		OAuth2: OAuth2Auth{AdditionalParams: []KeyValue{{Name: "audience", Value: "api"}}},
	}
	cloned := CloneAuthConfig(original)

	// The nested slice is the part a shallow copy gets wrong: the struct is
	// copied by value, so only the slice header is shared, and mutating through
	// it is invisible at the top level.
	cloned.OAuth2.AdditionalParams[0].Value = "MUTATED"
	if original.OAuth2.AdditionalParams[0].Value != "api" {
		t.Fatal("the nested slice is shared: a struct copy is not a deep copy")
	}
}

func TestCloneRequestBodyCopiesItsNestedSlices(t *testing.T) {
	original := RequestBody{
		Mode:      "multipart",
		Multipart: []FormPart{{Name: "file", Value: "a.txt"}},
		Files:     []FileBodyEntry{{FilePath: "a.txt", Selected: true}},
	}
	cloned := CloneRequestBody(original)

	cloned.Multipart[0].Value = "MUTATED"
	cloned.Files[0].FilePath = "MUTATED"
	if original.Multipart[0].Value != "a.txt" || original.Files[0].FilePath != "a.txt" {
		t.Fatal("nested body slices are shared")
	}
}

func TestCloneRequestItemForFolderCloneIsDeep(t *testing.T) {
	original := RequestItem{
		Name:    "thing",
		Headers: []KeyValue{{Name: "Accept", Value: "application/json"}},
		Tags:    []string{"smoke"},
		Body:    RequestBody{Mode: "json", JSON: `{"a":1}`},
	}
	cloned := CloneRequestItemForFolderClone(original)

	cloned.Headers[0].Value = "MUTATED"
	cloned.Tags[0] = "MUTATED"
	if original.Headers[0].Value != "application/json" || original.Tags[0] != "smoke" {
		t.Fatal("cloning a request for a folder clone shares its slices")
	}
}

// An empty input must not come back as a shared non-nil slice either, since
// appending to that would write into whatever the caller kept.
func TestCloneOfEmptyInputIsIndependent(t *testing.T) {
	original := []KeyValue{}
	cloned := CloneKeyValues(original)
	cloned = append(cloned, KeyValue{Name: "added"})
	if len(original) != 0 {
		t.Fatal("appending to the clone of an empty slice changed the original")
	}
}
