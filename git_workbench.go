package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	gitWorkbenchTimeout        = 12 * time.Second
	gitWorkbenchNetworkTimeout = 45 * time.Second
	gitWorkbenchOutputLimit    = 256 * 1024
	gitWorkbenchDisplayLimit   = 1024
)

type CollectionGitFile struct {
	Path       string `json:"path"`
	Index      string `json:"index"`
	Worktree   string `json:"worktree"`
	Staged     bool   `json:"staged"`
	Untracked  bool   `json:"untracked"`
	Conflicted bool   `json:"conflicted"`
	Binary     bool   `json:"binary"`
}

type CollectionGitRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type CollectionGitSnapshot struct {
	Available   bool                  `json:"available"`
	Initialized bool                  `json:"initialized"`
	RootLabel   string                `json:"rootLabel,omitempty"`
	Branch      string                `json:"branch,omitempty"`
	Detached    bool                  `json:"detached"`
	Upstream    string                `json:"upstream,omitempty"`
	Ahead       int                   `json:"ahead"`
	Behind      int                   `json:"behind"`
	Clean       bool                  `json:"clean"`
	Conflicts   bool                  `json:"conflicts"`
	Remotes     []CollectionGitRemote `json:"remotes"`
	Branches    []string              `json:"branches"`
	Files       []CollectionGitFile   `json:"files"`
}

type CollectionGitDiff struct {
	Path      string `json:"path"`
	Staged    bool   `json:"staged"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
}

type CollectionGitOperationResult struct {
	Snapshot CollectionGitSnapshot `json:"snapshot"`
	Message  string                `json:"message"`
}

type collectionGitRepository struct {
	collectionPath string
	repositoryPath string
	pathspec       string
}

type limitedGitBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedGitBuffer) Write(p []byte) (int, error) {
	// The b.Buffer.Write calls below must stay explicitly qualified: b.Write is
	// this method, so dropping the embedded field would recurse forever.
	if b.Len() < b.limit {
		remaining := b.limit - b.Len()
		if len(p) > remaining {
			_, _ = b.Buffer.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.Buffer.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

var gitWorkbenchURLUserInfoPattern = regexp.MustCompile(`(?i)(https?|ssh|git)://[^\s/@]+@`)
var gitWorkbenchURLQueryPattern = regexp.MustCompile(`(?i)(https?|ssh|git|file)://[^\s?#]+\?[^\s]+`)
var gitWorkbenchAbsolutePathPattern = regexp.MustCompile(`(?:^|[\s:'\"])(?:/[^\s:'\"]+|[A-Za-z]:\\[^\s:'\"]+)`)

func (a *App) gitWorkbenchExecutableName() string {
	if a != nil && strings.TrimSpace(a.gitWorkbenchExecutable) != "" {
		return strings.TrimSpace(a.gitWorkbenchExecutable)
	}
	return "git"
}

func (a *App) runCollectionGit(ctx context.Context, args ...string) (string, bool, error) {
	if _, err := exec.LookPath(a.gitWorkbenchExecutableName()); err != nil {
		return "", false, errors.New("git is not installed or not on PATH")
	}
	cmd := exec.CommandContext(ctx, a.gitWorkbenchExecutableName(), args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output := &limitedGitBuffer{limit: gitWorkbenchOutputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return output.String(), output.truncated, errors.New("git operation timed out")
	}
	return output.String(), output.truncated, err
}

func (a *App) runCollectionGitBounded(args ...string) (string, bool, error) {
	return a.runCollectionGitWithTimeout(gitWorkbenchTimeout, args...)
}

func (a *App) runCollectionGitNetworkBounded(args ...string) (string, bool, error) {
	return a.runCollectionGitWithTimeout(gitWorkbenchNetworkTimeout, args...)
}

func (a *App) runCollectionGitWithTimeout(timeout time.Duration, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return a.runCollectionGit(ctx, args...)
}

func (a *App) collectionGitPath(collectionID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return "", err
	}
	collection, err := a.findCollectionLocked(collectionID)
	if err != nil {
		return "", err
	}
	if collection.NotFoundLocally || strings.TrimSpace(collection.Path) == "" {
		return "", errors.New("collection is not available locally")
	}
	path, err := filepath.EvalSymlinks(collection.Path)
	if err != nil {
		return "", fmt.Errorf("resolve collection path: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("collection path is not a directory")
	}
	return filepath.Clean(path), nil
}

func (a *App) collectionGitRepository(collectionID string) (collectionGitRepository, error) {
	collectionPath, err := a.collectionGitPath(collectionID)
	if err != nil {
		return collectionGitRepository{}, err
	}
	output, _, err := a.runCollectionGitBounded("-C", collectionPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return collectionGitRepository{}, fmt.Errorf("collection is not initialized as a Git repository")
	}
	repoPath, err := filepath.EvalSymlinks(strings.TrimSpace(output))
	if err != nil {
		return collectionGitRepository{}, fmt.Errorf("resolve Git repository: %w", err)
	}
	repoPath = filepath.Clean(repoPath)
	if !pathInside(repoPath, collectionPath) {
		return collectionGitRepository{}, errors.New("collection is outside its discovered Git repository")
	}
	rel, err := filepath.Rel(repoPath, collectionPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return collectionGitRepository{}, errors.New("collection is outside its discovered Git repository")
	}
	pathspec := filepath.ToSlash(rel)
	if pathspec == "." || pathspec == "" {
		pathspec = "."
	}
	return collectionGitRepository{collectionPath: collectionPath, repositoryPath: repoPath, pathspec: pathspec}, nil
}

func redactCollectionGitText(value string) string {
	value = gitWorkbenchURLUserInfoPattern.ReplaceAllString(value, "$1://<redacted>@")
	value = gitWorkbenchURLQueryPattern.ReplaceAllString(value, "$1://<redacted>")
	value = gitWorkbenchAbsolutePathPattern.ReplaceAllStringFunc(value, func(match string) string {
		prefix := ""
		if len(match) > 0 && (match[0] == ' ' || match[0] == ':' || match[0] == '\'' || match[0] == '"') {
			prefix = match[:1]
		}
		return prefix + "<path>"
	})
	return strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
}

func sanitizeCollectionGitDiagnostic(value string) string {
	value = redactCollectionGitText(value)
	if len(value) > gitWorkbenchDisplayLimit {
		value = value[:gitWorkbenchDisplayLimit] + "…"
	}
	return value
}

func sanitizeCollectionGitDiff(value string) string {
	return redactCollectionGitText(value)
}

func collectionGitError(action, output string, err error) error {
	if err == nil {
		return nil
	}
	safe := sanitizeCollectionGitDiagnostic(output)
	lower := strings.ToLower(safe)
	if strings.Contains(lower, "please tell me who you are") || strings.Contains(lower, "unable to auto-detect email") || strings.Contains(lower, "empty ident name") || strings.Contains(lower, "empty ident email") {
		return errors.New("git author identity is not configured; set user.name and user.email before committing")
	}
	if safe == "" {
		safe = sanitizeCollectionGitDiagnostic(err.Error())
	}
	if safe == "" {
		return fmt.Errorf("git %s failed", action)
	}
	return fmt.Errorf("git %s failed: %s", action, safe)
}

func (a *App) collectionGitPathspec(repo collectionGitRepository, input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" || filepath.IsAbs(input) {
		return "", "", errors.New("git path must be a non-empty collection-relative path")
	}
	cleaned := filepath.Clean(filepath.FromSlash(input))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", errors.New("git path must stay inside the active collection")
	}
	absolute := filepath.Join(repo.collectionPath, cleaned)
	if !pathInside(repo.collectionPath, absolute) {
		return "", "", errors.New("git path must stay inside the active collection")
	}
	if parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute)); parentErr == nil && !pathInside(repo.collectionPath, parent) {
		return "", "", errors.New("git path symlink escapes the active collection")
	}
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil || !pathInside(repo.collectionPath, resolved) {
			return "", "", errors.New("git path symlink escapes the active collection")
		}
	}
	repoRelative, err := filepath.Rel(repo.repositoryPath, absolute)
	if err != nil || !pathInside(repo.repositoryPath, absolute) {
		return "", "", errors.New("git path is outside the active repository")
	}
	return filepath.ToSlash(repoRelative), filepath.ToSlash(cleaned), nil
}

func collectionGitStatusRows(repo collectionGitRepository, output string) []CollectionGitFile {
	rows := []CollectionGitFile{}
	parts := strings.Split(output, "\x00")
	for index := 0; index < len(parts); index++ {
		record := parts[index]
		if len(record) < 4 || strings.HasPrefix(record, "## ") {
			continue
		}
		indexStatus, worktreeStatus := record[0:1], record[1:2]
		path := record[3:]
		if indexStatus == "R" || indexStatus == "C" {
			if index+1 < len(parts) {
				index++
			}
		}
		absolute := filepath.Join(repo.repositoryPath, filepath.FromSlash(path))
		if !pathInside(repo.collectionPath, absolute) {
			continue
		}
		relative, err := filepath.Rel(repo.collectionPath, absolute)
		if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		conflicted := strings.ContainsAny(indexStatus+worktreeStatus, "U") || (indexStatus == "A" && worktreeStatus == "A") || (indexStatus == "D" && worktreeStatus == "D")
		rows = append(rows, CollectionGitFile{Path: filepath.ToSlash(relative), Index: indexStatus, Worktree: worktreeStatus, Staged: indexStatus != " " && indexStatus != "?", Untracked: indexStatus == "?" && worktreeStatus == "?", Conflicted: conflicted})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	return rows
}

func parseCollectionGitNameStatusPaths(output string) ([]string, error) {
	records := strings.Split(output, "\x00")
	paths := []string{}
	for index := 0; index < len(records); {
		if records[index] == "" {
			if index == len(records)-1 {
				break
			}
			return nil, errors.New("malformed staged Git name-status output")
		}
		status := records[index]
		if len(status) == 0 {
			return nil, errors.New("malformed staged Git name-status output")
		}
		switch status[0] {
		case 'R', 'C':
			if len(records) < index+3 || records[index+1] == "" || records[index+2] == "" {
				return nil, errors.New("malformed staged Git rename or copy output")
			}
			paths = append(paths, records[index+1], records[index+2])
			index += 3
		case 'A', 'M', 'D', 'T', 'U':
			if len(records) < index+2 || records[index+1] == "" {
				return nil, errors.New("malformed staged Git name-status output")
			}
			paths = append(paths, records[index+1])
			index += 2
		default:
			return nil, errors.New("unsupported staged Git name-status record")
		}
	}
	return paths, nil
}

func collectionGitPathWithinActiveCollection(repo collectionGitRepository, path string) bool {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return false
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return false
	}
	absolute := filepath.Join(repo.repositoryPath, cleaned)
	return pathInside(repo.repositoryPath, absolute) && pathInside(repo.collectionPath, absolute)
}

func (a *App) collectionGitSnapshot(repo collectionGitRepository) (CollectionGitSnapshot, error) {
	snapshot := CollectionGitSnapshot{Available: true, Initialized: true, Clean: true, Remotes: []CollectionGitRemote{}, Branches: []string{}, Files: []CollectionGitFile{}}
	snapshot.RootLabel = filepath.Base(repo.collectionPath)
	status, _, err := a.runCollectionGitBounded("-C", repo.repositoryPath, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", repo.pathspec)
	if err != nil {
		return CollectionGitSnapshot{}, collectionGitError("status", status, err)
	}
	snapshot.Files = collectionGitStatusRows(repo, status)
	snapshot.Clean = len(snapshot.Files) == 0
	for _, row := range snapshot.Files {
		if row.Conflicted {
			snapshot.Conflicts = true
		}
	}
	branch, _, branchErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branchErr != nil || strings.TrimSpace(branch) == "" {
		snapshot.Detached = true
		snapshot.Branch = "Detached"
	} else {
		snapshot.Branch = strings.TrimSpace(branch)
	}
	if !snapshot.Detached {
		upstream, _, upstreamErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if upstreamErr == nil {
			snapshot.Upstream = strings.TrimSpace(upstream)
			counts, _, countErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
			if countErr == nil {
				fields := strings.Fields(counts)
				if len(fields) == 2 {
					snapshot.Behind, _ = strconv.Atoi(fields[0])
					snapshot.Ahead, _ = strconv.Atoi(fields[1])
				}
			}
		}
	}
	branches, _, branchErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if branchErr == nil {
		for _, name := range strings.Fields(branches) {
			if name != "" {
				snapshot.Branches = append(snapshot.Branches, name)
			}
		}
		sort.Strings(snapshot.Branches)
	}
	remoteNames, _, remoteErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "remote")
	if remoteErr == nil {
		for _, name := range strings.Fields(remoteNames) {
			url, _, urlErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "remote", "get-url", name)
			if urlErr == nil {
				snapshot.Remotes = append(snapshot.Remotes, CollectionGitRemote{Name: name, URL: sanitizeCollectionGitDiagnostic(strings.TrimSpace(url))})
			}
		}
	}
	return snapshot, nil
}

func (a *App) GetCollectionGitStatus(collectionID string) (CollectionGitSnapshot, error) {
	if _, _, err := a.runCollectionGitBounded("--version"); err != nil {
		return CollectionGitSnapshot{Available: false, Clean: true, Remotes: []CollectionGitRemote{}, Branches: []string{}, Files: []CollectionGitFile{}}, nil
	}
	repo, err := a.collectionGitRepository(collectionID)
	if err != nil {
		if strings.Contains(err.Error(), "not initialized") {
			return CollectionGitSnapshot{Available: true, Initialized: false, Clean: true, Remotes: []CollectionGitRemote{}, Branches: []string{}, Files: []CollectionGitFile{}}, nil
		}
		return CollectionGitSnapshot{}, err
	}
	return a.collectionGitSnapshot(repo)
}

func (a *App) gitOperationRepository(collectionID string) (collectionGitRepository, error) {
	if _, _, err := a.runCollectionGitBounded("--version"); err != nil {
		return collectionGitRepository{}, errors.New("git is not installed or not on PATH")
	}
	return a.collectionGitRepository(collectionID)
}

func (a *App) gitOperationResult(repo collectionGitRepository, message string) (CollectionGitOperationResult, error) {
	snapshot, err := a.collectionGitSnapshot(repo)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	return CollectionGitOperationResult{Snapshot: snapshot, Message: message}, nil
}

func (a *App) InitializeCollectionGit(collectionID string) (CollectionGitOperationResult, error) {
	collectionPath, err := a.collectionGitPath(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	if _, _, err := a.runCollectionGitBounded("--version"); err != nil {
		return CollectionGitOperationResult{}, errors.New("git is not installed or not on PATH")
	}
	if _, _, err := a.runCollectionGitBounded("-C", collectionPath, "init"); err != nil {
		return CollectionGitOperationResult{}, collectionGitError("initialize", "", err)
	}
	repo, err := a.collectionGitRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	return a.gitOperationResult(repo, "Initialized Git repository")
}

func (a *App) GetCollectionGitDiff(collectionID, path string, staged bool) (CollectionGitDiff, error) {
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitDiff{}, err
	}
	repoPath, displayPath, err := a.collectionGitPathspec(repo, path)
	if err != nil {
		return CollectionGitDiff{}, err
	}
	numstatArgs := []string{"-C", repo.repositoryPath, "diff", "--numstat", "--no-ext-diff"}
	if staged {
		numstatArgs = append(numstatArgs, "--cached")
	}
	numstatArgs = append(numstatArgs, "--", repoPath)
	numstat, _, numstatErr := a.runCollectionGitBounded(numstatArgs...)
	if numstatErr != nil {
		return CollectionGitDiff{}, collectionGitError("diff", numstat, numstatErr)
	}
	binary := strings.Contains(numstat, "-\t-")
	args := []string{"-C", repo.repositoryPath, "diff", "--no-ext-diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", repoPath)
	output, truncated, runErr := a.runCollectionGitBounded(args...)
	if runErr != nil {
		return CollectionGitDiff{}, collectionGitError("diff", output, runErr)
	}
	return CollectionGitDiff{Path: displayPath, Staged: staged, Text: sanitizeCollectionGitDiff(output), Truncated: truncated, Binary: binary || strings.Contains(output, "Binary files ")}, nil
}

func (a *App) collectionGitMutatePaths(collectionID string, paths []string, stage bool) (CollectionGitOperationResult, error) {
	if len(paths) == 0 {
		return CollectionGitOperationResult{}, errors.New("select at least one collection path")
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	pathspecs := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		pathspec, _, pathErr := a.collectionGitPathspec(repo, path)
		if pathErr != nil {
			return CollectionGitOperationResult{}, pathErr
		}
		if !seen[pathspec] {
			seen[pathspec] = true
			pathspecs = append(pathspecs, pathspec)
		}
	}
	args := []string{"-C", repo.repositoryPath}
	message := "Staged selected collection paths"
	if stage {
		args = append(args, "add", "--")
	} else {
		hasHeadOutput, _, hasHeadErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "rev-parse", "--verify", "HEAD")
		if hasHeadErr == nil && strings.TrimSpace(hasHeadOutput) != "" {
			args = append(args, "restore", "--staged", "--")
		} else {
			// An unborn repository has no HEAD for `restore --staged` to read.
			// Removing only index entries keeps the working tree untouched.
			args = append(args, "rm", "--cached", "-r", "--ignore-unmatch", "--")
		}
		message = "Unstaged selected collection paths"
	}
	args = append(args, pathspecs...)
	output, _, runErr := a.runCollectionGitBounded(args...)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError(strings.ToLower(strings.TrimSuffix(message, " selected collection paths")), output, runErr)
	}
	return a.gitOperationResult(repo, message)
}

func (a *App) StageCollectionGitPaths(collectionID string, paths []string) (CollectionGitOperationResult, error) {
	return a.collectionGitMutatePaths(collectionID, paths, true)
}

func (a *App) UnstageCollectionGitPaths(collectionID string, paths []string) (CollectionGitOperationResult, error) {
	return a.collectionGitMutatePaths(collectionID, paths, false)
}

func (a *App) CommitCollectionGit(collectionID, message string) (CollectionGitOperationResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return CollectionGitOperationResult{}, errors.New("commit message is required")
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	staged, _, stagedErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "diff", "--cached", "--name-status", "-z", "--find-renames", "--find-copies", "--")
	if stagedErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("inspect staged changes", staged, stagedErr)
	}
	paths, parseErr := parseCollectionGitNameStatusPaths(staged)
	if parseErr != nil {
		return CollectionGitOperationResult{}, parseErr
	}
	for _, path := range paths {
		if !collectionGitPathWithinActiveCollection(repo, path) {
			return CollectionGitOperationResult{}, errors.New("commit is blocked because staged changes exist outside the active collection")
		}
	}
	output, _, runErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "commit", "-m", message)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("commit", output, runErr)
	}
	return a.gitOperationResult(repo, "Committed staged collection changes")
}

func validateCollectionGitBranch(branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") {
		return "", errors.New("git branch name is required")
	}
	if strings.ContainsAny(branch, "\r\n\x00") {
		return "", errors.New("git branch name is invalid")
	}
	return branch, nil
}

func validateCollectionGitRemoteName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n\t /\\:@") {
		return "", errors.New("git remote name is invalid")
	}
	return name, nil
}

func normalizeCollectionGitRemoteURL(raw string) (string, error) {
	remote, err := normalizeGitRemoteURL(raw)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(remote, "?#") || strings.Contains(remote, "@") && strings.Contains(remote, "://") {
		return "", errors.New("git remote URL must not embed credentials, queries, or fragments")
	}
	if strings.HasPrefix(remote, "file://") {
		parsed, parseErr := url.Parse(remote)
		if parseErr != nil || (parsed.Host != "" && parsed.Host != "localhost") || parsed.User != nil {
			return "", errors.New("file Git remote URL must be credential-free and local")
		}
	}
	return remote, nil
}

func (a *App) requireCollectionGitRemote(repo collectionGitRepository, remote string) (string, error) {
	remote, err := validateCollectionGitRemoteName(remote)
	if err != nil {
		return "", err
	}
	output, _, runErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "remote", "get-url", remote)
	if runErr != nil {
		return "", collectionGitError("find remote", output, runErr)
	}
	return remote, nil
}

func (a *App) ensureCollectionGitBranch(branch string) (string, error) {
	branch, err := validateCollectionGitBranch(branch)
	if err != nil {
		return "", err
	}
	output, _, runErr := a.runCollectionGitBounded("check-ref-format", "--branch", branch)
	if runErr != nil {
		return "", collectionGitError("validate branch", output, runErr)
	}
	return branch, nil
}

func (a *App) CreateCollectionGitBranch(collectionID, branch string, checkout bool) (CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	snapshot, err := a.collectionGitSnapshot(repo)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	if checkout && (!snapshot.Clean || snapshot.Conflicts) {
		return CollectionGitOperationResult{}, errors.New("switch branches only after the active collection is clean")
	}
	if checkout {
		// switch -c creates the ref and changes HEAD as one Git operation. In
		// particular, do not compose `branch` then `checkout`: a failed checkout
		// would otherwise leave a branch the caller was told was not created.
		output, _, runErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "switch", "-c", branch)
		if runErr != nil {
			return CollectionGitOperationResult{}, collectionGitError("create and switch branch", output, runErr)
		}
	} else {
		output, _, runErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "branch", branch)
		if runErr != nil {
			return CollectionGitOperationResult{}, collectionGitError("create branch", output, runErr)
		}
	}
	return a.gitOperationResult(repo, "Created Git branch "+branch)
}

func (a *App) CheckoutCollectionGitBranch(collectionID, branch string) (CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	snapshot, err := a.collectionGitSnapshot(repo)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	if !snapshot.Clean || snapshot.Conflicts {
		return CollectionGitOperationResult{}, errors.New("switch branches only after the active collection is clean")
	}
	output, _, runErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "checkout", branch)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("switch branch", output, runErr)
	}
	return a.gitOperationResult(repo, "Switched to Git branch "+branch)
}

func (a *App) persistCollectionGitRemote(collectionID, remote string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureReadyLocked(); err != nil {
		return err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		return err
	}
	previousRemote := collection.Remote
	previousCollectionUpdatedAt := collection.UpdatedAt
	previousWorkspaceUpdatedAt := ws.UpdatedAt
	managedIgnoreAdded := false
	if previousRemote == "" && collection.Path != "" {
		if err := updateManagedGitIgnore(ws.Path, collection.Path, true); err != nil {
			return err
		}
		managedIgnoreAdded = true
	}
	collection.Remote = remote
	collection.UpdatedAt = time.Now()
	ws.UpdatedAt = collection.UpdatedAt
	persist := a.persistLocked
	if a.gitWorkbenchPersist != nil {
		persist = a.gitWorkbenchPersist
	}
	if err := persist(); err != nil {
		collection.Remote = previousRemote
		collection.UpdatedAt = previousCollectionUpdatedAt
		ws.UpdatedAt = previousWorkspaceUpdatedAt
		if managedIgnoreAdded {
			if ignoreErr := updateManagedGitIgnore(ws.Path, collection.Path, false); ignoreErr != nil {
				return fmt.Errorf("%w; failed to restore managed Git ignore: %s", collectionGitError("persist remote metadata", err.Error(), err), sanitizeCollectionGitDiagnostic(ignoreErr.Error()))
			}
		}
		return collectionGitError("persist remote metadata", err.Error(), err)
	}
	return nil
}

func (a *App) rollbackCollectionGitRemote(repo collectionGitRepository, name, previousURL string, existed bool) error {
	args := []string{"-C", repo.repositoryPath, "remote"}
	if existed {
		args = append(args, "set-url", name, strings.TrimSpace(previousURL))
	} else {
		args = append(args, "remove", name)
	}
	output, _, err := a.runCollectionGitBounded(args...)
	if err != nil {
		return collectionGitError("rollback remote", output, err)
	}
	return nil
}

func (a *App) SetCollectionGitRemote(collectionID, name, remoteURL string) (CollectionGitOperationResult, error) {
	name, err := validateCollectionGitRemoteName(name)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	remoteURL, err = normalizeCollectionGitRemoteURL(remoteURL)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	remotesOutput, _, remotesErr := a.runCollectionGitBounded("-C", repo.repositoryPath, "remote")
	if remotesErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("list remotes", remotesOutput, remotesErr)
	}
	remoteExists := false
	for _, existing := range strings.Fields(remotesOutput) {
		if existing == name {
			remoteExists = true
			break
		}
	}
	previousURL := ""
	if remoteExists {
		var previousErr error
		previousURL, _, previousErr = a.runCollectionGitBounded("-C", repo.repositoryPath, "remote", "get-url", name)
		if previousErr != nil {
			return CollectionGitOperationResult{}, collectionGitError("read remote", previousURL, previousErr)
		}
	}
	args := []string{"-C", repo.repositoryPath, "remote"}
	if remoteExists {
		args = append(args, "set-url", name, remoteURL)
	} else {
		args = append(args, "add", name, remoteURL)
	}
	output, _, runErr := a.runCollectionGitBounded(args...)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("configure remote", output, runErr)
	}
	if err := a.persistCollectionGitRemote(collectionID, remoteURL); err != nil {
		if rollbackErr := a.rollbackCollectionGitRemote(repo, name, previousURL, remoteExists); rollbackErr != nil {
			return CollectionGitOperationResult{}, fmt.Errorf("%w; Git remote rollback failed and may require manual repair: %v", err, rollbackErr)
		}
		return CollectionGitOperationResult{}, err
	}
	return a.gitOperationResult(repo, "Configured Git remote "+name)
}

func (a *App) FetchCollectionGit(collectionID, remote string) (CollectionGitOperationResult, error) {
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	remote, err = a.requireCollectionGitRemote(repo, remote)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	output, _, runErr := a.runCollectionGitNetworkBounded("-C", repo.repositoryPath, "fetch", remote)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("fetch", output, runErr)
	}
	return a.gitOperationResult(repo, "Fetched Git remote "+remote)
}

func (a *App) PullCollectionGit(collectionID, remote, branch string) (CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	remote, err = a.requireCollectionGitRemote(repo, remote)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	output, _, runErr := a.runCollectionGitNetworkBounded("-C", repo.repositoryPath, "pull", "--ff-only", remote, branch)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("pull", output, runErr)
	}
	return a.gitOperationResult(repo, "Fast-forwarded from Git remote "+remote)
}

func (a *App) PushCollectionGit(collectionID, remote, branch string, setUpstream bool) (CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	remote, err = a.requireCollectionGitRemote(repo, remote)
	if err != nil {
		return CollectionGitOperationResult{}, err
	}
	if !setUpstream {
		snapshot, snapshotErr := a.collectionGitSnapshot(repo)
		if snapshotErr != nil {
			return CollectionGitOperationResult{}, snapshotErr
		}
		if snapshot.Upstream != remote+"/"+branch {
			return CollectionGitOperationResult{}, errors.New("git upstream is not configured; enable explicit set-upstream to push this branch")
		}
	}
	args := []string{"-C", repo.repositoryPath, "push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote, branch)
	output, _, runErr := a.runCollectionGitNetworkBounded(args...)
	if runErr != nil {
		return CollectionGitOperationResult{}, collectionGitError("push", output, runErr)
	}
	return a.gitOperationResult(repo, "Pushed Git branch "+branch)
}
