package recovery

// Where every recovery artefact lives, and the validation that keeps a hostile id inside the root.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	googleuuid "github.com/google/uuid"

	"github.com/mutexdev/lite_api/internal/filelock"
)

func Root(dataDir string) string { return filepath.Join(dataDir, "recovery-v2") }

func legacyRecoveryRoot(dataDir string) string { return filepath.Join(dataDir, "recovery") }

func recoveryWorkspaceRoot(dataDir, workspaceID string) (string, error) {
	if err := validateRecoveryWorkspaceID(workspaceID); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(workspaceID))
	return filepath.Join(Root(dataDir), hex.EncodeToString(digest[:])), nil
}

func ManifestPath(dataDir, workspaceID string) (string, error) {
	root, err := recoveryWorkspaceRoot(dataDir, workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "manifest.enc"), nil
}

func EntryDir(dataDir, workspaceID, entryID string) (string, error) {
	if err := validateRecoveryEntryID(entryID); err != nil {
		return "", err
	}
	root, err := recoveryWorkspaceRoot(dataDir, workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "entries", entryID), nil
}

func SnapshotPath(dataDir, workspaceID, entryID string) (string, error) {
	dir, err := EntryDir(dataDir, workspaceID, entryID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshot.enc"), nil
}

func recoveryPayloadPath(dataDir, workspaceID, entryID string) (string, error) {
	dir, err := EntryDir(dataDir, workspaceID, entryID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "payload.enc"), nil
}

func validateRecoveryWorkspaceID(id string) error {
	if strings.TrimSpace(id) == "" || strings.Contains(id, "\x00") {
		return errors.New("recovery workspace id is invalid")
	}
	return nil
}

func validateRecoveryEntryID(id string) error {
	if _, err := googleuuid.Parse(strings.TrimSpace(id)); err != nil {
		return errors.New("recovery entry id is invalid")
	}
	return nil
}

func ensureRecoveryRoot(dataDir, workspaceID string) error {
	root, err := recoveryWorkspaceRoot(dataDir, workspaceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	return os.Chmod(root, 0o700)
}

func withRecoveryWorkspaceLock(dataDir, workspaceID string, fn func() error) error {
	root, err := recoveryWorkspaceRoot(dataDir, workspaceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	guard := filepath.Join(root, ".lock")
	f, err := os.OpenFile(guard, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	unlockF, err := filelock.Exclusive(f)
	if err != nil {
		return err
	}
	// Best-effort: the deferred file close already releases this lock.
	defer unlockF()
	if err := migrateLegacyRecovery(dataDir, workspaceID); err != nil {
		return err
	}
	return fn()
}

func validateRecoveryRelativePath(path string) error {
	if path == "" || strings.Contains(path, "\x00") || filepath.IsAbs(path) {
		return errors.New("recovery payload path is invalid")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("recovery payload path escapes root")
	}
	return nil
}
