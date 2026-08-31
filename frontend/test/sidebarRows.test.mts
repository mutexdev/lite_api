// The sidebar's flat row model — the substrate for keyboard navigation.
//
// WHY A MODEL AND NOT THE DOM. The sidebar is virtualised: sidebarGroupWindow
// renders only group.items.slice(start, end) and replaces the rest with spacer
// divs. A row the user arrows onto may not be in the document at all, so the
// textbook roving-tabindex pattern — focus the next sibling element — cannot
// work here. Navigation moves an index in this array, scrolls, and focuses
// whatever rendered afterwards.
//
// WHY IT ALSO PRODUCES THE WINDOW OFFSETS. sidebarGroupOffset used to walk the
// same structure separately, re-encoding three rules: a collapsed collection
// contributes only its header, a collection whose directory is missing does the
// same, and a search overrides every collapse. Two walks that must agree, and
// whose disagreement is invisible until someone scrolls — the failure mode
// virtualList.ts warns about in its own comments. So there is now one walk with
// two outputs, and the equivalence test at the bottom is what holds them
// together.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  sidebarRows,
  walkSidebar,
  sidebarGroupOffset,
  type SidebarRowsInput
} from '../src/lib/sidebar/sidebarRows.ts'
import { sidebarGroupOffset as legacyGroupOffset } from '../src/lib/virtualList.ts'

const folderKey = (collectionId: string, folder: string) => `${collectionId}:${folder}`

type Fixture = {
  collections: { id: string; name: string; notFoundLocally?: boolean }[]
  groups: Record<string, { folder: string; items: { id: string; name: string; examples?: { id: string; name: string }[] }[] }[]>
  collapsedCollections?: Record<string, boolean>
  collapsedFolders?: Record<string, boolean>
  searchQuery?: string
}

function input(fixture: Fixture): SidebarRowsInput {
  return {
    collections: fixture.collections,
    groupsFor: (id) => fixture.groups[id] ?? [],
    collapsedCollections: fixture.collapsedCollections ?? {},
    collapsedFolders: fixture.collapsedFolders ?? {},
    searchQuery: fixture.searchQuery ?? '',
    folderKey
  }
}

/** One collection, one folder with two requests, one root request. */
const basic: Fixture = {
  collections: [{ id: 'c1', name: 'Alpha' }],
  groups: {
    c1: [
      { folder: 'auth', items: [{ id: 'r1', name: 'Login' }, { id: 'r2', name: 'Logout' }] },
      { folder: '', items: [{ id: 'r3', name: 'Health' }] }
    ]
  }
}

test('rows come out in the order the sidebar draws them', () => {
  const rows = sidebarRows(input(basic))

  assert.deepEqual(
    rows.map((row) => `${row.kind}:${row.label}`),
    ['collection:Alpha', 'folder:auth', 'request:Login', 'request:Logout', 'request:Health']
  )
})

test('depth reflects nesting, and a root request is shallower than a foldered one', () => {
  const rows = sidebarRows(input(basic))
  const depthOf = (label: string) => rows.find((row) => row.label === label)?.depth

  assert.equal(depthOf('Alpha'), 0)
  assert.equal(depthOf('auth'), 1)
  assert.equal(depthOf('Login'), 2)
  assert.equal(depthOf('Health'), 1)
})

test('every row key is unique, so focus can survive a re-walk', () => {
  const rows = sidebarRows(input({
    ...basic,
    groups: {
      c1: [
        { folder: 'auth', items: [{ id: 'r1', name: 'Login', examples: [{ id: 'e1', name: 'OK' }, { id: 'e2', name: 'Denied' }] }] },
        // Same request NAME in a different folder: the key must not collide.
        { folder: 'admin', items: [{ id: 'r9', name: 'Login' }] }
      ]
    }
  }))

  const keys = rows.map((row) => row.key)
  assert.equal(new Set(keys).size, keys.length, `duplicate key among ${JSON.stringify(keys)}`)
})

test('response examples are rows of their own, directly under their request', () => {
  const rows = sidebarRows(input({
    collections: [{ id: 'c1', name: 'Alpha' }],
    groups: { c1: [{ folder: '', items: [
      { id: 'r1', name: 'Login', examples: [{ id: 'e1', name: 'OK' }] },
      { id: 'r2', name: 'Logout' }
    ] }] }
  }))

  assert.deepEqual(
    rows.map((row) => `${row.kind}:${row.label}`),
    ['collection:Alpha', 'request:Login', 'example:OK', 'request:Logout']
  )
  const example = rows.find((row) => row.kind === 'example')
  assert.equal(example?.itemId, 'r1', 'the example row does not carry its parent request id')
})

// THE THREE COLLAPSE RULES. Each of these is also encoded in the window
// arithmetic, which is exactly why they now live in one place.

test('a collapsed collection contributes its header and nothing else', () => {
  const rows = sidebarRows(input({ ...basic, collapsedCollections: { c1: true } }))
  assert.deepEqual(rows.map((row) => row.kind), ['collection'])
})

test('a collection whose directory is missing contributes its header and nothing else', () => {
  const rows = sidebarRows(input({
    ...basic,
    collections: [{ id: 'c1', name: 'Alpha', notFoundLocally: true }]
  }))
  assert.deepEqual(rows.map((row) => row.kind), ['collection'])
})

test('a collapsed folder keeps its header but drops its requests', () => {
  const rows = sidebarRows(input({ ...basic, collapsedFolders: { 'c1:auth': true } }))
  assert.deepEqual(
    rows.map((row) => `${row.kind}:${row.label}`),
    ['collection:Alpha', 'folder:auth', 'request:Health']
  )
})

test('a search overrides every collapse, because a hidden result is unreachable', () => {
  const rows = sidebarRows(input({
    ...basic,
    collapsedCollections: { c1: true },
    collapsedFolders: { 'c1:auth': true },
    searchQuery: 'log'
  }))
  assert.deepEqual(
    rows.map((row) => `${row.kind}:${row.label}`),
    ['collection:Alpha', 'folder:auth', 'request:Login', 'request:Logout', 'request:Health']
  )
})

// THE EQUIVALENCE. This is the test that earns the single walk: the new offset
// must agree with the one that shipped, on every combination of the rules, or
// the focused row and the scrolled row drift apart.

const permutations: Fixture[] = [
  basic,
  { ...basic, collapsedCollections: { c1: true } },
  { ...basic, collapsedFolders: { 'c1:auth': true } },
  { ...basic, searchQuery: 'log' },
  { ...basic, searchQuery: 'log', collapsedFolders: { 'c1:auth': true } },
  {
    collections: [{ id: 'c1', name: 'Alpha' }, { id: 'c2', name: 'Beta' }, { id: 'c3', name: 'Gone', notFoundLocally: true }],
    groups: {
      c1: [{ folder: 'auth', items: [{ id: 'r1', name: 'Login' }, { id: 'r2', name: 'Logout' }] }, { folder: '', items: [{ id: 'r3', name: 'Health' }] }],
      c2: [{ folder: '', items: [{ id: 'r4', name: 'Ping' }] }, { folder: 'admin', items: [{ id: 'r5', name: 'Users' }] }],
      c3: [{ folder: '', items: [{ id: 'r6', name: 'Unreachable' }] }]
    }
  },
  {
    collections: [{ id: 'c1', name: 'Alpha' }, { id: 'c2', name: 'Beta' }],
    groups: {
      c1: [{ folder: 'auth', items: [{ id: 'r1', name: 'Login' }] }],
      c2: [{ folder: '', items: [{ id: 'r4', name: 'Ping' }] }]
    },
    collapsedCollections: { c1: true }
  },
  // An empty group, which contributes a header and no items.
  {
    collections: [{ id: 'c1', name: 'Alpha' }],
    groups: { c1: [{ folder: 'empty', items: [] }, { folder: '', items: [{ id: 'r1', name: 'Health' }] }] }
  }
]

test('the new group offset matches the one it replaces, on every collapse combination', () => {
  for (const [index, fixture] of permutations.entries()) {
    const rowsInput = input(fixture)
    const walk = walkSidebar(rowsInput)

    for (const collection of fixture.collections) {
      for (const group of fixture.groups[collection.id] ?? []) {
        const expected = legacyGroupOffset(rowsInput, collection.id, group.folder)
        const actual = sidebarGroupOffset(walk, collection.id, group.folder)
        assert.equal(
          actual,
          expected,
          `permutation ${index}: offset for ${collection.id}/${group.folder || '<root>'} is ${actual}, was ${expected}`
        )
      }
    }

    // And an unknown target still falls through to the grand total, the way the
    // original did after its loops ran out.
    assert.equal(
      sidebarGroupOffset(walk, 'nope', 'nope'),
      legacyGroupOffset(rowsInput, 'nope', 'nope'),
      `permutation ${index}: unknown target diverged`
    )
  }
})

// EXAMPLES ARE EXCLUDED FROM THE OFFSET ON PURPOSE, and this pins the reason.
//
// The window arithmetic converts a scrollTop into a row index by dividing by a
// SINGLE measured row height, taken from .request-row-shell. Example rows are
// not that height — .request-row sets min-height: 28px and .sidebar-example-row
// sets no height at all — so counting them would not make the arithmetic right,
// it would make it wrong in a new way. Fixing that properly means
// variable-height windowing, which is a separate change.
test('response examples do not shift the window offsets', () => {
  const withExamples = input({
    collections: [{ id: 'c1', name: 'Alpha' }],
    groups: { c1: [
      { folder: 'auth', items: [{ id: 'r1', name: 'Login', examples: [{ id: 'e1', name: 'OK' }, { id: 'e2', name: 'Denied' }] }] },
      { folder: '', items: [{ id: 'r3', name: 'Health' }] }
    ] }
  })

  const walk = walkSidebar(withExamples)
  assert.equal(sidebarGroupOffset(walk, 'c1', ''), legacyGroupOffset(withExamples, 'c1', ''))
  assert.ok(walk.rows.some((row) => row.kind === 'example'), 'the fixture produced no example rows')
})

// ── Nesting ─────────────────────────────────────────────────────────────────
//
// Folder paths are paths. Treating every folder as depth 1 made a nested folder
// a sibling of its own parent, so the tree drew flat and Left-arrow could not
// walk out of "api/v2" into "api".

test('a nested folder is deeper than the folder that contains it', () => {
  const rows = sidebarRows(input({
    collections: [{ id: 'c1', name: 'Alpha' }],
    groups: {
      c1: [
        { folder: 'api', items: [] },
        { folder: 'api/v2', items: [{ id: 'r1', name: 'List Users' }] },
        { folder: '', items: [{ id: 'r2', name: 'Health' }] }
      ]
    }
  }))

  assert.deepEqual(
    rows.map((row) => `${row.label}@${row.depth}`),
    ['Alpha@0', 'api@1', 'v2@2', 'List Users@3', 'Health@1']
  )
})

// The row shows its own name, not the whole path: "v2", not "api/v2". The path
// is what the indentation is for, and repeating it in the label makes a deep
// tree unreadable at sidebar width.
test('a nested folder row is labelled with its own name, not its path', () => {
  const rows = sidebarRows(input({
    collections: [{ id: 'c1', name: 'Alpha' }],
    groups: { c1: [{ folder: 'api/v2/admin', items: [] }] }
  }))

  const folder = rows.find((row) => row.kind === 'folder')
  assert.equal(folder?.label, 'admin')
  // The identity is still the full path, because that is what every collapse
  // key, offset lookup and action target is addressed by.
  assert.equal(folder?.folder, 'api/v2/admin')
  assert.equal(folder?.key, 'f:c1:api/v2/admin')
})

// ── Flows ───────────────────────────────────────────────────────────────────
//
// FLOW ROWS WERE DRAWN AND NOT WALKED, and the gap was load-bearing: because
// walkSidebar never emitted them, a keyboard cursor pointing at a flow resolved
// to index -1, so App.svelte's markup carried a comment refusing to move the
// cursor onto one at all. That left flows with no arrow-key route, no
// role="treeitem", and no ⋯ menu — every guarantee the tree had just built,
// skipped for one row type.
//
// These pin the two properties that make the rest of it work: the rows come out
// where the markup draws them, and their keys are the ones flowView.ts's
// flowRowKey already builds the DOM ids from.

const flowFixture: SidebarRowsInput = {
  collections: [{ id: 'c1', name: 'Alpha' }],
  groupsFor: () => [
    { folder: 'auth', items: [{ id: 'r1', name: 'Login' }] },
    { folder: '', items: [{ id: 'r2', name: 'Health' }] }
  ],
  collapsedCollections: {},
  collapsedFolders: {},
  searchQuery: '',
  folderKey,
  flowsFor: () => [{ id: 'f1', name: 'Signup' }, { id: 'f2', name: 'Checkout' }]
}

test('flows are rows, drawn after every folder and request in their collection', () => {
  const rows = sidebarRows(flowFixture)

  assert.deepEqual(
    rows.map((row) => `${row.kind}:${row.label}`),
    ['collection:Alpha', 'folder:auth', 'request:Login', 'request:Health', 'flow:Signup', 'flow:Checkout']
  )
})

test('a flow row carries the key flowRowKey builds, and its id in itemId', () => {
  const rows = sidebarRows(flowFixture)
  const flow = rows.find((row) => row.kind === 'flow')

  // `fl:` is flowView.ts's prefix. The markup derives its DOM ids from that
  // function, so a walk that invented its own would emit rows whose keys never
  // resolve to an element on screen.
  assert.equal(flow?.key, 'fl:c1:f1')
  assert.equal(flow?.itemId, 'f1')
  assert.equal(flow?.collectionId, 'c1')
  // A flow belongs to the collection, never to a folder.
  assert.equal(flow?.folder, '')
  assert.equal(flow?.depth, 1)
})

test('omitting flowsFor leaves the walk exactly as it was', () => {
  const { flowsFor, ...withoutFlows } = flowFixture
  assert.ok(flowsFor, 'the fixture supplies no flows to omit')

  assert.ok(!sidebarRows(withoutFlows).some((row) => row.kind === 'flow'))
  assert.deepEqual(
    sidebarRows(withoutFlows).map((row) => row.key),
    sidebarRows(flowFixture).filter((row) => row.kind !== 'flow').map((row) => row.key)
  )
})

test('a collapsed or missing collection draws no flow rows either', () => {
  assert.deepEqual(
    sidebarRows({ ...flowFixture, collapsedCollections: { c1: true } }).map((row) => row.kind),
    ['collection']
  )
  assert.deepEqual(
    sidebarRows({ ...flowFixture, collections: [{ id: 'c1', name: 'Alpha', notFoundLocally: true }] }).map((row) => row.kind),
    ['collection']
  )
})

// COUNTED IN THE WINDOW ARITHMETIC, unlike examples. A flow row sets the same
// min-height a request row does, so it occupies a slot the offsets must know
// about; a later collection whose offset was short by its predecessor's flow
// count would window itself wrong the moment anybody scrolled.
test('flow rows advance the offsets that later collections are measured from', () => {
  const twoCollections: SidebarRowsInput = {
    ...flowFixture,
    collections: [{ id: 'c1', name: 'Alpha' }, { id: 'c2', name: 'Beta' }],
    groupsFor: (id) => (id === 'c1'
      ? [{ folder: '', items: [{ id: 'r1', name: 'Login' }] }]
      : [{ folder: '', items: [{ id: 'r9', name: 'Ping' }] }]),
    flowsFor: (id) => (id === 'c1' ? [{ id: 'f1', name: 'Signup' }] : [])
  }

  const walk = walkSidebar(twoCollections)
  // Alpha header, Alpha's request, Alpha's flow, Beta header — so Beta's root
  // group starts at 4.
  assert.equal(sidebarGroupOffset(walk, 'c2', ''), 4)

  const withoutFlow = walkSidebar({ ...twoCollections, flowsFor: () => [] })
  assert.equal(sidebarGroupOffset(withoutFlow, 'c2', ''), 3)
})
