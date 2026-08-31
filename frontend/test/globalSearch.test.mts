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
  buildGlobalSearchResults,
  sortGlobalSearchResults,
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

// Every result row renders a subtitle and a meta chip. Both have fallbacks, and
// an unfilled one shows as a blank cell that reads like a rendering bug rather
// than like missing data.
test('a collection with no path is subtitled by its request count', () => {
  const workspace = {
    collections: [{ id: 'c1', name: 'Scratch', items: [{ id: 'i1', name: 'ping' }] }]
  } as unknown as types.Workspace
  const [result] = buildGlobalSearchResults(workspace, '')
  assert.equal(result.subtitle, '1 requests')
  assert.equal(result.meta, 'collection', 'a collection with no format still labels itself')
})

// The same two fallbacks are written twice — once for the empty-query listing
// and once for a matched collection. They must agree, or the same collection
// describes itself differently depending on whether the user has typed
// anything.
test('the empty-query and matched rows describe a collection identically', () => {
  const workspace = {
    collections: [{ id: 'c1', name: 'Scratch', items: [{ id: 'i1', name: 'ping' }] }]
  } as unknown as types.Workspace
  const [listed] = buildGlobalSearchResults(workspace, '')
  const [matched] = buildGlobalSearchResults(workspace, 'scratch')
  assert.equal(listed.subtitle, matched.subtitle)
  assert.equal(listed.meta, matched.meta)
  assert.notEqual(listed.rank, matched.rank, 'but a match still outranks a plain listing')
})

test('a collection with no items counts zero rather than showing nothing', () => {
  const workspace = { collections: [{ id: 'c1', name: 'Empty' }] } as unknown as types.Workspace
  assert.equal(buildGlobalSearchResults(workspace, '')[0].subtitle, '0 requests')
})

// The meta chip is what tells a GET row from a POST row at a glance. A request
// saved before the method field existed falls back through its type to a
// constant, rather than rendering an empty chip.
test('a request with no method falls back through its type', () => {
  const workspace = {
    collections: [{ id: 'c1', name: 'C', items: [
      { id: 'i1', name: 'typed', type: 'graphql' },
      { id: 'i2', name: 'bare' }
    ] }]
  } as unknown as types.Workspace
  const byName = Object.fromEntries(
    buildGlobalSearchResults(workspace, 'i').concat(buildGlobalSearchResults(workspace, 'typed'), buildGlobalSearchResults(workspace, 'bare'))
      .map((r) => [r.name, r.meta])
  )
  assert.equal(byName.typed, 'graphql')
  assert.equal(byName.bare, 'request')
})

// A request inside a folder shows where it lives; one at the collection root
// has no folder to name, and repeating the collection name twice would be
// noise.
test('a request subtitle names its folder only when it has one', () => {
  const workspace = {
    collections: [{ id: 'c1', name: 'C', items: [
      { id: 'i1', name: 'nested', folderPath: 'auth' },
      { id: 'i2', name: 'root' }
    ] }]
  } as unknown as types.Workspace
  const nested = buildGlobalSearchResults(workspace, 'nested')[0]
  const root = buildGlobalSearchResults(workspace, 'root')[0]
  assert.equal(nested.subtitle, 'C / auth')
  assert.equal(root.subtitle, 'C')
})

// A single character is a legitimate search — "a" narrows a long list usefully.
// A single PUNCTUATION mark is not: it matches on substring, so "." would
// return every request with a dot anywhere in a URL, which is all of them.
test('a one-character query is valid only if it is alphanumeric', () => {
  for (const query of ['a', 'Z', '1']) {
    assert.equal(isValidGlobalSearchQuery(query), true, query)
  }
  for (const query of ['.', '*', '-', '/']) {
    assert.equal(isValidGlobalSearchQuery(query), false, query)
  }
})

// Rank first, then type, then name. Without the last tiebreak two results that
// are equal on both earlier keys keep whatever order the collection happened to
// be stored in, so the list reshuffles between renders.
test('results equal on rank and type are ordered by name', () => {
  const rows = [
    { rank: 1, type: 'folder', name: 'beta' },
    { rank: 1, type: 'folder', name: 'alpha' }
  ] as unknown as Parameters<typeof sortGlobalSearchResults>[0][]
  assert.deepEqual([...rows].sort(sortGlobalSearchResults).map((r) => r.name), ['alpha', 'beta'])
})

// ── ⌘K and ⌘⇧P are one pair, and must stay one pair ─────────────────────────
//
// They are documented as a matched set — "keep Cmd+K search and add Cmd+Shift+P
// for commands" — and they shipped with two different accessibility stories.
// The palette was a real single-select listbox: aria-controls,
// aria-activedescendant, role="listbox", role="option", aria-selected. Global
// Search, the more used of the two, had none of it, so a screen reader was
// handed a bag of buttons and the arrow keys moved a highlight nothing
// announced.
//
// Asserted against the source, the way brandMark.test.mts and
// sidebarTree.test.mts already do, because the repo has no component-rendering
// harness. That makes this weak about pixels and strong about the wiring —
// which is the half that silently went missing.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const modalSource = (name: string) =>
  readFileSync(fileURLToPath(new URL(`../src/lib/modals/search/${name}`, import.meta.url)), 'utf8')

test('both search modals implement the same listbox semantics', () => {
  for (const name of ['GlobalSearchModal.svelte', 'CommandPaletteModal.svelte']) {
    const source = modalSource(name)
    for (const required of [
      /role="listbox"/,
      /role="option"/,
      /aria-selected=\{/,
      /aria-controls="/,
      /aria-activedescendant=\{/
    ]) {
      assert.match(source, required, `${name} is missing ${required}`)
    }
  }
})

// The highlight must not be carried by a CSS class ALONE, which is what the ⌘K
// results were: `class:active` and nothing else. The class stays — it is what
// paints the row — but it now travels with aria-selected on the same element.
test('the ⌘K result rows carry aria-selected, not just a CSS class', () => {
  const source = modalSource('GlobalSearchModal.svelte')
  const activeUses = source.match(/class:active=\{/g) ?? []
  const selectedUses = source.match(/aria-selected=\{/g) ?? []
  assert.equal(activeUses.length, selectedUses.length, 'a highlighted row is styled but not announced')
})

// aria-activedescendant must name an id that an option actually renders, and
// both modals build theirs from one helper for exactly that reason: two
// separate template literals is how the attribute ends up pointing at nothing.
test('each modal builds its option ids from a single helper', () => {
  for (const name of ['GlobalSearchModal.svelte', 'CommandPaletteModal.svelte']) {
    const source = modalSource(name)
    assert.match(source, /const optionId = \(id: string\) =>/, `${name} does not centralise its option ids`)
    // Once for aria-activedescendant on the input, once for the option's id.
    const uses = source.match(/optionId\(/g) ?? []
    assert.equal(uses.length, 2, `${name} uses optionId ${uses.length} times, expected 2`)
  }
})
