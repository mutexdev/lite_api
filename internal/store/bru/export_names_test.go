package bru

import (
	"testing"

	"github.com/mutexdev/lite_api/internal/types"
)

// uniqueExportFileBaseName sat at 33.3%. It is what stops two exported global
// environments landing on the same filename — where the second would simply
// overwrite the first, and the export would report success having written one
// fewer file than it had environments.
//
// The collision is not hypothetical. Environment names are free text and the
// filename is SANITISED, so two names that differ only in punctuation become
// the same base name.

func TestAFirstNameIsUsedUnchanged(t *testing.T) {
	used := map[string]bool{}
	if got := uniqueExportFileBaseName("staging", used); got != "staging" {
		t.Errorf("got %q, want staging", got)
	}
}

// The suffix sequence: the second is "copy", and the numbering starts at 2
// after that. "copy 1" is never produced, which matters because a directory
// listing shows the series as staging, staging copy, staging copy 2.
func TestRepeatedNamesGetTheCopySequence(t *testing.T) {
	used := map[string]bool{}
	want := []string{"staging", "staging copy", "staging copy 2", "staging copy 3"}
	for _, expected := range want {
		if got := uniqueExportFileBaseName("staging", used); got != expected {
			t.Fatalf("got %q, want %q", got, expected)
		}
	}
}

// A name already taken by an EARLIER environment's suffix is skipped rather
// than reused. Without the check the loop would hand out a name it had already
// given away.
func TestAnAlreadyTakenSuffixIsSkipped(t *testing.T) {
	used := map[string]bool{"staging": true, "staging copy": true}
	if got := uniqueExportFileBaseName("staging", used); got != "staging copy 2" {
		t.Errorf("got %q, want staging copy 2", got)
	}
}

// The map is an OUTPUT as well as an input — each returned name is recorded, or
// the next call would return the same one.
func TestTheChosenNameIsRecorded(t *testing.T) {
	used := map[string]bool{}
	first := uniqueExportFileBaseName("api", used)
	if !used[first] {
		t.Fatalf("%q was returned but not recorded, so the next call would repeat it", first)
	}
	if second := uniqueExportFileBaseName("api", used); second == first {
		t.Errorf("the same name was handed out twice: %q", first)
	}
}

func TestANilUsedMapIsAccepted(t *testing.T) {
	if got := uniqueExportFileBaseName("api", nil); got != "api" {
		t.Errorf("got %q", got)
	}
}

// THE END-TO-END CASE, and the reason any of this exists. Two environments
// whose names differ only in punctuation sanitise to the SAME base name.
// Without the uniquifier the export writes two files with one name, and
// whatever unpacks it keeps one.
func TestEnvironmentsThatSanitiseAlikeGetDistinctFiles(t *testing.T) {
	files, err := BrunoEnvironmentExportFiles([]types.Environment{
		{Name: "My Env!"},
		{Name: "My Env?"},
		{Name: "My Env#"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files for 3 environments", len(files))
	}
	seen := map[string]bool{}
	for _, file := range files {
		if seen[file.Name] {
			t.Errorf("two environments exported to %q; one would overwrite the other", file.Name)
		}
		seen[file.Name] = true
	}
}

// Every file keeps the .json extension regardless of which suffix it got, so
// the copies are still recognised as environment files on import.
func TestEveryExportedFileKeepsItsExtension(t *testing.T) {
	files, err := BrunoEnvironmentExportFiles([]types.Environment{{Name: "dup"}, {Name: "dup"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if len(file.Name) < 5 || file.Name[len(file.Name)-5:] != ".json" {
			t.Errorf("%q does not end in .json", file.Name)
		}
	}
}

// An environment with no usable name at all still gets a file rather than one
// called ".json", which most tools will not show.
func TestAnUnnameableEnvironmentStillGetsAFile(t *testing.T) {
	files, err := BrunoEnvironmentExportFiles([]types.Environment{{Name: "!!!"}, {Name: "???"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files", len(files))
	}
	if files[0].Name == ".json" || files[1].Name == ".json" {
		t.Error("an environment exported to a bare extension")
	}
	if files[0].Name == files[1].Name {
		t.Errorf("both unnameable environments got %q", files[0].Name)
	}
}
