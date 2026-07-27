package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const recoverySecretSentinel = "M5_RECOVERY_SECRET_SENTINEL"

func recoverySecuritySnapshot(workspaceID string) recoverySnapshot {
	entry := newRecoveryEntry(recoveryKindRequest, "private", workspaceID, "collection-"+workspaceID)
	return recoverySnapshot{Entry: entry, Collection: Collection{
		ID:                 "collection-" + workspaceID,
		Auth:               AuthConfig{Token: recoverySecretSentinel},
		ClientCertificates: []ClientCertificateConfig{{Passphrase: recoverySecretSentinel}},
		Items:              []RequestItem{{ID: "request", Headers: []KeyValue{{Name: "Authorization", Value: recoverySecretSentinel}}, Body: RequestBody{JSON: recoverySecretSentinel}, Auth: AuthConfig{Token: recoverySecretSentinel}}},
	}}
}

func TestRecoveryEncryptedScopedArtifactsHideSecretsAndRestoreExactly(t *testing.T) {
	dataDir, source := t.TempDir(), filepath.Join(t.TempDir(), "collection")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("auth " + recoverySecretSentinel + "\nbody " + recoverySecretSentinel)
	if err := os.WriteFile(filepath.Join(source, "request.bru"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := recoverySecuritySnapshot("workspace/a")
	if err := stageRecoverySnapshot(dataDir, snapshot, source, true); err != nil {
		t.Fatal(err)
	}
	if err := scanRecoveryBytes(dataDir, []byte(recoverySecretSentinel)); err != nil {
		t.Fatal(err)
	}
	root, err := recoveryWorkspaceRoot(dataDir, snapshot.Entry.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, recoveryRoot(dataDir), 0o700)
	assertMode(t, root, 0o700)
	manifest, err := recoveryManifestPath(dataDir, snapshot.Entry.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, manifest, 0o600)
	payload, err := recoveryPayloadPath(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, payload, 0o600)
	restored := filepath.Join(t.TempDir(), "restored")
	if err := restoreRecoveryPayload(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID, restored); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(restored, "request.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload restore changed bytes: %q", got)
	}
}

func TestRecoveryWorkspaceIsolationTamperAndTraversal(t *testing.T) {
	dataDir := t.TempDir()
	a, b := recoverySecuritySnapshot("workspace-A"), recoverySecuritySnapshot("workspace-B")
	if err := stageRecoverySnapshot(dataDir, a, "", false); err != nil {
		t.Fatal(err)
	}
	if err := stageRecoverySnapshot(dataDir, b, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := findRecoveryEntry(dataDir, b.Entry.WorkspaceID, a.Entry.ID); err == nil {
		t.Fatal("workspace B listed workspace A entry")
	}
	if err := removeRecoveryEntry(dataDir, b.Entry.WorkspaceID, a.Entry.ID); err == nil {
		t.Fatal("workspace B removed workspace A entry")
	}
	if _, err := findRecoveryEntry(dataDir, a.Entry.WorkspaceID, a.Entry.ID); err != nil {
		t.Fatalf("workspace A entry changed: %v", err)
	}
	path, err := recoverySnapshotPath(dataDir, a.Entry.WorkspaceID, a.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecoverySnapshot(dataDir, a.Entry.WorkspaceID, a.Entry.ID); err == nil {
		t.Fatal("tampered recovery snapshot was accepted")
	}
	if _, err := recoveryEntryDir(dataDir, a.Entry.WorkspaceID, "../escape"); err == nil {
		t.Fatal("traversal entry id was accepted")
	}
}

func TestRecoveryRejectsArtifactsCopiedToDifferentDataDir(t *testing.T) {
	sourceDataDir, targetDataDir := t.TempDir(), t.TempDir()
	snapshot := recoverySecuritySnapshot("workspace-copy")
	if err := stageRecoverySnapshot(sourceDataDir, snapshot, "", false); err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := recoveryWorkspaceRoot(sourceDataDir, snapshot.Entry.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot, err := recoveryWorkspaceRoot(targetDataDir, snapshot.Entry.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyRecoveryTestTree(sourceRoot, targetRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecoveryManifest(targetDataDir, snapshot.Entry.WorkspaceID); err == nil {
		t.Fatal("recovery artifact copied to another data dir was accepted")
	}
}

func TestRecoveryConcurrentWorkspaceManifestUpdatesDoNotLoseEntries(t *testing.T) {
	dataDir, workspaceID := t.TempDir(), "workspace-concurrent"
	const count = 16
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- stageRecoverySnapshot(dataDir, recoverySecuritySnapshot(workspaceID), "", false)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := readRecoveryManifest(dataDir, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != count {
		t.Fatalf("concurrent manifest entries = %d, want %d", len(manifest.Entries), count)
	}
}

func TestRecoveryConcurrentFreshWorkspacesShareStableLegacyLock(t *testing.T) {
	dataDir := t.TempDir()
	const count = 64
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	snapshots := make([]recoverySnapshot, count)
	for i := range snapshots {
		snapshots[i] = recoverySecuritySnapshot("fresh-workspace-" + fmt.Sprint(i))
		wg.Add(1)
		go func(snapshot recoverySnapshot) {
			defer wg.Done()
			errCh <- stageRecoverySnapshot(dataDir, snapshot, "", false)
		}(snapshots[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, snapshot := range snapshots {
		entry, err := findRecoveryEntry(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
		if err != nil || entry.ID != snapshot.Entry.ID {
			t.Fatalf("fresh workspace %s lost recovery entry: %#v err=%v", snapshot.Entry.WorkspaceID, entry, err)
		}
	}
	lockPath := filepath.Join(dataDir, ".liteapi-legacy-migration.lock")
	assertMode(t, lockPath, 0o600)
	if _, err := os.Stat(legacyRecoveryRoot(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deletable legacy payload root survived fresh migration: %v", err)
	}
}

func TestLegacyRecoveryMigrationIsEncryptedAndFailureKeepsPlaintextSource(t *testing.T) {
	dataDir := t.TempDir()
	snapshot := recoverySecuritySnapshot("legacy-workspace")
	legacyDir := filepath.Join(legacyRecoveryRoot(dataDir), snapshot.Entry.ID)
	if err := os.MkdirAll(filepath.Join(legacyDir, "collection"), 0o700); err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "snapshot.json"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "collection", "private.bru"), []byte(recoverySecretSentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyManifest, err := json.Marshal(recoveryManifest{Version: 1, Entries: []RecoveryEntry{snapshot.Entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRecoveryRoot(dataDir), "manifest.json"), legacyManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecoveryManifest(dataDir, snapshot.Entry.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "snapshot.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy plaintext snapshot remained: %v", err)
	}
	if err := scanRecoveryBytes(dataDir, []byte(recoverySecretSentinel)); err != nil {
		t.Fatal(err)
	}

	failureDir := t.TempDir()
	failureSnapshot := recoverySecuritySnapshot("legacy-failure")
	failureLegacyDir := filepath.Join(legacyRecoveryRoot(failureDir), failureSnapshot.Entry.ID)
	if err := os.MkdirAll(failureLegacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	failurePlain, _ := json.Marshal(failureSnapshot)
	if err := os.WriteFile(filepath.Join(failureLegacyDir, "snapshot.json"), failurePlain, 0o600); err != nil {
		t.Fatal(err)
	}
	failureManifest, _ := json.Marshal(recoveryManifest{Version: 1, Entries: []RecoveryEntry{failureSnapshot.Entry}})
	if err := os.WriteFile(filepath.Join(legacyRecoveryRoot(failureDir), "manifest.json"), failureManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	oldWriter := recoveryWritePrivateAtomic
	recoveryWritePrivateAtomic = func(string, []byte) error { return errors.New("injected write failure") }
	_, err = readRecoveryManifest(failureDir, failureSnapshot.Entry.WorkspaceID)
	recoveryWritePrivateAtomic = oldWriter
	if err == nil {
		t.Fatal("migration write failure was accepted")
	}
	if _, err := os.Stat(filepath.Join(failureLegacyDir, "snapshot.json")); err != nil {
		t.Fatalf("failure removed reversible legacy snapshot: %v", err)
	}
}

func TestLegacyRecoveryMigrationScrubsArbitraryGitIgnorePath(t *testing.T) {
	dataDir := t.TempDir()
	snapshot := recoverySecuritySnapshot("legacy-malicious-path")
	snapshot.Entry.Kind = recoveryKindCollection
	snapshot.GitIgnorePath = filepath.Join(t.TempDir(), "outside-authority")
	legacyDir := filepath.Join(legacyRecoveryRoot(dataDir), snapshot.Entry.ID)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "snapshot.json"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(recoveryManifest{Version: 1, Entries: []RecoveryEntry{snapshot.Entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRecoveryRoot(dataDir), "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecoveryManifest(dataDir, snapshot.Entry.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	migrated, err := readRecoverySnapshot(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.GitIgnorePath != "" {
		t.Fatalf("legacy filesystem authority survived migration: %q", migrated.GitIgnorePath)
	}
}

func TestLegacyRecoveryConcurrentWorkspaceMigrationKeepsBothEntries(t *testing.T) {
	dataDir := t.TempDir()
	a, b := recoverySecuritySnapshot("legacy-A"), recoverySecuritySnapshot("legacy-B")
	for _, snapshot := range []recoverySnapshot{a, b} {
		dir := filepath.Join(legacyRecoveryRoot(dataDir), snapshot.Entry.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		plain, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), plain, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := json.Marshal(recoveryManifest{Version: 1, Entries: []RecoveryEntry{a.Entry, b.Entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRecoveryRoot(dataDir), "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, workspaceID := range []string{a.Entry.WorkspaceID, b.Entry.WorkspaceID} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _, err := readRecoveryManifest(dataDir, id); errs <- err }(workspaceID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, snapshot := range []recoverySnapshot{a, b} {
		if _, err := findRecoveryEntry(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID); err != nil {
			t.Fatalf("migrated entry missing for %s: %v", snapshot.Entry.WorkspaceID, err)
		}
	}
	if _, err := os.Stat(legacyRecoveryRoot(dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy recovery was not cleaned after both migrations: %v", err)
	}
}

func TestLegacyRecoveryCleanupFailureRetainsRetryableSource(t *testing.T) {
	dataDir := t.TempDir()
	snapshot := recoverySecuritySnapshot("legacy-cleanup")
	legacyDir := filepath.Join(legacyRecoveryRoot(dataDir), snapshot.Entry.ID)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "snapshot.json"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(recoveryManifest{Version: 1, Entries: []RecoveryEntry{snapshot.Entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRecoveryRoot(dataDir), "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	oldRemove := recoveryRemoveAll
	recoveryRemoveAll = func(path string) error {
		if path == legacyDir {
			return errors.New("injected cleanup failure")
		}
		return oldRemove(path)
	}
	_, err = readRecoveryManifest(dataDir, snapshot.Entry.WorkspaceID)
	recoveryRemoveAll = oldRemove
	if err == nil {
		t.Fatal("cleanup failure was accepted")
	}
	encryptedSnapshot, pathErr := recoverySnapshotPath(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, err := os.Stat(encryptedSnapshot); err != nil {
		t.Fatalf("encrypted output was not durable before cleanup failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "snapshot.json")); err != nil {
		t.Fatalf("cleanup failure removed retry source: %v", err)
	}
	if _, err := readRecoveryManifest(dataDir, snapshot.Entry.WorkspaceID); err != nil {
		t.Fatalf("cleanup retry failed: %v", err)
	}
	if _, err := os.Stat(legacyDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup retry left plaintext legacy directory: %v", err)
	}
}

func scanRecoveryBytes(root string, needle []byte) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, needle) {
			return errors.New("plaintext secret found in recovery artifact")
		}
		return nil
	})
}

func copyRecoveryTestTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(target, 0o700)
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
}
