// Tree keyboard navigation, decided without a browser.
//
// The two rules worth testing hardest are the ones that look redundant and get
// simplified away by the next person to read the file: Left both collapses and
// walks to the parent, and Right both expands and descends. Each is one key
// doing two jobs depending on state, and collapsing either pair into a single
// job produces a tree you can enter but not leave.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  isExpandable,
  parentIndex,
  resolveSidebarKey,
  typeAheadIndex,
  type SidebarNavContext
} from '../src/lib/sidebar/sidebarNavigation.ts'
import { sidebarRows } from '../src/lib/sidebar/sidebarRows.ts'

const rows = sidebarRows({
  collections: [{ id: 'c1', name: 'Alpha' }],
  groupsFor: () => [
    { folder: 'auth', items: [{ id: 'r1', name: 'Login' }, { id: 'r2', name: 'Logout' }] },
    { folder: '', items: [{ id: 'r3', name: 'Health' }] }
  ],
  collapsedCollections: {},
  collapsedFolders: {},
  searchQuery: '',
  folderKey: (c, f) => `${c}:${f}`
})
// rows: 0 Alpha(collection) 1 auth(folder) 2 Login 3 Logout 4 Health

function context(overrides: Partial<SidebarNavContext> = {}): SidebarNavContext {
  return {
    rows,
    index: 0,
    isExpanded: () => true,
    matches: () => false,
    typeAhead: '',
    ...overrides
  }
}

const key = (k: string, extra: Record<string, boolean> = {}) => ({ key: k, ...extra })

test('the fixture is the shape the rest of these tests assume', () => {
  assert.deepEqual(rows.map((row) => row.label), ['Alpha', 'auth', 'Login', 'Logout', 'Health'])
  assert.deepEqual(rows.map((row) => row.depth), [0, 1, 2, 2, 1])
})

test('arrow keys walk the flat row list and stop at both ends', () => {
  assert.deepEqual(resolveSidebarKey(key('ArrowDown'), context({ index: 0 })), { kind: 'focus', index: 1 })
  assert.deepEqual(resolveSidebarKey(key('ArrowUp'), context({ index: 3 })), { kind: 'focus', index: 2 })
  assert.deepEqual(resolveSidebarKey(key('ArrowDown'), context({ index: 4 })), { kind: 'focus', index: 4 })
  assert.deepEqual(resolveSidebarKey(key('ArrowUp'), context({ index: 0 })), { kind: 'focus', index: 0 })
})

test('Arrow-Up with nothing focused enters the list from the bottom', () => {
  assert.deepEqual(resolveSidebarKey(key('ArrowUp'), context({ index: -1 })), { kind: 'focus', index: 4 })
})

test('Home and End jump to the ends', () => {
  assert.deepEqual(resolveSidebarKey(key('Home'), context({ index: 3 })), { kind: 'focus', index: 0 })
  assert.deepEqual(resolveSidebarKey(key('End'), context({ index: 0 })), { kind: 'focus', index: 4 })
})

// RIGHT: EXPAND, THEN DESCEND. Two presses, not one.
test('Right expands a closed folder without moving off it', () => {
  const action = resolveSidebarKey(key('ArrowRight'), context({ index: 1, isExpanded: () => false }))
  assert.deepEqual(action, { kind: 'expand', index: 1 })
})

test('Right on an already-open folder descends into its first child', () => {
  const action = resolveSidebarKey(key('ArrowRight'), context({ index: 1, isExpanded: () => true }))
  assert.deepEqual(action, { kind: 'focus', index: 2 })
})

test('Right on a request does nothing, because a leaf has nothing to open', () => {
  assert.equal(resolveSidebarKey(key('ArrowRight'), context({ index: 2 })), null)
})

test('Right on an open folder with no children does not fall into the next branch', () => {
  // A folder whose next row is a SIBLING, not a child, must not "descend" into it.
  const emptyFolderRows = sidebarRows({
    collections: [{ id: 'c1', name: 'Alpha' }],
    groupsFor: () => [{ folder: 'empty', items: [] }, { folder: '', items: [{ id: 'r1', name: 'Health' }] }],
    collapsedCollections: {},
    collapsedFolders: {},
    searchQuery: '',
    folderKey: (c, f) => `${c}:${f}`
  })
  // rows: 0 Alpha(0) 1 empty(1) 2 Health(1)
  const action = resolveSidebarKey(key('ArrowRight'), context({ rows: emptyFolderRows, index: 1, isExpanded: () => true }))
  assert.equal(action, null, 'Right descended into a sibling at the same depth')
})

// LEFT: COLLAPSE, THEN WALK OUT.
test('Left collapses an open folder rather than leaving it', () => {
  assert.deepEqual(
    resolveSidebarKey(key('ArrowLeft'), context({ index: 1, isExpanded: () => true })),
    { kind: 'collapse', index: 1 }
  )
})

test('Left on an already-collapsed folder walks up to its parent', () => {
  assert.deepEqual(
    resolveSidebarKey(key('ArrowLeft'), context({ index: 1, isExpanded: () => false })),
    { kind: 'focus', index: 0 }
  )
})

test('Left on a request walks out to the folder that contains it', () => {
  assert.deepEqual(resolveSidebarKey(key('ArrowLeft'), context({ index: 3 })), { kind: 'focus', index: 1 })
})

test('Left on a root-level request walks out to its collection, not past it', () => {
  assert.deepEqual(resolveSidebarKey(key('ArrowLeft'), context({ index: 4 })), { kind: 'focus', index: 0 })
  assert.equal(resolveSidebarKey(key('ArrowLeft'), context({ index: 0, isExpanded: () => false })), null)
})

test('Enter opens the focused row', () => {
  assert.deepEqual(resolveSidebarKey(key('Enter'), context({ index: 2 })), { kind: 'activate', index: 2 })
})

test('Space toggles an expandable row but opens a leaf', () => {
  assert.deepEqual(
    resolveSidebarKey(key(' '), context({ index: 1, isExpanded: () => false })),
    { kind: 'expand', index: 1 }
  )
  assert.deepEqual(
    resolveSidebarKey(key(' '), context({ index: 1, isExpanded: () => true })),
    { kind: 'collapse', index: 1 }
  )
  assert.deepEqual(resolveSidebarKey(key(' '), context({ index: 2 })), { kind: 'activate', index: 2 })
})

// THE CONTEXT MENU HAS TWO KEYS BECAUSE APPLE KEYBOARDS HAVE NO MENU KEY.
test('both Shift+F10 and the ContextMenu key open the action menu', () => {
  assert.deepEqual(resolveSidebarKey(key('ContextMenu'), context({ index: 2 })), { kind: 'menu', index: 2 })
  assert.deepEqual(resolveSidebarKey(key('F10', { shiftKey: true }), context({ index: 2 })), { kind: 'menu', index: 2 })
})

test('a bare F10 is not the menu key and is left alone', () => {
  assert.equal(resolveSidebarKey(key('F10'), context({ index: 2 })), null)
})

test('Escape hands focus back out of the tree', () => {
  assert.deepEqual(resolveSidebarKey(key('Escape'), context({ index: 2 })), { kind: 'exit' })
})

// THE BINDINGS PREFERENCES ALREADY ADVERTISED. renameItem and cloneItem have
// shipped in the shortcuts sheet, bound to ⌘R and ⌘D, implemented by nothing.
test('the configured item bindings resolve to their actions', () => {
  const only = (name: string) => (candidate: string) => candidate === name

  assert.deepEqual(
    resolveSidebarKey(key('r', { metaKey: true }), context({ index: 2, matches: only('renameItem') })),
    { kind: 'action', index: 2, action: 'rename' }
  )
  assert.deepEqual(
    resolveSidebarKey(key('d', { metaKey: true }), context({ index: 2, matches: only('cloneItem') })),
    { kind: 'action', index: 2, action: 'clone' }
  )
  assert.deepEqual(
    resolveSidebarKey(key('Backspace', { metaKey: true }), context({ index: 2, matches: only('deleteItem') })),
    { kind: 'action', index: 2, action: 'delete' }
  )
})

// A CONFIGURED BINDING MAY BE A BARE LETTER, and type-ahead must not eat it.
test('a configured binding wins over type-ahead for the same character', () => {
  const action = resolveSidebarKey(
    key('l'),
    context({ index: 0, matches: (candidate) => candidate === 'renameItem', typeAhead: '' })
  )
  assert.deepEqual(action, { kind: 'action', index: 0, action: 'rename' })
})

test('typing a letter jumps to the next row that starts with it', () => {
  assert.deepEqual(resolveSidebarKey(key('l'), context({ index: 0 })), { kind: 'focus', index: 2 })
})

test('repeating a letter cycles through the rows that start with it', () => {
  // On "Login" already; a second "l" advances to "Logout" rather than sticking.
  assert.deepEqual(resolveSidebarKey(key('l'), context({ index: 2, typeAhead: 'l' })), { kind: 'focus', index: 3 })
  // And wraps back round.
  assert.deepEqual(resolveSidebarKey(key('l'), context({ index: 3, typeAhead: 'l' })), { kind: 'focus', index: 2 })
})

test('extending the buffer narrows without moving off a row that still matches', () => {
  // "l" landed on Login (index 2); typing "o" makes "lo", which Login still matches.
  assert.deepEqual(resolveSidebarKey(key('o'), context({ index: 2, typeAhead: 'l' })), { kind: 'focus', index: 2 })
})

test('a prefix nothing matches leaves the focus where it is', () => {
  assert.equal(resolveSidebarKey(key('z'), context({ index: 2 })), null)
})

test('a modified character is not type-ahead', () => {
  assert.equal(resolveSidebarKey(key('l', { metaKey: true }), context({ index: 0 })), null)
  assert.equal(resolveSidebarKey(key('l', { ctrlKey: true }), context({ index: 0 })), null)
})

test('an empty tree resolves nothing at all', () => {
  assert.equal(resolveSidebarKey(key('ArrowDown'), context({ rows: [], index: -1 })), null)
})

test('the helpers agree with the row kinds they classify', () => {
  assert.ok(isExpandable(rows[0]))
  assert.ok(isExpandable(rows[1]))
  assert.ok(!isExpandable(rows[2]))
  assert.equal(parentIndex(rows, 2), 1)
  assert.equal(parentIndex(rows, 4), 0)
  assert.equal(parentIndex(rows, 0), -1)
  assert.equal(typeAheadIndex(rows, 0, 'health'), 4)
  assert.equal(typeAheadIndex(rows, 0, ''), -1)
})

// ── Flows are ordinary rows now ─────────────────────────────────────────────
//
// They were not reachable at all: walkSidebar did not emit them, so the cursor
// could never name one, and the markup carried a comment saying it deliberately
// would not try. Nothing in this module needed changing for them — which is the
// claim these tests exist to check, because "it should just work" is precisely
// the assumption that leaves a row type stranded for a second time.

const rowsWithFlows = sidebarRows({
  collections: [{ id: 'c1', name: 'Alpha' }],
  groupsFor: () => [{ folder: '', items: [{ id: 'r1', name: 'Login' }] }],
  collapsedCollections: {},
  collapsedFolders: {},
  searchQuery: '',
  folderKey: (c, f) => `${c}:${f}`,
  flowsFor: () => [{ id: 'f1', name: 'Signup' }]
})
// rows: 0 Alpha(collection) 1 Login(request) 2 Signup(flow)

test('the arrow keys reach a flow row like any other', () => {
  assert.deepEqual(rowsWithFlows.map((row) => row.kind), ['collection', 'request', 'flow'])
  assert.deepEqual(
    resolveSidebarKey(key('ArrowDown'), context({ rows: rowsWithFlows, index: 1 })),
    { kind: 'focus', index: 2 }
  )
  assert.deepEqual(
    resolveSidebarKey(key('Enter'), context({ rows: rowsWithFlows, index: 2 })),
    { kind: 'activate', index: 2 }
  )
})

// A FLOW IS A LEAF. Its steps are not sidebar rows — they live in the flow
// editor — so Right must not pretend there is a branch to open.
test('a flow does not expand, and Left walks out to its collection', () => {
  assert.equal(isExpandable(rowsWithFlows[2]), false)
  assert.equal(resolveSidebarKey(key('ArrowRight'), context({ rows: rowsWithFlows, index: 2 })), null)
  assert.deepEqual(
    resolveSidebarKey(key('ArrowLeft'), context({ rows: rowsWithFlows, index: 2 })),
    { kind: 'focus', index: 0 }
  )
  assert.equal(parentIndex(rowsWithFlows, 2), 0)
})

test('the action menu and type-ahead both work on a flow row', () => {
  assert.deepEqual(
    resolveSidebarKey(key('F10', { shiftKey: true }), context({ rows: rowsWithFlows, index: 2 })),
    { kind: 'menu', index: 2 }
  )
  assert.equal(typeAheadIndex(rowsWithFlows, 0, 'sig'), 2)
})
