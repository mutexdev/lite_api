package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	googleuuid "github.com/google/uuid"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
)

const (
	recoveryManifestVersion = 2
	recoveryEnvelopeVersion = 1
)

// Test seam for migration failure injection. Production always uses the
// package-private atomic writer shared by the workspace persistence stores.
var (
	recoveryWritePrivateAtomic   = atomicfile.WritePrivate
	recoveryRemoveAll            = os.RemoveAll
	ManagedGitIgnoreBeforeCommit = func() {}
)

type recoveryManifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Snapshot is private recovery state. It is only ever serialized into
// an authenticated recovery envelope; never write it directly as JSON.
type Snapshot struct {
	Entry Entry `json:"entry"`
	// WorkspaceIndex is retained only to decode M3 snapshots. Recovery code
	// must resolve the workspace by immutable ID, never by this old index.
	WorkspaceIndex       int              `json:"workspaceIndex,omitempty"`
	CollectionIndex      int              `json:"collectionIndex"`
	Collection           types.Collection `json:"collection"`
	WorkspaceUpdatedAt   time.Time        `json:"workspaceUpdatedAt"`
	OpenTabs             []types.OpenTab  `json:"openTabs"`
	ClosedTabs           []types.OpenTab  `json:"closedTabs"`
	ActiveTabID          string           `json:"activeTabId"`
	AffectedRequestIDs   []string         `json:"affectedRequestIds,omitempty"`
	GitIgnorePath        string           `json:"gitIgnorePath,omitempty"`
	GitIgnoreExists      bool             `json:"gitIgnoreExists,omitempty"`
	GitIgnoreContent     []byte           `json:"gitIgnoreContent,omitempty"`
	PostGitIgnoreExists  bool             `json:"postGitIgnoreExists,omitempty"`
	PostGitIgnoreContent []byte           `json:"postGitIgnoreContent,omitempty"`
	PostCollection       types.Collection `json:"postCollection"`
	PostFingerprint      string           `json:"postFingerprint,omitempty"`
	PostOpenTabs         []types.OpenTab  `json:"postOpenTabs,omitempty"`
	PostClosedTabs       []types.OpenTab  `json:"postClosedTabs,omitempty"`
	PostActiveTabID      string           `json:"postActiveTabId,omitempty"`
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

func readRecoveryManifestLocked(dataDir, workspaceID string) (recoveryManifest, error) {
	manifest := recoveryManifest{Version: recoveryManifestVersion}
	path, err := ManifestPath(dataDir, workspaceID)
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
	path, err := ManifestPath(dataDir, workspaceID)
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

func writeRecoverySnapshotLocked(dataDir string, snapshot Snapshot) error {
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
	path, err := SnapshotPath(dataDir, snapshot.Entry.WorkspaceID, snapshot.Entry.ID)
	if err != nil {
		return err
	}
	return recoveryWritePrivateAtomic(path, raw)
}
func WriteSnapshot(dataDir string, snapshot Snapshot) error {
	return withRecoveryWorkspaceLock(dataDir, snapshot.Entry.WorkspaceID, func() error { return writeRecoverySnapshotLocked(dataDir, snapshot) })
}
func readRecoverySnapshotLocked(dataDir, workspaceID, entryID string) (Snapshot, error) {
	path, err := SnapshotPath(dataDir, workspaceID, entryID)
	if err != nil {
		return Snapshot{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	plain, err := decryptRecovery(dataDir, workspaceID, "snapshot:"+entryID, raw)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(plain, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("parse recovery snapshot: %w", err)
	}
	if snapshot.Entry.ID != entryID || snapshot.Entry.WorkspaceID != workspaceID {
		return Snapshot{}, errors.New("recovery snapshot identity is invalid")
	}
	return snapshot, nil
}
func ReadSnapshot(dataDir, workspaceID, entryID string) (Snapshot, error) {
	var result Snapshot
	err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		var err error
		result, err = readRecoverySnapshotLocked(dataDir, workspaceID, entryID)
		return err
	})
	return result, err
}

func StageSnapshot(dataDir string, snapshot Snapshot, sourcePath string, includePayload bool) error {
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
		dir, err := EntryDir(dataDir, workspaceID, snapshot.Entry.ID)
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

func RemoveEntry(dataDir, workspaceID, entryID string) error {
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
		dir, err := EntryDir(dataDir, workspaceID, entryID)
		if err != nil {
			return err
		}
		return os.RemoveAll(dir)
	})
}
func FindEntry(dataDir, workspaceID, entryID string) (Entry, error) {
	manifest, err := readRecoveryManifest(dataDir, workspaceID)
	if err != nil {
		return Entry{}, err
	}
	for _, entry := range manifest.Entries {
		if entry.ID == entryID {
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("recovery entry %s not found", entryID)
}

func NewEntry(kind, displayName, workspaceID, collectionID string) Entry {
	now := time.Now().UTC()
	return Entry{ID: googleuuid.NewString(), Kind: kind, DisplayName: displayName, WorkspaceID: workspaceID, CollectionID: collectionID, DeletedAt: now, ExpiresAt: now.Add(recoveryEntryTTL)}
}
func MarkEntryRestorable(dataDir, workspaceID, entryID string) (Entry, error) {
	var result Entry
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
func RestorePayload(dataDir, workspaceID, entryID, target string) error {
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
		if !scalar.PathInside(target, dest) {
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
func copyPrivateBytes(data []byte, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o600)
}

// restoreRecoveryTree remains for restoring an encrypted payload. Source is a
// recovery entry ID path only through recovery callers; generic tree copying is
// intentionally not used for recovery storage anymore.
func CopyTree(source, target string) error {
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
func restoreRecoveryTree(source, target string) error { return CopyTree(source, target) }
func ReplaceTree(source, target string) error {
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

func CollectionFingerprint(root string) (string, error) {
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

func RemoveExpiredEntries(dataDir, workspaceID string, now time.Time) ([]Entry, error) {
	active := make([]Entry, 0)
	err := withRecoveryWorkspaceLock(dataDir, workspaceID, func() error {
		manifest, err := readRecoveryManifestLocked(dataDir, workspaceID)
		if err != nil {
			return err
		}
		retained := make([]Entry, 0, len(manifest.Entries))
		for _, entry := range manifest.Entries {
			if !entry.ExpiresAt.After(now) {
				dir, err := EntryDir(dataDir, workspaceID, entry.ID)
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
