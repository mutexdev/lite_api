package core

// The remembered-approval store — §6.
//
// EVERY TEST HERE IS ABOUT A KEY, and every one of them fails in the same
// direction when it breaks: an approval the user gave about one configuration
// starts authorizing another. That failure is silent — the run simply succeeds,
// with no prompt to notice the absence of — which is why the negative half of
// each case (the lookup that must MISS) is the assertion that matters.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mutexdev/lite_api/internal/types"
)

// siteUnder is testSite with the collection environment swapped, so a test can
// say "the same request, a different environment" in one place.
func siteUnder(requestID, environmentID string, globals ...string) mcpDefinitionSite {
	site := testSite(requestID)
	site.environmentID = environmentID
	if globals != nil {
		site.globalEnvironmentIDs = globals
	}
	return site
}

func approvedUnder(t *testing.T, app *App, site mcpDefinitionSite, origin Origin, class string) bool {
	t.Helper()
	ok, err := app.mcpRememberedOriginApproved(site, origin, class)
	if err != nil {
		t.Fatalf("mcpRememberedOriginApproved: %v", err)
	}
	return ok
}

// --- the key ----------------------------------------------------------------

// The stored entry and the lookup must render the SAME key, or a remembered
// approval would never match and the store would be write-only. Two functions
// build it — one from a site, one from a decoded entry — and this is what keeps
// them from drifting.
func TestStoredApprovalKeyMatchesTheLookupKey(t *testing.T) {
	site := siteUnder("req_charge", "env_production")
	origin := mustOrigin(t, "https://api.example.com")

	stored := types.MCPApproval{
		WorkspacePath:        site.workspacePath,
		CollectionID:         site.collectionID,
		RequestID:            site.requestID,
		EnvironmentID:        site.environmentID,
		GlobalEnvironmentIDs: site.globalEnvironmentIDs,
		Origin:               origin.String(),
		KindClass:            kindClassRequest,
	}
	if got, want := mcpStoredApprovalKey(stored), site.approvalKey(origin, kindClassRequest); got != want {
		t.Fatalf("stored key %q != lookup key %q", got, want)
	}
}

// A stored origin that cannot be parsed matches NOTHING rather than matching by
// text. Hand-edited or truncated entries must not become wildcards.
func TestStoredApprovalKeyRefusesAnUnparseableOrigin(t *testing.T) {
	for _, origin := range []string{"", "api.example.com", "not a url", "ftp://api.example.com:21"} {
		stored := types.MCPApproval{RequestID: "req", Origin: origin, KindClass: kindClassRequest}
		if key := mcpStoredApprovalKey(stored); key != "" {
			t.Errorf("origin %q produced key %q, want none", origin, key)
		}
	}
}

// --- environment scoping ----------------------------------------------------

// §6: "an approval remembered under dev NEVER authorizes the same request under
// production, nor under a different active global-environment list".
//
// THIS IS THE CASE THE WHOLE KEY EXISTS FOR. Production and dev resolve the same
// request's {{baseUrl}} to different places and hold different credentials; an
// approval that spanned them would send the production credential to whatever
// the dev environment happens to point at, which is the exact mistake the
// boundary is built to catch.
func TestMCPApprovalDoesNotCrossEnvironments(t *testing.T) {
	app := newAppForTest(t)
	origin := mustOrigin(t, "https://reports.example.com")
	dev := siteUnder("req_charge", "env_dev")

	if err := app.rememberMCPApproval(dev, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}

	if !approvedUnder(t, app, dev, origin, kindClassRequest) {
		t.Fatal("the approval does not hold under the environment it was given in")
	}
	if approvedUnder(t, app, siteUnder("req_charge", "env_production"), origin, kindClassRequest) {
		t.Error("an approval remembered under dev authorized the same request under production")
	}
	// "No environment selected" is a configuration of its own, not a wildcard.
	if approvedUnder(t, app, siteUnder("req_charge", ""), origin, kindClassRequest) {
		t.Error("an approval remembered under dev authorized the same request with no environment selected")
	}
	// The global-environment list is part of the identity too.
	if approvedUnder(t, app, siteUnder("req_charge", "env_dev", "global_other"), origin, kindClassRequest) {
		t.Error("an approval authorized the same request under a different active global environment")
	}
	if approvedUnder(t, app, siteUnder("req_charge", "env_dev"), origin, kindClassRequest) == false {
		t.Error("the exact site stopped matching")
	}
	// Ordering cannot widen: a reordered equivalent list is a different key, and
	// the cost of that is one conservative re-prompt.
	both := siteUnder("req_charge", "env_dev", "global_team", "global_ops")
	if err := app.rememberMCPApproval(both, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	if approvedUnder(t, app, siteUnder("req_charge", "env_dev", "global_ops", "global_team"), origin, kindClassRequest) {
		t.Error("a reordered global-environment list matched an approval given for the other order")
	}
}

// The allow-once grants a policy holds in memory must be scoped exactly as the
// persisted ones are (§6: "identical key shape"). If they were not, the cheaper
// grant would be the wider one — which is backwards.
func TestMCPSessionGrantDoesNotCrossEnvironments(t *testing.T) {
	policy := newMCPEgressPolicy()
	origin := mustOrigin(t, "https://reports.example.com")

	asked := 0
	policy.prompt = func(_ context.Context, _ types.MCPApprovalRequest) mcpPromptOutcome {
		asked++
		return mcpPromptAllowOnce
	}

	devScope := mcpScopeOrigins{site: siteUnder("req_charge", "env_dev")}
	policy.SetScope(devScope)
	if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
		t.Fatalf("the first authorization was denied: %v", err)
	}
	if asked != 1 {
		t.Fatalf("the first authorization prompted %d times, want 1", asked)
	}
	// The same origin, the same request, the same execution — the grant holds.
	if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
		t.Fatalf("the session grant did not hold: %v", err)
	}
	if asked != 1 {
		t.Fatalf("the session grant re-prompted: asked %d times", asked)
	}

	// Switch the run's environment identity and the grant must be gone.
	policy.SetScope(mcpScopeOrigins{site: siteUnder("req_charge", "env_production")})
	if err := policy.Authorize(context.Background(), origin, egressKindMain); err != nil {
		t.Fatalf("unexpected denial: %v", err)
	}
	if asked != 2 {
		t.Error("a session grant made under dev authorized the same request under production")
	}
}

// --- request scoping --------------------------------------------------------

// An approval for request A never authorizes request B, even in the same
// collection under the same environment. Two requests in one collection are two
// different destinations with two different reasons to be trusted.
func TestMCPApprovalScopedToRequest(t *testing.T) {
	app := newAppForTest(t)
	origin := mustOrigin(t, "https://reports.example.com")

	if err := app.rememberMCPApproval(testSite("req_charge"), origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	if !approvedUnder(t, app, testSite("req_charge"), origin, kindClassRequest) {
		t.Fatal("the approval does not hold for the request it was given for")
	}
	if approvedUnder(t, app, testSite("req_refund"), origin, kindClassRequest) {
		t.Error("an approval for one request authorized another")
	}
	// An empty request id is not a wildcard either.
	if approvedUnder(t, app, testSite(""), origin, kindClassRequest) {
		t.Error("an empty request id matched a remembered approval")
	}
}

// --- kind classes -----------------------------------------------------------

// §6: "token-class never authorizes request-class". The OAuth token endpoint a
// request talks to is a different destination with a different justification,
// and approving one must not approve the other in either direction.
func TestMCPApprovalKindClassScoped(t *testing.T) {
	app := newAppForTest(t)
	site := testSite("req_charge")
	origin := mustOrigin(t, "https://auth.example.com")

	if err := app.rememberMCPApproval(site, origin, kindClassToken); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	if !approvedUnder(t, app, site, origin, kindClassToken) {
		t.Fatal("the token-class approval does not hold")
	}
	if approvedUnder(t, app, site, origin, kindClassRequest) {
		t.Error("a token-class approval authorized a request-class egress")
	}
	if approvedUnder(t, app, site, origin, kindClassAWS) {
		t.Error("a token-class approval authorized an AWS-class egress")
	}
	// And the reverse, so neither direction is the one that was only assumed.
	if err := app.rememberMCPApproval(site, mustOrigin(t, "https://api.example.com"), kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	if approvedUnder(t, app, site, mustOrigin(t, "https://api.example.com"), kindClassToken) {
		t.Error("a request-class approval authorized a token-class egress")
	}
}

// A port or a scheme difference is a different origin, and therefore a
// different approval. §1.4(9): localhost is not one place.
func TestMCPApprovalScopedToOrigin(t *testing.T) {
	app := newAppForTest(t)
	site := testSite("req_charge")

	if err := app.rememberMCPApproval(site, mustOrigin(t, "https://api.example.com:8443"), kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	for _, other := range []string{"https://api.example.com", "http://api.example.com:8443", "https://api.example.com:9443"} {
		if approvedUnder(t, app, site, mustOrigin(t, other), kindClassRequest) {
			t.Errorf("an approval for https://api.example.com:8443 authorized %s", other)
		}
	}
	// The default port written down and left out are the SAME origin, though.
	if err := app.rememberMCPApproval(site, mustOrigin(t, "https://plain.example.com"), kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	if !approvedUnder(t, app, site, mustOrigin(t, "https://plain.example.com:443"), kindClassRequest) {
		t.Error("the explicit default port did not match the implicit one")
	}
}

// --- the file ---------------------------------------------------------------

func TestRememberedApprovalRoundTripsThroughTheFile(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	site := testSite("req_charge")
	origin := mustOrigin(t, "https://api.example.com")

	if err := app.rememberMCPApproval(site, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	// Remembering twice must not grow the file: the same decision is one entry.
	if err := app.rememberMCPApproval(site, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval (again): %v", err)
	}

	data, err := os.ReadFile(app.mcpApprovalsPath())
	if err != nil {
		t.Fatalf("read the approvals file: %v", err)
	}
	var stored types.MCPApprovalFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stored.Version != mcpApprovalStoreVersion {
		t.Errorf("version = %d, want %d", stored.Version, mcpApprovalStoreVersion)
	}
	if len(stored.Approvals) != 1 {
		t.Fatalf("the file holds %d entries, want 1: %s", len(stored.Approvals), data)
	}

	// A SECOND App over the same directory reads it back and matches. This is
	// the property that makes "remember" mean anything at all.
	reloaded := newAppInDirForTest(t, dir)
	if !approvedUnder(t, reloaded, site, origin, kindClassRequest) {
		t.Error("a remembered approval did not survive a reload")
	}
	if approvedUnder(t, reloaded, siteUnder("req_charge", "env_dev"), origin, kindClassRequest) {
		t.Error("the reloaded approval matched a different environment")
	}
}

// The environment id is written even when it is empty, because ABSENT and EMPTY
// mean different things on reload: empty is "no collection environment
// selected", absent is "written before the environment was part of the key" and
// is ignored. Marshalling with omitempty would turn every one of the former into
// one of the latter on the next load.
func TestARememberedApprovalWithNoEnvironmentSurvivesAReload(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	site := siteUnder("req_charge", "")
	site.globalEnvironmentIDs = nil
	origin := mustOrigin(t, "https://api.example.com")

	if err := app.rememberMCPApproval(site, origin, kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	data, err := os.ReadFile(app.mcpApprovalsPath())
	if err != nil {
		t.Fatalf("read the approvals file: %v", err)
	}
	for _, field := range []string{`"environmentId"`, `"globalEnvironmentIds"`, `"workspacePath"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("%s is missing from the written file; it would reload as a pre-v6 entry: %s", field, data)
		}
	}

	reloaded := newAppInDirForTest(t, dir)
	if !approvedUnder(t, reloaded, site, origin, kindClassRequest) {
		t.Error("an approval with no environment selected did not survive a reload")
	}
}

// --- migration --------------------------------------------------------------

// The whole migration contract in one test: an entry this build will not honour
// is IGNORED, the original file is moved aside BYTE FOR BYTE, the user is TOLD,
// and nothing is deleted.
//
// THE BYTE-IDENTICAL ASSERTION IS THE POINT OF THE BACKUP. If the backup were
// re-encoded from what this build managed to parse, it would be a record of this
// build's opinion rather than of what the user had — and the one time anybody
// reads it is when they want to know exactly that.
func TestLegacyApprovalFileIgnoredBackedUpWarned(t *testing.T) {
	for _, legacy := range []struct {
		name string
		body string
	}{
		{
			// The shipped shape: no version, (secret, host) pairs.
			name: "pre-v6 secret/host pairs",
			body: `{
  "approvals": [
    {"secret": "apiToken", "host": "attacker.example", "approvedAt": "2026-01-02T03:04:05Z"}
  ]
}
`,
		},
		{
			// Version 1 in the file, but an entry still carrying the old fields.
			name: "legacy fields inside a versioned file",
			body: `{"version":1,"approvals":[{"secret":"apiToken","host":"attacker.example"}]}`,
		},
		{
			// The request id is what makes an approval about ONE request; an
			// entry without it was written under a key that spanned them all.
			name: "missing the request id",
			body: `{"version":1,"approvals":[{"workspacePath":"/w","collectionId":"c","environmentId":"","globalEnvironmentIds":[],"origin":"https://attacker.example:443","kindClass":"request"}]}`,
		},
		{
			// Same argument for the environment half.
			name: "missing the environment fields",
			body: `{"version":1,"approvals":[{"workspacePath":"/w","collectionId":"c","requestId":"r","origin":"https://attacker.example:443","kindClass":"request"}]}`,
		},
		{
			name: "an unknown version",
			body: `{"version":2,"approvals":[{"workspacePath":"/w","collectionId":"c","requestId":"r","environmentId":"","globalEnvironmentIds":[],"origin":"https://attacker.example:443","kindClass":"request"}]}`,
		},
		{
			name: "not JSON at all",
			body: `{not valid json`,
		},
	} {
		t.Run(legacy.name, func(t *testing.T) {
			dir := t.TempDir()
			app := newAppInDirForTest(t, dir)
			var warnings []Notification
			app.notificationEmit = func(notification Notification) {
				warnings = append(warnings, notification)
			}
			original := []byte(legacy.body)
			if err := os.WriteFile(app.mcpApprovalsPath(), original, 0o600); err != nil {
				t.Fatalf("seed the legacy file: %v", err)
			}

			// IGNORED: nothing in that file authorizes anything, under any site.
			if approvedUnder(t, app, testSite("r"), mustOrigin(t, "https://attacker.example"), kindClassRequest) {
				t.Fatal("a legacy approvals file authorized a destination")
			}
			if approvedUnder(t, app, siteUnder("r", ""), mustOrigin(t, "https://attacker.example"), kindClassRequest) {
				t.Fatal("a legacy approvals file authorized a destination")
			}

			// NEVER DELETED, MOVED: and the backup is the original's bytes.
			backup := filepath.Join(dir, mcpApprovalsBackupFileName)
			kept, err := os.ReadFile(backup)
			if err != nil {
				t.Fatalf("the legacy file was not kept at %s: %v", backup, err)
			}
			if string(kept) != string(original) {
				t.Errorf("the backup is not byte-identical to the original:\n got: %q\nwant: %q", kept, original)
			}
			if _, err := os.Stat(app.mcpApprovalsPath()); !os.IsNotExist(err) {
				t.Errorf("the unreadable file is still in place at %s", app.mcpApprovalsPath())
			}

			// ANNOUNCED: silence here would mean the user is re-prompted for
			// destinations they had already allowed, with nothing to explain it.
			if len(warnings) != 1 {
				t.Fatalf("got %d warnings, want exactly 1", len(warnings))
			}
			if warnings[0].Level != "warning" {
				t.Errorf("the warning level is %q", warnings[0].Level)
			}
			if !strings.Contains(warnings[0].Message, mcpApprovalsBackupFileName) {
				t.Errorf("the warning does not say where the old file went: %q", warnings[0].Message)
			}

			// A FRESH VERSION 1 FILE ON THE NEXT REMEMBER, with only what was
			// remembered after the migration in it.
			site := testSite("req_charge")
			origin := mustOrigin(t, "https://api.example.com")
			if err := app.rememberMCPApproval(site, origin, kindClassRequest); err != nil {
				t.Fatalf("rememberMCPApproval after migration: %v", err)
			}
			data, err := os.ReadFile(app.mcpApprovalsPath())
			if err != nil {
				t.Fatalf("read the rewritten file: %v", err)
			}
			var stored types.MCPApprovalFile
			if err := json.Unmarshal(data, &stored); err != nil {
				t.Fatalf("decode the rewritten file: %v", err)
			}
			if stored.Version != mcpApprovalStoreVersion {
				t.Errorf("the rewritten file is version %d", stored.Version)
			}
			if len(stored.Approvals) != 1 {
				t.Errorf("the rewritten file holds %d entries, want 1: %s", len(stored.Approvals), data)
			}
			if strings.Contains(string(data), "attacker.example") {
				t.Errorf("a legacy entry was carried into the new file: %s", data)
			}
		})
	}
}

// An old file that held nothing is not a migration: no approval was refused, so
// warning that some were dropped would be false and the backup would be a copy
// of nothing.
func TestAnEmptyPreV6FileIsNotAnnouncedAsAMigration(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	warned := 0
	app.notificationEmit = func(Notification) { warned++ }

	if err := os.WriteFile(app.mcpApprovalsPath(), []byte(`{"approvals":[]}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if approvedUnder(t, app, testSite("req_charge"), mustOrigin(t, "https://api.example.com"), kindClassRequest) {
		t.Fatal("an empty file authorized something")
	}
	if warned != 0 {
		t.Errorf("an empty pre-v6 file raised %d warnings", warned)
	}
	if _, err := os.Stat(filepath.Join(dir, mcpApprovalsBackupFileName)); !os.IsNotExist(err) {
		t.Error("an empty pre-v6 file was backed up")
	}
}

// A file this build fully understands is loaded in silence — no backup, no
// warning. The migration path must not fire on the ordinary case, or every
// launch would warn about nothing.
func TestAValidApprovalFileIsNotBackedUpOrWarnedAbout(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	warned := 0
	app.notificationEmit = func(Notification) { warned++ }

	valid := types.MCPApprovalFile{
		Version: mcpApprovalStoreVersion,
		Approvals: []types.MCPApproval{{
			WorkspacePath:        "/workspaces/payments",
			CollectionID:         "col_payments",
			RequestID:            "req_charge",
			EnvironmentID:        "env_production",
			GlobalEnvironmentIDs: []string{"global_team"},
			Origin:               "https://reports.example.com:443",
			KindClass:            kindClassRequest,
			ApprovedAt:           time.Now().UTC(),
		}},
	}
	encoded, err := json.MarshalIndent(valid, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(app.mcpApprovalsPath(), encoded, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if !approvedUnder(t, app, testSite("req_charge"), mustOrigin(t, "https://reports.example.com"), kindClassRequest) {
		t.Fatal("a valid remembered approval was not honoured")
	}
	if warned != 0 {
		t.Errorf("a valid file raised %d warnings", warned)
	}
	if _, err := os.Stat(filepath.Join(dir, mcpApprovalsBackupFileName)); !os.IsNotExist(err) {
		t.Error("a valid file was backed up")
	}
}

// A file where SOME entries are honourable and some are not keeps the good ones
// and still says so. Dropping the whole file would punish the user for one bad
// line; keeping the bad line would be the widening this rule exists to stop.
func TestAMixedApprovalFileKeepsTheUsableEntriesAndStillWarns(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	warned := 0
	app.notificationEmit = func(Notification) { warned++ }

	body := `{"version":1,"approvals":[
      {"secret":"apiToken","host":"attacker.example"},
      {"workspacePath":"/workspaces/payments","collectionId":"col_payments","requestId":"req_charge","environmentId":"env_production","globalEnvironmentIds":["global_team"],"origin":"https://reports.example.com:443","kindClass":"request"}
    ]}`
	if err := os.WriteFile(app.mcpApprovalsPath(), []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if !approvedUnder(t, app, testSite("req_charge"), mustOrigin(t, "https://reports.example.com"), kindClassRequest) {
		t.Error("a usable entry alongside a legacy one was dropped")
	}
	if warned != 1 {
		t.Errorf("got %d warnings, want 1", warned)
	}
	if _, err := os.Stat(filepath.Join(dir, mcpApprovalsBackupFileName)); err != nil {
		t.Errorf("the mixed file was not kept: %v", err)
	}
}

// --- the shipped host guard's remembered half -------------------------------

// The (secret, host) lookup answers "nothing", deliberately: the store no longer
// holds such pairs, and honouring the ones the migration ignored would
// reintroduce exactly the wider scope §6 removed. The direction of the change is
// one extra prompt, never one extra destination.
func TestLegacyRememberedHostLookupIsEmpty(t *testing.T) {
	app := newAppForTest(t)
	if err := app.rememberMCPApproval(testSite("req_charge"), mustOrigin(t, "https://api.example.com"), kindClassRequest); err != nil {
		t.Fatalf("rememberMCPApproval: %v", err)
	}
	hosts, err := app.mcpRememberedHostsForSecret("apiToken")
	if err != nil {
		t.Fatalf("mcpRememberedHostsForSecret: %v", err)
	}
	if len(hosts) != 0 {
		t.Errorf("the legacy per-secret lookup returned %v, want nothing", hosts)
	}
}
