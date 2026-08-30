// Tests for "which environment is active".
//
// The bug these exist to pin down was not a wrong answer, it was three answers.
// The command strip said "Development", the Active-environment select said
// nothing, and the id handed to the backend was "" so `{{host}}` was sent
// literally. Each display was faithful to what it read; the readings differed.
//
// So the tests below are about the resolution rules being total: every
// combination of (stored value, available environments) has one defined answer,
// including the two that produced the original split — an id that matches
// nothing, and no stored value at all.

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  environmentSelectionKey,
  readEnvironmentSelections,
  resolveEnvironmentId,
  withEnvironmentSelection,
  writeEnvironmentSelections,
  type SelectionStorage
} from '../src/lib/environmentSelection.ts'

const environments = [
  { id: 'env-dev', name: 'Development' },
  { id: 'env-prod', name: 'Production' }
]

/** An in-memory Storage, so the tests never touch a real one. */
function fakeStorage(seed: Record<string, string> = {}) {
  const data = new Map(Object.entries(seed))
  const storage: SelectionStorage & { data: Map<string, string>; failWrites?: boolean } = {
    data,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => {
      if (storage.failWrites) throw new Error('quota exceeded')
      data.set(key, value)
    }
  }
  return storage
}

test('a collection never looked at defaults to its first environment', () => {
  // This is the behaviour the old startup line had, applied per collection
  // instead of once per session.
  assert.equal(resolveEnvironmentId(environments, undefined), 'env-dev')
})

test('an explicit "No environment" choice sticks', () => {
  // The regression this guards: if "" were treated the same as "never chosen",
  // picking No environment would silently reselect Development and the option
  // would be impossible to use.
  assert.equal(resolveEnvironmentId(environments, ''), '')
})

test('a stored id that still exists is honoured', () => {
  assert.equal(resolveEnvironmentId(environments, 'env-prod'), 'env-prod')
})

test('a stale id resolves to a real environment rather than staying stale', () => {
  // The original split-brain state: an id matching nothing, which the command
  // strip papered over with `?? environments[0]` while the select and the
  // backend both saw the unusable value. Resolving it HERE means the name shown
  // and the id sent are the same environment.
  assert.equal(resolveEnvironmentId(environments, 'env-deleted'), 'env-dev')
})

test('a collection with no environments resolves to empty for every input', () => {
  assert.equal(resolveEnvironmentId([], undefined), '')
  assert.equal(resolveEnvironmentId([], ''), '')
  assert.equal(resolveEnvironmentId([], 'env-dev'), '')
  assert.equal(resolveEnvironmentId(undefined, undefined), '')
})

test('selections are recorded per collection', () => {
  // Switching collections is the event the old single-string state missed.
  let selections = withEnvironmentSelection({}, 'col-a', 'env-prod')
  selections = withEnvironmentSelection(selections, 'col-b', '')

  assert.equal(resolveEnvironmentId(environments, selections['col-a']), 'env-prod')
  assert.equal(resolveEnvironmentId(environments, selections['col-b']), '')
  assert.equal(resolveEnvironmentId(environments, selections['col-c']), 'env-dev')
})

test('recording a selection does not mutate the previous map', () => {
  const before = { 'col-a': 'env-prod' }
  const after = withEnvironmentSelection(before, 'col-a', 'env-dev')

  assert.deepEqual(before, { 'col-a': 'env-prod' })
  assert.equal(after['col-a'], 'env-dev')
})

test('a selection with no collection id is ignored', () => {
  const before = { 'col-a': 'env-prod' }
  assert.equal(withEnvironmentSelection(before, '', 'env-dev'), before)
})

test('the storage key is scoped, and absent until the scope is known', () => {
  // The scope arrives from an async binding call. An unscoped fallback would
  // make every workspace window share one entry and fight over it.
  assert.equal(environmentSelectionKey(''), '')
  assert.match(environmentSelectionKey('window-1'), /window-1/)
  assert.notEqual(environmentSelectionKey('window-1'), environmentSelectionKey('window-2'))
})

test('selections survive a round trip through storage', () => {
  const storage = fakeStorage()
  writeEnvironmentSelections('window-1', { 'col-a': 'env-prod', 'col-b': '' }, storage)

  assert.deepEqual(readEnvironmentSelections('window-1', storage), { 'col-a': 'env-prod', 'col-b': '' })
})

test('another window scope reads none of them', () => {
  const storage = fakeStorage()
  writeEnvironmentSelections('window-1', { 'col-a': 'env-prod' }, storage)

  assert.deepEqual(readEnvironmentSelections('window-2', storage), {})
})

test('nothing is read or written before the scope is known', () => {
  const storage = fakeStorage()
  writeEnvironmentSelections('', { 'col-a': 'env-prod' }, storage)

  assert.equal(storage.data.size, 0)
  assert.deepEqual(readEnvironmentSelections('', storage), {})
})

test('a corrupt entry degrades to no stored choices instead of throwing', () => {
  // This runs during workspace load. A throw here would take the whole startup
  // down over a browser-storage value the user can edit by hand.
  const key = environmentSelectionKey('window-1')
  for (const raw of ['not json', '[]', 'null', '"a string"', '42']) {
    assert.deepEqual(readEnvironmentSelections('window-1', fakeStorage({ [key]: raw })), {}, raw)
  }
})

test('non-string values inside a stored entry are dropped, not adopted', () => {
  const key = environmentSelectionKey('window-1')
  const storage = fakeStorage({ [key]: JSON.stringify({ 'col-a': 'env-prod', 'col-b': 7, 'col-c': null }) })

  assert.deepEqual(readEnvironmentSelections('window-1', storage), { 'col-a': 'env-prod' })
})

test('a storage that refuses writes does not break the selection', () => {
  // Private-mode WebViews and quota limits. Losing the choice across a reload
  // beats failing the click that made it.
  const storage = fakeStorage()
  storage.failWrites = true

  assert.doesNotThrow(() => writeEnvironmentSelections('window-1', { 'col-a': 'env-prod' }, storage))
})

test('no storage at all is tolerated', () => {
  assert.deepEqual(readEnvironmentSelections('window-1', null), {})
  assert.doesNotThrow(() => writeEnvironmentSelections('window-1', { 'col-a': 'x' }, null))
})
