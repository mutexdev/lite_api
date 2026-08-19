import { test } from 'node:test'
import assert from 'node:assert/strict'
import { historyEntryExists, withOptimisticPatch } from '../src/lib/optimisticPatch.ts'
import type { types } from '../wailsjs/go/models'

function state(): types.AppState {
  return {
    workspaces: [
      {
        id: 'ws-1',
        collections: [
          { id: 'col-1', items: [{ id: 'req-1', name: 'Login', url: 'https://a.test' }] },
          { id: 'col-2', items: [{ id: 'req-2', name: 'Other' }] },
        ],
      },
      {
        id: 'ws-2',
        collections: [{ id: 'col-3', items: [{ id: 'req-3', name: 'Elsewhere' }] }],
      },
    ],
  } as types.AppState
}

function itemIn(next: types.AppState, collectionId: string, itemId: string) {
  for (const workspace of next.workspaces ?? []) {
    for (const collection of workspace.collections ?? []) {
      if (collection.id !== collectionId) continue
      const found = collection.items?.find((item) => item.id === itemId)
      if (found) return found
    }
  }
  return undefined
}

test('the patch is written onto the target request', () => {
  const next = withOptimisticPatch(state(), 'col-1', 'req-1', { url: 'https://b.test' } as types.RequestPatch)
  const patched = itemIn(next, 'col-1', 'req-1')
  assert.equal(patched?.url, 'https://b.test')
  assert.equal(patched?.name, 'Login', 'fields the patch does not mention are kept')
})

// THE PROPERTY THAT MATTERS MOST. The tab strip reads `draft` to show the
// unsaved dot, and the close guard reads it before letting a tab go. An edit
// applied without it looks saved, and closing the tab discards it with no
// prompt.
test('the patched request is marked as a draft', () => {
  const next = withOptimisticPatch(state(), 'col-1', 'req-1', { url: 'https://b.test' } as types.RequestPatch)
  assert.equal(itemIn(next, 'col-1', 'req-1')?.draft, true)
})

// A patch that happened to carry draft:false would otherwise unmark the request
// mid-edit, which is the one value it must never take here.
test('the draft flag cannot be overridden by the patch', () => {
  const next = withOptimisticPatch(
    state(),
    'col-1',
    'req-1',
    { draft: false } as unknown as types.RequestPatch,
  )
  assert.equal(itemIn(next, 'col-1', 'req-1')?.draft, true)
})

test('the original state is not mutated', () => {
  const original = state()
  withOptimisticPatch(original, 'col-1', 'req-1', { url: 'https://b.test' } as types.RequestPatch)
  assert.equal(original.workspaces?.[0].collections?.[0].items?.[0].url, 'https://a.test')
  assert.equal(original.workspaces?.[0].collections?.[0].items?.[0].draft, undefined)
})

// This runs per keystroke. Every $derived and {#each} keyed on a collection
// would re-run for all of them if untouched collections were copied — turning
// one character into a re-render of the whole sidebar.
test('collections without the target keep their identity', () => {
  const before = state()
  const next = withOptimisticPatch(before, 'col-1', 'req-1', { url: 'https://b.test' } as types.RequestPatch)
  assert.equal(next.workspaces?.[0].collections?.[1], before.workspaces?.[0].collections?.[1])
  assert.equal(next.workspaces?.[1].collections?.[0], before.workspaces?.[1].collections?.[0])
  assert.notEqual(next.workspaces?.[0].collections?.[0], before.workspaces?.[0].collections?.[0])
})

test('requests in the same collection that are not the target keep their identity', () => {
  const before = {
    workspaces: [
      {
        id: 'ws',
        collections: [{ id: 'c', items: [{ id: 'a' }, { id: 'b' }] }],
      },
    ],
  } as types.AppState
  const next = withOptimisticPatch(before, 'c', 'a', { url: 'x' } as types.RequestPatch)
  assert.equal(next.workspaces?.[0].collections?.[0].items?.[1], before.workspaces?.[0].collections?.[0].items?.[1])
})

// A collection id is unique across the tree, and a request can be patched from
// a window whose active workspace is not the one that owns it.
test('a collection in another workspace is still found', () => {
  const next = withOptimisticPatch(state(), 'col-3', 'req-3', { name: 'Renamed' } as types.RequestPatch)
  const patched = itemIn(next, 'col-3', 'req-3')
  assert.equal(patched?.name, 'Renamed')
  assert.equal(patched?.draft, true)
})

test('an unknown collection or request leaves everything alone', () => {
  const before = state()
  for (const [collectionId, itemId] of [['gone', 'req-1'], ['col-1', 'gone']]) {
    const next = withOptimisticPatch(before, collectionId, itemId, { url: 'x' } as types.RequestPatch)
    assert.deepEqual(next.workspaces, before.workspaces, `${collectionId}/${itemId}`)
  }
})

test('an empty tree is handled without throwing', () => {
  const next = withOptimisticPatch({} as types.AppState, 'c', 'i', {} as types.RequestPatch)
  assert.deepEqual(next.workspaces, [])
})

test('a workspace with no collections is handled', () => {
  const next = withOptimisticPatch(
    { workspaces: [{ id: 'ws' }] } as types.AppState,
    'c',
    'i',
    {} as types.RequestPatch,
  )
  assert.deepEqual(next.workspaces?.[0].collections, [])
})

// A collection with no items array at all — one that failed to load, or was
// created empty — must not throw while a keystroke is being applied, and comes
// back with an empty array so the caller can render a tree without a null
// check.
test('a collection with no items array is handled and normalised', () => {
  const before = { workspaces: [{ id: 'w', collections: [{ id: 'c' }] }] } as types.AppState
  const next = withOptimisticPatch(before, 'c', 'i', { url: 'x' } as types.RequestPatch)
  assert.deepEqual(next.workspaces?.[0].collections?.[0].items, [])
})

// THE PROPERTY THAT MAKES THIS ONE FUNCTION RATHER THAN TWO CHECKS. The item is
// looked for inside the MATCHING collection. Checking "some collection has this
// id" and "some item has this id" separately both pass for a request that has
// since been moved to a different collection, and the history row would offer
// to open something that is not there.
test('a request moved to another collection does not count as present', () => {
  const moved = {
    workspaces: [
      {
        id: 'ws',
        collections: [
          { id: 'col-a', items: [{ id: 'other' }] },
          { id: 'col-b', items: [{ id: 'req-1' }] }
        ]
      }
    ]
  } as types.AppState

  // Both halves exist somewhere, but not together.
  assert.equal(historyEntryExists(moved, 'col-a', 'req-1'), false)
  // And the pair that does match is found.
  assert.equal(historyEntryExists(moved, 'col-b', 'req-1'), true)
})

// History records a send, and a scratch request sent before it was ever saved
// has no collection to point back at. Those rows must not offer to open.
test('an entry missing either id is not openable', () => {
  const state = { workspaces: [{ id: 'ws', collections: [{ id: 'c', items: [{ id: 'i' }] }] }] } as types.AppState
  assert.equal(historyEntryExists(state, undefined, 'i'), false)
  assert.equal(historyEntryExists(state, 'c', undefined), false)
  assert.equal(historyEntryExists(state, '', ''), false)
})

// The case above cannot show why the guard is needed: against well-formed data
// the comparison already fails, since no collection has an id of undefined. The
// guard earns its place against MALFORMED data — a collection that arrived
// without an id MATCHES an entry that has none, and undefined === undefined is
// true. The row would then offer to open a request that is not there.
//
// Removing the guard fails only this test, which is the point of it.
test('an entry with no ids does not match a collection that has none either', () => {
  const malformed = {
    workspaces: [{ id: 'ws', collections: [{ items: [{}] }] }]
  } as unknown as types.AppState
  assert.equal(historyEntryExists(malformed, undefined, undefined), false)
})

test('an entry is found across any workspace', () => {
  const state = {
    workspaces: [
      { id: 'ws-1', collections: [{ id: 'c1', items: [{ id: 'a' }] }] },
      { id: 'ws-2', collections: [{ id: 'c2', items: [{ id: 'b' }] }] }
    ]
  } as types.AppState
  assert.equal(historyEntryExists(state, 'c2', 'b'), true)
})

test('a deleted request is not found', () => {
  const state = { workspaces: [{ id: 'ws', collections: [{ id: 'c', items: [] }] }] } as types.AppState
  assert.equal(historyEntryExists(state, 'c', 'gone'), false)
})

// The history panel renders before the first state arrives.
test('an absent or empty tree is handled', () => {
  assert.equal(historyEntryExists(undefined, 'c', 'i'), false)
  assert.equal(historyEntryExists({} as types.AppState, 'c', 'i'), false)
  assert.equal(historyEntryExists({ workspaces: [{ id: 'ws' }] } as types.AppState, 'c', 'i'), false)
})
