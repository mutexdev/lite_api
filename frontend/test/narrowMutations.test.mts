// US-014 — tests for applying narrow mutator results.
//
// Almost every test here is about a result that must NOT be applied. The
// happy path is one line of merging; the value is in the refusals, because a
// narrow result wrongly applied to a stale copy produces a UI that looks
// correct, stays wrong, and never self-corrects.

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyRequestMutation,
  applyTabsMutation,
  canApplyNarrowResult,
} from '../src/lib/narrowMutations.ts'

const stateWith = (revision: number) =>
  ({
    revision,
    activeTabId: 't1',
    openTabs: [{ id: 't1' }, { id: 't2' }],
    workspaces: [
      {
        id: 'w1',
        collections: [
          { id: 'c1', items: [{ id: 'i1', url: 'old' }, { id: 'i2', url: 'other' }] },
          { id: 'c2', items: [{ id: 'i3', url: 'elsewhere' }] },
        ],
      },
    ],
  }) as never

const requestResult = (revision: number, overrides: Record<string, unknown> = {}) =>
  ({
    revision,
    collectionId: 'c1',
    item: { id: 'i1', url: 'new' },
    ...overrides,
  }) as never

const tabsResult = (revision: number) =>
  ({
    revision,
    openTabs: [{ id: 't2' }, { id: 't1' }],
    activeTabId: 't2',
  }) as never

test('the next revision is applied', () => {
  const outcome = applyRequestMutation(stateWith(5), 5, requestResult(6))
  assert.equal(outcome.kind, 'applied')
  if (outcome.kind !== 'applied') return
  assert.equal(outcome.revision, 6)
  const item = (outcome.state as never as typeof outcome.state & {
    workspaces: Array<{ collections: Array<{ id: string; items: Array<{ id: string; url: string }> }> }>
  }).workspaces[0].collections[0].items[0]
  assert.equal(item.url, 'new', 'the changed item must be replaced')
})

test('an unchanged revision is applied, not treated as a gap', () => {
  // A no-op MoveOpenTab does not bump the counter. Refetching here would fire a
  // full AppState fetch every time a tab is dragged against the edge.
  assert.equal(canApplyNarrowResult(9, 9), null)
  const outcome = applyTabsMutation(stateWith(9), 9, tabsResult(9))
  assert.equal(outcome.kind, 'applied')
})

test('a skipped revision forces a refetch', () => {
  const outcome = applyRequestMutation(stateWith(5), 5, requestResult(8))
  assert.equal(outcome.kind, 'refetch')
  if (outcome.kind !== 'refetch') return
  assert.match(outcome.reason, /missed 2 update/)
})

test('a revision going backwards forces a refetch', () => {
  const outcome = applyTabsMutation(stateWith(9), 9, tabsResult(4))
  assert.equal(outcome.kind, 'refetch')
  if (outcome.kind !== 'refetch') return
  assert.match(outcome.reason, /backwards/)
})

test('a result for a collection we do not hold forces a refetch', () => {
  // Applying nothing and reporting success is the dangerous outcome: the user's
  // edit vanishes from the UI with no indication anything went wrong.
  const outcome = applyRequestMutation(stateWith(5), 5, requestResult(6, { collectionId: 'c-unknown' }))
  assert.equal(outcome.kind, 'refetch')
})

test('a result for an item we do not hold forces a refetch', () => {
  const outcome = applyRequestMutation(stateWith(5), 5, requestResult(6, { item: { id: 'i-unknown', url: 'new' } }))
  assert.equal(outcome.kind, 'refetch')
})

test('applying a request mutation does not mutate the previous state object', () => {
  // Svelte's legacy reactivity tracks assignment, not mutation. An in-place
  // splice updates the data and not the view.
  const before = stateWith(5)
  const snapshot = JSON.stringify(before)
  const outcome = applyRequestMutation(before, 5, requestResult(6))
  assert.equal(outcome.kind, 'applied')
  assert.equal(JSON.stringify(before), snapshot, 'the input state must be left untouched')
  if (outcome.kind !== 'applied') return
  assert.notEqual(outcome.state, before, 'a new state object must be produced')
})

test('unrelated collections and items are left strictly alone', () => {
  const outcome = applyRequestMutation(stateWith(5), 5, requestResult(6))
  assert.equal(outcome.kind, 'applied')
  if (outcome.kind !== 'applied') return
  const workspaces = (outcome.state as never as {
    workspaces: Array<{ collections: Array<{ id: string; items: Array<{ id: string; url: string }> }> }>
  }).workspaces
  assert.equal(workspaces[0].collections[0].items[1].url, 'other')
  assert.equal(workspaces[0].collections[1].items[0].url, 'elsewhere')
})

test('a tabs mutation replaces order and selection together', () => {
  const outcome = applyTabsMutation(stateWith(2), 2, tabsResult(3))
  assert.equal(outcome.kind, 'applied')
  if (outcome.kind !== 'applied') return
  const state = outcome.state as never as { openTabs: Array<{ id: string }>; activeTabId: string }
  assert.deepEqual(state.openTabs.map((tab) => tab.id), ['t2', 't1'])
  assert.equal(state.activeTabId, 't2')
})

test('the applied state carries the new revision forward', () => {
  // Forgetting this makes the NEXT call look like a gap, so every second
  // mutation would trigger a full refetch — the story would appear to work
  // while delivering half its benefit.
  const outcome = applyRequestMutation(stateWith(5), 5, requestResult(6))
  assert.equal(outcome.kind, 'applied')
  if (outcome.kind !== 'applied') return
  assert.equal((outcome.state as never as { revision: number }).revision, 6)
})
