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
//
// This test used to append to the clone and then assert `len(original) != 0`.
// That could never fail: append returns a NEW slice header, so the original's
// length is unreachable from it no matter what the clone shares. The assertion
// held whether CloneKeyValues copied, aliased, or returned the input untouched.
//
// The property that actually matters is about the BACKING ARRAY, and it is only
// observable when the input has spare capacity — which is exactly the case a
// zero-length literal cannot produce.
func TestCloneOfEmptyInputIsIndependent(t *testing.T) {
	if cloned := CloneKeyValues([]KeyValue{}); cloned != nil {
		t.Errorf("got %#v, want nil — there is then no array for a later append to reach", cloned)
	}
	if cloned := CloneKeyValues(nil); cloned != nil {
		t.Errorf("got %#v, want nil", cloned)
	}
}

// Appending to a clone must not reach into the caller's spare capacity.
//
// A clone that returned `values[:len(values)]` would pass every length and
// equality check and still corrupt the caller: the append lands in the shared
// array at index len, which the caller can still see by re-slicing.
func TestCloneDoesNotWriteIntoTheOriginalsSpareCapacity(t *testing.T) {
	original := make([]KeyValue, 1, 4)
	original[0] = KeyValue{Name: "kept"}

	cloned := CloneKeyValues(original)
	cloned = append(cloned, KeyValue{Name: "added"})

	if spare := original[:2]; spare[1].Name != "" {
		t.Errorf("append to the clone wrote %q into the original's spare capacity", spare[1].Name)
	}
	if original[0].Name != "kept" {
		t.Errorf("the original's first element became %q", original[0].Name)
	}
	if cloned[1].Name != "added" {
		t.Errorf("the clone did not receive the appended value: %#v", cloned)
	}
}

// CloneAuthConfig makes FOUR near-identical calls, one per parameter list, and
// the existing test above covers only AdditionalParams. The other three were
// exercised solely on their nil path — so deleting any one of those lines
// failed nothing.
//
// The consequence is not abstract. Auth config is inherited down the folder
// chain, so a shared slice means editing one request's OAuth2 refresh
// parameters rewrites another request's. Nothing reports it; the second request
// simply starts sending a value nobody set on it.
func TestCloneAuthConfigCopiesEveryParameterList(t *testing.T) {
	param := func(name string) []OAuth2AdditionalParam {
		return []OAuth2AdditionalParam{{Name: name, Value: "original"}}
	}

	for _, tc := range []struct {
		list string
		get  func(*AuthConfig) []OAuth2AdditionalParam
	}{
		{"AuthorizationAdditionalParams", func(a *AuthConfig) []OAuth2AdditionalParam {
			return a.OAuth2.AuthorizationAdditionalParams
		}},
		{"TokenAdditionalParams", func(a *AuthConfig) []OAuth2AdditionalParam {
			return a.OAuth2.TokenAdditionalParams
		}},
		{"RefreshAdditionalParams", func(a *AuthConfig) []OAuth2AdditionalParam {
			return a.OAuth2.RefreshAdditionalParams
		}},
	} {
		original := AuthConfig{Mode: "oauth2", OAuth2: OAuth2Auth{
			AuthorizationAdditionalParams: param("auth"),
			TokenAdditionalParams:         param("token"),
			RefreshAdditionalParams:       param("refresh"),
		}}
		cloned := CloneAuthConfig(original)

		tc.get(&cloned)[0].Value = "MUTATED"
		if got := tc.get(&original)[0].Value; got != "original" {
			t.Errorf("%s is shared with the original (value became %q)", tc.list, got)
		}
	}
}

// The three remaining slice clones sat at 40% for the same reason: only their
// empty-input path was reached. Each is a copy that a caller relies on to edit
// without the original changing.
func TestTheRemainingSliceClonesDoNotAlias(t *testing.T) {
	assertions := []Assertion{{Expression: "res.status", Operator: "eq", Value: "200"}}
	clonedAssertions := CloneAssertions(assertions)
	clonedAssertions[0].Value = "MUTATED"
	if assertions[0].Value != "200" {
		t.Error("CloneAssertions shares its backing array")
	}

	grpc := []GrpcMessage{{Name: "req", Content: "{}"}}
	clonedGrpc := CloneGrpcMessages(grpc)
	clonedGrpc[0].Content = "MUTATED"
	if grpc[0].Content != "{}" {
		t.Error("CloneGrpcMessages shares its backing array")
	}

	ws := []WSMessage{{Type: "text", Content: "hi"}}
	clonedWS := CloneWSMessages(ws)
	clonedWS[0].Content = "MUTATED"
	if ws[0].Content != "hi" {
		t.Error("CloneWSMessages shares its backing array")
	}
}

// Empty input returns nil rather than an empty non-nil slice, for every one of
// them. An empty slice with spare capacity is the case the earlier correction
// in this file is about: appending to it writes into an array the caller may
// still hold.
func TestEverySliceCloneReturnsNilForEmptyInput(t *testing.T) {
	if got := CloneOAuth2AdditionalParams(nil); got != nil {
		t.Errorf("CloneOAuth2AdditionalParams(nil) = %#v", got)
	}
	if got := CloneOAuth2AdditionalParams([]OAuth2AdditionalParam{}); got != nil {
		t.Errorf("CloneOAuth2AdditionalParams(empty) = %#v", got)
	}
	if got := CloneAssertions([]Assertion{}); got != nil {
		t.Errorf("CloneAssertions(empty) = %#v", got)
	}
	if got := CloneGrpcMessages([]GrpcMessage{}); got != nil {
		t.Errorf("CloneGrpcMessages(empty) = %#v", got)
	}
	if got := CloneWSMessages([]WSMessage{}); got != nil {
		t.Errorf("CloneWSMessages(empty) = %#v", got)
	}
}

// And the length is preserved — a clone that drops elements is worse than one
// that shares them, because the loss is silent and permanent.
func TestSliceClonesPreserveEveryElement(t *testing.T) {
	params := []OAuth2AdditionalParam{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	cloned := CloneOAuth2AdditionalParams(params)
	if len(cloned) != len(params) {
		t.Fatalf("got %d elements, want %d", len(cloned), len(params))
	}
	for i := range params {
		if cloned[i].Name != params[i].Name {
			t.Errorf("element %d: got %q, want %q", i, cloned[i].Name, params[i].Name)
		}
	}
}
