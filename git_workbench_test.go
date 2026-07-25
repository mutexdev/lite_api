package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitSnapshotFile(snapshot CollectionGitSnapshot, path string) (CollectionGitFile, bool) {
	for _, row := range snapshot.Files {
		if row.Path == path {
			return row, true
		}
	}
	return CollectionGitFile{}, false
}

func gitFixtureOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(output)
}

func gitFixtureCollectionMetadata(t *testing.T, app *App, collectionID string) (string, time.Time, time.Time, string) {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	workspace, collection, err := app.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		t.Fatal(err)
	}
	return collection.Remote, collection.UpdatedAt, workspace.UpdatedAt, workspace.Path
}

func gitFixtureFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

func gitFailingNetworkExecutable(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "git-failing-network")
	content := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    fetch|push) echo 'fatal: https://user:token@example.test/repo.git?token=topsecret at /private/secret/repository' >&2; exit 19 ;;\n  esac\ndone\nexec %q \"$@\"\n", gitPath)
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func isolatedGitExecutable(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	script := filepath.Join(t.TempDir(), "git-without-author")
	content := fmt.Sprintf("#!/bin/sh\nexport HOME=%q\nexport XDG_CONFIG_HOME=%q\nexport GIT_CONFIG_NOSYSTEM=1\nexport GIT_CONFIG_GLOBAL=/dev/null\nexec %q -c user.name= -c user.email= \"$@\"\n", home, filepath.Join(home, "config"), gitPath)
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestInitializeCollectionGitCreatesOnlyDotGit(t *testing.T) {
	if _, err := gitVersion(); err != nil {
		t.Skip(err)
	}
	app := NewAppWithDir(t.TempDir())
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.CreateCollection(state.Workspaces[0].ID, "Initialize Me", "bru")
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	beforeEntries, err := os.ReadDir(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]bool{}
	for _, entry := range beforeEntries {
		before[entry.Name()] = true
	}
	status, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil || status.Initialized || !status.Available {
		t.Fatalf("expected available uninitialized status: %#v, %v", status, err)
	}
	if _, err := app.InitializeCollectionGit(collection.ID); err != nil {
		t.Fatal(err)
	}
	afterEntries, err := os.ReadDir(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range afterEntries {
		if !before[entry.Name()] && entry.Name() != ".git" {
			t.Fatalf("Git initialization created an unexpected path: %q", entry.Name())
		}
	}
	if info, err := os.Stat(filepath.Join(collection.Path, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("Git initialization did not create .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(collection.Path, "unborn.bru"), []byte("meta { name: Unborn }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"unborn.bru"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UnstageCollectionGitPaths(collection.ID, []string{"unborn.bru"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collection.Path, "unborn.bru")); err != nil {
		t.Fatalf("unborn unstage removed the working-tree file: %v", err)
	}
	status, err = app.GetCollectionGitStatus(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, found := gitSnapshotFile(status, "unborn.bru")
	if !found || !row.Untracked || row.Staged {
		t.Fatalf("unborn unstage did not leave an untracked, unstaged file: %#v", status.Files)
	}
}

func gitWorkbenchFixture(t *testing.T) (*App, Collection, string) {
	t.Helper()
	if _, err := gitVersion(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	api := filepath.Join(seed, "Api With Space")
	other := filepath.Join(seed, "Other")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(api, "bruno.json"), []byte(`{"version":"1","name":"Git Workbench API","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(api, "ping.bru"), []byte("meta {\n  name: Ping\n  type: http\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "outside.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "init")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.name=LiteAPI Test", "-c", "user.email=liteapi@example.test", "commit", "-m", "seed")

	bare := filepath.Join(root, "remote.git")
	runGit(t, root, "init", "--bare", bare)
	runGit(t, seed, "remote", "add", "origin", "file://"+bare)
	runGit(t, seed, "push", "-u", "origin", "HEAD")

	app := NewAppWithDir(filepath.Join(root, "app-data"))
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := app.CloneGitRepository("file://"+bare, filepath.Join(root, "clones"), "workbench")
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.Candidates) != 1 {
		t.Fatalf("expected one collection candidate, got %#v", clone.Candidates)
	}
	state, err = app.OpenGitCollections(state.Workspaces[0].ID, []string{clone.Candidates[0].Path}, "file://"+bare)
	if err != nil {
		t.Fatal(err)
	}
	collection := state.Workspaces[0].Collections[len(state.Workspaces[0].Collections)-1]
	return app, collection, clone.TargetPath
}

func TestCollectionGitWorkbenchScopedWorkflow(t *testing.T) {
	app, collection, clonePath := gitWorkbenchFixture(t)

	status, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || !status.Initialized || !status.Clean || status.RootLabel == "" {
		t.Fatalf("unexpected clean Git snapshot: %#v", status)
	}

	collectionPath := filepath.Join(clonePath, "Api With Space")
	if err := os.WriteFile(filepath.Join(collectionPath, "ping.bru"), []byte("meta {\n  name: Changed\n  type: http\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "space ü.bru"), []byte("meta { name: Unicode }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clonePath, "Other", "outside.txt"), []byte("must stay outside collection scope\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err = app.GetCollectionGitStatus(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(status.Files); got != 2 || status.Files[0].Path != "ping.bru" || status.Files[1].Path != "space ü.bru" {
		t.Fatalf("status must include only active collection files: %#v", status.Files)
	}
	diff, err := app.GetCollectionGitDiff(collection.ID, "ping.bru", false)
	if err != nil || !strings.Contains(diff.Text, "Changed") || diff.Path != "ping.bru" {
		t.Fatalf("unexpected scoped diff: %#v, %v", diff, err)
	}
	if _, err := app.GetCollectionGitDiff(collection.ID, "../Other/outside.txt", false); err == nil {
		t.Fatal("expected sibling traversal to be rejected")
	}
	if _, err := app.GetCollectionGitDiff(collection.ID, filepath.Join(collectionPath, "ping.bru"), false); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"../Other/outside.txt"}); err == nil {
		t.Fatal("expected sibling stage attempt to be rejected")
	}

	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"ping.bru", "space ü.bru"}); err != nil {
		t.Fatal(err)
	}
	status, err = app.GetCollectionGitStatus(collection.ID)
	if err != nil || !status.Files[0].Staged || !status.Files[1].Staged {
		t.Fatalf("stage did not update scoped rows: %#v, %v", status.Files, err)
	}
	if _, err := app.UnstageCollectionGitPaths(collection.ID, []string{"ping.bru"}); err != nil {
		t.Fatal(err)
	}
	status, err = app.GetCollectionGitStatus(collection.ID)
	if err != nil || status.Files[0].Staged {
		t.Fatalf("unstage did not update scoped row: %#v, %v", status.Files, err)
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"ping.bru"}); err != nil {
		t.Fatal(err)
	}
	runGit(t, clonePath, "config", "user.name", "LiteAPI Test")
	runGit(t, clonePath, "config", "user.email", "liteapi@example.test")
	if _, err := app.CommitCollectionGit(collection.ID, "Update scoped collection"); err != nil {
		t.Fatal(err)
	}
	status, err = app.GetCollectionGitStatus(collection.ID)
	if err != nil || !status.Clean {
		t.Fatalf("commit did not clean scoped collection: %#v, %v", status, err)
	}
	if data, err := os.ReadFile(filepath.Join(clonePath, "Other", "outside.txt")); err != nil || !strings.Contains(string(data), "outside collection") {
		t.Fatalf("sibling file was changed by scoped operations: %q, %v", data, err)
	}
}

func TestCollectionGitWorkbenchSafetyAndErrors(t *testing.T) {
	app, collection, clonePath := gitWorkbenchFixture(t)
	if _, err := app.StageCollectionGitPaths(collection.ID, nil); err == nil {
		t.Fatal("expected empty stage list to fail")
	}
	if _, err := app.CommitCollectionGit(collection.ID, " "); err == nil {
		t.Fatal("expected empty commit message to fail")
	}
	if got := sanitizeCollectionGitDiagnostic("fatal: https://user:secret@example.test/repo.git?token=secret /private/tmp/liteapi"); strings.Contains(got, "secret") || strings.Contains(got, "/private/tmp") {
		t.Fatalf("sanitizer leaked sensitive detail: %q", got)
	}

	outside := filepath.Join(filepath.Dir(clonePath), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(clonePath, "Api With Space", "escape-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"escape-link"}); err == nil {
		t.Fatal("expected escaping symlink to be rejected")
	}

	app.gitWorkbenchExecutable = filepath.Join(t.TempDir(), "missing-git")
	status, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil || status.Available {
		t.Fatalf("missing Git must be a safe unavailable snapshot: %#v, %v", status, err)
	}
	if _, err := app.InitializeCollectionGit(collection.ID); err == nil {
		t.Fatal("expected unavailable Git operation to fail")
	}
}

func TestCollectionGitCommitReportsMissingAuthorWithoutCommitting(t *testing.T) {
	app, collection, clonePath := gitWorkbenchFixture(t)
	collectionPath := filepath.Join(clonePath, "Api With Space")
	if err := os.WriteFile(filepath.Join(collectionPath, "ping.bru"), []byte("missing author\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"ping.bru"}); err != nil {
		t.Fatal(err)
	}
	before := exec.Command("git", "rev-parse", "HEAD")
	before.Dir = clonePath
	beforeHead, err := before.Output()
	if err != nil {
		t.Fatal(err)
	}
	app.gitWorkbenchExecutable = isolatedGitExecutable(t)
	if _, err := app.CommitCollectionGit(collection.ID, "Should not commit"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "author identity") || strings.Contains(err.Error(), clonePath) {
		t.Fatalf("missing author error was not actionable and sanitized: %v", err)
	}
	after := exec.Command("git", "rev-parse", "HEAD")
	after.Dir = clonePath
	afterHead, err := after.Output()
	if err != nil || string(afterHead) != string(beforeHead) {
		t.Fatalf("missing-author commit changed HEAD: %q, %v", afterHead, err)
	}
	status, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, found := gitSnapshotFile(status, "ping.bru")
	if !found || !row.Staged {
		t.Fatalf("missing-author commit changed the staged index: %#v", status.Files)
	}
}

func TestCollectionGitStatusParserSupportsRenameAndConflicts(t *testing.T) {
	repo := collectionGitRepository{repositoryPath: "/repo", collectionPath: "/repo/Api"}
	rows := collectionGitStatusRows(repo, "RM Api/renamed ü.bru\x00Api/old ü.bru\x00UU Api/conflict.bru\x00?? Other/ignored.bru\x00")
	if len(rows) != 2 || rows[0].Path != "conflict.bru" || !rows[0].Conflicted || rows[1].Path != "renamed ü.bru" || !rows[1].Staged {
		t.Fatalf("unexpected porcelain rows: %#v", rows)
	}
}

func TestCollectionGitCommitRejectsCrossCollectionRenames(t *testing.T) {
	for _, direction := range []struct {
		name string
		from string
		to   string
	}{
		{name: "outside to inside", from: filepath.Join("Other", "outside.txt"), to: filepath.Join("Api With Space", "moved.txt")},
		{name: "inside to outside", from: filepath.Join("Api With Space", "ping.bru"), to: filepath.Join("Other", "moved.bru")},
	} {
		t.Run(direction.name, func(t *testing.T) {
			app, collection, clonePath := gitWorkbenchFixture(t)
			if err := os.Rename(filepath.Join(clonePath, direction.from), filepath.Join(clonePath, direction.to)); err != nil {
				t.Fatal(err)
			}
			// Fixture setup creates a staged cross-collection rename; product code
			// must inspect it but must never commit or otherwise alter it.
			runGit(t, clonePath, "add", "-A")
			beforeHead := gitFixtureOutput(t, clonePath, "rev-parse", "HEAD")
			beforeIndex := gitFixtureOutput(t, clonePath, "diff", "--cached", "--name-status", "-z", "--find-renames", "--")
			if _, err := app.CommitCollectionGit(collection.ID, "Must be blocked"); err == nil || !strings.Contains(err.Error(), "outside the active collection") {
				t.Fatalf("cross-collection rename was not rejected: %v", err)
			}
			afterHead := gitFixtureOutput(t, clonePath, "rev-parse", "HEAD")
			afterIndex := gitFixtureOutput(t, clonePath, "diff", "--cached", "--name-status", "-z", "--find-renames", "--")
			if afterHead != beforeHead || afterIndex != beforeIndex {
				t.Fatal("rejected commit mutated HEAD or the staged index")
			}
			if _, err := os.Stat(filepath.Join(clonePath, direction.to)); err != nil {
				t.Fatalf("rejected commit altered renamed working-tree content: %v", err)
			}
			if !strings.Contains(afterIndex, "R") && !strings.Contains(afterIndex, "D") {
				t.Fatalf("sibling deletion is no longer staged after rejected commit: %q", afterIndex)
			}
		})
	}
}

func TestCollectionGitNameStatusParserHandlesRenameCopyAndMalformedRecords(t *testing.T) {
	paths, err := parseCollectionGitNameStatusPaths("R100\x00Other/outside.txt\x00Api/moved.txt\x00C075\x00Api/source.txt\x00Api/copy.txt\x00M\x00Api/changed.txt\x00")
	if err != nil || strings.Join(paths, "|") != "Other/outside.txt|Api/moved.txt|Api/source.txt|Api/copy.txt|Api/changed.txt" {
		t.Fatalf("unexpected rename/copy parse: %#v, %v", paths, err)
	}
	for _, malformed := range []string{"R100\x00Api/only-one\x00", "C\x00\x00Api/target\x00", "Q\x00Api/file\x00", "M\x00"} {
		if _, err := parseCollectionGitNameStatusPaths(malformed); err == nil {
			t.Fatalf("malformed name-status record was accepted: %q", malformed)
		}
	}
}

func TestCollectionGitG2BranchRemoteAndUpstreamGuards(t *testing.T) {
	app, collection, clonePath := gitWorkbenchFixture(t)
	root := filepath.Dir(filepath.Dir(clonePath))
	before, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateCollectionGitBranch(collection.ID, "g2-created", false); err != nil {
		t.Fatal(err)
	}
	afterCreate, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil || afterCreate.Branch != before.Branch {
		t.Fatalf("create without checkout changed branch: %#v, %v", afterCreate, err)
	}
	if err := os.WriteFile(filepath.Join(clonePath, "Other", "unrelated.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CheckoutCollectionGitBranch(collection.ID, "g2-created"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clonePath, "Other", "unrelated.txt")); err != nil {
		t.Fatalf("branch checkout changed an unrelated sibling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clonePath, "Api With Space", "ping.bru"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CheckoutCollectionGitBranch(collection.ID, before.Branch); err == nil {
		t.Fatal("expected dirty scoped branch switch refusal")
	}
	runGit(t, clonePath, "restore", "Api With Space/ping.bru")
	remote := strings.TrimSpace(gitFixtureOutput(t, clonePath, "remote", "get-url", "origin"))
	if _, err := app.SetCollectionGitRemote(collection.ID, "origin", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := app.FetchCollectionGit(collection.ID, "missing"); err == nil {
		t.Fatal("expected missing remote rejection")
	}
	if _, err := app.PushCollectionGit(collection.ID, "origin", "g2-created", false); err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("expected missing upstream guard, got %v", err)
	}
	if _, err := app.PushCollectionGit(collection.ID, "origin", "g2-created", true); err != nil {
		t.Fatal(err)
	}

	peer := filepath.Join(root, "peer-g2")
	runGit(t, root, "clone", remote, peer)
	runGit(t, peer, "checkout", "g2-created")
	if err := os.WriteFile(filepath.Join(peer, "Api With Space", "from-peer.bru"), []byte("meta {\n  name: Peer\n  type: http\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, peer, "add", "Api With Space/from-peer.bru")
	runGit(t, peer, "-c", "user.name=LiteAPI Test", "-c", "user.email=liteapi@example.test", "commit", "-m", "peer update")
	runGit(t, peer, "push", "origin", "g2-created")
	if _, err := app.FetchCollectionGit(collection.ID, "origin"); err != nil {
		t.Fatal(err)
	}
	fetched, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil || fetched.Behind != 1 || fetched.Ahead != 0 {
		t.Fatalf("fetch did not report peer update: %#v, %v", fetched, err)
	}
	if _, err := app.PullCollectionGit(collection.ID, "origin", "g2-created"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clonePath, "Api With Space", "from-peer.bru")); err != nil {
		t.Fatalf("fast-forward pull did not materialize peer file: %v", err)
	}

	bare := strings.TrimPrefix(remote, "file://")
	runGit(t, root, "--git-dir", bare, "symbolic-ref", "HEAD", "refs/heads/g2-created")
	second, err := app.CloneGitRepository(remote, filepath.Join(root, "second-clone"), "workbench-copy")
	if err != nil || len(second.Candidates) != 1 {
		t.Fatalf("product clone did not discover the collection: %#v, %v", second, err)
	}
	state, err := app.GetState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = app.OpenGitCollections(state.Workspaces[0].ID, []string{second.Candidates[0].Path}, remote)
	if err != nil {
		t.Fatal(err)
	}
	var secondCollection *Collection
	for i := range state.Workspaces[0].Collections {
		candidate := &state.Workspaces[0].Collections[i]
		if filepath.Clean(candidate.Path) == filepath.Clean(second.Candidates[0].Path) {
			secondCollection = candidate
			break
		}
	}
	if secondCollection == nil || filepath.Clean(secondCollection.Path) == filepath.Clean(collection.Path) {
		t.Fatalf("product open did not retain a distinct second collection: %#v", state.Workspaces[0].Collections)
	}
	foundPeer := false
	for _, item := range secondCollection.Items {
		if item.Name == "Peer" {
			foundPeer = true
			break
		}
	}
	if !foundPeer {
		t.Fatalf("product open did not hydrate the peer-pulled request: %#v", secondCollection.Items)
	}

	runGit(t, clonePath, "config", "user.name", "LiteAPI Test")
	runGit(t, clonePath, "config", "user.email", "liteapi@example.test")
	if err := os.WriteFile(filepath.Join(clonePath, "Api With Space", "local-diverge.bru"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clonePath, "Api With Space", "ping.bru"), []byte("meta { name: Local divergence }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"local-diverge.bru", "ping.bru"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CommitCollectionGit(collection.ID, "local divergence"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peer, "Api With Space", "peer-diverge.bru"), []byte("peer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peer, "Api With Space", "ping.bru"), []byte("meta { name: Peer divergence }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, peer, "add", "Api With Space/peer-diverge.bru", "Api With Space/ping.bru")
	runGit(t, peer, "commit", "-m", "peer divergence")
	runGit(t, peer, "push", "origin", "g2-created")
	if _, err := app.PullCollectionGit(collection.ID, "origin", "g2-created"); err == nil {
		t.Fatal("expected diverged fast-forward-only pull refusal")
	}
	if _, err := os.Stat(filepath.Join(clonePath, "Api With Space", "local-diverge.bru")); err != nil {
		t.Fatalf("refused pull changed local work: %v", err)
	}
	if _, err := app.FetchCollectionGit(collection.ID, "origin"); err != nil {
		t.Fatal(err)
	}
	merge := exec.Command("git", "merge", "origin/g2-created")
	merge.Dir = clonePath
	if output, mergeErr := merge.CombinedOutput(); mergeErr == nil || !strings.Contains(string(output), "CONFLICT") {
		t.Fatalf("expected fixture merge conflict, got %v: %s", mergeErr, output)
	}
	conflicted, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil || !conflicted.Conflicts {
		t.Fatalf("actual merge conflict was not reported: %#v, %v", conflicted, err)
	}
	if _, err := app.CheckoutCollectionGitBranch(collection.ID, before.Branch); err == nil {
		t.Fatal("branch checkout resolved or ignored a collection conflict")
	}
	if _, err := app.SetCollectionGitRemote(collection.ID, "origin", "https://user:token@example.test/repo.git"); err == nil {
		t.Fatal("expected credential remote rejection")
	}
	if _, err := app.SetCollectionGitRemote(collection.ID, "origin", "https://example.test/repo.git?token=secret"); err == nil {
		t.Fatal("expected query remote rejection")
	}
	if _, err := app.SetCollectionGitRemote(collection.ID, "origin", "https://example.test/repo.git#secret"); err == nil {
		t.Fatal("expected fragment remote rejection")
	}
	if _, err := app.SetCollectionGitRemote(collection.ID, "origin", "file://remote-host/tmp/repo.git"); err == nil {
		t.Fatal("expected non-local file remote rejection")
	}
	if _, err := app.CreateCollectionGitBranch(collection.ID, "--upload-pack=evil", false); err == nil {
		t.Fatal("expected option-like branch rejection")
	}
}

func TestCollectionGitG2ReportsUnavailableGit(t *testing.T) {
	app, collection, _ := gitWorkbenchFixture(t)
	app.gitWorkbenchExecutable = filepath.Join(t.TempDir(), "missing-git")
	if _, err := app.FetchCollectionGit(collection.ID, "origin"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected unavailable Git diagnostic, got %v", err)
	}
}

func TestCollectionGitG2CreateCheckoutDoesNotLeavePartialBranch(t *testing.T) {
	app, collection, clonePath := gitWorkbenchFixture(t)
	before, err := app.GetCollectionGitStatus(collection.ID)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, clonePath, "checkout", "-b", "sibling-change")
	if err := os.WriteFile(filepath.Join(clonePath, "Other", "outside.txt"), []byte("branch version\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, clonePath, "add", "Other/outside.txt")
	runGit(t, clonePath, "-c", "user.name=LiteAPI Test", "-c", "user.email=liteapi@example.test", "commit", "-m", "change sibling")
	runGit(t, clonePath, "checkout", before.Branch)
	if err := os.WriteFile(filepath.Join(clonePath, "Other", "outside.txt"), []byte("dirty sibling\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	headBefore := strings.TrimSpace(gitFixtureOutput(t, clonePath, "rev-parse", "HEAD"))
	if _, err := app.CheckoutCollectionGitBranch(collection.ID, "sibling-change"); err == nil {
		t.Fatal("expected sibling dirty conflict to refuse branch switch")
	}
	if headAfter := strings.TrimSpace(gitFixtureOutput(t, clonePath, "rev-parse", "HEAD")); headAfter != headBefore {
		t.Fatalf("failed branch switch changed HEAD: got %s want %s", headAfter, headBefore)
	}
	if contents, err := os.ReadFile(filepath.Join(clonePath, "Other", "outside.txt")); err != nil || string(contents) != "dirty sibling\n" {
		t.Fatalf("failed branch switch changed sibling worktree: %q, %v", contents, err)
	}

	branchesBefore := gitFixtureOutput(t, clonePath, "branch", "--format=%(refname:short)")
	lockPath := filepath.Join(clonePath, ".git", "refs", "heads", "atomic-create.lock")
	if err := os.WriteFile(lockPath, []byte("lock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateCollectionGitBranch(collection.ID, "atomic-create", true); err == nil {
		t.Fatal("expected Git to reject locked atomic branch creation")
	}
	if branchesAfter := gitFixtureOutput(t, clonePath, "branch", "--format=%(refname:short)"); branchesAfter != branchesBefore {
		t.Fatalf("failed create-and-checkout left an unreported branch: before=%q after=%q", branchesBefore, branchesAfter)
	}
	if headAfter := strings.TrimSpace(gitFixtureOutput(t, clonePath, "rev-parse", "HEAD")); headAfter != headBefore {
		t.Fatalf("failed create-and-checkout changed HEAD: got %s want %s", headAfter, headBefore)
	}
	if contents, err := os.ReadFile(filepath.Join(clonePath, "Other", "outside.txt")); err != nil || string(contents) != "dirty sibling\n" {
		t.Fatalf("failed create-and-checkout changed sibling worktree: %q, %v", contents, err)
	}
}

func TestCollectionGitG2RemotePersistenceRollback(t *testing.T) {
	t.Run("restores existing remote", func(t *testing.T) {
		app, collection, clonePath := gitWorkbenchFixture(t)
		beforeURL := strings.TrimSpace(gitFixtureOutput(t, clonePath, "remote", "get-url", "origin"))
		beforeRemote, beforeCollectionUpdated, beforeWorkspaceUpdated, _ := gitFixtureCollectionMetadata(t, app, collection.ID)
		app.gitWorkbenchPersist = func() error { return errors.New("injected persistence failure") }
		_, err := app.SetCollectionGitRemote(collection.ID, "origin", "file:///tmp/alternate.git")
		app.gitWorkbenchPersist = nil
		if err == nil || !strings.Contains(err.Error(), "persist remote metadata") {
			t.Fatalf("expected persistence failure, got %v", err)
		}
		if got := strings.TrimSpace(gitFixtureOutput(t, clonePath, "remote", "get-url", "origin")); got != beforeURL {
			t.Fatalf("existing Git remote was not restored: got %q want %q", got, beforeURL)
		}
		afterRemote, afterCollectionUpdated, afterWorkspaceUpdated, _ := gitFixtureCollectionMetadata(t, app, collection.ID)
		if afterRemote != beforeRemote || !afterCollectionUpdated.Equal(beforeCollectionUpdated) || !afterWorkspaceUpdated.Equal(beforeWorkspaceUpdated) {
			t.Fatalf("persistence failure left collection metadata changed: remote=%q collection=%s workspace=%s", afterRemote, afterCollectionUpdated, afterWorkspaceUpdated)
		}
	})

	t.Run("removes newly added remote and managed ignore", func(t *testing.T) {
		app, collection, clonePath := gitWorkbenchFixture(t)
		app.mu.Lock()
		workspace, mutableCollection, err := app.findCollectionWithWorkspaceLocked(collection.ID)
		if err != nil {
			app.mu.Unlock()
			t.Fatal(err)
		}
		workspace.Path = clonePath
		mutableCollection.Remote = ""
		collectionPath := mutableCollection.Path
		app.mu.Unlock()
		if err := updateManagedGitIgnore(clonePath, collectionPath, false); err != nil {
			t.Fatal(err)
		}
		ignorePath := filepath.Join(clonePath, ".gitignore")
		beforeIgnore, beforeIgnoreExists := gitFixtureFile(t, ignorePath)
		beforeRemote, beforeCollectionUpdated, beforeWorkspaceUpdated, _ := gitFixtureCollectionMetadata(t, app, collection.ID)
		app.gitWorkbenchPersist = func() error { return errors.New("injected persistence failure") }
		_, err = app.SetCollectionGitRemote(collection.ID, "backup", "file:///tmp/alternate.git")
		app.gitWorkbenchPersist = nil
		if err == nil || !strings.Contains(err.Error(), "persist remote metadata") {
			t.Fatalf("expected persistence failure, got %v", err)
		}
		if output, checkErr := exec.Command("git", "-C", clonePath, "remote", "get-url", "backup").CombinedOutput(); checkErr == nil {
			t.Fatalf("new Git remote survived failed persistence: %s", output)
		}
		afterRemote, afterCollectionUpdated, afterWorkspaceUpdated, _ := gitFixtureCollectionMetadata(t, app, collection.ID)
		if afterRemote != beforeRemote || !afterCollectionUpdated.Equal(beforeCollectionUpdated) || !afterWorkspaceUpdated.Equal(beforeWorkspaceUpdated) {
			t.Fatalf("new remote persistence failure left metadata changed: remote=%q collection=%s workspace=%s", afterRemote, afterCollectionUpdated, afterWorkspaceUpdated)
		}
		afterIgnore, afterIgnoreExists := gitFixtureFile(t, ignorePath)
		if afterIgnoreExists != beforeIgnoreExists || afterIgnore != beforeIgnore {
			t.Fatalf("managed ignore was not restored: before=%q/%t after=%q/%t", beforeIgnore, beforeIgnoreExists, afterIgnore, afterIgnoreExists)
		}
	})
}

func TestCollectionGitG2RedactsNetworkDiagnostics(t *testing.T) {
	app, collection, _ := gitWorkbenchFixture(t)
	app.gitWorkbenchExecutable = gitFailingNetworkExecutable(t)
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"fetch", func() error { _, err := app.FetchCollectionGit(collection.ID, "origin"); return err }},
		{"push", func() error {
			snapshot, err := app.GetCollectionGitStatus(collection.ID)
			if err != nil {
				return err
			}
			_, err = app.PushCollectionGit(collection.ID, "origin", snapshot.Branch, false)
			return err
		}},
	} {
		err := operation.run()
		if err == nil || len(err.Error()) > len("Git "+operation.name+" failed: ")+gitWorkbenchDisplayLimit+1 || strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "/private/secret") || !strings.Contains(err.Error(), "<path>") {
			t.Fatalf("%s did not return a bounded redacted diagnostic: %v", operation.name, err)
		}
	}
}

func TestCollectionGitDiffIsBoundedAndDetectsBinary(t *testing.T) {
	app, collection, clonePath := gitWorkbenchFixture(t)
	collectionPath := filepath.Join(clonePath, "Api With Space")
	if err := os.WriteFile(filepath.Join(collectionPath, "ping.bru"), []byte(strings.Repeat("changed https://user:secret@example.test/path?token=secret\n", 2_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	large, err := app.GetCollectionGitDiff(collection.ID, "ping.bru", false)
	if err != nil || large.Truncated || len(large.Text) <= gitWorkbenchDisplayLimit || len(large.Text) > gitWorkbenchOutputLimit || strings.Contains(large.Text, "secret") {
		t.Fatalf("large diff was not retained safely: %#v, %v", large, err)
	}
	if err := os.WriteFile(filepath.Join(collectionPath, "binary.dat"), []byte{0, 1, 2, 0, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StageCollectionGitPaths(collection.ID, []string{"binary.dat"}); err != nil {
		t.Fatal(err)
	}
	binary, err := app.GetCollectionGitDiff(collection.ID, "binary.dat", true)
	if err != nil || !binary.Binary {
		t.Fatalf("binary diff was not detected: %#v, %v", binary, err)
	}
	if err := collectionGitError("commit", "Author identity unknown\n\n*** Please tell me who you are.", errors.New("exit status 128")); err == nil || !strings.Contains(err.Error(), "author identity") {
		t.Fatalf("missing author error was not actionable: %v", err)
	}
	buffer := &limitedGitBuffer{limit: gitWorkbenchOutputLimit}
	_, _ = buffer.Write([]byte(strings.Repeat("x", gitWorkbenchOutputLimit+1)))
	if !buffer.truncated || buffer.Len() != gitWorkbenchOutputLimit {
		t.Fatalf("bounded Git output buffer failed: %d bytes, truncated=%v", buffer.Len(), buffer.truncated)
	}
}
