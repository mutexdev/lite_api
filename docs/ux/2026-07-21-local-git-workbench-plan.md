# Local Git Workbench Plan

Status: **Root-approved implementation contract after import slice 2b acceptance**

## Context

LiteAPI already validates Git availability, clones repositories, scans them for collections, opens selected collections, and stores credential-free remote metadata. The current surface does not expose repository initialization, status, diff, staging, commits, branches, fetch, pull, push, or conflicts. The objective requires those operations against a real temporary repository and local bare remote without broad or destructive Git commands.

Relevant code today:

- `app.go`: `GitVersion`, `CloneGitRepository`, `ScanGitCollections`, `OpenGitCollections`, credential-free `normalizeGitRemoteURL`, and clone progress events.
- `frontend/src/App.svelte`: the existing Import > Git Repository clone/scan/open surface and Git entry points.
- `app_test.go`: local clone/scan/open and credential-rejection fixtures.
- `docs/qa/2026-07-21-yaak-bruno-feature-ledger.md`: rows 107-118 define the missing workflow and selected product standard.

## Goals and non-goals

Goals:

- Give an opened local collection a compact, explicit Git workbench.
- Support init, repository discovery, scoped status/diff, stage/unstage, commit, branch list/create/switch, remote configuration, fetch, fast-forward pull, and upstream-aware push.
- Detect conflicts and authentication/unavailable-Git failures without overwriting files or exposing credentials.
- Scope status and path mutations to the active collection, even when multiple collections share a repository.
- Preserve the existing clone/scan/open workflow and plain-file interoperability.

Non-goals:

- No reset, clean, force push, rebase, discard, conflict auto-resolution, credential storage, embedded terminal, or history rewriting.
- No cloud account, hosting integration, or background synchronization.
- No shell command construction; every Git invocation uses a fixed executable plus an argument slice.

## Selected approach

Add a focused `git_workbench.go` gateway rather than adding more Git process code to `app.go`. Public App methods resolve a collection ID to its canonical path under the App mutex, copy the minimum immutable inputs, release the mutex, and then run bounded Git commands. Results are read models; only remote metadata changes require AppState persistence.

Repository scope is discovered with `git -C <collection-path> rev-parse --show-toplevel`. Every file operation validates a normalized repository-relative path, rejects absolute and parent-traversal paths, and additionally verifies that it falls under the active collection pathspec. Status and diff commands include that pathspec so sibling collections and unrelated repository files do not appear.

Git runs with:

- `GIT_TERMINAL_PROMPT=0` and bounded contexts;
- no inherited secret arguments beyond a validated remote URL;
- credential-bearing URLs rejected before execution;
- stdout/stderr passed through one sanitizer that redacts URL userinfo/query-like credentials and replaces unexpected absolute paths with safe summaries;
- `--no-ext-diff`, `--no-color`, and explicit `--` path separators where applicable.

The default pull policy is `--ff-only`. A non-fast-forward or conflicted repository is reported as an actionable error; LiteAPI never silently merges, rebases, checks out theirs/ours, or resets. Branch switching is refused when the scoped worktree is dirty. Push uses the configured upstream when present, otherwise an explicit user-approved `--set-upstream origin <branch>` action.

## Rejected alternatives

- A general-purpose shell/terminal wrapper was rejected because it expands authorization and makes path and credential guarantees untestable.
- Libgit2/go-git was rejected for this slice because Git is already a declared runtime dependency, existing code uses the Git CLI, and adding another implementation would create behavior drift around credentials and local configuration.
- Repository-wide status was rejected because a repository may hold several collections or unrelated files; the collection pathspec is the product boundary.
- Automatic conflict resolution and force operations were rejected because they can destroy user work and are outside the explicit safe workflow.

## Backend contract

Create typed models for:

- repository snapshot: available/initialized, safe root label, current/detached branch, upstream, ahead/behind, clean, conflicts, remotes, branches, scoped file rows;
- file row: normalized path, index/worktree status, staged/untracked/conflicted flags, binary flag;
- diff result: path, staged flag, bounded text, truncated/binary indicators;
- operation result: refreshed snapshot plus safe message.

Expose bounded methods named consistently with existing Wails APIs:

1. `GetCollectionGitStatus(collectionID)`
2. `InitializeCollectionGit(collectionID)`
3. `GetCollectionGitDiff(collectionID, path, staged)`
4. `StageCollectionGitPaths(collectionID, paths)`
5. `UnstageCollectionGitPaths(collectionID, paths)`
6. `CommitCollectionGit(collectionID, message)`
7. `CreateCollectionGitBranch(collectionID, branch, checkout)`
8. `CheckoutCollectionGitBranch(collectionID, branch)`
9. `SetCollectionGitRemote(collectionID, name, remoteURL)`
10. `FetchCollectionGit(collectionID, remote)`
11. `PullCollectionGit(collectionID, remote, branch)` using fast-forward only
12. `PushCollectionGit(collectionID, remote, branch, setUpstream)`

Initialization creates only `.git`; it does not stage or commit. Stage/unstage require an explicit non-empty path list. Commit requires a non-empty message and reports missing author configuration. Conflict rows are status-only until the user edits and explicitly stages them.

## Frontend contract

The existing Git command/workbench entry opens a dedicated compact surface for the active collection. When no collection is active, it explains the prerequisite. When Git is missing, it reuses the existing Git Required dialog.

The surface contains:

- repository/branch/upstream/ahead-behind summary and Refresh;
- Initialize when not a repository;
- file table with staged/worktree/conflict badges, explicit selection, View diff, Stage, and Unstage;
- bounded diff viewer with staged/unstaged toggle and binary/truncation messages;
- commit message plus Commit Staged;
- branch selector plus Create/Switch, with dirty-state explanation;
- remote name and credential-free URL configuration;
- Fetch, Pull (fast-forward only), and Push controls;
- an `aria-live` operation result and stable keyboard focus.

Actions refresh the snapshot on success and meaningful failure. Destructive operations are absent. The UI never renders raw command lines, environment values, or unsanitized Git output.

## Implementation sequence

1. Add the Git gateway, parsers, sanitizers, path validators, timeouts, and deterministic unit tests.
2. Add a full disposable integration fixture: seed collection, init/commit, bare remote, clone peer, status/diff, stage/unstage/commit, branch create/switch, push, peer commit/push, pull, second clone/open, conflict, unavailable Git, credential rejection, and sibling-file isolation.
3. Root review and independent functional QA before UI wiring.
4. Regenerate Wails bindings and add the Git workbench UI.
5. Re-run the same functional agent, then packaged native QA with filesystem assertions.

## Validation

Automated acceptance must demonstrate:

- clean status after initial commit;
- exact scoped modified/untracked rows and bounded diffs;
- stage, unstage, commit, branch creation, and switching;
- push to and pull from a local bare remote;
- clone/open again and matching collection content;
- conflict detection without file overwrite;
- unavailable Git and missing author/remote/upstream errors;
- embedded credentials rejected and sanitizer tests;
- sibling collection/unrelated sibling files absent from status, diff, and mutation targets;
- no force/reset/clean command is reachable.

Then run focused tests, `go test -race ./...`, `go vet ./...`, frontend check/build, Wails build, and `git diff --check`. Native QA must execute the same safe workflow in the packaged app and verify the repository with direct read-only Git commands.

## Risks, rollback, and open questions

- Porcelain parsing is byte-oriented and must cover rename/unmerged records and spaces/Unicode with `-z` fixtures.
- A user may change the repository between snapshot and action; each mutation re-resolves the repository and validates paths immediately before execution.
- Remote authentication varies by local credential helper. Automated acceptance uses only `file://` bare remotes; native unavailable/auth errors must remain safe and actionable.
- A pull can become non-fast-forward after fetch. `--ff-only` makes this a safe reported failure rather than an implicit merge.
- Rollback is code removal plus generated-binding removal; no state migration is required. Git commits and pushes are deliberate external side effects and therefore are never automatically rolled back.

