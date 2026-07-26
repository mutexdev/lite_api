//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	googleuuid "github.com/google/uuid"
)

// The Windows half of the workspace directory operations.
//
// WHAT THIS GUARANTEES, AND HOW IT DIFFERS FROM THE UNIX HALF.
//
// The unix implementation opens each path component with O_NOFOLLOW and then
// operates through the resulting directory descriptor. That is ATOMIC: the
// kernel refuses the open if the component is a symlink, so there is no window
// between deciding a path is safe and using it.
//
// Windows has no openat/O_NOFOLLOW pair. This implementation uses os.Root
// (Go 1.24+), which constrains every operation to stay inside the opened
// directory — a planted link cannot redirect a read or a write to somewhere
// outside the workspace. On top of that, each name is Lstat'd and refused if it
// is a symlink or reparse point, which restores the "no symlink at all" rule
// the unix side gets from O_NOFOLLOW.
//
// The residual difference is a TOCTOU window between that Lstat and the open.
// An attacker who can create a symlink inside the workspace directory between
// the two calls could win it. They cannot escape the root either way, because
// os.Root enforces that at the syscall layer — so the worst case is redirection
// WITHIN the workspace, not outside it. On unix there is no window at all.
//
// This is documented rather than hidden because it is a real difference in a
// security property, and the next person to touch this should know which
// platform they are reasoning about.

// openWorkspaceDirectoryNoFollow opens the workspace directory, refusing any
// component of the path that is a symlink.
func openWorkspaceDirectoryNoFollow(path string) (*os.Root, string, error) {
	value := strings.TrimSpace(path)
	if value == "" || strings.Contains(value, "\x00") {
		return nil, "", errors.New("workspace path is invalid")
	}
	absPath, err := filepath.Abs(value)
	if err != nil {
		return nil, "", err
	}
	absPath = filepath.Clean(absPath)

	volume := filepath.VolumeName(absPath)
	if volume == "" {
		return nil, "", fmt.Errorf("workspace path %q has no volume", absPath)
	}
	root, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		return nil, "", err
	}

	rest := strings.TrimPrefix(absPath, volume)
	for _, component := range strings.Split(rest, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		info, statErr := root.Lstat(component)
		if statErr != nil {
			_ = root.Close()
			return nil, "", fmt.Errorf("open workspace directory component %q: %w", component, statErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close()
			return nil, "", fmt.Errorf("workspace directory component %q is a symlink", component)
		}
		next, openErr := root.OpenRoot(component)
		_ = root.Close()
		if openErr != nil {
			return nil, "", fmt.Errorf("open workspace directory component %q: %w", component, openErr)
		}
		root = next
	}
	return root, absPath, nil
}

// readWorkspaceFileAt reads a direct child, refusing anything that is not a
// regular file.
func readWorkspaceFileAt(dir *os.Root, name string) ([]byte, bool, error) {
	info, err := dir.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("workspace %s is a symlink", name)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("workspace %s is not a regular file", name)
	}
	file, err := dir.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	return data, true, err
}

// writeWorkspaceFileAtomicAt writes a direct child through a temporary file in
// the same directory, then renames it into place.
//
// No directory fsync afterwards: Windows offers no equivalent, so the rename's
// ordering is what the filesystem provides rather than something this code can
// force. The rename itself is still atomic with respect to readers.
func writeWorkspaceFileAtomicAt(dir *os.Root, name string, content []byte, rejectExistingSymlink bool) error {
	if strings.ContainsAny(name, `/\`+"\x00") || name == "" {
		return errors.New("workspace filename is invalid")
	}
	if rejectExistingSymlink {
		info, err := dir.Lstat(name)
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("workspace %s must not be a symlink", name)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}

	tmp := ".liteapi-" + name + "-" + googleuuid.NewString()
	file, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	// Best-effort: on the success path the temp file has already been renamed away.
	defer func() { _ = dir.Remove(tmp) }()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	managedGitIgnoreBeforeCommit()
	return dir.Rename(tmp, name)
}

// removeWorkspaceFileAt deletes a direct child. os.Root.Remove will not follow
// a symlink out of the directory.
func removeWorkspaceFileAt(dir *os.Root, name string) error {
	if err := dir.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func closeWorkspaceDir(dir *os.Root) { _ = dir.Close() }

// canonicalizeTrustedLeadingPath resolves a path to an absolute, cleaned form.
//
// The unix version additionally resolves one level of symlink on the leading
// component, which is what makes /var work on macOS where it is a link to
// /private/var. Windows has no such conventional alias at the volume root, so
// this is the plain absolute form.
func canonicalizeTrustedLeadingPath(path string) (string, error) {
	value := strings.TrimSpace(path)
	if value == "" || strings.Contains(value, "\x00") {
		return "", errors.New("path is invalid")
	}
	absPath, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absPath), nil
}
