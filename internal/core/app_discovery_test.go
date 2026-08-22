// The first-run offer (US-064).
//
// Bringing another client's collections across, and adopting the proxy a
// managed machine is configured with, are the two things that decide whether
// this app is usable in the first five minutes. Both are offered once, and both
// keep the same boundary: presence is detected freely, contents are read only
// after the user says yes.
package core

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeDiscoveryFixture(t *testing.T, root string) {
	t.Helper()
	base := filepath.Join(root, "config", "Insomnia")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"_id":"wrk_1","type":"Workspace","name":"Payments API"}
{"_id":"req_1","type":"Request","parentId":"wrk_1","name":"List users","method":"GET","url":"https://api.test/users"}`
	if err := os.WriteFile(filepath.Join(base, "insomnia.Workspace.db"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverImportSourcesFindsInstalledClients(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	report, err := app.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Installations) != 1 || report.Installations[0].Client != "insomnia" {
		t.Fatalf("installations = %#v", report.Installations)
	}
	// Detection reports that a client is there. It does not report what is
	// inside, because it has not looked.
	if report.Installations[0].Readable != true {
		t.Fatalf("installation = %#v", report.Installations[0])
	}
}

func TestDiscoveredCollectionsAreOnlyReadWhenAsked(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	found, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "Payments API" || found[0].RequestCount != 1 {
		t.Fatalf("found = %#v", found)
	}
}

func TestReadingAnUndetectedClientIsRefused(t *testing.T) {
	app := newAppForTest(t)
	app.discoveryRootsForTest(t.TempDir())
	if _, err := app.ReadDiscoveredCollections("insomnia"); err == nil {
		t.Fatal("reading a client that is not installed should fail")
	}
	if _, err := app.ReadDiscoveredCollections("postman"); err == nil {
		t.Fatal("Postman's store must never be read")
	}
}

func TestDiscoveredCollectionsImportThroughTheNormalPipeline(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	found, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	// The point of emitting import sources rather than collections: the preview
	// and apply the user already knows are what runs, including the conflict
	// handling and the content hash.
	sources := make([]CollectionImportSource, 0, len(found))
	for index, entry := range found {
		sources = append(sources, entry.Source(index))
	}

	preview, err := app.PreviewCollectionImport(CollectionImportPreviewRequest{WorkspaceID: state.Workspaces[0].ID, Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 1 || preview.Rows[0].Error != "" {
		t.Fatalf("preview = %#v", preview.Rows)
	}
	selections := []CollectionImportSelection{{
		SourceID:            preview.Rows[0].SourceID,
		CandidateID:         preview.Rows[0].CandidateID,
		ExpectedContentHash: preview.Rows[0].ContentHash,
	}}
	result, err := app.ApplyCollectionImport(CollectionImportApplyRequest{
		WorkspaceID: state.Workspaces[0].ID, Sources: sources, Selections: selections,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("result = %#v", result)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if collection.Name != "Payments API" || len(collection.Items) != 1 {
		t.Fatalf("imported = %#v", collection)
	}
}

func TestTheOfferIsMadeOnceAndRemembersBeingDismissed(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	report, err := app.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if !report.ShouldPrompt {
		t.Fatal("a machine with a client installed and nothing dismissed should be prompted")
	}
	if _, err := app.DismissDiscoveryPrompt(); err != nil {
		t.Fatal(err)
	}
	report, err = app.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if report.ShouldPrompt {
		t.Fatal("the offer was made again after being dismissed")
	}
	// Still discoverable on purpose: dismissing the prompt hides the
	// interruption, not the feature.
	if len(report.Installations) != 1 {
		t.Fatalf("installations = %#v", report.Installations)
	}

	// State is written by a background writer, so a relaunch that races it
	// would prove nothing either way.
	if err := app.flushPersist(); err != nil {
		t.Fatal(err)
	}
	restarted := newAppInDirForTest(t, app.dataDir)
	restarted.discoveryRootsForTest(root)
	report, err = restarted.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if report.ShouldPrompt {
		t.Fatal("the dismissal did not survive a relaunch, so the offer returns every launch")
	}
}

func TestNothingToOfferMeansNoPrompt(t *testing.T) {
	app := newAppForTest(t)
	app.discoveryRootsForTest(t.TempDir())
	report, err := app.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if report.ShouldPrompt {
		t.Fatalf("an empty machine was prompted: %#v", report)
	}
}

func TestAdoptingACACertificateSetsThePreferenceAndNothingElse(t *testing.T) {
	app := newAppForTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "corp.crt")
	writeCATestFile(t, path)
	app.discoveryCADirsForTest(dir)

	report, err := app.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.CACertificates) != 1 {
		t.Fatalf("candidates = %#v", report.CACertificates)
	}
	// Detection alone changes nothing: a scan that could switch trust on is a
	// scan that could be made to.
	before, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if before.Preferences.Request.CustomCaCertificate.Enabled {
		t.Fatal("discovery enabled a CA by itself")
	}
	state, err := app.AdoptDiscoveredCACertificate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Preferences.Request.CustomCaCertificate.Enabled || state.Preferences.Request.CustomCaCertificate.FilePath != path {
		t.Fatalf("preference = %#v", state.Preferences.Request.CustomCaCertificate)
	}
	// The system roots stay, or adopting a corporate CA would break every
	// request to the public internet.
	if state.Preferences.Request.KeepDefaultCaCertificates.Enabled != nil && !*state.Preferences.Request.KeepDefaultCaCertificates.Enabled {
		t.Fatal("adopting a CA dropped the system trust store")
	}
}

func TestAdoptingAnUndiscoveredCACertificateIsRefused(t *testing.T) {
	app := newAppForTest(t)
	app.discoveryCADirsForTest(t.TempDir())
	// A path the user never saw in a prompt is a path that arrived from
	// somewhere else, and trust is not something to take on faith from a
	// caller.
	if _, err := app.AdoptDiscoveredCACertificate("/etc/passwd"); err == nil {
		t.Fatal("adopting an unscanned path should be refused")
	}
}

func TestDiscoveryReportIsSerialisableForTheFrontend(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)
	report, err := app.DiscoverImportSources()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"installations", "shouldPrompt", "proxy"} {
		if !strings.Contains(string(data), field) {
			t.Errorf("report is missing %q: %s", field, data)
		}
	}
}

// writeCATestFile writes a self-signed CA, which is what a corporate
// provisioning script leaves behind and what the scan is meant to find.
func writeCATestFile(t *testing.T, path string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Example Corp Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The converted documents must not cross to the frontend and back. Sending
// another application's requests out to the UI so it can hand them straight
// back is a copy of somebody's data making a trip for no reason.
func TestDiscoveredContentIsNotExposedToTheFrontend(t *testing.T) {
	app := newAppForTest(t)
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)
	found, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(found)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "https://api.test/users") {
		t.Fatalf("the converted document was serialised to the frontend: %s", data)
	}
}

func TestImportDiscoveredCollectionsImportsTheChosenOnes(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)

	// Selected by the id ReadDiscoveredCollections handed out. This used to pass
	// the display name, which is what let a name shared by two collections
	// import both; see app_discovery_identity_test.go.
	found, err := app.ReadDiscoveredCollections("insomnia")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found = %#v", found)
	}
	result, err := app.ImportDiscoveredCollections(state.Workspaces[0].ID, "insomnia", []string{found[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("result = %#v", result)
	}
	collection := result.State.Workspaces[0].Collections[len(result.State.Workspaces[0].Collections)-1]
	if collection.Name != "Payments API" || len(collection.Items) != 1 {
		t.Fatalf("imported = %#v", collection)
	}
}

func TestImportingACollectionThatWasNotFoundIsRefused(t *testing.T) {
	app := newAppForTest(t)
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeDiscoveryFixture(t, root)
	app.discoveryRootsForTest(root)
	if _, err := app.ImportDiscoveredCollections(state.Workspaces[0].ID, "insomnia", []string{"Not There"}); err == nil {
		t.Fatal("importing a name that was never offered should fail")
	}
}
