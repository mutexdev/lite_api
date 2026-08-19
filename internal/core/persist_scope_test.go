package core

// US-013 — secrets.json and the OAuth2 credential file are written only when
// their own contents change, independently of state.json.
//
// These tests assert on WHETHER THE FILE WAS WRITTEN, observed through its
// modification time and inode, rather than on timing. Both auxiliary writers go
// through atomicfile.Write / writePrivateAtomic, which rename a fresh temp file
// into place, so a write that happens always changes the inode — there is no
// way for a real write to slip past this check, and no way for a skipped write
// to look like one.

import (
	"os"
	"testing"
)

// fileIdentity returns something that changes if and only if the file was
// rewritten. Identity alone is enough for the atomic-rename writers used here;
// mtime is compared too so that a future non-atomic in-place write would also
// be caught rather than silently passing.
//
// This deliberately goes through os.FileInfo and os.SameFile rather than
// syscall.Stat_t. The previous version read stat.Mtimespec, which exists only
// on Darwin: on Linux the field is Mtim, and on Windows syscall.Stat_t does not
// exist at all, so this file compiled on exactly one of the three platforms we
// release. os.SameFile is the portable spelling of the same question — it
// compares device and inode on Unix and the volume/file index on Windows.
func fileIdentity(t *testing.T, path string) (info os.FileInfo, exists bool) {
	t.Helper()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info, true
}

// fileWasRewritten reports whether the file changed between two fileIdentity
// observations.
//
// Note this is a real behaviour change, not just a portability one. The old
// identity string built its inode component as string(rune(stat.Ino)), which
// converts the inode NUMBER to a single code point — every inode above
// 0x10FFFF, and every value in the surrogate range, collapses to U+FFFD. Two
// genuinely different inodes compared equal, so the inode half of the check was
// mostly inert and the assertion rested on mtime alone.
func fileWasRewritten(before, after os.FileInfo) bool {
	return !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime())
}

// TestSecretsFileIsNotRewrittenByUnrelatedMutations guards the write-skipping
// contract for the secrets scope.
//
// HONEST SCOPE NOTE: this test would also pass before US-013, because US-012
// already skipped the WRITE by re-reading the file and comparing bytes. What
// US-013 changed is the COST of reaching that conclusion — an in-memory
// fingerprint instead of an os.ReadFile on every keystroke — and cost is not
// something a behavioural test can see. The evidence for the cost claim is the
// benchmark, measured on this machine at -benchtime=200x -count=3:
//
//	BenchmarkMarkDirty  before 67,484 ns/op  3,078 B/op  26 allocs/op
//	                    after  48,742 ns/op  2,290 B/op  24 allocs/op
//
// This test exists so that a later story cannot delete the skip altogether.
func TestSecretsFileIsNotRewrittenByUnrelatedMutations(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	collectionID := state.Workspaces[0].Collections[0].ID

	// Settle: get whatever one-time writes startup performs out of the way.
	if _, err := app.CreateRequest(collectionID, "http", "settle"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	secretsPath := app.environmentSecretsPath()
	before, existedBefore := fileIdentity(t, secretsPath)

	// Mutations that touch no secret at all. This is the keystroke path.
	for i := range 20 {
		if _, err := app.CreateRequest(collectionID, "http", "no secrets here"); err != nil {
			t.Fatalf("CreateRequest #%d: %v", i, err)
		}
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	after, existsAfter := fileIdentity(t, secretsPath)
	if existedBefore != existsAfter {
		t.Fatalf("secrets.json existence changed across unrelated mutations: %v -> %v", existedBefore, existsAfter)
	}
	if existsAfter && fileWasRewritten(before, after) {
		t.Errorf("secrets.json was rewritten by 20 mutations that changed no secret")
	}
}

// TestSecretsFileIsRewrittenWhenASecretChanges is the other half, and it is the
// one that keeps the test above honest. A gate that never writes would pass
// every assertion in that test and lose the user's secrets.
func TestSecretsFileIsRewrittenWhenASecretChanges(t *testing.T) {
	dir := t.TempDir()
	app := newAppInDirForTest(t, dir)
	state, err := app.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	workspace := state.Workspaces[0]

	created, err := app.CreateGlobalEnvironment(workspace.ID, "secret env")
	if err != nil {
		t.Fatalf("CreateGlobalEnvironment: %v", err)
	}
	var target Environment
	for _, ws := range created.Workspaces {
		for _, env := range ws.GlobalEnvironments {
			if env.Name == "secret env" {
				target = env
			}
		}
	}
	if target.ID == "" {
		t.Fatalf("could not find the environment just created")
	}
	secret := []Variable{{ID: "token", Name: "token", Value: "first", DataType: "string", Enabled: true, Secret: true}}
	if _, err := app.UpdateGlobalEnvironmentVariables(workspace.ID, target.ID, secret); err != nil {
		t.Fatalf("UpdateGlobalEnvironmentVariables: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	secretsPath := app.environmentSecretsPath()
	before, existed := fileIdentity(t, secretsPath)
	if !existed {
		t.Fatalf("secrets.json should exist once a secret variable has been saved")
	}

	// Change the secret's VALUE. Nothing else about the workspace moves.
	secret[0].Value = "second"
	if _, err := app.UpdateGlobalEnvironmentVariables(workspace.ID, target.ID, secret); err != nil {
		t.Fatalf("UpdateGlobalEnvironmentVariables: %v", err)
	}
	if err := app.flushPersist(); err != nil {
		t.Fatalf("flushPersist: %v", err)
	}

	after, _ := fileIdentity(t, secretsPath)
	if !fileWasRewritten(before, after) {
		t.Errorf("secrets.json was NOT rewritten after a secret value changed — the gate is too aggressive")
	}
}

// TestOAuth2FingerprintDistinguishesDeletionFromAbsence pins the reason the
// fingerprint covers BOTH the baseline and the local map.
//
// storeOAuth2Credentials merges a delta of local against baseline. A key
// present in baseline and missing from local is a DELETION; a key missing from
// both never existed. Those two states have identical local maps, so a
// fingerprint over local alone would call them equal and the skip would swallow
// the deletion — a revoked token that stays on disk.
func TestOAuth2FingerprintDistinguishesDeletionFromAbsence(t *testing.T) {
	token := oauth2TokenResponse{AccessToken: "a"}

	deletion, err := oauth2TokenMapFingerprint(
		map[string]oauth2TokenResponse{"k": token},
		map[string]oauth2TokenResponse{},
	)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	absence, err := oauth2TokenMapFingerprint(
		map[string]oauth2TokenResponse{},
		map[string]oauth2TokenResponse{},
	)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if deletion == absence {
		t.Error("a pending deletion hashes the same as never having had the credential; the skip would swallow a revocation")
	}
}

// TestOAuth2FingerprintIsStableAndOrderIndependent. Map iteration order is
// randomised in Go, so a fingerprint that folded the map without sorting would
// differ between two calls over identical data — disabling the gate entirely,
// silently, and only sometimes.
func TestOAuth2FingerprintIsStableAndOrderIndependent(t *testing.T) {
	build := func() map[string]oauth2TokenResponse {
		return map[string]oauth2TokenResponse{
			"alpha":   {AccessToken: "1"},
			"beta":    {AccessToken: "2"},
			"gamma":   {AccessToken: "3"},
			"delta":   {AccessToken: "4"},
			"epsilon": {AccessToken: "5"},
		}
	}
	first, err := oauth2TokenMapFingerprint(build(), build())
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	for i := range 20 {
		again, err := oauth2TokenMapFingerprint(build(), build())
		if err != nil {
			t.Fatalf("fingerprint #%d: %v", i, err)
		}
		if again != first {
			t.Fatalf("fingerprint is not stable across map iteration orders (attempt %d)", i)
		}
	}

	// And it must actually distinguish different content.
	changed := build()
	changed["beta"] = oauth2TokenResponse{AccessToken: "changed"}
	other, err := oauth2TokenMapFingerprint(build(), changed)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if other == first {
		t.Error("fingerprint does not change when a token value changes")
	}
}

// TestOAuth2FingerprintIsUnambiguousAcrossKeyValueBoundaries. Without length
// prefixes, {"ab": "c"} and {"a": "bc"} concatenate to the same bytes. A
// collision here means a real credential change is skipped as a no-op.
func TestOAuth2FingerprintIsUnambiguousAcrossKeyValueBoundaries(t *testing.T) {
	empty := map[string]oauth2TokenResponse{}
	first, err := oauth2TokenMapFingerprint(empty, map[string]oauth2TokenResponse{"ab": {AccessToken: "c"}})
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	second, err := oauth2TokenMapFingerprint(empty, map[string]oauth2TokenResponse{"a": {AccessToken: "bc"}})
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if first == second {
		t.Error("keys and values are not delimited; different content hashes identically")
	}
}
