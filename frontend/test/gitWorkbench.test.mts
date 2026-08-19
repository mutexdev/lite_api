import assert from 'node:assert/strict'
import test from 'node:test'

import { canPushGitBranch, canStageGitSelection, canSwitchGitBranch, canUnstageGitSelection, reconcileGitBranch, reconcileGitRemoteBranch, reconcileGitRemoteSelection, reconcileGitSelection } from '../src/lib/gitWorkbench.ts'

const files = [
  { path: 'changed.bru', staged: false, untracked: false, conflicted: false },
  { path: 'staged.bru', staged: true, untracked: false, conflicted: false },
  { path: 'conflict.bru', staged: false, untracked: false, conflicted: true }
]

test('Git selection reconciliation preserves only still-present explicit selections', () => {
  assert.deepEqual(reconcileGitSelection(['changed.bru', 'gone.bru', 'changed.bru'], files), ['changed.bru'])
})

test('Git workbench keeps manually resolved conflicts stageable and protects branch switches', () => {
  assert.equal(canStageGitSelection(['changed.bru'], files), true)
  assert.equal(canStageGitSelection(['conflict.bru'], files), true)
  assert.equal(canUnstageGitSelection(['staged.bru'], files), true)
  assert.equal(canUnstageGitSelection(['conflict.bru'], files), false)
  assert.equal(canSwitchGitBranch({ clean: true, conflicts: false }), true)
  assert.equal(canSwitchGitBranch({ clean: false, conflicts: false }), false)
  assert.equal(canSwitchGitBranch({ clean: true, conflicts: true }), false)
})

test('Git push needs the exact requested upstream unless the user opts in to set it', () => {
  assert.equal(canPushGitBranch('origin/main', 'origin', 'main', false), true)
  assert.equal(canPushGitBranch('origin/main', 'origin', 'feature', false), false)
  assert.equal(canPushGitBranch('backup/main', 'origin', 'main', false), false)
  assert.equal(canPushGitBranch('', 'origin', 'main', true), true)
})

test('Git remote branch follows active branch transitions without overwriting an explicit override', () => {
  assert.equal(reconcileGitRemoteBranch('', undefined, 'main'), 'main', 'initial snapshot selects its branch')
  assert.equal(reconcileGitRemoteBranch('main', 'main', 'Qa-r3'), 'Qa-r3', 'default follows a checked-out branch')
  assert.equal(reconcileGitRemoteBranch('release', 'main', 'Qa-r3'), 'release', 'explicit remote branch is preserved')
})

const remotes = [
  { name: 'origin', url: 'git@example.test:a/b.git' },
  { name: 'fork', url: 'git@example.test:me/b.git' }
]

// A refresh must not move the user off the remote they were about to push to.
test('the selected remote survives a refresh', () => {
  assert.deepEqual(reconcileGitRemoteSelection('fork', remotes), {
    name: 'fork',
    url: 'git@example.test:me/b.git'
  })
})

// A repository with remotes and none selected offers a push button that cannot
// work.
test('an unknown remote falls back to the first', () => {
  assert.deepEqual(reconcileGitRemoteSelection('gone', remotes), {
    name: 'origin',
    url: 'git@example.test:a/b.git'
  })
  assert.deepEqual(reconcileGitRemoteSelection('', remotes), {
    name: 'origin',
    url: 'git@example.test:a/b.git'
  })
})

// A stale URL beside an empty remote list reads as a configured remote, and the
// connect dialog would open pre-filled with an address the repository no longer
// has.
test('with no remotes the url is cleared', () => {
  assert.equal(reconcileGitRemoteSelection('origin', []).url, '')
  assert.equal(reconcileGitRemoteSelection('origin', undefined).url, '')
})

// Otherwise the checkout button targets a branch deleted or renamed elsewhere,
// and git refuses with a message about a ref rather than about the picker.
test('a branch that no longer exists falls back to head', () => {
  assert.equal(reconcileGitBranch('feature', ['main', 'feature'], 'main'), 'feature')
  assert.equal(reconcileGitBranch('deleted', ['main'], 'main'), 'main')
  assert.equal(reconcileGitBranch('', ['main'], 'main'), 'main')
})

test('a repository with no head yields no branch', () => {
  assert.equal(reconcileGitBranch('deleted', [], undefined), '')
  assert.equal(reconcileGitBranch('', undefined, undefined), '')
})

// Before the first status arrives there is no snapshot at all. Defaulting to
// "can switch" would offer a checkout against a working tree nobody has looked
// at yet — the one moment when uncommitted work is most likely to be lost.
test('branch switching is refused until a snapshot exists', () => {
  assert.equal(canSwitchGitBranch(undefined), false)
})

// Each half of the guard is load-bearing on its own: a dirty tree loses edits
// on checkout, and conflicts mean a merge is half-finished.
test('branch switching needs both a clean tree and no conflicts', () => {
  assert.equal(canSwitchGitBranch({ clean: true, conflicts: false }), true)
  assert.equal(canSwitchGitBranch({ clean: false, conflicts: false }), false)
  assert.equal(canSwitchGitBranch({ clean: true, conflicts: true }), false)
  assert.equal(canSwitchGitBranch({ clean: false, conflicts: true }), false)
})

// A branch with no upstream at all cannot be pushed without opting in to
// setting one. Treating "no upstream" as a match would push to whatever the
// remote's default happened to be.
test('a branch with no upstream cannot be pushed without opting in', () => {
  assert.equal(canPushGitBranch(undefined, 'origin', 'main', false), false)
  assert.equal(canPushGitBranch(undefined, 'origin', 'main', true), true)
  assert.equal(canPushGitBranch('', 'origin', 'main', false), false)
})

// The upstream must match the remote AND the branch. Matching on either alone
// would let a push to origin/main go out while the user has origin/release
// selected.
test('an upstream for a different branch or remote is not a match', () => {
  assert.equal(canPushGitBranch('origin/main', 'origin', 'main', false), true)
  assert.equal(canPushGitBranch('origin/other', 'origin', 'main', false), false)
  assert.equal(canPushGitBranch('upstream/main', 'origin', 'main', false), false)
})

// A conflicted row reports as staged in porcelain, so unstaging it would undo a
// resolution the user is part-way through recording rather than unstage a file.
test('a conflicted row is not unstageable even though it reports as staged', () => {
  const files = [{ path: 'a', staged: true, untracked: false, conflicted: true }]
  assert.equal(canUnstageGitSelection(['a'], files), false)
  assert.equal(canStageGitSelection(['a'], files), true, 'but it is still stageable, which records the resolution')
})

// The buttons are disabled with nothing selected rather than acting on
// everything, which is what a bare `files.some` without the membership test
// would do.
test('neither button is enabled with an empty selection', () => {
  const files = [
    { path: 'a', staged: false, untracked: true, conflicted: false },
    { path: 'b', staged: true, untracked: false, conflicted: false }
  ]
  assert.equal(canStageGitSelection([], files), false)
  assert.equal(canUnstageGitSelection([], files), false)
})

// A duplicated path in the selection must not stage the same file twice, and
// the reconcile is where that is collapsed.
test('a duplicated selection collapses to one entry', () => {
  const files = [{ path: 'a', staged: false, untracked: false, conflicted: false }]
  assert.deepEqual(reconcileGitSelection(['a', 'a', 'b'], files), ['a'])
})

// A repository whose HEAD is detached or unborn reports no branch. The remote
// branch follows it to empty rather than keeping a name that no longer refers
// to anything.
test('the remote branch follows an absent head to empty', () => {
  assert.equal(reconcileGitRemoteBranch('main', 'main', undefined), '')
  assert.equal(reconcileGitRemoteBranch('', undefined, 'dev'), 'dev')
})
