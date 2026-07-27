package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	googleuuid "github.com/google/uuid"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

const (
	recoveryManifestVersion = 2
	recoveryEnvelopeVersion = 1
)

// Test seam for migration failure injection. Production always uses the
// package-private atomic writer shared by the workspace persistence stores.
var (
	recoveryWritePrivateAtomic   = writePrivateAtomic
	recoveryRemoveAll            = os.RemoveAll
	managedGitIgnoreBeforeCommit = func() {}
)

type recoveryManifest struct {
	Version int             `json:"version"`
	Entries []RecoveryEntry `json:"entries"`
}

// recoverySnapshot is private recovery state. It is only ever serialized into
// an authenticated recovery envelope; never write it directly as JSON.
type recoverySnapshot struct {
	Entry RecoveryEntry `json:"entry"`
	// WorkspaceIndex is retained only to decode M3 snapshots. Recovery code
	// must resolve the workspace by immutable ID, never by this old index.
	WorkspaceIndex       int        `json:"workspaceIndex,omitempty"`
	CollectionIndex      int        `json:"collectionIndex"`
	Collection           Collection `json:"collection"`
	WorkspaceUpdatedAt   time.Time  `json:"workspaceUpdatedAt"`
	OpenTabs             []OpenTab  `json:"openTabs"`
	ClosedTabs           []OpenTab  `json:"closedTabs"`
	ActiveTabID          string     `json:"activeTabId"`
	AffectedRequestIDs   []string   `json:"affectedRequestIds,omitempty"`
	GitIgnorePath        string     `json:"gitIgnorePath,omitempty"`
	GitIgnoreExists      bool       `json:"gitIgnoreExists,omitempty"`
	GitIgnoreContent     []byte     `json:"gitIgnoreContent,omitempty"`
	PostGitIgnoreExists  bool       `json:"postGitIgnoreExists,omitempty"`
	PostGitIgnoreContent []byte     `json:"postGitIgnoreContent,omitempty"`
	PostCollection       Collection `json:"postCollection"`
	PostFingerprint      string     `json:"postFingerprint,omitempty"`
	PostOpenTabs         []OpenTab  `json:"postOpenTabs,omitempty"`
	PostClosedTabs       []OpenTab  `json:"postClosedTabs,omitempty"`
	PostActiveTabID      string     `json:"postActiveTabId,omitempty"`
}

type recoveryEnvelope struct {
	Version    int    `json:"version"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type recoveryPayload struct {
	Entries []recoveryPayloadEntry `json:"entries"`
}
type recoveryPayloadEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Data []byte `json:"data,omitempty"`
	Link string `json:"link,omitempty"`
}

func recoveryRoot(dataDir string) string       { return filepath.Join(dataDir, "recovery-v2") }
func legacyRecoveryRoot(dataDir string) string { return filepath.Join(dataDir, "recovery") }

func recoveryWorkspaceRoot(dataDir, workspaceID string) (string, error) {
	if err := validateRecoveryWorkspaceID(workspaceID); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(workspaceID))
	return filepath.Join(recoveryRoot(dataDir), hex.EncodeToString(digest[:])), nil
}
func recoveryManifestPath(dataDir, workspaceID string) (string, error) {
	root, err := recoveryWorkspaceRoot(dataDir, workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "manifest.enc"), nil
}
func recoveryEntryDir(dataDir, workspaceID, entryID string) (string, error) {
	if err := validateRecoveryEntryID(entryID); err != nil {
		return "", err
	}
	root, err := recoveryWorkspaceRoot(dataDir, workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "entries", entryID), nil
}
func recoverySnapshotPath(dataDir, workspaceID, entryID string) (string, error) {
	dir, err := recoveryEntryDir(dataDir, workspaceID, entryID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshot.enc"), nil
}
func recoveryPayloadPath(dataDir, workspaceID, entryID string) (string, error) {
	dir, err := recoveryEntryDir(dataDir, workspaceID, entryID)
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
	unlockF, err := lockFileExclusive(f)
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
	unlockLock, err := lockFileExclusive(lock)
	if err != nil {
		return err
	}
	// Best-effort: the deferred file close already releases this lock.
	defer unlockLock()
	return migrateLegacyRecoveryLocked(dataDir, workspaceID)
}

func recoveryAAD(dataDir, workspaceID, artifact string) []byte {
	return []byte("liteapi-recovery/v1\x00" + filepath.Clean(dataDir) + "\x00" + workspaceID + "\x00" + artifact)
}
func encryptRecovery(dataDir, workspaceID, artifact string, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(environmentSecretAESKey(dataDir))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	envelope := recoveryEnvelope{Version: recoveryEnvelopeVersion, Algorithm: "AES-256-GCM", Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, recoveryAAD(dataDir, workspaceID, artifact)))}
	return json.Marshal(envelope)
}
func decryptRecovery(dataDir, workspaceID, artifact string, raw []byte) ([]byte, error) {
	var envelope recoveryEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse recovery envelope: %w", err)
	}
	if envelope.Version != recoveryEnvelopeVersion || envelope.Algorithm != "AES-256-GCM" {
		return nil, errors.New("unsupported recovery envelope")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, errors.New("invalid recovery nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid recovery ciphertext")
	}
	block, err := aes.NewCipher(environmentSecretAESKey(dataDir))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("invalid recovery nonce")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, recoveryAAD(dataDir, workspaceID, artifact))
	if err != nil {
		return nil, errors.New("recovery authentication failed")
	}
	return plain, nil
}

func readRecoveryManifestLocked(dataDir, workspaceID string) (recoveryManifest, error) {
	manifest := recoveryManifest{Version: recoveryManifestVersion}
	path, err := recoveryManifestPath(dataDir, workspaceID)
	if err != nil {
		return manifest, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return manifest, nil
	}
	if err != nil {
		return manifest, err
	}
	plain, err := decryptRecovery(dataDir, workspaceID, "manifest", raw)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(plain, &manifest); err != nil {
		return manifest, fmt.Errorf("parse recovery manifest: %w", err)
	}
	if manifest.Version != recoveryManifestVersion {
		return manifest, fmt.Errorf("unsupported recovery manifest version %d", manifest.Version)
	}
	for _, entry := range manifest.Entries {
		if entry.WorkspaceID != workspaceID || validateRecoveryEntryID(entry.ID) != nil {
			return manifest, errors.New("recovery manifest contains an invalid entry")
		}
	}
	return manifest, nil
}
func writeRecoveryManifestLocked(dataDir, workspaceID string, manifest recoveryManifest) error {
	if err := ensureRecoveryRoot(dataDir, workspaceID); err != nil {
		return err
	}
	manifest.Version = recoveryManifestVersion
	plain, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	raw, err := encryptRecovery(dataDir, workspaceID, "manifest", plain)
	if err != nil {
		return err
	}
	path, err := recoveryManifestPath(dataDir, workspaceID)
	if err != nil {
		return err
	}
	return recoveryWritePrivateAtomic(path, raw)
}
func readRecoveryManifest(dataDir, workspaceID string) (recoveryManifest, error) {
	var result recoveryManifest
	err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		var err error
		result, err = readRecoveryManifestLocked(dataDir, workspaceID)
		return err
	})
	return result, err
}

func writeRecoverySnapshotLocked(dataDir string, snapshot recoverySnapshot) error {
	if err := validateRecoveryEntryID(snapshot.Entry.ID); err != nil {
		return err
	}
	if err := validateRecoveryWorkspaceID(snapshot.Entry.WorkspaceID); err != nil {
		return err
	}
	plain, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	raw, err := encryptRecovery(dataDir, snapshot.Entry.WorkspaceID, "snapshot:"+snapshot.Entry.ID, plain)
	if err != nil {
		return err
	}
	path, err := recoverySnapshotPath(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		return err
	}
	return recoveryWritePrivateAtomic(path, raw)
}
func writeRecoverySnapshot(dataDir string, snapshot recoverySnapshot) error {
	return withRecoveryWorkspaceLock(dataDir, snapshot.Entry.WorkspaceID, func() error { return writeRecoverySnapshotLocked(dataDir, snapshot) })
}
func readRecoverySnapshotLocked(dataDir, workspaceID, entryID string) (recoverySnapshot, error) {
	path, err := recoverySnapshotPath(dataDir, workspaceID, entryID)
	if err != nil {
		return recoverySnapshot{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return recoverySnapshot{}, err
	}
	plain, err := decryptRecovery(dataDir, workspaceID, "snapshot:"+entryID, raw)
	if err != nil {
		return recoverySnapshot{}, err
	}
	var snapshot recoverySnapshot
	if err := json.Unmarshal(plain, &snapshot); err != nil {
		return recoverySnapshot{}, fmt.Errorf("parse recovery snapshot: %w", err)
	}
	if snapshot.Entry.ID != entryID || snapshot.Entry.WorkspaceID != workspaceID {
		return recoverySnapshot{}, errors.New("recovery snapshot identity is invalid")
	}
	return snapshot, nil
}
func readRecoverySnapshot(dataDir, workspaceID, entryID string) (recoverySnapshot, error) {
	var result recoverySnapshot
	err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		var err error
		result, err = readRecoverySnapshotLocked(dataDir, workspaceID, entryID)
		return err
	})
	return result, err
}

func stageRecoverySnapshot(dataDir string, snapshot recoverySnapshot, sourcePath string, includePayload bool) error {
	workspaceID := snapshot.Entry.WorkspaceID
	return withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		if err := validateRecoveryEntryID(snapshot.Entry.ID); err != nil {
			return err
		}
		manifest, err := readRecoveryManifestLocked(dataDir, workspaceID)
		if err != nil {
			return err
		}
		for _, entry := range manifest.Entries {
			if entry.ID == snapshot.Entry.ID {
				return errors.New("recovery entry already exists")
			}
		}
		dir, err := recoveryEntryDir(dataDir, workspaceID, snapshot.Entry.ID)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
		cleanup := func(err error) error { _ = os.RemoveAll(dir); return err }
		if includePayload {
			if err := writeRecoveryPayloadLocked(dataDir, workspaceID, snapshot.Entry.ID, sourcePath); err != nil {
				return cleanup(err)
			}
		}
		if err := writeRecoverySnapshotLocked(dataDir, snapshot); err != nil {
			return cleanup(err)
		}
		manifest.Entries = append(manifest.Entries, snapshot.Entry)
		if err := writeRecoveryManifestLocked(dataDir, workspaceID, manifest); err != nil {
			return cleanup(err)
		}
		return nil
	})
}

func removeRecoveryEntry(dataDir, workspaceID, entryID string) error {
	return withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		manifest, err := readRecoveryManifestLocked(dataDir, workspaceID)
		if err != nil {
			return err
		}
		next := manifest.Entries[:0]
		found := false
		for _, entry := range manifest.Entries {
			if entry.ID == entryID {
				found = true
				continue
			}
			next = append(next, entry)
		}
		if !found {
			return fmt.Errorf("recovery entry %s not found", entryID)
		}
		manifest.Entries = next
		if err := writeRecoveryManifestLocked(dataDir, workspaceID, manifest); err != nil {
			return err
		}
		dir, err := recoveryEntryDir(dataDir, workspaceID, entryID)
		if err != nil {
			return err
		}
		return os.RemoveAll(dir)
	})
}
func findRecoveryEntry(dataDir, workspaceID, entryID string) (RecoveryEntry, error) {
	manifest, err := readRecoveryManifest(dataDir, workspaceID)
	if err != nil {
		return RecoveryEntry{}, err
	}
	for _, entry := range manifest.Entries {
		if entry.ID == entryID {
			return entry, nil
		}
	}
	return RecoveryEntry{}, fmt.Errorf("recovery entry %s not found", entryID)
}

func newRecoveryEntry(kind, displayName, workspaceID, collectionID string) RecoveryEntry {
	now := time.Now().UTC()
	return RecoveryEntry{ID: googleuuid.NewString(), Kind: kind, DisplayName: displayName, WorkspaceID: workspaceID, CollectionID: collectionID, DeletedAt: now, ExpiresAt: now.Add(recoveryEntryTTL)}
}
func markRecoveryEntryRestorable(dataDir, workspaceID, entryID string) (RecoveryEntry, error) {
	var result RecoveryEntry
	err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		snapshot, err := readRecoverySnapshotLocked(dataDir, workspaceID, entryID)
		if err != nil {
			return err
		}
		snapshot.Entry.Restorable = true
		if err := writeRecoverySnapshotLocked(dataDir, snapshot); err != nil {
			return err
		}
		manifest, err := readRecoveryManifestLocked(dataDir, workspaceID)
		if err != nil {
			return err
		}
		for i := range manifest.Entries {
			if manifest.Entries[i].ID == entryID {
				manifest.Entries[i].Restorable = true
				result = manifest.Entries[i]
				return writeRecoveryManifestLocked(dataDir, workspaceID, manifest)
			}
		}
		return fmt.Errorf("recovery entry %s not found", entryID)
	})
	return result, err
}

func writeRecoveryPayloadLocked(dataDir, workspaceID, entryID, source string) error {
	payload, err := captureRecoveryPayload(source)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw, err := encryptRecovery(dataDir, workspaceID, "payload:"+entryID, plain)
	if err != nil {
		return err
	}
	path, err := recoveryPayloadPath(dataDir, workspaceID, entryID)
	if err != nil {
		return err
	}
	return recoveryWritePrivateAtomic(path, raw)
}
func captureRecoveryPayload(source string) (recoveryPayload, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return recoveryPayload{}, err
	}
	if !info.IsDir() {
		return recoveryPayload{}, fmt.Errorf("recovery source %s is not a directory", source)
	}
	payload := recoveryPayload{}
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		rel = filepath.ToSlash(rel)
		if err := validateRecoveryRelativePath(rel); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			payload.Entries = append(payload.Entries, recoveryPayloadEntry{Path: rel, Type: "symlink", Link: link})
			return nil
		}
		if entry.IsDir() {
			payload.Entries = append(payload.Entries, recoveryPayloadEntry{Path: rel, Type: "dir"})
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported recovery file type at %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payload.Entries = append(payload.Entries, recoveryPayloadEntry{Path: rel, Type: "file", Data: data})
		return nil
	})
	return payload, err
}
func readRecoveryPayloadLocked(dataDir, workspaceID, entryID string) (recoveryPayload, error) {
	path, err := recoveryPayloadPath(dataDir, workspaceID, entryID)
	if err != nil {
		return recoveryPayload{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return recoveryPayload{}, err
	}
	plain, err := decryptRecovery(dataDir, workspaceID, "payload:"+entryID, raw)
	if err != nil {
		return recoveryPayload{}, err
	}
	var payload recoveryPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return recoveryPayload{}, fmt.Errorf("parse recovery payload: %w", err)
	}
	for _, entry := range payload.Entries {
		if err := validateRecoveryRelativePath(entry.Path); err != nil {
			return recoveryPayload{}, err
		}
		if entry.Type != "dir" && entry.Type != "file" && entry.Type != "symlink" {
			return recoveryPayload{}, errors.New("recovery payload entry type is invalid")
		}
	}
	return payload, nil
}
func restoreRecoveryPayload(dataDir, workspaceID, entryID, target string) error {
	var payload recoveryPayload
	if err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		var err error
		payload, err = readRecoveryPayloadLocked(dataDir, workspaceID, entryID)
		return err
	}); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, entry := range payload.Entries {
		if entry.Type == "dir" {
			if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(entry.Path)), 0o700); err != nil {
				return err
			}
		}
	}
	for _, entry := range payload.Entries {
		dest := filepath.Join(target, filepath.FromSlash(entry.Path))
		if !pathInside(target, dest) {
			return errors.New("recovery payload path escapes restore target")
		}
		switch entry.Type {
		case "dir":
			continue
		case "file":
			if err := copyPrivateBytes(entry.Data, dest); err != nil {
				return err
			}
		case "symlink":
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(entry.Link, dest); err != nil {
				return err
			}
		}
	}
	return nil
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
func copyPrivateBytes(data []byte, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o600)
}

// restoreRecoveryTree remains for restoring an encrypted payload. Source is a
// recovery entry ID path only through recovery callers; generic tree copying is
// intentionally not used for recovery storage anymore.
func copyRecoveryTree(source, target string) error {
	payload, err := captureRecoveryPayload(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	for _, e := range payload.Entries {
		if e.Type == "dir" {
			if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(e.Path)), 0o700); err != nil {
				return err
			}
		}
	}
	for _, e := range payload.Entries {
		dest := filepath.Join(target, filepath.FromSlash(e.Path))
		switch e.Type {
		case "file":
			if err := copyPrivateBytes(e.Data, dest); err != nil {
				return err
			}
		case "symlink":
			if err := os.Symlink(e.Link, dest); err != nil {
				return err
			}
		}
	}
	return nil
}
func restoreRecoveryTree(source, target string) error { return copyRecoveryTree(source, target) }
func replaceRecoveryTree(source, target string) error {
	entries, err := os.ReadDir(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
	}
	return restoreRecoveryTree(source, target)
}

func collectionRecoveryFingerprint(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(rel)+"\x00"+entry.Type().String()+"\x00")
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(hash, link)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported collection file type at %s", path)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(hash, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func removeExpiredRecoveryEntries(dataDir, workspaceID string, now time.Time) ([]RecoveryEntry, error) {
	active := make([]RecoveryEntry, 0)
	err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		manifest, err := readRecoveryManifestLocked(dataDir, workspaceID)
		if err != nil {
			return err
		}
		retained := make([]RecoveryEntry, 0, len(manifest.Entries))
		for _, entry := range manifest.Entries {
			if !entry.ExpiresAt.After(now) {
				dir, err := recoveryEntryDir(dataDir, workspaceID, entry.ID)
				if err != nil {
					return err
				}
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
				continue
			}
			retained = append(retained, entry)
			if entry.Restorable {
				active = append(active, entry)
			}
		}
		if len(retained) != len(manifest.Entries) {
			manifest.Entries = retained
			return writeRecoveryManifestLocked(dataDir, workspaceID, manifest)
		}
		return nil
	})
	sort.Slice(active, func(i, j int) bool { return active[i].DeletedAt.After(active[j].DeletedAt) })
	return active, err
}

func recoveryGitIgnoreSnapshot(workspace Workspace) (string, bool, []byte, error) {
	dir, root, err := openWorkspaceDirectoryNoFollow(workspace.Path)
	if err != nil {
		return "", false, nil, err
	}
	defer closeWorkspaceDir(dir)
	data, exists, err := readWorkspaceFileAt(dir, ".gitignore")
	return filepath.Join(root, ".gitignore"), exists, data, err
}

func restoreGitIgnore(workspace Workspace, exists bool, content []byte) error {
	dir, _, err := openWorkspaceDirectoryNoFollow(workspace.Path)
	if err != nil {
		return err
	}
	defer closeWorkspaceDir(dir)
	if !exists {
		return removeWorkspaceFileAt(dir, ".gitignore")
	}
	return writeWorkspaceFileAtomicAt(dir, ".gitignore", content, true)
}

// updateManagedGitIgnoreSecure is the filesystem boundary used by the public
// collection APIs. Every workspace path component is opened relative to a
// verified directory FD with O_NOFOLLOW; final file operations use *at calls.
func updateManagedGitIgnoreSecure(workspacePath, collectionPath string, add bool) error {
	dir, root, err := openWorkspaceDirectoryNoFollow(workspacePath)
	if err != nil {
		return err
	}
	defer closeWorkspaceDir(dir)
	collectionAbs, err := canonicalizeTrustedLeadingPath(collectionPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, collectionAbs)
	if err != nil || rel == "." || rel == "" || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	entry := "/" + filepath.ToSlash(rel)
	data, _, err := readWorkspaceFileAt(dir, ".gitignore")
	if err != nil {
		return err
	}
	content := string(data)
	entries := managedGitIgnoreEntries(content)
	if add {
		entries[entry] = true
	} else {
		delete(entries, entry)
	}
	next := replaceManagedGitIgnoreBlock(content, entries)
	if strings.TrimSpace(next) == "" {
		return removeWorkspaceFileAt(dir, ".gitignore")
	}
	return writeWorkspaceFileAtomicAt(dir, ".gitignore", []byte(next), true)
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
	selected := make([]RecoveryEntry, 0)
	retained := make([]RecoveryEntry, 0)
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
		var snapshot recoverySnapshot
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
		dir, err := recoveryEntryDir(dataDir, workspaceID, entry.ID)
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

// writePrivateAtomic now lives in internal/atomicfile, which is where the five
// other callers outside this file reach it. Kept as a one-line alias because
// recovery_store.go names it in several places and the indirection costs
// nothing.
func writePrivateAtomic(path string, data []byte) error {
	return atomicfile.WritePrivate(path, data)
}
