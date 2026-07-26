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
