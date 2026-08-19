package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/gitworkbench"
)

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
	output := gitworkbench.NewLimitedBuffer(gitworkbench.OutputLimit)
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return output.String(), output.Truncated(), errors.New("git operation timed out")
	}
	return output.String(), output.Truncated(), err
}

func (a *App) runCollectionGitBounded(args ...string) (string, bool, error) {
	return a.runCollectionGitWithTimeout(gitworkbench.Timeout, args...)
}

func (a *App) runCollectionGitNetworkBounded(args ...string) (string, bool, error) {
	return a.runCollectionGitWithTimeout(gitworkbench.NetworkTimeout, args...)
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

func (a *App) gitRepositoryFor(collectionID string) (gitworkbench.Repository, error) {
	collectionPath, err := a.collectionGitPath(collectionID)
	if err != nil {
		return gitworkbench.Repository{}, err
	}
	output, _, err := a.runCollectionGitBounded("-C", collectionPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitworkbench.Repository{}, fmt.Errorf("collection is not initialized as a Git repository")
	}
	repoPath, err := filepath.EvalSymlinks(strings.TrimSpace(output))
	if err != nil {
		return gitworkbench.Repository{}, fmt.Errorf("resolve Git repository: %w", err)
	}
	repoPath = filepath.Clean(repoPath)
	if !pathInside(repoPath, collectionPath) {
		return gitworkbench.Repository{}, errors.New("collection is outside its discovered Git repository")
	}
	rel, err := filepath.Rel(repoPath, collectionPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return gitworkbench.Repository{}, errors.New("collection is outside its discovered Git repository")
	}
	pathspec := filepath.ToSlash(rel)
	if pathspec == "." || pathspec == "" {
		pathspec = "."
	}
	return gitworkbench.Repository{CollectionPath: collectionPath, RepositoryPath: repoPath, Pathspec: pathspec}, nil
}

func (a *App) collectionGitPathspec(repo gitworkbench.Repository, input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" || filepath.IsAbs(input) {
		return "", "", errors.New("git path must be a non-empty collection-relative path")
	}
	cleaned := filepath.Clean(filepath.FromSlash(input))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", errors.New("git path must stay inside the active collection")
	}
	absolute := filepath.Join(repo.CollectionPath, cleaned)
	if !pathInside(repo.CollectionPath, absolute) {
		return "", "", errors.New("git path must stay inside the active collection")
	}
	if parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute)); parentErr == nil && !pathInside(repo.CollectionPath, parent) {
		return "", "", errors.New("git path symlink escapes the active collection")
	}
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil || !pathInside(repo.CollectionPath, resolved) {
			return "", "", errors.New("git path symlink escapes the active collection")
		}
	}
	repoRelative, err := filepath.Rel(repo.RepositoryPath, absolute)
	if err != nil || !pathInside(repo.RepositoryPath, absolute) {
		return "", "", errors.New("git path is outside the active repository")
	}
	return filepath.ToSlash(repoRelative), filepath.ToSlash(cleaned), nil
}

func (a *App) collectionGitSnapshot(repo gitworkbench.Repository) (gitworkbench.CollectionGitSnapshot, error) {
	snapshot := gitworkbench.CollectionGitSnapshot{Available: true, Initialized: true, Clean: true, Remotes: []gitworkbench.CollectionGitRemote{}, Branches: []string{}, Files: []gitworkbench.CollectionGitFile{}}
	snapshot.RootLabel = filepath.Base(repo.CollectionPath)
	status, _, err := a.runCollectionGitBounded("-C", repo.RepositoryPath, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", repo.Pathspec)
	if err != nil {
		return gitworkbench.CollectionGitSnapshot{}, gitworkbench.Error("status", status, err)
	}
	snapshot.Files = gitworkbench.StatusRows(repo, status)
	snapshot.Clean = len(snapshot.Files) == 0
	for _, row := range snapshot.Files {
		if row.Conflicted {
			snapshot.Conflicts = true
		}
	}
	branch, _, branchErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branchErr != nil || strings.TrimSpace(branch) == "" {
		snapshot.Detached = true
		snapshot.Branch = "Detached"
	} else {
		snapshot.Branch = strings.TrimSpace(branch)
	}
	if !snapshot.Detached {
		upstream, _, upstreamErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if upstreamErr == nil {
			snapshot.Upstream = strings.TrimSpace(upstream)
			counts, _, countErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
			if countErr == nil {
				fields := strings.Fields(counts)
				if len(fields) == 2 {
					snapshot.Behind, _ = strconv.Atoi(fields[0])
					snapshot.Ahead, _ = strconv.Atoi(fields[1])
				}
			}
		}
	}
	branches, _, branchErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if branchErr == nil {
		for _, name := range strings.Fields(branches) {
			if name != "" {
				snapshot.Branches = append(snapshot.Branches, name)
			}
		}
		sort.Strings(snapshot.Branches)
	}
	remoteNames, _, remoteErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "remote")
	if remoteErr == nil {
		for _, name := range strings.Fields(remoteNames) {
			url, _, urlErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "remote", "get-url", name)
			if urlErr == nil {
				snapshot.Remotes = append(snapshot.Remotes, gitworkbench.CollectionGitRemote{Name: name, URL: gitworkbench.SanitizeDiagnostic(strings.TrimSpace(url))})
			}
		}
	}
	return snapshot, nil
}

func (a *App) GetCollectionGitStatus(collectionID string) (gitworkbench.CollectionGitSnapshot, error) {
	if _, _, err := a.runCollectionGitBounded("--version"); err != nil {
		return gitworkbench.CollectionGitSnapshot{Available: false, Clean: true, Remotes: []gitworkbench.CollectionGitRemote{}, Branches: []string{}, Files: []gitworkbench.CollectionGitFile{}}, nil
	}
	repo, err := a.gitRepositoryFor(collectionID)
	if err != nil {
		if strings.Contains(err.Error(), "not initialized") {
			return gitworkbench.CollectionGitSnapshot{Available: true, Initialized: false, Clean: true, Remotes: []gitworkbench.CollectionGitRemote{}, Branches: []string{}, Files: []gitworkbench.CollectionGitFile{}}, nil
		}
		return gitworkbench.CollectionGitSnapshot{}, err
	}
	return a.collectionGitSnapshot(repo)
}

func (a *App) gitOperationRepository(collectionID string) (gitworkbench.Repository, error) {
	if _, _, err := a.runCollectionGitBounded("--version"); err != nil {
		return gitworkbench.Repository{}, errors.New("git is not installed or not on PATH")
	}
	return a.gitRepositoryFor(collectionID)
}

func (a *App) gitOperationResult(repo gitworkbench.Repository, message string) (gitworkbench.CollectionGitOperationResult, error) {
	snapshot, err := a.collectionGitSnapshot(repo)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	return gitworkbench.CollectionGitOperationResult{Snapshot: snapshot, Message: message}, nil
}

func (a *App) InitializeCollectionGit(collectionID string) (gitworkbench.CollectionGitOperationResult, error) {
	collectionPath, err := a.collectionGitPath(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	if _, _, err := a.runCollectionGitBounded("--version"); err != nil {
		return gitworkbench.CollectionGitOperationResult{}, errors.New("git is not installed or not on PATH")
	}
	if _, _, err := a.runCollectionGitBounded("-C", collectionPath, "init"); err != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("initialize", "", err)
	}
	repo, err := a.gitRepositoryFor(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	return a.gitOperationResult(repo, "Initialized Git repository")
}

func (a *App) GetCollectionGitDiff(collectionID, path string, staged bool) (gitworkbench.CollectionGitDiff, error) {
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitDiff{}, err
	}
	repoPath, displayPath, err := a.collectionGitPathspec(repo, path)
	if err != nil {
		return gitworkbench.CollectionGitDiff{}, err
	}
	numstatArgs := []string{"-C", repo.RepositoryPath, "diff", "--numstat", "--no-ext-diff"}
	if staged {
		numstatArgs = append(numstatArgs, "--cached")
	}
	numstatArgs = append(numstatArgs, "--", repoPath)
	numstat, _, numstatErr := a.runCollectionGitBounded(numstatArgs...)
	if numstatErr != nil {
		return gitworkbench.CollectionGitDiff{}, gitworkbench.Error("diff", numstat, numstatErr)
	}
	binary := strings.Contains(numstat, "-\t-")
	args := []string{"-C", repo.RepositoryPath, "diff", "--no-ext-diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", repoPath)
	output, truncated, runErr := a.runCollectionGitBounded(args...)
	if runErr != nil {
		return gitworkbench.CollectionGitDiff{}, gitworkbench.Error("diff", output, runErr)
	}
	return gitworkbench.CollectionGitDiff{Path: displayPath, Staged: staged, Text: gitworkbench.SanitizeDiff(output), Truncated: truncated, Binary: binary || strings.Contains(output, "Binary files ")}, nil
}

func (a *App) collectionGitMutatePaths(collectionID string, paths []string, stage bool) (gitworkbench.CollectionGitOperationResult, error) {
	if len(paths) == 0 {
		return gitworkbench.CollectionGitOperationResult{}, errors.New("select at least one collection path")
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	pathspecs := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		pathspec, _, pathErr := a.collectionGitPathspec(repo, path)
		if pathErr != nil {
			return gitworkbench.CollectionGitOperationResult{}, pathErr
		}
		if !seen[pathspec] {
			seen[pathspec] = true
			pathspecs = append(pathspecs, pathspec)
		}
	}
	args := []string{"-C", repo.RepositoryPath}
	message := "Staged selected collection paths"
	if stage {
		args = append(args, "add", "--")
	} else {
		hasHeadOutput, _, hasHeadErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "rev-parse", "--verify", "HEAD")
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
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error(strings.ToLower(strings.TrimSuffix(message, " selected collection paths")), output, runErr)
	}
	return a.gitOperationResult(repo, message)
}

func (a *App) StageCollectionGitPaths(collectionID string, paths []string) (gitworkbench.CollectionGitOperationResult, error) {
	return a.collectionGitMutatePaths(collectionID, paths, true)
}

func (a *App) UnstageCollectionGitPaths(collectionID string, paths []string) (gitworkbench.CollectionGitOperationResult, error) {
	return a.collectionGitMutatePaths(collectionID, paths, false)
}

func (a *App) CommitCollectionGit(collectionID, message string) (gitworkbench.CollectionGitOperationResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return gitworkbench.CollectionGitOperationResult{}, errors.New("commit message is required")
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	staged, _, stagedErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "diff", "--cached", "--name-status", "-z", "--find-renames", "--find-copies", "--")
	if stagedErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("inspect staged changes", staged, stagedErr)
	}
	paths, parseErr := gitworkbench.ParseNameStatusPaths(staged)
	if parseErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, parseErr
	}
	for _, path := range paths {
		if !gitworkbench.PathWithinActiveCollection(repo, path) {
			return gitworkbench.CollectionGitOperationResult{}, errors.New("commit is blocked because staged changes exist outside the active collection")
		}
	}
	output, _, runErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "commit", "-m", message)
	if runErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("commit", output, runErr)
	}
	return a.gitOperationResult(repo, "Committed staged collection changes")
}

func (a *App) requireCollectionGitRemote(repo gitworkbench.Repository, remote string) (string, error) {
	remote, err := gitworkbench.ValidateRemoteName(remote)
	if err != nil {
		return "", err
	}
	output, _, runErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "remote", "get-url", remote)
	if runErr != nil {
		return "", gitworkbench.Error("find remote", output, runErr)
	}
	return remote, nil
}

func (a *App) ensureCollectionGitBranch(branch string) (string, error) {
	branch, err := gitworkbench.ValidateBranch(branch)
	if err != nil {
		return "", err
	}
	output, _, runErr := a.runCollectionGitBounded("check-ref-format", "--branch", branch)
	if runErr != nil {
		return "", gitworkbench.Error("validate branch", output, runErr)
	}
	return branch, nil
}

func (a *App) CreateCollectionGitBranch(collectionID, branch string, checkout bool) (gitworkbench.CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	snapshot, err := a.collectionGitSnapshot(repo)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	if checkout && (!snapshot.Clean || snapshot.Conflicts) {
		return gitworkbench.CollectionGitOperationResult{}, errors.New("switch branches only after the active collection is clean")
	}
	if checkout {
		// switch -c creates the ref and changes HEAD as one Git operation. In
		// particular, do not compose `branch` then `checkout`: a failed checkout
		// would otherwise leave a branch the caller was told was not created.
		output, _, runErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "switch", "-c", branch)
		if runErr != nil {
			return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("create and switch branch", output, runErr)
		}
	} else {
		output, _, runErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "branch", branch)
		if runErr != nil {
			return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("create branch", output, runErr)
		}
	}
	return a.gitOperationResult(repo, "Created Git branch "+branch)
}

func (a *App) CheckoutCollectionGitBranch(collectionID, branch string) (gitworkbench.CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	snapshot, err := a.collectionGitSnapshot(repo)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	if !snapshot.Clean || snapshot.Conflicts {
		return gitworkbench.CollectionGitOperationResult{}, errors.New("switch branches only after the active collection is clean")
	}
	output, _, runErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "checkout", branch)
	if runErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("switch branch", output, runErr)
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
				return fmt.Errorf("%w; failed to restore managed Git ignore: %s", gitworkbench.Error("persist remote metadata", err.Error(), err), gitworkbench.SanitizeDiagnostic(ignoreErr.Error()))
			}
		}
		return gitworkbench.Error("persist remote metadata", err.Error(), err)
	}
	return nil
}

func (a *App) rollbackCollectionGitRemote(repo gitworkbench.Repository, name, previousURL string, existed bool) error {
	args := []string{"-C", repo.RepositoryPath, "remote"}
	if existed {
		args = append(args, "set-url", name, strings.TrimSpace(previousURL))
	} else {
		args = append(args, "remove", name)
	}
	output, _, err := a.runCollectionGitBounded(args...)
	if err != nil {
		return gitworkbench.Error("rollback remote", output, err)
	}
	return nil
}

func (a *App) SetCollectionGitRemote(collectionID, name, remoteURL string) (gitworkbench.CollectionGitOperationResult, error) {
	name, err := gitworkbench.ValidateRemoteName(name)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	remoteURL, err = gitworkbench.NormalizeCollectionRemoteURL(remoteURL)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	remotesOutput, _, remotesErr := a.runCollectionGitBounded("-C", repo.RepositoryPath, "remote")
	if remotesErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("list remotes", remotesOutput, remotesErr)
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
		previousURL, _, previousErr = a.runCollectionGitBounded("-C", repo.RepositoryPath, "remote", "get-url", name)
		if previousErr != nil {
			return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("read remote", previousURL, previousErr)
		}
	}
	args := []string{"-C", repo.RepositoryPath, "remote"}
	if remoteExists {
		args = append(args, "set-url", name, remoteURL)
	} else {
		args = append(args, "add", name, remoteURL)
	}
	output, _, runErr := a.runCollectionGitBounded(args...)
	if runErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("configure remote", output, runErr)
	}
	if err := a.persistCollectionGitRemote(collectionID, remoteURL); err != nil {
		if rollbackErr := a.rollbackCollectionGitRemote(repo, name, previousURL, remoteExists); rollbackErr != nil {
			return gitworkbench.CollectionGitOperationResult{}, fmt.Errorf("%w; Git remote rollback failed and may require manual repair: %v", err, rollbackErr)
		}
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	return a.gitOperationResult(repo, "Configured Git remote "+name)
}

func (a *App) FetchCollectionGit(collectionID, remote string) (gitworkbench.CollectionGitOperationResult, error) {
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	remote, err = a.requireCollectionGitRemote(repo, remote)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	output, _, runErr := a.runCollectionGitNetworkBounded("-C", repo.RepositoryPath, "fetch", remote)
	if runErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("fetch", output, runErr)
	}
	return a.gitOperationResult(repo, "Fetched Git remote "+remote)
}

func (a *App) PullCollectionGit(collectionID, remote, branch string) (gitworkbench.CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	remote, err = a.requireCollectionGitRemote(repo, remote)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	output, _, runErr := a.runCollectionGitNetworkBounded("-C", repo.RepositoryPath, "pull", "--ff-only", remote, branch)
	if runErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("pull", output, runErr)
	}
	return a.gitOperationResult(repo, "Fast-forwarded from Git remote "+remote)
}

func (a *App) PushCollectionGit(collectionID, remote, branch string, setUpstream bool) (gitworkbench.CollectionGitOperationResult, error) {
	branch, err := a.ensureCollectionGitBranch(branch)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	repo, err := a.gitOperationRepository(collectionID)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	remote, err = a.requireCollectionGitRemote(repo, remote)
	if err != nil {
		return gitworkbench.CollectionGitOperationResult{}, err
	}
	if !setUpstream {
		snapshot, snapshotErr := a.collectionGitSnapshot(repo)
		if snapshotErr != nil {
			return gitworkbench.CollectionGitOperationResult{}, snapshotErr
		}
		if snapshot.Upstream != remote+"/"+branch {
			return gitworkbench.CollectionGitOperationResult{}, errors.New("git upstream is not configured; enable explicit set-upstream to push this branch")
		}
	}
	args := []string{"-C", repo.RepositoryPath, "push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote, branch)
	output, _, runErr := a.runCollectionGitNetworkBounded(args...)
	if runErr != nil {
		return gitworkbench.CollectionGitOperationResult{}, gitworkbench.Error("push", output, runErr)
	}
	return a.gitOperationResult(repo, "Pushed Git branch "+branch)
}
