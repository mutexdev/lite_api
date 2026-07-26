//go:build unix

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	googleuuid "github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func openWorkspaceDirectoryNoFollow(path string) (*os.File, string, error) {
	value := strings.TrimSpace(path)
	if value == "" || strings.Contains(value, "\x00") {
		return nil, "", errors.New("workspace path is invalid")
	}
	absPath, err := canonicalizeTrustedLeadingPath(value)
	if err != nil {
		return nil, "", err
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	components := strings.Split(strings.TrimPrefix(absPath, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." {
			continue
		}
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, "", fmt.Errorf("open workspace directory component %q without following links: %w", component, openErr)
		}
		fd = next
	}
	dir := os.NewFile(uintptr(fd), absPath)
	if dir == nil {
		_ = unix.Close(fd)
		return nil, "", errors.New("open workspace directory")
	}
	return dir, absPath, nil
}

func canonicalizeTrustedLeadingPath(path string) (string, error) {
	value := strings.TrimSpace(path)
	if value == "" || strings.Contains(value, "\x00") {
		return "", errors.New("path is invalid")
	}
	absPath, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absPath = filepath.Clean(absPath)
	// Resolve only a root-owned leading platform alias (for example macOS
	// /var -> /private/var). User-controlled symlink components are rejected by
	// workspace directory openat(O_NOFOLLOW) walks, including post-check swaps.
	absPath, err = resolveTrustedLeadingDirectoryAlias(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve trusted leading path alias: %w", err)
	}
	return absPath, nil
}

func readWorkspaceFileAt(dir *os.File, name string) ([]byte, bool, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open workspace %s without following links: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, false, errors.New("open workspace file")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("workspace %s is not a regular file", name)
	}
	data, err := io.ReadAll(file)
	return data, true, err
}

func writeWorkspaceFileAtomicAt(dir *os.File, name string, content []byte, rejectExistingSymlink bool) error {
	if strings.ContainsAny(name, "/\\\x00") || name == "" {
		return errors.New("workspace filename is invalid")
	}
	if rejectExistingSymlink {
		var stat unix.Stat_t
		err := unix.Fstatat(int(dir.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil && stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return fmt.Errorf("workspace %s must not be a symlink", name)
		}
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	tmp := ".liteapi-" + name + "-" + googleuuid.NewString()
	fd, err := unix.Openat(int(dir.Fd()), tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	// Best-effort: on the success path the temp file has already been renamed away.
	defer func() { _ = unix.Unlinkat(int(dir.Fd()), tmp, 0) }()
	file := os.NewFile(uintptr(fd), tmp)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("open workspace temporary file")
	}
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
	if err := unix.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), name); err != nil {
		return err
	}
	return unix.Fsync(int(dir.Fd()))
}

// removeWorkspaceFileAt deletes a direct child of the workspace directory.
//
// unlinkat removes the entry itself and never follows a symlink, so a planted
// link cannot redirect the delete at something outside the workspace. The
// directory is fsynced afterwards so the removal survives a crash.
func removeWorkspaceFileAt(dir *os.File, name string) error {
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return unix.Fsync(int(dir.Fd()))
}

func closeWorkspaceDir(dir *os.File) { _ = dir.Close() }

func resolveTrustedLeadingDirectoryAlias(absPath string) (string, error) {
	for attempts := 0; attempts < 8; attempts++ {
		components := strings.Split(strings.TrimPrefix(absPath, string(filepath.Separator)), string(filepath.Separator))
		if len(components) == 0 || components[0] == "" {
			return absPath, nil
		}
		rootFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return "", err
		}
		var stat unix.Stat_t
		err = unix.Fstatat(rootFD, components[0], &stat, unix.AT_SYMLINK_NOFOLLOW)
		if err != nil {
			_ = unix.Close(rootFD)
			return "", err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFLNK {
			_ = unix.Close(rootFD)
			return absPath, nil
		}
		if stat.Uid != 0 {
			_ = unix.Close(rootFD)
			return "", fmt.Errorf("workspace leading directory %q is an untrusted symlink", components[0])
		}
		buffer := make([]byte, 4096)
		n, err := unix.Readlinkat(rootFD, components[0], buffer)
		_ = unix.Close(rootFD)
		if err != nil {
			return "", err
		}
		target := string(buffer[:n])
		if !filepath.IsAbs(target) {
			target = filepath.Join(string(filepath.Separator), target)
		}
		parts := append([]string{target}, components[1:]...)
		absPath = filepath.Clean(filepath.Join(parts...))
	}
	return "", errors.New("workspace leading directory has too many symlink aliases")
}
