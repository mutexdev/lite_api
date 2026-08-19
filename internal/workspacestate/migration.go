package workspacestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mutexdev/lite_api/internal/scalar"
	"github.com/mutexdev/lite_api/internal/types"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/atomicfile"
	"github.com/mutexdev/lite_api/internal/envsecrets"
	"github.com/mutexdev/lite_api/internal/filelock"
)

const workspaceMigrationVersion = 1

type WorkspaceMigrationMarker struct {
	Version           int               `json:"version"`
	Complete          bool              `json:"complete"`
	LegacyChecksum    string            `json:"legacyChecksum"`
	DefaultSessionID  string            `json:"defaultSessionId"`
	ArtifactChecksums map[string]string `json:"artifactChecksums"`
	CompletedAt       time.Time         `json:"completedAt"`
}

var (
	// Test seams. Production keeps both at their private atomic/read-back
	// implementations; tests may inject a deterministic failure.
	PersistenceWriteAtomic          = atomicfile.WritePrivate
	workspaceMigrationVerifyOutputs = verifyWorkspaceMigrationOutputs
)

func MigrationMarkerPath(dataDir string) string {
	return filepath.Join(dataDir, "workspace-migration-v1.json")
}

func DefaultSessionPath(dataDir, sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return filepath.Join(dataDir, "window-sessions", hex.EncodeToString(digest[:])+".json")
}

func WriteWorkspaceScopedState(dataDir string, scoped WorkspaceScopedState) error {
	data, err := EncodeWorkspaceScopedState(scoped)
	if err != nil {
		return err
	}
	return PersistenceWriteAtomic(WorkspaceScopedStatePath(dataDir, scoped.Workspace.ID), data)
}

func ReadWorkspaceScopedState(dataDir, workspaceID string) (WorkspaceScopedState, error) {
	if err := ValidateWorkspaceRegistryID(workspaceID); err != nil {
		return WorkspaceScopedState{}, err
	}
	data, err := os.ReadFile(WorkspaceScopedStatePath(dataDir, workspaceID))
	if err != nil {
		return WorkspaceScopedState{}, err
	}
	var scoped WorkspaceScopedState
	if err := json.Unmarshal(data, &scoped); err != nil {
		return WorkspaceScopedState{}, fmt.Errorf("parse workspace scoped state: %w", err)
	}
	if scoped.Version != WorkspaceScopedStateVersion || ValidateWorkspaceRegistryID(scoped.Workspace.ID) != nil || scoped.Workspace.ID != workspaceID {
		return WorkspaceScopedState{}, errors.New("workspace scoped state is invalid")
	}
	if _, err := CanonicalWorkspaceIdentity(scoped.Workspace.Path); err != nil {
		return WorkspaceScopedState{}, errors.New("workspace scoped state path is invalid")
	}
	return CloneWorkspaceScopedState(scoped)
}

func ExecuteWorkspaceMigration(dataDir string, legacy types.AppState, defaultSessionID string) error {
	return withWorkspaceMigrationGuard(dataDir, func() error {
		return executeWorkspaceMigrationLocked(dataDir, legacy, defaultSessionID)
	})
}

func executeWorkspaceMigrationLocked(dataDir string, legacy types.AppState, defaultSessionID string) error {
	if err := ValidateWorkspaceMigrationSessionID(defaultSessionID); err != nil {
		return err
	}
	markerPath := MigrationMarkerPath(dataDir)
	if _, err := os.Lstat(markerPath); err == nil {
		if err := ValidatePrivateRegularArtifact(markerPath); err != nil {
			return err
		}
		marker, err := ReadMigrationMarker(dataDir)
		if err != nil {
			return err
		}
		return ValidateMutableArtifacts(dataDir, marker)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	registry, plan, err := BuildWorkspaceMigrationPlan(dataDir, legacy)
	if err != nil {
		return err
	}
	if len(plan.States) == 0 {
		return errors.New("workspace migration requires a workspace for the default session")
	}
	shared, err := ProjectSharedAppState(legacy, dataDir)
	if err != nil {
		return err
	}
	legacyChecksum, err := workspaceMigrationLegacyChecksum(legacy, dataDir)
	if err != nil {
		return err
	}

	// A marker is a commit record. Remove it before modifying any artifact so
	// a failed retry can never leave an old "complete" marker beside partial
	// new output.

	if err := WriteWorkspaceMigrationRegistry(dataDir, registry); err != nil {
		return err
	}
	for _, scoped := range plan.States {
		if err := WriteWorkspaceScopedState(dataDir, scoped); err != nil {
			return err
		}
	}
	if err := WriteSharedAppState(dataDir, shared); err != nil {
		return err
	}
	session, err := buildDefaultWorkspaceMigrationSession(defaultSessionID, legacy, plan.States)
	if err != nil {
		return err
	}
	if err := WriteWorkspaceMigrationSession(dataDir, session); err != nil {
		return err
	}

	checksums, err := workspaceMigrationArtifactChecksums(dataDir, registry, plan.States, session)
	if err != nil {
		return err
	}
	marker := WorkspaceMigrationMarker{
		Version:           workspaceMigrationVersion,
		Complete:          true,
		LegacyChecksum:    legacyChecksum,
		DefaultSessionID:  defaultSessionID,
		ArtifactChecksums: checksums,
		CompletedAt:       time.Now().UTC(),
	}
	if err := workspaceMigrationVerifyOutputs(dataDir, marker); err != nil {
		return err
	}
	if err := writeWorkspaceMigrationMarker(dataDir, marker); err != nil {
		return err
	}
	// Validate the commit record as the final write/read-back gate.
	if _, err = ReadMigrationMarker(dataDir); err != nil {
		_ = os.Remove(markerPath)
		return err
	}
	return nil
}

func withWorkspaceMigrationGuard(dataDir string, fn func() error) error {
	guardPath := filepath.Join(filepath.Clean(dataDir), "workspace-migration.guard")
	if err := os.MkdirAll(filepath.Dir(guardPath), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	unlock, err := filelock.Exclusive(file)
	if err != nil {
		return err
	}
	// Best-effort: the deferred file close already releases this lock.
	defer unlock()
	return fn()
}

func buildDefaultWorkspaceMigrationSession(sessionID string, legacy types.AppState, states []WorkspaceScopedState) (WindowSession, error) {
	selected := states[0]
	for _, scoped := range states {
		if scoped.Workspace.ID == legacy.ActiveWorkspaceID {
			selected = scoped
			break
		}
	}
	session := WindowSession{
		Version:                 WindowSessionVersion,
		ID:                      sessionID,
		WorkspaceID:             selected.Workspace.ID,
		OpenTabs:                append([]types.OpenTab(nil), selected.OpenTabs...),
		ClosedTabs:              append([]types.OpenTab(nil), selected.ClosedTabs...),
		ActiveTabID:             selected.ActiveTabID,
		ResponsePaneOrientation: legacy.Preferences.Layout.ResponsePaneOrientation,
		UpdatedAt:               time.Now().UTC(),
	}
	return session, session.Validate()
}

func WriteWorkspaceMigrationRegistry(dataDir string, registry WorkspaceRegistry) error {
	if err := registry.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return PersistenceWriteAtomic(WorkspaceRegistryPath(dataDir), data)
}

func WriteWorkspaceMigrationSession(dataDir string, session WindowSession) error {
	if err := session.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return PersistenceWriteAtomic(DefaultSessionPath(dataDir, session.ID), data)
}

func writeWorkspaceMigrationMarker(dataDir string, marker WorkspaceMigrationMarker) error {
	if err := marker.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return PersistenceWriteAtomic(MigrationMarkerPath(dataDir), data)
}

func ReadMigrationMarker(dataDir string) (WorkspaceMigrationMarker, error) {
	data, err := os.ReadFile(MigrationMarkerPath(dataDir))
	if err != nil {
		return WorkspaceMigrationMarker{}, err
	}
	var marker WorkspaceMigrationMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return WorkspaceMigrationMarker{}, fmt.Errorf("parse workspace migration marker: %w", err)
	}
	return marker, marker.Validate()
}

func (marker WorkspaceMigrationMarker) Validate() error {
	if marker.Version != workspaceMigrationVersion || !marker.Complete || strings.TrimSpace(marker.LegacyChecksum) == "" || strings.TrimSpace(marker.DefaultSessionID) == "" || marker.CompletedAt.IsZero() {
		return errors.New("workspace migration marker is invalid")
	}
	if err := ValidateWorkspaceMigrationSessionID(marker.DefaultSessionID); err != nil {
		return err
	}
	if len(marker.ArtifactChecksums) < 3 {
		return errors.New("workspace migration marker has incomplete checksums")
	}
	for path, checksum := range marker.ArtifactChecksums {
		if !workspaceMigrationArtifactPathAllowed(path) || !isSHA256Checksum(checksum) {
			return errors.New("workspace migration marker checksum is invalid")
		}
	}
	return nil
}

func verifyWorkspaceMigrationOutputs(dataDir string, marker WorkspaceMigrationMarker) error {
	if err := marker.Validate(); err != nil {
		return err
	}
	registry, err := ReadWorkspaceRegistry(dataDir)
	if err != nil {
		return err
	}
	if _, err := ReadSharedAppState(dataDir); err != nil {
		return err
	}
	if session, err := ReadWindowSession(DefaultSessionPath(dataDir, marker.DefaultSessionID)); err != nil || session.ID != marker.DefaultSessionID {
		if err != nil {
			return err
		}
		return errors.New("default workspace migration session is invalid")
	}
	expectedArtifacts := map[string]bool{
		"workspace-registry.json": true,
		"shared-state.json":       true,
	}
	sessionRelativePath, err := filepath.Rel(dataDir, DefaultSessionPath(dataDir, marker.DefaultSessionID))
	if err != nil {
		return err
	}
	expectedArtifacts[filepath.ToSlash(sessionRelativePath)] = true
	for _, workspace := range registry.Workspaces {
		relativePath, err := filepath.Rel(dataDir, WorkspaceScopedStatePath(dataDir, workspace.ID))
		if err != nil {
			return err
		}
		expectedArtifacts[filepath.ToSlash(relativePath)] = true
		if _, ok := marker.ArtifactChecksums[filepath.ToSlash(relativePath)]; !ok {
			return errors.New("workspace migration marker is missing a scoped state checksum")
		}
	}
	if len(marker.ArtifactChecksums) != len(expectedArtifacts) {
		return errors.New("workspace migration marker has unexpected artifacts")
	}
	for path := range marker.ArtifactChecksums {
		if !expectedArtifacts[path] {
			return errors.New("workspace migration marker has unexpected artifact path")
		}
	}
	for path, expected := range marker.ArtifactChecksums {
		fullPath := filepath.Join(dataDir, filepath.FromSlash(path))
		if !scalar.PathInside(dataDir, fullPath) {
			return errors.New("workspace migration artifact escapes data directory")
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return err
		}
		if FileChecksum(data) != expected {
			return fmt.Errorf("workspace migration checksum mismatch for %s", path)
		}
		if info, err := os.Stat(fullPath); err != nil || info.Mode().Perm() != 0o600 {
			if err != nil {
				return err
			}
			return fmt.Errorf("workspace migration artifact permissions are invalid for %s", path)
		}
		if strings.HasPrefix(path, "workspace-state/") {
			id, err := workspaceIDForScopedStatePath(dataDir, fullPath)
			if err != nil {
				return err
			}
			if _, err := ReadWorkspaceScopedState(dataDir, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateMutableArtifacts validates the live post-migration state.
// Marker checksums prove the initial transaction only; scoped persistence is
// expected to change these files afterwards.
func ValidateMutableArtifacts(dataDir string, marker WorkspaceMigrationMarker) error {
	if err := marker.Validate(); err != nil {
		return err
	}
	for _, path := range []string{MigrationMarkerPath(dataDir), WorkspaceRegistryPath(dataDir), SharedAppStatePath(dataDir), DefaultSessionPath(dataDir, marker.DefaultSessionID)} {
		if err := ValidatePrivateRegularArtifact(path); err != nil {
			return err
		}
	}
	registry, err := ReadWorkspaceRegistry(dataDir)
	if err != nil {
		return err
	}
	if _, err := ReadSharedAppState(dataDir); err != nil {
		return err
	}
	if _, err := ReadWindowSession(DefaultSessionPath(dataDir, marker.DefaultSessionID)); err != nil {
		return err
	}
	paths := []string{WorkspaceRegistryPath(dataDir), SharedAppStatePath(dataDir), DefaultSessionPath(dataDir, marker.DefaultSessionID)}
	for _, workspace := range registry.Workspaces {
		path := WorkspaceScopedStatePath(dataDir, workspace.ID)
		if err := ValidatePrivateRegularArtifact(path); err != nil {
			return err
		}
		scoped, err := ReadWorkspaceScopedState(dataDir, workspace.ID)
		if err != nil {
			return err
		}
		if scoped.Workspace.ID != workspace.ID || !SameCanonicalWorkspacePath(scoped.Workspace.Path, workspace.Path) {
			return errors.New("registry and scoped workspace identity do not match")
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("workspace artifact permissions are invalid for %s", path)
		}
	}
	return nil
}

func ValidatePrivateRegularArtifact(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("workspace artifact is not a regular file: %s", path)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("workspace artifact permissions are invalid for %s", path)
	}
	return nil
}

func workspaceMigrationArtifactChecksums(dataDir string, registry WorkspaceRegistry, states []WorkspaceScopedState, session WindowSession) (map[string]string, error) {
	checksums := map[string]string{}
	paths := []string{WorkspaceRegistryPath(dataDir), SharedAppStatePath(dataDir), DefaultSessionPath(dataDir, session.ID)}
	for _, scoped := range states {
		paths = append(paths, WorkspaceScopedStatePath(dataDir, scoped.Workspace.ID))
	}
	for _, fullPath := range paths {
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(dataDir, fullPath)
		if err != nil || !workspaceMigrationArtifactPathAllowed(filepath.ToSlash(rel)) {
			return nil, errors.New("workspace migration artifact path is invalid")
		}
		checksums[filepath.ToSlash(rel)] = FileChecksum(data)
	}
	return checksums, nil
}

func workspaceIDForScopedStatePath(dataDir, fullPath string) (string, error) {
	registry, err := ReadWorkspaceRegistry(dataDir)
	if err != nil {
		return "", err
	}
	for _, workspace := range registry.Workspaces {
		if WorkspaceScopedStatePath(dataDir, workspace.ID) == fullPath {
			return workspace.ID, nil
		}
	}
	return "", errors.New("workspace scoped state is not registered")
}

func workspaceMigrationLegacyChecksum(legacy types.AppState, dataDir string) (string, error) {
	stored := envsecrets.StateForStorage(legacy, dataDir)
	data, err := json.Marshal(stored)
	if err != nil {
		return "", err
	}
	return FileChecksum(data), nil
}

func FileChecksum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func ValidateWorkspaceMigrationSessionID(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(sessionID) > 256 || strings.ContainsAny(sessionID, "/\\\x00") || sessionID == "." || sessionID == ".." {
		return errors.New("workspace migration session ID is invalid")
	}
	return nil
}

func workspaceMigrationArtifactPathAllowed(path string) bool {
	if path == "workspace-registry.json" || path == "shared-state.json" {
		return true
	}
	for _, prefix := range []string{"workspace-state/", "window-sessions/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		filename := strings.TrimPrefix(path, prefix)
		if strings.ContainsAny(filename, "/\\") || !strings.HasSuffix(filename, ".json") {
			return false
		}
		name := strings.TrimSuffix(filename, ".json")
		return isSHA256Checksum(name)
	}
	return false
}

func isSHA256Checksum(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
