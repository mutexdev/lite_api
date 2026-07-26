// Global search: what matches, and in what order.
//
// The ranking is the feature. Someone types three characters, presses Enter and
// gets result zero — so the order decides which request opens. A ranking that
// puts a URL match above a name match sends people somewhere they did not ask
// for while looking, from the outside, like search is simply bad at its job.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  normalizeGlobalSearchQuery,
  isValidGlobalSearchQuery,
  globalSearchTermsMatch,
  globalSearchItemPath,
  buildGlobalSearchResults
} from '../src/lib/globalSearch.ts'

const workspace = {
  collections: [
    {
      id: 'c1',
      name: 'Billing',
      path: '/repos/billing',
      format: 'bru',
      items: [
        { id: 'r1', name: 'Create invoice', url: 'https://api.test/invoices', method: 'POST', folderPath: 'invoices' },
        { id: 'r2', name: 'List users', url: 'https://api.test/users', method: 'GET', folderPath: 'users' },
        { id: 'r3', name: 'Health', url: 'https://api.test/healthz', method: 'GET', folderPath: '' }
      ]
    },
    { id: 'c2', name: 'Reporting', path: '/repos/reporting', format: 'yaml', items: [] }
  ]
} as never

const search = (query: string) => buildGlobalSearchResults(workspace, query)

test('an empty query lists every collection and nothing else', () => {
  const results = search('')
  assert.deepEqual(results.map((r) => r.type), ['collection', 'collection'])
  assert.deepEqual(results.map((r) => r.name), ['Billing', 'Reporting'])
})

test('the query is lowercased and repeated slashes collapse', () => {
  assert.equal(normalizeGlobalSearchQuery('  Users//Create  '), 'users/create')
  assert.equal(normalizeGlobalSearchQuery('ABC'), 'abc')
})

// A lone "/" or a single punctuation mark matches everything, which is worse
// than matching nothing.
test('a query that would match everything is rejected', () => {
  assert.equal(isValidGlobalSearchQuery('/'), false)
  assert.equal(isValidGlobalSearchQuery('.'), false)
  assert.equal(isValidGlobalSearchQuery(''), false)
  assert.equal(isValidGlobalSearchQuery('a'), true, 'a single letter is a real prefix search')
  assert.equal(isValidGlobalSearchQuery('7'), true)
  assert.deepEqual(search('/'), [], 'and it returns nothing rather than the whole workspace')
})

// Every term must match, so typing more narrows rather than widens.
test('terms are AND-ed, not OR-ed', () => {
  assert.equal(globalSearchTermsMatch(['create invoice'], ['create', 'invoice']), true)
  assert.equal(globalSearchTermsMatch(['create invoice'], ['create', 'refund']), false)
  assert.equal(globalSearchTermsMatch([null, undefined, 'x'], ['x']), true, 'absent fields are not a match failure')
})

test('a name match outranks a url match, which outranks a method match', () => {
  // "Health" matches only the request name — no folder is called that, which
  // matters because folders rank above requests and would take result zero.
  const byName = search('health')
  assert.equal(byName[0].itemId, 'r3')
  assert.equal(byName[0].rank, 2)

  const byUrl = search('healthz')
  assert.equal(byUrl[0].itemId, 'r3')
  assert.equal(byUrl[0].rank, 3, 'a url-only match must rank below a name match')

  const byMethod = search('post')
  assert.equal(byMethod[0].rank, 4, 'a method-only match is the weakest signal')
})

// A folder outranks the requests inside it. Typing "invoice" surfaces the
// "invoices" folder before "Create invoice", on the grounds that the container
// is the broader answer to an ambiguous query. Recorded because it is
// surprising, and because I assumed the opposite first.
test('a folder match outranks a request name match', () => {
  const results = search('invoice')
  assert.equal(results[0].type, 'folder')
  assert.equal(results[0].name, 'invoices')
  assert.equal(results[0].rank, 1)

  const request = results.find((r) => r.itemId === 'r1')
  assert.equal(request?.rank, 2, 'the request is still found, just ranked below its folder')
})

// If a request matches by name and another only by URL, the name match must
// come first even though both are requests.
test('results are ordered by rank before type or name', () => {
  const results = search('users')
  const ranks = results.map((r) => r.rank)
  assert.deepEqual([...ranks].sort((a, b) => a - b), ranks, 'results must be in ascending rank order')
})

test('a collection matches on name, path or format and ranks first', () => {
  assert.equal(search('billing')[0].type, 'collection')
  assert.equal(search('billing')[0].rank, 0)
  assert.equal(search('reporting')[0].name, 'Reporting')
  assert.ok(search('yaml').some((r) => r.id === 'collection:c2'), 'format is searchable')
})

test('folders are derived from the items that live in them', () => {
  const results = search('invoices')
  assert.ok(results.some((r) => r.type === 'folder' && r.name === 'invoices'))
  assert.ok(!results.some((r) => r.type === 'folder' && r.name === ''), 'a root-level item does not create a blank folder')
})

// A slash is what turns on path matching — it is how "billing/users" finds a
// request by where it lives rather than what it is called.
test('a slash enables path matching', () => {
  const withSlash = search('billing/list')
  assert.ok(withSlash.some((r) => r.itemId === 'r2'), 'path match should find the request through its collection name')

  const withoutSlash = search('billing list')
  assert.ok(
    !withoutSlash.some((r) => r.itemId === 'r2'),
    'without a slash the collection name is not part of a request path'
  )
})

test('globalSearchItemPath joins the parts it has and skips the ones it does not', () => {
  assert.equal(
    globalSearchItemPath({ name: 'Billing' } as never, { folderPath: 'users', name: 'List' } as never),
    'Billing/users/List'
  )
  assert.equal(
    globalSearchItemPath({ name: 'Billing' } as never, { folderPath: '', name: 'Health' } as never),
    'Billing/Health',
    'a root-level request must not produce a double slash'
  )
})

test('a query matching nothing returns nothing', () => {
  assert.deepEqual(search('zzzznomatch'), [])
})

test('an absent workspace is not an error', () => {
  assert.deepEqual(buildGlobalSearchResults(undefined, 'x'), [])
  assert.deepEqual(buildGlobalSearchResults(undefined, ''), [])
})

test('every result carries the ids the caller needs to open it', () => {
  for (const result of search('invoice')) {
    assert.ok(result.collectionId, `${result.id} has no collectionId`)
    if (result.type === 'request') assert.ok(result.itemId, `${result.id} has no itemId`)
  }
})

// Ids must be unique or the keyed list reuses the wrong row, and a click opens
// something other than what is under the cursor.
test('result ids are unique', () => {
  const ids = search('').map((r) => r.id).concat(search('api').map((r) => r.id))
  assert.equal(new Set(ids).size, ids.length)
})
