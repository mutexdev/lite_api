package core

// Git-backed collections: version probing, clone progress and repository scanning.
//
// Split out of app_collection_io.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mutexdev/lite_api/internal/recovery"
)

func gitVersion() (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed or not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "--version").CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.New("git --version timed out")
		}
		return "", fmt.Errorf("git --version failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (a *App) emitGitCloneProgress(stage, message, targetPath string) {
	if a == nil || a.ctx == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "git:clone:progress", GitCloneProgress{
		Stage:      stage,
		Message:    message,
		TargetPath: targetPath,
		At:         time.Now().Format(time.RFC3339Nano),
	})
}

func scanGitProgressToken(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for index, b := range data {
		if b == '\n' || b == '\r' {
			return index + 1, bytes.TrimSpace(data[:index]), nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), bytes.TrimSpace(data), nil
	}
	return 0, nil, nil
}

func deriveGitRepoName(remote string) string {
	remote = strings.TrimSpace(remote)
	// Opaque must be empty as well as the scheme set. "myserver:repo.git" — the
	// scp form with the host coming from an ssh config alias — parses as
	// scheme="myserver" with an empty Path, so accepting it here derives the
	// name from nothing. A URL with a real "//" authority always has an empty
	// Opaque, which separates the two forms without a list of known schemes.
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" && parsed.Opaque == "" {
		base := strings.TrimSuffix(pathBase(parsed.Path), ".git")
		return sanitizeFilename(base)
	}
	if colon := strings.LastIndex(remote, ":"); colon >= 0 && colon < len(remote)-1 {
		return sanitizeFilename(strings.TrimSuffix(pathBase(remote[colon+1:]), ".git"))
	}
	return sanitizeFilename(strings.TrimSuffix(pathBase(remote), ".git"))
}

func pathBase(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	index := strings.LastIndex(value, "/")
	if index >= 0 {
		return value[index+1:]
	}
	return value
}

func scanBrunoCollections(rootPath string) ([]GitCollectionCandidate, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, errors.New("scan path is required")
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", rootPath)
	}
	rootPath = filepath.Clean(rootPath)
	candidates := []GitCollectionCandidate{}
	err = filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if path != rootPath && (base == ".git" || base == "node_modules" || base == "environments") {
			return filepath.SkipDir
		}
		if !looksLikeCollectionDir(path) {
			return nil
		}
		collection, err := readCollectionFromDisk(path)
		if err != nil {
			return err
		}
		candidates = append(candidates, GitCollectionCandidate{
			Name:         collection.Name,
			Path:         filepath.Clean(path),
			Format:       collection.Format,
			RequestCount: len(collection.Items),
		})
		if path != rootPath {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].Path) < strings.ToLower(candidates[j].Path)
	})
	return candidates, nil
}

func looksLikeCollectionDir(path string) bool {
	for _, name := range []string{"bruno.json", "opencollection.yml", "opencollection.yaml"} {
		if info, err := os.Stat(filepath.Join(path, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func updateManagedGitIgnore(workspacePath, collectionPath string, add bool) error {
	return recovery.UpdateManagedGitIgnore(workspacePath, collectionPath, add)
}
