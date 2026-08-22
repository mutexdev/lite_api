// Finding Bruno's collections (US-062).
//
// Bruno is the easy case, and deliberately so: its collections are folders of
// .bru files, and this app already opens those. Discovery has to produce paths
// and nothing else -- no converter, no copy, no parse.
//
// The only work is finding the paths. Bruno 2.x introduced workspaces, so the
// index moved: workspaces.lastOpenedWorkspaces points at workspace directories,
// each holding a workspace.yml that lists its collections. The older
// lastOpenedCollections key is still read, because it is what a Bruno that has
// not been upgraded still writes, and because the upgrade keeps it as migration
// input.
package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type brunoPreferences struct {
	LastOpenedCollections []string `json:"lastOpenedCollections"`
	Workspaces            struct {
		LastOpenedWorkspaces []string `json:"lastOpenedWorkspaces"`
	} `json:"workspaces"`
}

type brunoWorkspaceFile struct {
	Name        string `yaml:"name"`
	Collections []struct {
		Path string `yaml:"path"`
	} `yaml:"collections"`
}

func readBrunoCollections(directory string) ([]Discovered, error) {
	preferences := brunoPreferences{}
	if data, err := readBoundedFile(filepath.Join(directory, "preferences.json")); err == nil {
		// A preferences file that will not parse means we do not know what was
		// open; it does not mean Bruno is broken, and it is not worth an error.
		_ = json.Unmarshal(data, &preferences)
	}
	paths := append([]string{}, preferences.LastOpenedCollections...)
	for _, workspacePath := range preferences.Workspaces.LastOpenedWorkspaces {
		paths = append(paths, brunoWorkspaceCollections(workspacePath)...)
	}
	// Bruno's default workspace is not always in the index -- a fresh install
	// has one before it has opened anything.
	for _, entry := range brunoDefaultWorkspaces(directory) {
		paths = append(paths, brunoWorkspaceCollections(entry)...)
	}

	seen := map[string]bool{}
	discovered := []Discovered{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		// A remembered path that is no longer a collection is ordinary: people
		// move and delete directories. Offering it would produce an import that
		// fails for a reason the user cannot act on.
		if !brunoCollectionFolder(cleaned) {
			continue
		}
		discovered = append(discovered, Discovered{
			Client:     ClientBruno,
			Name:       brunoCollectionName(cleaned),
			SourcePath: cleaned,
			Kind:       "collection-folder",
		})
	}
	return discovered, nil
}

func brunoWorkspaceCollections(workspacePath string) []string {
	data, err := readBoundedFile(filepath.Join(workspacePath, "workspace.yml"))
	if err != nil {
		return nil
	}
	var workspace brunoWorkspaceFile
	if err := yaml.Unmarshal(data, &workspace); err != nil {
		return nil
	}
	paths := make([]string, 0, len(workspace.Collections))
	for _, entry := range workspace.Collections {
		if strings.TrimSpace(entry.Path) != "" {
			paths = append(paths, entry.Path)
		}
	}
	return paths
}

// brunoDefaultWorkspaces finds the default workspace directories Bruno creates,
// which are suffixed when more than one profile exists.
func brunoDefaultWorkspaces(directory string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "default-workspace") {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	return paths
}

// brunoCollectionFolder recognises both markers. Checking only for bruno.json
// misses every collection Bruno writes in the OpenCollection format, which is
// the current one.
func brunoCollectionFolder(path string) bool {
	for _, marker := range []string{"bruno.json", "collection.bru", "opencollection.yml", "opencollection.yaml"} {
		if info, err := os.Lstat(filepath.Join(path, marker)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// brunoCollectionName reads the declared name, falling back to the directory
// name. This is the one file read here, and it is the collection's own
// metadata rather than any of its requests.
func brunoCollectionName(path string) string {
	if data, err := readBoundedFile(filepath.Join(path, "bruno.json")); err == nil {
		var config struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &config); err == nil && strings.TrimSpace(config.Name) != "" {
			return strings.TrimSpace(config.Name)
		}
	}
	return filepath.Base(path)
}
