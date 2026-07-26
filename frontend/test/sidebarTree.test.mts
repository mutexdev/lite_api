// US-031 — tests for flattening the collection tree.
//
// Every failure here is silent in the UI rather than an error: a collapsed
// folder whose children still render looks like a broken toggle, a row at the
// wrong depth reads as belonging to a different folder, and a duplicate key
// makes Svelte reuse the wrong DOM node so a click lands on a different
// request than the one under the cursor.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  flattenSidebar,
  indexOfRequest,
  visibleRequestCount,
  type SidebarGroup
} from '../src/lib/sidebarTree.ts'

const folderKey = (collectionId: string, folder: string) => `${collectionId}:${folder}`

const groups: Record<string, SidebarGroup[]> = {
  c1: [
    { folder: '', items: [{ id: 'root-1' }, { id: 'root-2' }] },
    { folder: 'Users', items: [{ id: 'u-1' }, { id: 'u-2' }] },
    { folder: 'Orders', items: [{ id: 'o-1' }] }
  ],
  c2: [{ folder: '', items: [{ id: 'other-1' }] }]
}

function flatten(overrides: Partial<Parameters<typeof flattenSidebar>[0]> = {}) {
  return flattenSidebar({
    collections: [{ id: 'c1' }, { id: 'c2' }],
    groupsFor: (id) => groups[id] ?? [],
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey,
    ...overrides
  })
}

test('a fully expanded tree emits every collection, folder and request', () => {
  const rows = flatten()
  assert.deepEqual(
    rows.map((row) => row.key),
    [
      'collection:c1',
      'request:c1:root-1',
      'request:c1:root-2',
      'folder:c1:Users',
      'request:c1:u-1',
      'request:c1:u-2',
      'folder:c1:Orders',
      'request:c1:o-1',
      'collection:c2',
      'request:c2:other-1'
    ]
  )
})

test('depth places root requests above foldered ones', () => {
  const rows = flatten()
  const byKey = Object.fromEntries(rows.map((row) => [row.key, row.depth]))
  assert.equal(byKey['collection:c1'], 0)
  assert.equal(byKey['request:c1:root-1'], 1, 'a request with no folder sits at the collection level')
  assert.equal(byKey['folder:c1:Users'], 1)
  assert.equal(byKey['request:c1:u-1'], 2, 'a request inside a folder is one deeper')
})

test('a collapsed collection hides everything under it', () => {
  const rows = flatten({ collapsedCollections: { c1: true } })
  assert.deepEqual(
    rows.map((row) => row.key),
    ['collection:c1', 'collection:c2', 'request:c2:other-1']
  )
  assert.equal(rows[0].collapsed, true)
})

test('a collapsed folder hides its requests but keeps its own row', () => {
  const rows = flatten({ collapsedFolders: { 'c1:Users': true } })
  const keys = rows.map((row) => row.key)
  assert.ok(keys.includes('folder:c1:Users'), 'the folder row must remain so it can be expanded again')
  assert.ok(!keys.includes('request:c1:u-1'), 'its children must be gone')
  assert.ok(keys.includes('request:c1:o-1'), 'a sibling folder is unaffected')
  assert.ok(keys.includes('request:c1:root-1'), 'root-level requests are unaffected')
})

// Someone typing a query wants matches wherever they are. Hiding them inside a
// folder collapsed an hour ago makes the search look broken.
test('a search query overrides every collapse', () => {
  const rows = flatten({
    collapsedCollections: { c1: true },
    collapsedFolders: { 'c1:Users': true },
    searchQuery: 'user'
  })
  const keys = rows.map((row) => row.key)
  assert.ok(keys.includes('request:c1:u-1'), 'a match inside a collapsed folder must still appear')
  assert.ok(keys.includes('request:c1:root-1'), 'a collapsed collection must still expand')
  assert.equal(rows[0].collapsed, false, 'and the row must not report itself collapsed')
})

test('a whitespace-only query does not count as searching', () => {
  const rows = flatten({ collapsedCollections: { c1: true }, searchQuery: '   ' })
  assert.ok(!rows.some((row) => row.key === 'request:c1:root-1'), 'blank input must not force everything open')
})

// A duplicate key makes Svelte's keyed {#each} reuse the wrong DOM node, so a
// click lands on a different request than the one under the cursor. The same
// request id genuinely can appear twice when one folder is mounted as two
// collections.
test('keys are unique even when two collections share a request id', () => {
  const shared: Record<string, SidebarGroup[]> = {
    c1: [{ folder: '', items: [{ id: 'same' }] }],
    c2: [{ folder: '', items: [{ id: 'same' }] }]
  }
  const rows = flattenSidebar({
    collections: [{ id: 'c1' }, { id: 'c2' }],
    groupsFor: (id) => shared[id] ?? [],
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey
  })
  const keys = rows.map((row) => row.key)
  assert.equal(new Set(keys).size, keys.length, `duplicate key in ${JSON.stringify(keys)}`)
})

test('every key in a large tree is unique', () => {
  const many: SidebarGroup[] = []
  for (let f = 0; f < 20; f++) {
    many.push({ folder: `Folder ${f}`, items: Array.from({ length: 25 }, (_, i) => ({ id: `f${f}-i${i}` })) })
  }
  const rows = flattenSidebar({
    collections: [{ id: 'big' }],
    groupsFor: () => many,
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey
  })
  assert.equal(visibleRequestCount(rows), 500, 'the 500-request fixture the story names')
  const keys = rows.map((row) => row.key)
  assert.equal(new Set(keys).size, keys.length)
})

test('an empty tree produces no rows', () => {
  const rows = flattenSidebar({
    collections: [],
    groupsFor: () => [],
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey
  })
  assert.deepEqual(rows, [])
})

test('a collection with no groups still emits its own row', () => {
  const rows = flattenSidebar({
    collections: [{ id: 'empty' }],
    groupsFor: () => [],
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey
  })
  assert.deepEqual(rows.map((row) => row.key), ['collection:empty'])
})

// Keyboard navigation scrolls by flattened index. Searching the collection's
// own item list instead would give a different number as soon as anything is
// collapsed, landing the user elsewhere in the tree.
test('indexOfRequest reports the position in the FLATTENED list', () => {
  const expanded = flatten()
  assert.equal(indexOfRequest(expanded, 'c1', 'u-1'), 4)

  // Collapsed layout: collection, root-1, root-2, Users (row kept, children
  // hidden), Orders, o-1. The collapsed folder's OWN row still occupies a
  // position — which is exactly what the test above asserts — so o-1 lands at
  // 5, not 4. I had this wrong first.
  const collapsed = flatten({ collapsedFolders: { 'c1:Users': true } })
  assert.equal(indexOfRequest(collapsed, 'c1', 'o-1'), 5, 'collapsing a folder above shifts the index')
  assert.equal(indexOfRequest(collapsed, 'c1', 'u-1'), -1, 'a hidden request has no position')
})

test('indexOfRequest does not confuse the same id in another collection', () => {
  const shared: Record<string, SidebarGroup[]> = {
    c1: [{ folder: '', items: [{ id: 'same' }] }],
    c2: [{ folder: '', items: [{ id: 'same' }] }]
  }
  const rows = flattenSidebar({
    collections: [{ id: 'c1' }, { id: 'c2' }],
    groupsFor: (id) => shared[id] ?? [],
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey
  })
  assert.equal(indexOfRequest(rows, 'c2', 'same'), 3)
})

test('visibleRequestCount counts only rendered requests', () => {
  assert.equal(visibleRequestCount(flatten()), 6)
  assert.equal(visibleRequestCount(flatten({ collapsedFolders: { 'c1:Users': true } })), 4)
  assert.equal(visibleRequestCount(flatten({ collapsedCollections: { c1: true } })), 1)
})
