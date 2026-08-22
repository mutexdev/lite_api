// Finding the API clients already installed on the machine (US-062).
//
// Every case here runs against a synthetic tree built in a temp directory. No
// client is installed on the development machine and none will ever be
// installed on CI, so a test that needed a real Insomnia would be a test that
// never ran.
//
// The privacy boundary is the thing most worth testing: Detect must never open
// a file. Somebody who has not yet agreed to anything has not agreed to us
// reading a store that holds their bearer tokens.
package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rootsIn(dir string) Roots {
	return Roots{Home: dir, ConfigDir: filepath.Join(dir, "config"), DataDir: filepath.Join(dir, "data")}
}

func installationFor(installations []Installation, client string) (Installation, bool) {
	for _, installation := range installations {
		if installation.Client == client {
			return installation, true
		}
	}
	return Installation{}, false
}

func TestNothingInstalledIsDetectedAsNothing(t *testing.T) {
	if found := Detect(rootsIn(t.TempDir())); len(found) != 0 {
		t.Fatalf("detected %#v on an empty machine", found)
	}
}

func TestPostmanIsDetectedButNeverRead(t *testing.T) {
	dir := t.TempDir()
	roots := rootsIn(dir)
	// The real store: a Chromium IndexedDB directory. Its contents are V8
	// structured-clone blobs behind a LevelDB lock, which is why we only ever
	// look at the name of the directory.
	writeFile(t, filepath.Join(roots.ConfigDir, "Postman", "IndexedDB", "file__0.indexeddb.leveldb", "000003.log"), "\x00binary\x00")
	found := Detect(roots)
	installation, ok := installationFor(found, ClientPostman)
	if !ok {
		t.Fatalf("Postman not detected: %#v", found)
	}
	if installation.Readable {
		t.Fatal("Postman reported as readable; its store cannot be parsed and holds nothing for a signed-out user")
	}
	if strings.TrimSpace(installation.Guidance) == "" {
		t.Fatal("an unreadable client must say what the user should do instead")
	}
	if _, err := ReadCollections(installation); err == nil {
		t.Fatal("reading Postman should refuse rather than pretend")
	}
}

func TestDetectOpensNoFiles(t *testing.T) {
	dir := t.TempDir()
	roots := rootsIn(dir)
	// Every store detection knows about, all unreadable. If Detect opens any of
	// them the read fails and the test fails with it.
	secret := filepath.Join(roots.ConfigDir, "Insomnia", "insomnia.Request.db")
	writeFile(t, secret, `{"_id":"req_1","type":"Request","parentId":"wrk_1","name":"r","url":"https://e.test","method":"GET"}`)
	writeFile(t, filepath.Join(roots.ConfigDir, "Postman", "IndexedDB", "x.ldb"), "x")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Skip("cannot remove read permission on this filesystem")
	}
	defer func() { _ = os.Chmod(secret, 0o600) }()
	if os.Geteuid() == 0 {
		t.Skip("running as root, where a permission bit proves nothing")
	}
	found := Detect(roots)
	if len(found) == 0 {
		t.Fatal("detection found nothing")
	}
	if _, ok := installationFor(found, ClientInsomnia); !ok {
		t.Fatal("Insomnia should be detected from its directory alone")
	}
}

const insomniaNeDB = `{"_id":"wrk_1","type":"Workspace","parentId":null,"name":"Payments API","modified":1}
{"_id":"fld_1","type":"RequestGroup","parentId":"wrk_1","name":"Admin","modified":2}
{"_id":"req_1","type":"Request","parentId":"wrk_1","name":"List","method":"GET","url":"https://api.test/list","modified":3}
{"_id":"req_2","type":"Request","parentId":"fld_1","name":"Delete","method":"DELETE","url":"https://api.test/x","modified":4}
{"_id":"req_3","type":"Request","parentId":"wrk_1","name":"Gone","method":"GET","url":"https://api.test/gone","modified":5}
{"_id":"req_3","$$deleted":true}
{"_id":"req_1","type":"Request","parentId":"wrk_1","name":"List users","method":"GET","url":"https://api.test/users","modified":6}`

func writeInsomnia(t *testing.T, roots Roots) {
	t.Helper()
	writeFile(t, filepath.Join(roots.ConfigDir, "Insomnia", "insomnia.Workspace.db"), insomniaNeDB)
}

func TestInsomniaCollectionsAreReadFromItsDatabase(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeInsomnia(t, roots)
	installation, ok := installationFor(Detect(roots), ClientInsomnia)
	if !ok {
		t.Fatal("Insomnia not detected")
	}
	if !installation.Readable {
		t.Fatal("Insomnia should be readable")
	}
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 {
		t.Fatalf("collections = %#v", collections)
	}
	found := collections[0]
	if found.Name != "Payments API" {
		t.Fatalf("name = %q", found.Name)
	}
	if found.Kind != "insomnia" {
		t.Fatalf("kind = %q; the existing importer should be reused", found.Kind)
	}
	if found.RequestCount != 2 {
		t.Fatalf("request count = %d; the tombstoned request should be gone", found.RequestCount)
	}
	// The last write of a record wins: NeDB is append-only, so the first copy
	// of req_1 is history, not data.
	if !strings.Contains(found.Content, "List users") || strings.Contains(found.Content, `"Gone"`) {
		t.Fatalf("content did not fold the append-only log: %s", found.Content)
	}
}

func TestInsomniaTornTrailingLineIsTolerated(t *testing.T) {
	roots := rootsIn(t.TempDir())
	// A half-written final record is normal for an append-only store that is
	// running while we read it. Insomnia's own reader tolerates some.
	writeFile(t, filepath.Join(roots.ConfigDir, "Insomnia", "insomnia.Workspace.db"), insomniaNeDB+"\n{\"_id\":\"req_9\",\"type\":\"Req")
	installation, _ := installationFor(Detect(roots), ClientInsomnia)
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatalf("a torn trailing line failed the read: %v", err)
	}
	if len(collections) != 1 || collections[0].RequestCount != 2 {
		t.Fatalf("collections = %#v", collections)
	}
}

func TestInsomniaSecretValuesAreNotImported(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeFile(t, filepath.Join(roots.ConfigDir, "Insomnia", "insomnia.Workspace.db"), `{"_id":"wrk_1","type":"Workspace","name":"W"}
{"_id":"req_1","type":"Request","parentId":"wrk_1","name":"r","method":"GET","url":"https://api.test","authentication":{"type":"bearer","token":"sk-live-do-not-copy"}}`)
	installation, _ := installationFor(Detect(roots), ClientInsomnia)
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(collections[0].Content, "sk-live-do-not-copy") {
		t.Fatal("a credential was carried out of another application's store")
	}
	if len(collections[0].Warnings) == 0 {
		t.Fatal("blanking a credential must be reported, or it is discovered by a request that fails")
	}
}

func TestBrunoCollectionsAreFoundThroughItsWorkspaceIndex(t *testing.T) {
	dir := t.TempDir()
	roots := rootsIn(dir)
	collectionPath := filepath.Join(dir, "code", "payments-api")
	writeFile(t, filepath.Join(collectionPath, "bruno.json"), `{"version":"1","name":"Payments","type":"collection"}`)
	writeFile(t, filepath.Join(collectionPath, "list.bru"), "meta {\n  name: list\n  type: http\n}\n")
	workspacePath := filepath.Join(roots.ConfigDir, "bruno", "default-workspace")
	writeFile(t, filepath.Join(workspacePath, "workspace.yml"), "name: Default\ncollections:\n  - path: "+collectionPath+"\n")
	writeFile(t, filepath.Join(roots.ConfigDir, "bruno", "preferences.json"),
		`{"workspaces":{"lastOpenedWorkspaces":["`+workspacePath+`"]}}`)

	installation, ok := installationFor(Detect(roots), ClientBruno)
	if !ok {
		t.Fatal("Bruno not detected")
	}
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 {
		t.Fatalf("collections = %#v", collections)
	}
	if collections[0].SourcePath != collectionPath {
		t.Fatalf("source path = %q", collections[0].SourcePath)
	}
	// A Bruno collection is a folder this app already opens; there is nothing
	// to convert and nothing to copy.
	if collections[0].Kind != "collection-folder" {
		t.Fatalf("kind = %q", collections[0].Kind)
	}
}

func TestBrunoLegacyPreferencesKeyStillWorks(t *testing.T) {
	dir := t.TempDir()
	roots := rootsIn(dir)
	collectionPath := filepath.Join(dir, "old-collection")
	writeFile(t, filepath.Join(collectionPath, "bruno.json"), `{"version":"1","name":"Old","type":"collection"}`)
	writeFile(t, filepath.Join(roots.ConfigDir, "bruno", "preferences.json"),
		`{"lastOpenedCollections":["`+collectionPath+`"]}`)
	installation, _ := installationFor(Detect(roots), ClientBruno)
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 || collections[0].SourcePath != collectionPath {
		t.Fatalf("collections = %#v", collections)
	}
}

// Bruno's newer collections carry opencollection.yml instead of bruno.json, and
// checking only for the latter misses every current one.
func TestBrunoOpenCollectionFolderIsRecognised(t *testing.T) {
	dir := t.TempDir()
	roots := rootsIn(dir)
	collectionPath := filepath.Join(dir, "oc")
	writeFile(t, filepath.Join(collectionPath, "opencollection.yml"), "name: OC\n")
	writeFile(t, filepath.Join(roots.ConfigDir, "bruno", "preferences.json"),
		`{"lastOpenedCollections":["`+collectionPath+`"]}`)
	installation, _ := installationFor(Detect(roots), ClientBruno)
	collections, err := ReadCollections(installation)
	if err != nil || len(collections) != 1 {
		t.Fatalf("collections=%#v err=%v", collections, err)
	}
}

func TestBrunoEntriesThatNoLongerExistAreDropped(t *testing.T) {
	dir := t.TempDir()
	roots := rootsIn(dir)
	writeFile(t, filepath.Join(roots.ConfigDir, "bruno", "preferences.json"),
		`{"lastOpenedCollections":["`+filepath.Join(dir, "deleted-long-ago")+`"]}`)
	installation, _ := installationFor(Detect(roots), ClientBruno)
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 0 {
		t.Fatalf("a path that is no longer a collection was offered: %#v", collections)
	}
}

func writeThunderClient(t *testing.T, roots Roots, storage string) {
	t.Helper()
	base := filepath.Join(roots.ConfigDir, storage, "User", "globalStorage", "rangav.vscode-thunder-client")
	writeFile(t, filepath.Join(base, "thunderCollection.json"), `[
	  {"_id":"col_1","colName":"Billing","folders":[{"_id":"fld_1","name":"Invoices","containerId":""}]}
	]`)
	writeFile(t, filepath.Join(base, "thunderclient.json"), `[
	  {"_id":"r1","colId":"col_1","containerId":"","name":"Ping","method":"GET","url":"https://billing.test/ping","headers":[{"name":"Accept","value":"application/json"}]},
	  {"_id":"r2","colId":"col_1","containerId":"fld_1","name":"Create","method":"POST","url":"https://billing.test/invoices","body":{"type":"json","raw":"{\"amount\":1}"}}
	]`)
}

func TestThunderClientCollectionsAreConvertedForTheExistingImporter(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeThunderClient(t, roots, "Code")
	installation, ok := installationFor(Detect(roots), ClientThunderClient)
	if !ok {
		t.Fatal("Thunder Client not detected")
	}
	collections, err := ReadCollections(installation)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 1 {
		t.Fatalf("collections = %#v", collections)
	}
	found := collections[0]
	if found.Name != "Billing" || found.RequestCount != 2 {
		t.Fatalf("found = %#v", found)
	}
	// Emitting the Postman shape means every tolerance and every mapping the
	// Postman importer already has applies here too, rather than a second
	// converter drifting from it.
	if found.Kind != "postman" {
		t.Fatalf("kind = %q", found.Kind)
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(found.Content), &document); err != nil {
		t.Fatalf("emitted content is not JSON: %v", err)
	}
	if _, ok := document["info"]; !ok {
		t.Fatalf("emitted content is not a collection: %s", found.Content)
	}
	if !strings.Contains(found.Content, "Invoices") {
		t.Fatalf("folder lost: %s", found.Content)
	}
}

func TestThunderClientIsFoundUnderEveryVSCodeFlavour(t *testing.T) {
	for _, storage := range []string{"Code", "Code - Insiders", "VSCodium"} {
		roots := rootsIn(t.TempDir())
		writeThunderClient(t, roots, storage)
		if _, ok := installationFor(Detect(roots), ClientThunderClient); !ok {
			t.Errorf("not detected under %q", storage)
		}
	}
}

func TestYaakIsDetectedButNotYetRead(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeFile(t, filepath.Join(roots.DataDir, "app.yaak.desktop", "db.sqlite"), "SQLite format 3\x00")
	installation, ok := installationFor(Detect(roots), ClientYaak)
	if !ok {
		t.Fatal("Yaak not detected")
	}
	if installation.Readable {
		t.Fatal("Yaak reading needs a SQLite driver that is not a dependency yet")
	}
	if strings.TrimSpace(installation.Guidance) == "" {
		t.Fatal("an unreadable client must say what to do instead")
	}
}

func TestDetectionIsStableAndSorted(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeInsomnia(t, roots)
	writeThunderClient(t, roots, "Code")
	writeFile(t, filepath.Join(roots.ConfigDir, "Postman", "IndexedDB", "x.ldb"), "x")
	first := Detect(roots)
	if len(first) != 3 {
		t.Fatalf("detected %#v", first)
	}
	second := Detect(roots)
	for index := range first {
		if first[index].Client != second[index].Client {
			t.Fatal("detection order is not stable")
		}
	}
	// Readable clients first: the actionable rows belong at the top of a list
	// whose whole purpose is to be acted on.
	if first[0].Readable == false {
		t.Fatalf("an unreadable client sorted first: %#v", first)
	}
}

func TestAFileWhereADirectoryIsExpectedIsNotAnInstallation(t *testing.T) {
	roots := rootsIn(t.TempDir())
	writeFile(t, filepath.Join(roots.ConfigDir, "Insomnia"), "not a directory")
	if found := Detect(roots); len(found) != 0 {
		t.Fatalf("detected %#v", found)
	}
}
