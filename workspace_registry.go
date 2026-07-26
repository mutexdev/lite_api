package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/atomicfile"
)

const workspaceRegistryVersion = 1

type WorkspaceRegistryEntry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type WorkspaceRegistry struct {
	Version    int                      `json:"version"`
	Workspaces []WorkspaceRegistryEntry `json:"workspaces"`
}

func workspaceRegistryPath(dataDir string) string {
	return filepath.Join(dataDir, "workspace-registry.json")
}
func (r WorkspaceRegistry) Validate() error {
	if r.Version != workspaceRegistryVersion {
		return fmt.Errorf("unsupported workspace registry version %d", r.Version)
	}
	seenID := map[string]bool{}
	seenPath := map[string]bool{}
	for _, e := range r.Workspaces {
		if err := validateWorkspaceRegistryID(e.ID); err != nil || strings.TrimSpace(e.Path) == "" {
			return errors.New("workspace registry entry is invalid")
		}
		p, err := canonicalWorkspaceIdentity(e.Path)
		if err != nil {
			return err
		}
		if seenID[e.ID] || seenPath[p] {
			return errors.New("workspace registry has duplicate id or path")
		}
		seenID[e.ID] = true
		seenPath[p] = true
	}
	return nil
}

func validateWorkspaceRegistryID(id string) error {
	if id != strings.TrimSpace(id) || id == "" || len(id) > 256 || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
		return errors.New("workspace registry id is invalid")
	}
	return nil
}
func WriteWorkspaceRegistry(dataDir string, r WorkspaceRegistry) error {
	if err := r.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WritePrivate(workspaceRegistryPath(dataDir), data)
}
func ReadWorkspaceRegistry(dataDir string) (WorkspaceRegistry, error) {
	data, err := os.ReadFile(workspaceRegistryPath(dataDir))
	if err != nil {
		return WorkspaceRegistry{}, err
	}
	var r WorkspaceRegistry
	if err := json.Unmarshal(data, &r); err != nil {
		return WorkspaceRegistry{}, fmt.Errorf("parse workspace registry: %w", err)
	}
	return r, r.Validate()
}
func (r WorkspaceRegistry) Resolve(id, path string) (WorkspaceRegistryEntry, error) {
	id, path = strings.TrimSpace(id), strings.TrimSpace(path)
	if id != "" && path != "" {
		return WorkspaceRegistryEntry{}, errors.New("workspace registry identity is ambiguous")
	}
	if id != "" {
		if err := validateWorkspaceRegistryID(id); err != nil {
			return WorkspaceRegistryEntry{}, err
		}
	} else if path != "" {
		if _, err := canonicalWorkspaceIdentity(path); err != nil {
			return WorkspaceRegistryEntry{}, err
		}
	}
	for _, e := range r.Workspaces {
		if id != "" && e.ID == id {
			return e, nil
		}
		if path != "" && sameCanonicalWorkspacePath(e.Path, path) {
			return e, nil
		}
	}
	return WorkspaceRegistryEntry{}, errors.New("workspace registry entry not found")
}
