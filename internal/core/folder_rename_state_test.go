package core

import "testing"

// updateCollectionFolderRenameState normalises its four path arguments before
// use. A scan for "N sibling lines calling one function" flagged them, and
// deleting any of the four failed nothing — so this exercises the function's
// own contract directly, with the unnormalised input the normalisation exists
// for.
//
// Two of the four are load-bearing beyond the prefix replacement (which
// re-normalises internally): newPath is COMPARED against a normalised folder
// path, and newDisplayPath is used directly to derive the folder's display
// name. If either arrives with a trailing slash or an uncleaned segment, the
// comparison misses and the folder keeps its old name after a rename.
//
// BACKSLASH SEPARATORS ARE NOT TESTED HERE, and that is deliberate. filepath
// .ToSlash is the identity on Unix, where a backslash is a legal filename
// character — so `api\v1` genuinely IS a different folder from `api/v1` on
// this platform, and asserting otherwise tests Windows semantics on a Linux CI
// run. I wrote that case first and it failed correctly.

func TestFolderRenameStateNormalisesItsPathArguments(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		oldPath, newPath               string
		oldDisplayPath, newDisplayPath string
	}{
		{"already clean", "api/v1", "api/v2", "API/V1", "API/V2"},
		{"trailing slashes", "api/v1/", "api/v2/", "API/V1/", "API/V2/"},
		{"leading slashes", "/api/v1", "/api/v2", "/API/V1", "/API/V2"},
		{"surrounding whitespace", "  api/v1  ", "  api/v2  ", "  API/V1  ", "  API/V2  "},
		{"redundant segments", "api/./v1", "api/./v2", "API/./V1", "API/./V2"},
	} {
		collection := &Collection{
			Folders: []FolderConfig{
				{Path: "api/v1", DisplayPath: "API/V1", Name: "V1"},
				{Path: "api/v1/nested", DisplayPath: "API/V1/Nested", Name: "Nested"},
				{Path: "other", DisplayPath: "Other", Name: "Other"},
			},
			Items: []RequestItem{
				{ID: "r1", FolderPath: "API/V1"},
				{ID: "r2", FolderPath: "Other"},
			},
		}

		updateCollectionFolderRenameState(
			collection,
			tc.oldPath, tc.newPath,
			tc.oldDisplayPath, tc.newDisplayPath,
			"/tmp/col/api/v1", "/tmp/col/api/v2",
		)

		byPath := map[string]FolderConfig{}
		for _, folder := range collection.Folders {
			byPath[folder.Path] = folder
		}

		renamed, ok := byPath["api/v2"]
		if !ok {
			t.Errorf("%s: the renamed folder is missing; paths are %v", tc.name, keysOf(byPath))
			continue
		}
		// The name is derived from the NEW display path, and only when the
		// normalised comparison matches. An unnormalised newPath makes that
		// comparison miss and leaves the old name in place.
		if renamed.Name != "V2" {
			t.Errorf("%s: folder name = %q, want V2 — the rename comparison missed", tc.name, renamed.Name)
		}
		if renamed.DisplayPath != "API/V2" {
			t.Errorf("%s: display path = %q, want API/V2", tc.name, renamed.DisplayPath)
		}

		// A nested folder moves with its parent.
		if _, ok := byPath["api/v2/nested"]; !ok {
			t.Errorf("%s: the nested folder did not move; paths are %v", tc.name, keysOf(byPath))
		}

		// An unrelated folder is untouched.
		if other, ok := byPath["other"]; !ok || other.Name != "Other" {
			t.Errorf("%s: an unrelated folder was modified", tc.name)
		}

		// Items under the renamed folder follow its display path.
		for _, item := range collection.Items {
			if item.ID == "r1" && item.FolderPath != "API/V2" {
				t.Errorf("%s: item folder path = %q, want API/V2", tc.name, item.FolderPath)
			}
			if item.ID == "r2" && item.FolderPath != "Other" {
				t.Errorf("%s: an unrelated item was moved to %q", tc.name, item.FolderPath)
			}
		}
	}
}

func keysOf(m map[string]FolderConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
