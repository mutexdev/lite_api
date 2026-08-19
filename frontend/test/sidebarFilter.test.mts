// Filtering and grouping the sidebar tree.
//
// Every failure here is silent in the UI: the sidebar simply shows fewer rows,
// or more, and looks like the collection genuinely contains that.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  searchHit,
  collectionMatches,
  requestMatches,
  folderMatches,
  filteredItems,
  filteredFolders,
  computeGroupedItems
} from '../src/lib/sidebarFilter.ts'

const collection = {
  name: 'Billing',
  format: 'bru',
  path: '/repos/billing',
  folders: [
    { path: 'invoices', displayPath: 'invoices', name: 'invoices' },
    { path: 'users', displayPath: 'users', name: 'users' }
  ],
  items: [
    { id: 'r1', name: 'Create invoice', method: 'POST', type: 'http', url: 'https://api.test/invoices', folderPath: 'invoices' },
    { id: 'r2', name: 'List users', method: 'GET', type: 'http', url: 'https://api.test/users', folderPath: 'users' },
    { id: 'r3', name: 'Health', method: 'GET', type: 'http', url: 'https://api.test/healthz', folderPath: '' }
  ]
} as never

test('searchHit is a case-insensitive substring test that tolerates absent fields', () => {
  assert.equal(searchHit('Create Invoice', 'invoice'), true)
  assert.equal(searchHit(undefined, 'x'), false)
  assert.equal(searchHit(null, 'x'), false)
  assert.equal(searchHit(42, '4'), true, 'non-strings are stringified rather than skipped')
  assert.equal(searchHit('abc', ''), true, 'an empty query matches everything')
})

// The needle is not lowercased here; the caller does it. Recorded because it is
// the kind of asymmetry that looks like a bug until you find the call site.
test('searchHit lowercases the candidate, not the query', () => {
  assert.equal(searchHit('ABC', 'abc'), true)
  assert.equal(searchHit('abc', 'ABC'), false, 'callers must lowercase the query first')
})

test('a collection matches on name, format or path', () => {
  assert.equal(collectionMatches(collection, 'billing'), true)
  assert.equal(collectionMatches(collection, 'bru'), true)
  assert.equal(collectionMatches(collection, 'repos'), true)
  assert.equal(collectionMatches(collection, 'nope'), false)
})

// Someone typing a collection name wants that collection, not the subset of its
// requests that happen to repeat the name.
test('a collection match keeps every request inside it', () => {
  assert.equal(filteredItems(collection, 'billing').length, 3)
  assert.equal(filteredItems(collection, 'invoice').length, 1, 'a non-collection query still narrows')

  // The discriminating case. requestMatches already includes collection.name,
  // so a NAME query keeps everything with or without the shortcut in
  // filteredItems — testing only that proves nothing. collectionMatches also
  // covers the FORMAT and the PATH, which requestMatches does not, so those are
  // the queries the shortcut actually decides.
  assert.equal(filteredItems(collection, 'bru').length, 3, 'a format query must keep every request')
  assert.equal(filteredItems(collection, '/repos/billing').length, 3, 'a path query must keep every request')
})

test('an empty query keeps everything', () => {
  assert.equal(filteredItems(collection, '').length, 3)
  assert.equal(filteredFolders(collection, '').length, 2)
  assert.equal(filteredFolders(collection, '   ').length, 2, 'whitespace is not a query')
})

test('a request matches on its own fields and its folder path', () => {
  assert.equal(requestMatches(collection, (collection as never as { items: unknown[] }).items[0] as never, 'create'), true)
  assert.equal(requestMatches(collection, (collection as never as { items: unknown[] }).items[0] as never, 'post'), true)
  assert.equal(requestMatches(collection, (collection as never as { items: unknown[] }).items[0] as never, 'healthz'), false)
  assert.equal(requestMatches(collection, (collection as never as { items: unknown[] }).items[1] as never, 'users'), true)
})

// Saved examples are searchable, because a request is often findable only by
// the example someone saved on it.
test('a request matches through its saved examples', () => {
  const item = {
    name: 'Opaque',
    url: 'https://api.test/x',
    examples: [{ name: 'refund path', description: 'covers the 402', request: { url: 'https://api.test/refund' } }]
  } as never
  assert.equal(requestMatches(collection, item, 'refund path'), true)
  assert.equal(requestMatches(collection, item, 'covers the 402'), true)
  assert.equal(requestMatches(collection, item, '/refund'), true)
  assert.equal(requestMatches(collection, item, 'unrelated'), false)
})

test('a folder matches on any of its three names', () => {
  const folder = { path: 'a/b', displayPath: 'A / B', name: 'b' } as never
  assert.equal(folderMatches(folder, 'a/b'), true)
  assert.equal(folderMatches(folder, 'a / b'), true)
  assert.equal(folderMatches(folder, 'b'), true)
  assert.equal(folderMatches(folder, 'zzz'), false)
})

test('grouping puts every request under its folder', () => {
  const groups = computeGroupedItems(collection)
  const byFolder = Object.fromEntries(groups.map((g) => [g.folder, g.items.map((i) => i.id)]))
  assert.deepEqual(byFolder['invoices'], ['r1'])
  assert.deepEqual(byFolder['users'], ['r2'])
  assert.deepEqual(byFolder[''], ['r3'], 'a root-level request groups under the empty folder')
})

// A folder with no requests still gets a row, or an empty folder would vanish
// from the tree and look deleted.
test('an empty folder still produces a group', () => {
  const withEmpty = {
    ...(collection as never as Record<string, unknown>),
    folders: [{ path: 'empty', displayPath: 'empty', name: 'empty' }],
    items: []
  } as never
  const groups = computeGroupedItems(withEmpty)
  assert.equal(groups.length, 1)
  assert.equal(groups[0].folder, 'empty')
  assert.deepEqual(groups[0].items, [])
})

test('grouping preserves folder order then discovery order', () => {
  const groups = computeGroupedItems(collection)
  assert.deepEqual(groups.map((g) => g.folder), ['invoices', 'users', ''])
})

test('a query narrows both the folders and the requests', () => {
  const groups = computeGroupedItems(collection, 'invoice')
  assert.deepEqual(groups.map((g) => g.folder), ['invoices'])
  assert.deepEqual(groups[0].items.map((i) => i.id), ['r1'])
})

test('a query matching nothing produces no groups', () => {
  assert.deepEqual(computeGroupedItems(collection, 'zzzznomatch'), [])
})

test('an empty collection is not an error', () => {
  const empty = { name: 'e' } as never
  assert.deepEqual(computeGroupedItems(empty), [])
  assert.deepEqual(filteredItems(empty, ''), [])
  assert.deepEqual(filteredFolders(empty, ''), [])
})

// A request in a folder the query did not match still needs a group, or it
// would be silently dropped from the tree.
test('a request whose folder did not match still gets a group', () => {
  const groups = computeGroupedItems(collection, 'healthz')
  assert.deepEqual(groups.map((g) => g.folder), [''])
  assert.deepEqual(groups[0].items.map((i) => i.id), ['r3'])
})
