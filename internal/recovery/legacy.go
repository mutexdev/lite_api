package recovery

// Migrating a pre-workspace recovery directory, and cleaning up what it leaves behind.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mutexdev/lite_api/internal/filelock"
)

// The legacy manifest was global. Converting different workspaces therefore
// must serialize on one global lock in addition to their scoped write locks.
func migrateLegacyRecovery(dataDir, workspaceID string) error {
	stableRoot := filepath.Clean(dataDir)
	if err := os.MkdirAll(stableRoot, 0o700); err != nil {
		return err
	}
	// This lock must outlive the deletable legacy payload root. Contenders may
	// already be waiting on its inode while cleanup removes recovery/, so a lock
	// inside that tree can split synchronization across two inodes.
	lock, err := os.OpenFile(filepath.Join(stableRoot, ".liteapi-legacy-migration.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	unlockLock, err := filelock.Exclusive(lock)
	if err != nil {
		return err
	}
	// Best-effort: the deferred file close already releases this lock.
	defer unlockLock()
	return migrateLegacyRecoveryLocked(dataDir, workspaceID)
}

// M3 compatibility: a legacy entry stays untouched until all encrypted output
// for its workspace has been durably written. A failed conversion therefore
// remains reversible by the old reader/version.
func migrateLegacyRecoveryLocked(dataDir, workspaceID string) error {
	legacyManifestPath := filepath.Join(legacyRecoveryRoot(dataDir), "manifest.json")
	raw, err := os.ReadFile(legacyManifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return cleanupLegacyRecoveryOrphans(dataDir, recoveryManifest{Version: 1})
	}
	if err != nil {
		return err
	}
	var legacy recoveryManifest
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("parse legacy recovery manifest: %w", err)
	}
	if legacy.Version != 1 {
		return fmt.Errorf("unsupported legacy recovery manifest version %d", legacy.Version)
	}
	selected := make([]Entry, 0)
	retained := make([]Entry, 0)
	for _, entry := range legacy.Entries {
		if entry.WorkspaceID == workspaceID {
			selected = append(selected, entry)
		} else {
			retained = append(retained, entry)
		}
	}
	if len(selected) == 0 {
		return cleanupLegacyRecoveryOrphans(dataDir, legacy)
	}
	manifest, err := readRecoveryManifestLocked(dataDir, workspaceID)
	if err != nil {
		return err
	}
	created := []string{}
	for _, entry := range selected {
		if validateRecoveryEntryID(entry.ID) != nil {
			return errors.New("legacy recovery entry id is invalid")
		}
		exists := false
		for _, current := range manifest.Entries {
			exists = exists || current.ID == entry.ID
		}
		if exists {
			continue
		}
		legacySnapshotPath := filepath.Join(legacyRecoveryRoot(dataDir), entry.ID, "snapshot.json")
		plain, err := os.ReadFile(legacySnapshotPath)
		if err != nil {
			return err
		}
		var snapshot Snapshot
		if err := json.Unmarshal(plain, &snapshot); err != nil {
			return err
		}
		if snapshot.Entry.ID != entry.ID || snapshot.Entry.WorkspaceID != workspaceID {
			return errors.New("legacy recovery snapshot identity is invalid")
		}
		// M3 stored a filesystem path in the snapshot. It was never a safe
		// authority, so conversion deliberately drops it. Restore derives the
		// direct-child path from the current resolved workspace instead.
		snapshot.GitIgnorePath = ""
		dir, err := EntryDir(dataDir, workspaceID, entry.ID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		created = append(created, dir)
		if _, err := os.Stat(filepath.Join(legacyRecoveryRoot(dataDir), entry.ID, "collection")); err == nil {
			if err := writeRecoveryPayloadLocked(dataDir, workspaceID, entry.ID, filepath.Join(legacyRecoveryRoot(dataDir), entry.ID, "collection")); err != nil {
				for _, d := range created {
					_ = os.RemoveAll(d)
				}
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeRecoverySnapshotLocked(dataDir, snapshot); err != nil {
			for _, d := range created {
				_ = os.RemoveAll(d)
			}
			return err
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	if err := writeRecoveryManifestLocked(dataDir, workspaceID, manifest); err != nil {
		for _, d := range created {
			_ = os.RemoveAll(d)
		}
		return err
	}
	// Commit the reduced legacy manifest before deleting any legacy payload.
	// An interrupted cleanup then leaves only unreferenced plaintext remnants,
	// which are deterministically removed on the next migration call; it never
	// leaves a manifest referring to deleted data.
	legacy.Entries = retained
	data, err := json.Marshal(legacy)
	if err != nil {
		return err
	}
	if err := recoveryWritePrivateAtomic(legacyManifestPath, data); err != nil {
		return err
	}
	return cleanupLegacyRecoveryOrphans(dataDir, legacy)
}

// cleanupLegacyRecoveryOrphans only removes entry directories no longer
// referenced by the already-committed legacy manifest. It makes an interrupted
// post-commit cleanup retryable without ever deleting a referenced snapshot.
func cleanupLegacyRecoveryOrphans(dataDir string, legacy recoveryManifest) error {
	root := legacyRecoveryRoot(dataDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	keep := make(map[string]bool, len(legacy.Entries))
	for _, entry := range legacy.Entries {
		keep[entry.ID] = true
	}
	for _, entry := range entries {
		if !entry.IsDir() || keep[entry.Name()] {
			continue
		}
		if err := recoveryRemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	if len(legacy.Entries) != 0 {
		return nil
	}
	if err := os.Remove(filepath.Join(root, "manifest.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(root)
}
