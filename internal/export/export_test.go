package export

import (
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

// What this package emits LEAVES the machine — a shared collection, a zip, a
// Postman file. Two things matter more than the rest: a share must not carry a
// secret, and an archive entry must not name a path outside the archive.
//
// These were reachable only through the application's integration tests before
// the package existed. They are its own now.

func collectionWithSecrets() types.Collection {
	return types.Collection{
		Name:             "API",
		Format:           "yml",
		Remote:           "git@github.com:someone/private.git",
		RuntimeVariables: []types.Variable{{Name: "runtime", Value: "leaked"}},
		Environments: []types.Environment{{
			Name: "prod",
			Variables: []types.Variable{
				{Name: "token", Value: "s3cret", Secret: true},
				{Name: "host", Value: "example.test"},
			},
		}},
		Items: []types.RequestItem{
			{ID: "a", Name: "Saved", FilePath: "/home/someone/api/saved.yml"},
			{ID: "b", Name: "Scratch", Transient: true},
		},
	}
}

// THE ONE THAT MATTERS. A shared collection must not carry secret VALUES. The
// names stay so the recipient knows what to supply; the values do not travel.
func TestShareSnapshotStripsSecretValuesAndKeepsTheirNames(t *testing.T) {
	snapshot := ShareSnapshot(collectionWithSecrets())

	if len(snapshot.Environments) != 1 || len(snapshot.Environments[0].Variables) != 2 {
		t.Fatalf("environments changed shape: %+v", snapshot.Environments)
	}
	var secret, plain types.Variable
	for _, v := range snapshot.Environments[0].Variables {
		if v.Secret {
			secret = v
		} else {
			plain = v
		}
	}
	if secret.Name != "token" {
		t.Errorf("the secret variable lost its name: %+v", secret)
	}
	if secret.Value != "" {
		t.Errorf("a SECRET VALUE was included in a shared collection: %v", secret.Value)
	}
	if plain.Value != "example.test" {
		t.Errorf("a non-secret value was stripped: %v", plain.Value)
	}
}

// The snapshot must not mutate the live collection. It takes its argument by
// value, but a Collection holds slices — so the environment variables are
// shared unless they are copied, and stripping the share would strip the
// user's own environment.
func TestShareSnapshotDoesNotStripTheLiveCollection(t *testing.T) {
	live := collectionWithSecrets()
	_ = ShareSnapshot(live)

	if got := live.Environments[0].Variables[0].Value; got != "s3cret" {
		t.Errorf("the live collection's secret was cleared: %v", got)
	}
	if len(live.Items) != 2 {
		t.Errorf("the live collection lost items: %d", len(live.Items))
	}
}

// A share carries the user's requests, not their machine. Absolute file paths
// name their home directory, and a transient scratch request is not part of
// what they meant to share.
func TestShareSnapshotDropsLocalDetail(t *testing.T) {
	snapshot := ShareSnapshot(collectionWithSecrets())

	if snapshot.Remote != "" {
		t.Errorf("the git remote travelled with the share: %q", snapshot.Remote)
	}
	if snapshot.RuntimeVariables != nil {
		t.Errorf("runtime variables travelled: %+v", snapshot.RuntimeVariables)
	}
	if len(snapshot.Items) != 1 {
		t.Fatalf("expected only the saved request, got %d", len(snapshot.Items))
	}
	if snapshot.Items[0].Name != "Saved" {
		t.Errorf("the wrong request survived: %q", snapshot.Items[0].Name)
	}
	if snapshot.Items[0].FilePath != "" {
		t.Errorf("a local file path travelled: %q", snapshot.Items[0].FilePath)
	}
}

// ARCHIVE PATH SAFETY. An entry name is written into a zip and later extracted
// by something the user chooses. A name that escapes the archive root is how an
// extraction writes outside the directory it was pointed at.
func TestArchivePathsCannotEscapeTheArchive(t *testing.T) {
	for _, escape := range []string{
		"../outside.yml",
		"../../etc/passwd",
		"/etc/passwd",
		"a/../../b.yml",
		"..",
		"./..",
	} {
		if got := cleanExportArchivePath(escape); got != "" {
			t.Errorf("cleanExportArchivePath(%q) = %q, want it refused", escape, got)
		}
	}
}

// Ordinary names survive, normalised to forward slashes so the archive reads
// the same on every platform.
func TestArchivePathsKeepOrdinaryNames(t *testing.T) {
	for input, want := range map[string]string{
		"request.yml":          "request.yml",
		"folder/request.yml":   "folder/request.yml",
		"  spaced.yml  ":       "spaced.yml",
		"./request.yml":        "request.yml",
		"folder//request.yml":  "folder/request.yml",
		"folder/./request.yml": "folder/request.yml",
	} {
		if got := cleanExportArchivePath(input); got != want {
			t.Errorf("cleanExportArchivePath(%q) = %q, want %q", input, got, want)
		}
	}
}

// .git and node_modules are refused anywhere in the path. A collection
// directory can contain either, and neither belongs in something the user
// hands to somebody else.
func TestArchivePathsRefuseRepositoryAndDependencyDirectories(t *testing.T) {
	for _, name := range []string{
		".git/config",
		"folder/.git/config",
		"node_modules/pkg/index.js",
		"a/node_modules/b.yml",
	} {
		if got := cleanExportArchivePath(name); got != "" {
			t.Errorf("cleanExportArchivePath(%q) = %q, want it refused", name, got)
		}
	}
	// A file merely NAMED like one is fine; only a path segment counts.
	if got := cleanExportArchivePath("gitignore-notes.yml"); got == "" {
		t.Error("a file whose name merely resembles .git was refused")
	}
}

// Two requests with the same name must not collide into one archive entry, or
// the export silently contains fewer files than the collection had.
func TestArchiveNamesAreMadeUnique(t *testing.T) {
	used := map[string]bool{}
	first := uniqueCollectionExportPath("request.yml", used)
	used[first] = true
	second := uniqueCollectionExportPath("request.yml", used)
	used[second] = true
	third := uniqueCollectionExportPath("request.yml", used)

	if first != "request.yml" {
		t.Errorf("first = %q, want the name unchanged", first)
	}
	if second == first || third == first || second == third {
		t.Errorf("names collided: %q, %q, %q", first, second, third)
	}
	for _, name := range []string{second, third} {
		if !strings.HasSuffix(name, ".yml") {
			t.Errorf("%q lost its extension; the archive entry would not be recognised", name)
		}
	}
}

// The zip builder produces one entry per request plus the collection's own
// metadata, and every entry name is archive-safe.
func TestBuildZipFilesProducesSafeEntriesForEveryRequest(t *testing.T) {
	collection := types.Collection{
		Name:   "API",
		Format: "yml",
		Items: []types.RequestItem{
			{ID: "a", Name: "First"},
			{ID: "b", Name: "Second"},
		},
	}
	// The order is (files, FOLDER count, REQUEST count, err). Reading the second
	// value as the request count is an easy mistake and was mine first.
	files, folders, requests, err := BuildZipFiles(collection)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("reported %d requests, want 2", requests)
	}
	if folders != 0 {
		t.Errorf("reported %d folders for a flat collection", folders)
	}
	if len(files) < 3 {
		t.Fatalf("expected collection metadata plus two requests, got %d entries", len(files))
	}
	seen := map[string]bool{}
	for _, f := range files {
		if f.Name == "" {
			t.Error("an entry has no name")
		}
		if cleanExportArchivePath(f.Name) != f.Name {
			t.Errorf("entry %q is not archive-safe", f.Name)
		}
		if seen[f.Name] {
			t.Errorf("duplicate entry %q", f.Name)
		}
		seen[f.Name] = true
		if len(f.Content) == 0 {
			t.Errorf("entry %q is empty", f.Name)
		}
	}
}

// The zip is a real archive, not a concatenation.
func TestZipFilesProducesAReadableArchive(t *testing.T) {
	data, err := ZipFiles([]File{{Name: "a.yml", Content: []byte("one")}, {Name: "b/c.yml", Content: []byte("two")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("no bytes produced")
	}
	if string(data[:2]) != "PK" {
		t.Errorf("output does not start with the zip magic: %q", data[:2])
	}
}

// The version is NORMALISED to a three-part semver, not passed through. A
// collection carrying "2.1" exports as "v2.1.0", which is what an importer
// expecting semver needs.
func TestDisplayVersionNormalisesToSemver(t *testing.T) {
	for input, want := range map[string]string{
		"":        "v1.0.0",
		"2.1":     "v2.1.0",
		"3":       "v3.0.0",
		"v4.5.6":  "v4.5.6",
		"1.2.3":   "v1.2.3",
		"  2.0  ": "v2.0.0",
	} {
		if got := DisplayVersion(input); got != want {
			t.Errorf("DisplayVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

// The docs builder produces YAML that parses, with the request count it claims.
func TestBuildDocsYAMLCountsWhatItEmits(t *testing.T) {
	collection := types.Collection{
		Name:   "API",
		Format: "yml",
		Items: []types.RequestItem{
			{ID: "a", Name: "First", Method: "GET", URL: "https://example.test/a"},
			{ID: "b", Name: "Second", Method: "POST", URL: "https://example.test/b"},
		},
	}
	content, folders, requests, err := BuildDocsYAML(collection, nil, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("reported %d requests, want 2", requests)
	}
	if folders != 0 {
		t.Errorf("reported %d folders for a flat collection", folders)
	}
	for _, want := range []string{"First", "Second"} {
		if !strings.Contains(content, want) {
			t.Errorf("the docs do not mention %q:\n%s", want, content)
		}
	}
}

// The docs are HTML that embeds the YAML; the YAML must be escaped or a
// collection name containing markup would break the page.
func TestBuildDocsHTMLEscapesTheContentItEmbeds(t *testing.T) {
	html, err := BuildDocsHTML("API", "name: <script>alert(1)</script>")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("raw markup from the collection reached the generated HTML unescaped")
	}
}
