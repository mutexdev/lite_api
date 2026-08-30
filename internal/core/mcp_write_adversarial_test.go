package core

// Adversarial tests for the write tier and its authoring-time host guard,
// written against docs/mcp-agent-interface.md and the existing coverage in
// mcp_write_test.go (which this file deliberately does not repeat). Each
// test is one of:
//
//   - CONFIRMED-SAFE / COVERAGE-ADDED: an attack was attempted, the boundary
//     held, and there was no regression test pinning it yet.
//   - A CLOSED VULNERABILITY: an attack that once succeeded and has since been
//     fixed in production code. The test asserts the FIXED behaviour, and its
//     comment records both the hole and what holds in its place, so the shape
//     cannot come back unnoticed.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mutexdev/lite_api/internal/mcpserver"
	"github.com/mutexdev/lite_api/internal/types"
)

// --- 1. allowlist poisoning: a DIFFERENT collection's same-named secret -----

// This test pins a CLOSED VULNERABILITY.
//
// THE HOLE. mcpKnownHostsForSecret was handed EVERY open collection
// (mcpGuardCollections walks every workspace's every collection) and asked "is
// this host known FOR SECRET NAME X" — never "is this host known for the
// specific secret variable this collection defines". A secret had no identity
// beyond its name. Two collections that both declare "apiToken" — the single
// most reusable name a real workspace has — were therefore NOT isolated from
// each other's allowlists, though they hold different values and have never
// shared a host. A request an agent authored in collection A, aiming A's
// "apiToken" at a host, was approved with no prompt the moment collection B's
// UNRELATED "apiToken" already used that host; and because mcpSecretsWithoutHost
// used the identical name-keyed helper, the same widening let the RUN tier send
// A's real credential there too.
//
// WHAT NOW HOLDS. The allowlist is scoped by DEFINITION SITE (mcpSecretOwner,
// mcp_guard.go). Here the fixture's "apiToken" is defined in the workspace's
// GLOBAL environment, so its allowlist unions that workspace's collections —
// EXCEPT any collection that declares the same name secret at collection scope,
// because inside such a collection the name means that collection's own
// credential and there is no way to tell from a request's braces which of the
// two it meant. That is the fail-closed answer, and it is what isolates the two
// here: the unrelated collection declares its own "apiToken" in its own
// environment, so nothing it does teaches the guard anything about the global
// one. Both halves — authoring and running — are asserted, because the fix had
// to reach both.
func TestMCPAuthoringADifferentCollectionsSameNamedSecretCannotWidenThisAllowlist(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend() // deny by default: nobody is there to approve anything

	// A real, reachable server stands in for the "cross-collection" host, so
	// the run-tier half of this test measures the guard rather than DNS.
	var sawAuth string
	crossCollectionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer crossCollectionServer.Close()
	crossCollectionHost := crossCollectionServer.URL

	// A SECOND, otherwise unrelated collection in the same workspace. Its own
	// "apiToken" is a DIFFERENT secret — different value, different owner in
	// spirit — that already happens to be sent to crossCollectionHost. Nothing
	// here is authored through this pass's fixture collection at all.
	f.app.mu.Lock()
	workspace := &f.app.state.Workspaces[0]
	other := types.Collection{
		ID:   "col_other_unrelated",
		Name: "Unrelated collection",
		Environments: []types.Environment{{
			ID:   "env_other",
			Name: "Other env",
			Variables: []types.Variable{
				{ID: "v1", Name: "apiToken", Value: "OTHER-COLLECTIONS-OWN-SECRET-VALUE", Secret: true, Enabled: true},
			},
		}},
	}
	otherReq := types.NewRequestItem("Other collection's own call", "http", 1)
	otherReq.Method = "GET"
	otherReq.URL = crossCollectionHost + "/x"
	otherReq.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
	otherReq.Body = types.RequestBody{Mode: "none"}
	other.Items = append(other.Items, otherReq)
	workspace.Collections = append(workspace.Collections, other)
	f.app.mu.Unlock()

	// Author a NEW request in the FIXTURE's OWN collection, aiming ITS OWN
	// apiToken at the SAME host — a host the fixture's own collection has
	// never sent this secret to, and which nobody has approved for it.
	_, err := f.create(mcpserver.CreateRequestParams{
		Name:    "Reaches into the other collection's allowlist",
		URL:     crossCollectionHost + "/y",
		Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
	})
	if err == nil {
		t.Fatal("a save reaching another collection's allowlist was accepted with nobody to approve it")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "apiToken") {
		t.Errorf("the refusal should name the secret: %v", err)
	}

	// The run half, which used the identical helper and so had the identical
	// hole: retargeting the FIXTURE's OWN pre-existing request at the
	// cross-collection host must deny, and its real secret must never leave.
	_, runErr := f.backend.RunRequest(context.Background(), mcpserver.RunRequestParams{
		CollectionID: f.collectionID,
		RequestID:    f.existingID,
		Variables:    map[string]string{"baseUrl": crossCollectionHost},
	})
	if runErr == nil {
		t.Fatal("the run tier allowed the cross-collection host")
	}
	if !errors.Is(runErr, mcpserver.ErrDenied) {
		t.Fatalf("run error is %v, want one that wraps mcpserver.ErrDenied", runErr)
	}
	if sawAuth != "" {
		t.Fatalf("the cross-collection host was reached at all, with Authorization %q", sawAuth)
	}
}

// --- 1b. the two halves of definition-site scoping, side by side ------------

// The scoping rule has two directions, and a fix that only refused things would
// pass half of it. Both are asserted here against the SAME fixture:
//
//   - A secret defined in the workspace's GLOBAL environment legitimately
//     serves every collection in that workspace, so a sibling collection's use
//     of it DOES teach the allowlist and authoring against that host is allowed
//     with no prompt.
//   - A secret defined at COLLECTION scope belongs to its collection, so
//     another collection's same-named secret teaches nothing and authoring
//     against that host is refused.
func TestMCPAuthoringAllowlistIsScopedToWhereTheSecretIsDefined(t *testing.T) {
	t.Run("a global secret is shared across its own workspace's collections", func(t *testing.T) {
		f := newMCPWriteFixture(t)
		f.enableWriteTier()
		f.noFrontend() // anything that reaches a prompt denies, so a pass means no prompt

		// A sibling collection that does NOT redefine apiToken: every
		// {{apiToken}} in it IS the workspace-global secret, so the host it
		// sends it to is a host the user really has chosen for that credential.
		const sharedHost = "api.shared-global.example.com"
		f.app.mu.Lock()
		workspace := &f.app.state.Workspaces[0]
		sibling := types.Collection{ID: "col_sibling_global", Name: "Sibling collection"}
		siblingReq := types.NewRequestItem("Sibling's call with the global secret", "http", 1)
		siblingReq.Method = "GET"
		siblingReq.URL = "https://" + sharedHost + "/x"
		siblingReq.Headers = []KeyValue{{Name: "Authorization", Value: "Bearer {{apiToken}}", Enabled: true}}
		siblingReq.Body = types.RequestBody{Mode: "none"}
		sibling.Items = append(sibling.Items, siblingReq)
		workspace.Collections = append(workspace.Collections, sibling)
		f.app.mu.Unlock()

		if _, err := f.create(mcpserver.CreateRequestParams{
			Name:    "Uses the global secret at the sibling's host",
			URL:     "https://" + sharedHost + "/y",
			Headers: []mcpserver.AuthoredRow{{Name: "Authorization", Value: "Bearer {{apiToken}}"}},
		}); err != nil {
			t.Fatalf("a global secret's own workspace did not widen its allowlist: %v", err)
		}
		if len(f.prompts()) != 0 {
			t.Errorf("the user was asked about a host their global secret already goes to (%d prompts)", len(f.prompts()))
		}
	})

	t.Run("a collection-scoped secret is not widened by another collection", func(t *testing.T) {
		f := newMCPWriteFixture(t)
		f.enableWriteTier()
		f.noFrontend()

		const teamHost = "api.team-scoped.example.com"
		f.app.mu.Lock()
		workspace := &f.app.state.Workspaces[0]
		// The FIXTURE's collection gets its own, collection-scoped secret.
		workspace.Collections[0].Variables = append(workspace.Collections[0].Variables,
			Variable{ID: "team-token-a", Name: "teamToken", Value: "COLLECTION-A-TEAM-TOKEN-VALUE", Secret: true, Enabled: true})
		// An unrelated collection declares the same NAME at collection scope —
		// a different credential — and already sends it to teamHost.
		other := types.Collection{
			ID:   "col_other_team",
			Name: "Other team's collection",
			Variables: []types.Variable{
				{ID: "team-token-b", Name: "teamToken", Value: "COLLECTION-B-TEAM-TOKEN-VALUE", Secret: true, Enabled: true},
			},
		}
		otherReq := types.NewRequestItem("Other team's call", "http", 1)
		otherReq.Method = "GET"
		otherReq.URL = "https://" + teamHost + "/x"
		otherReq.Headers = []KeyValue{{Name: "X-Team", Value: "{{teamToken}}", Enabled: true}}
		otherReq.Body = types.RequestBody{Mode: "none"}
		other.Items = append(other.Items, otherReq)
		workspace.Collections = append(workspace.Collections, other)
		f.app.mu.Unlock()

		_, err := f.create(mcpserver.CreateRequestParams{
			Name:    "Aims this collection's teamToken at the other team's host",
			URL:     "https://" + teamHost + "/y",
			Headers: []mcpserver.AuthoredRow{{Name: "X-Team", Value: "{{teamToken}}"}},
		})
		if err == nil {
			t.Fatal("another collection's same-named secret widened this collection-scoped secret's allowlist")
		}
		if !errors.Is(err, mcpserver.ErrDenied) {
			t.Fatalf("error is %v, want one that wraps mcpserver.ErrDenied", err)
		}
		if !strings.Contains(err.Error(), "teamToken") || !strings.Contains(err.Error(), teamHost) {
			t.Errorf("the refusal should name the secret and the host: %v", err)
		}

		// And this collection's OWN prior use of teamToken does teach the
		// allowlist — otherwise the assertion above would only prove that the
		// guard refuses everything.
		f.app.mu.Lock()
		collection := &f.app.state.Workspaces[0].Collections[0]
		sibling := types.NewRequestItem("This collection's own teamToken call", "http", len(collection.Items)+1)
		sibling.Method = "GET"
		sibling.URL = "https://api.own-team.example.com/x"
		sibling.Headers = []KeyValue{{Name: "X-Team", Value: "{{teamToken}}", Enabled: true}}
		sibling.Body = types.RequestBody{Mode: "none"}
		collection.Items = append(collection.Items, sibling)
		f.app.mu.Unlock()

		if _, err := f.create(mcpserver.CreateRequestParams{
			Name:    "A second call to this collection's own teamToken host",
			URL:     "https://api.own-team.example.com/y",
			Headers: []mcpserver.AuthoredRow{{Name: "X-Team", Value: "{{teamToken}}"}},
		}); err != nil {
			t.Fatalf("a host this collection's own secret already uses was refused: %v", err)
		}
	})
}

// --- 2. allowlist poisoning: does a folder choice change which secrets apply? -

// CONFIRMED-SAFE / COVERAGE-ADDED. This pass's brief asks explicitly: "Can a
// folder-path choice change which folder/collection auth applies and thus
// which secrets are referenced?" It can, by design (a folder's own Variables
// and Auth fold into whatever is created inside it) — but the authoring
// guard has to be aware of a SECRET REACHED ONLY THROUGH THE CHOSEN FOLDER,
// not just of secrets referenced directly in the authored fields. This test
// is the positive proof that it is: a request created inside a folder that
// carries its OWN secret variable — one the top-level collection never
// mentions — still trips the guard for a host that secret has never been
// sent to.
func TestMCPCreateRequestGuardSeesASecretReachedOnlyThroughTheChosenFolder(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.enableWriteTier()
	f.noFrontend()

	if _, err := f.app.CreateFolder(f.collectionID, "", "scoped", "scoped"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := f.app.UpdateFolderSettings(f.collectionID, "scoped", FolderConfig{
		Variables: []Variable{{ID: "folder-secret", Name: "folderSecret", Value: "FOLDER-ONLY-SECRET-VALUE", Secret: true, Enabled: true}},
	}); err != nil {
		t.Fatalf("UpdateFolderSettings: %v", err)
	}

	// The request references ONLY the folder-scoped secret, never apiToken,
	// and aims at a brand-new host nothing in the collection has used.
	_, err := f.create(mcpserver.CreateRequestParams{
		Name:       "Uses the folder's own secret",
		URL:        "https://folder-secret-destination.example/x",
		FolderPath: "scoped",
		Headers:    []mcpserver.AuthoredRow{{Name: "X-Folder-Secret", Value: "{{folderSecret}}"}},
	})
	if err == nil {
		t.Fatal("a request reaching a new host through a folder-only secret was saved with nobody to approve it")
	}
	if !errors.Is(err, mcpserver.ErrDenied) {
		t.Errorf("the refusal does not wrap ErrDenied: %v", err)
	}
	if !strings.Contains(err.Error(), "folderSecret") || !strings.Contains(err.Error(), "folder-secret-destination.example") {
		t.Errorf("the refusal does not name the folder secret and the host: %v", err)
	}

	// The same request, aimed at a host the folder secret is ALREADY known to
	// use (a sibling request placed in the same folder first), saves with no
	// prompt at all — this is what proves the guard is scoped to the right
	// secret rather than refusing everything unconditionally.
	f.app.mu.Lock()
	collection := &f.app.state.Workspaces[0].Collections[0]
	sibling := types.NewRequestItem("Sibling in the scoped folder", "http", len(collection.Items)+1)
	sibling.Method = "GET"
	sibling.URL = "https://folder-secret-destination.example/sibling"
	sibling.FolderPath = "scoped"
	sibling.Headers = []KeyValue{{Name: "X-Folder-Secret", Value: "{{folderSecret}}", Enabled: true}}
	sibling.Body = types.RequestBody{Mode: "none"}
	collection.Items = append(collection.Items, sibling)
	f.app.mu.Unlock()

	if _, err := f.create(mcpserver.CreateRequestParams{
		Name:       "A second request to the now-known folder-secret host",
		URL:        "https://folder-secret-destination.example/y",
		FolderPath: "scoped",
		Headers:    []mcpserver.AuthoredRow{{Name: "X-Folder-Secret", Value: "{{folderSecret}}"}},
	}); err != nil {
		t.Fatalf("a request to a host the folder secret already uses was refused: %v", err)
	}
}

// --- 3. write tier off: nothing reaches disk, not just state -----------------

// COVERAGE-ADDED. mcp_write_test.go's TestMCPWriteTierIsOffByDefault checks
// STATE (item count, the stored item's URL) but never reads the actual bytes
// on disk. "Nothing is written" is a claim about the file, and the only way
// to measure it is to read the file: a bug that mutated in-memory state
// back to the old value after a stray write (or that wrote a temp file and
// failed to clean it up) would pass the existing test and fail this one.
func TestMCPWriteTierOffLeavesTheCollectionFileByteForByteUnchanged(t *testing.T) {
	f := newMCPWriteFixture(t)
	// Write tier left OFF deliberately.

	// mcpWriteFixture's own "existing" request is planted directly into
	// in-memory state (as every fixture in this package does) and was never
	// saved to disk, so it has no file to compare. Create and save a REAL
	// on-disk request first, through the app's own bindings — bypassing the
	// MCP tier entirely — so there is an actual file for the refused calls
	// below to (not) touch.
	stateAfterCreate, err := f.app.CreateRequestInFolder(f.collectionID, "http", "On-disk sentinel", "")
	if err != nil {
		t.Fatalf("CreateRequestInFolder: %v", err)
	}
	var onDiskID string
	for _, workspace := range stateAfterCreate.Workspaces {
		for _, collection := range workspace.Collections {
			if collection.ID != f.collectionID {
				continue
			}
			for _, item := range collection.Items {
				if item.Name == "On-disk sentinel" {
					onDiskID = item.ID
				}
			}
		}
	}
	if onDiskID == "" {
		t.Fatal("could not find the sentinel request that was just created")
	}
	if _, err := f.app.SaveRequest(f.collectionID, onDiskID); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	existing := f.storedItem(onDiskID)
	if strings.TrimSpace(existing.FilePath) == "" {
		t.Fatal("the on-disk sentinel request has no file path; this test measures nothing")
	}
	before, err := os.ReadFile(existing.FilePath)
	if err != nil {
		t.Fatalf("read the collection file before the refused calls: %v", err)
	}

	// Snapshot every file under the collection's directory, not just the one
	// request's file: a stray write anywhere in the collection (a new file
	// for the "created" request, a folder's config file) would still be a
	// violation of "nothing is written while the tier is off".
	collectionDir := filepath.Dir(existing.FilePath)
	beforeTree := snapshotDirectoryForTest(t, collectionDir)

	if _, err := f.create(mcpserver.CreateRequestParams{Name: "New", URL: "{{baseUrl}}/new"}); err == nil {
		t.Fatal("create_request was accepted with the write tier off")
	}
	if _, err := f.update(mcpserver.UpdateRequestParams{
		RequestID: f.existingID, URL: stringPointer("{{baseUrl}}/changed"),
	}); err == nil {
		t.Fatal("update_request was accepted with the write tier off")
	}
	if _, err := f.backend.CreateFlow(mcpserver.CreateFlowParams{
		CollectionID: f.collectionID,
		Flow:         mcpserver.FlowDefinition{Name: "F", Steps: []mcpserver.FlowStep{{ID: "one", RequestID: f.existingID}}},
	}); err == nil {
		t.Fatal("create_flow was accepted with the write tier off")
	}

	after, err := os.ReadFile(existing.FilePath)
	if err != nil {
		t.Fatalf("read the collection file after the refused calls: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("the existing request's file changed while the write tier was off:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	afterTree := snapshotDirectoryForTest(t, collectionDir)
	if len(beforeTree) != len(afterTree) {
		t.Fatalf("the collection directory grew from %d files to %d while every write was refused: before=%v after=%v",
			len(beforeTree), len(afterTree), beforeTree, afterTree)
	}
	for name, beforeHash := range beforeTree {
		afterHash, present := afterTree[name]
		if !present {
			t.Fatalf("file %q vanished from the collection directory", name)
		}
		if beforeHash != afterHash {
			t.Fatalf("file %q changed while every write was refused", name)
		}
	}
}

// snapshotDirectoryForTest returns every regular file under dir (relative
// path -> contents), recursively, so a caller can compare two snapshots for
// exact equality without caring about mtimes or file handles.
func snapshotDirectoryForTest(t *testing.T, dir string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[relative] = string(contents)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return snapshot
}
