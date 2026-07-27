package recovery

// Snapshotting and restoring the managed .gitignore block around a delete.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"path/filepath"
	"strings"

	"github.com/mutexdev/lite_api/internal/gitignore"
	"github.com/mutexdev/lite_api/internal/types"
)

func GitIgnoreSnapshot(workspace types.Workspace) (string, bool, []byte, error) {
	dir, root, err := openWorkspaceDirectoryNoFollow(workspace.Path)
	if err != nil {
		return "", false, nil, err
	}
	defer closeWorkspaceDir(dir)
	data, exists, err := readWorkspaceFileAt(dir, ".gitignore")
	return filepath.Join(root, ".gitignore"), exists, data, err
}

func RestoreGitIgnore(workspace types.Workspace, exists bool, content []byte) error {
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

// UpdateManagedGitIgnore is the filesystem boundary used by the public
// collection APIs. Every workspace path component is opened relative to a
// verified directory FD with O_NOFOLLOW; final file operations use *at calls.
func UpdateManagedGitIgnore(workspacePath, collectionPath string, add bool) error {
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
	entries := gitignore.Entries(content)
	if add {
		entries[entry] = true
	} else {
		delete(entries, entry)
	}
	next := gitignore.ReplaceBlock(content, entries)
	if strings.TrimSpace(next) == "" {
		return removeWorkspaceFileAt(dir, ".gitignore")
	}
	return writeWorkspaceFileAtomicAt(dir, ".gitignore", []byte(next), true)
}
