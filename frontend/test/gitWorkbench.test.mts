import assert from 'node:assert/strict'
import test from 'node:test'

import { canPushGitBranch, canStageGitSelection, canSwitchGitBranch, canUnstageGitSelection, reconcileGitRemoteBranch, reconcileGitSelection } from '../src/lib/gitWorkbench.ts'

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
