// The object action registry: one answer to "what can be done to this thing",
// serving every row in the sidebar.
//
// AN EARLIER VERSION OF THIS FILE PINNED THE OLD MARKUP glyph for glyph, to
// prove that replacing two hardcoded button lists with one registry changed
// nothing on screen. That was the right test while the work was meant to be
// invisible. It was deliberately replaced when the row toolbar became the thing
// being fixed: the folder row's seven single-letter buttons and the request
// row's six-word disclosure are both gone, and both rows now open one menu.
//
// What is pinned instead is the ORDER — create, inspect, modify, destroy — and
// the rule that delete stands alone at the end. Those are the properties a
// careless edit would break without anyone noticing until something destructive
// sat where Clone used to be.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  SIDEBAR_ACTION_BINDINGS,
  sidebarActionsFor,
  sidebarObjectForRow,
  type SidebarObject
} from '../src/lib/sidebar/sidebarActions.ts'

/** Stands in for the live keybinding table. */
const shortcutFor = (action: string) =>
  ({ renameItem: '⌘R', cloneItem: '⌘D', deleteItem: '⌘⌫' })[action] ?? ''

const context = { revealLabel: 'Reveal in Finder', supportsGenerateCode: true, shortcutFor }

const folder: SidebarObject = { kind: 'folder', collectionId: 'c1', folder: 'auth', itemId: '', label: 'auth' }
const request: SidebarObject = { kind: 'request', collectionId: 'c1', folder: 'auth', itemId: 'r1', label: 'Login' }

// A FOLDER LEADS WITH THE TWO CREATING ACTIONS, because filling a folder is
// what its menu is opened for — and because "create a directory under a
// directory, then put an API in it" is the flow this ordering exists to serve.
test('a folder leads with New Request and New Folder, then inspect, modify, destroy', () => {
  const actions = sidebarActionsFor(folder, context)

  assert.deepEqual(actions.map((action) => action.id), [
    'new-request', 'new-folder', 'reveal', 'info', 'open-terminal', 'rename', 'clone', 'delete'
  ])
})

test('a request offers the six actions that apply to a leaf', () => {
  const actions = sidebarActionsFor(request, context)

  assert.deepEqual(actions.map((action) => action.id), [
    'reveal', 'generate-code', 'info', 'rename', 'clone', 'delete'
  ])
  // A request is not a container: it can hold neither a request nor a folder.
  assert.ok(!actions.some((action) => action.id === 'new-request'))
  assert.ok(!actions.some((action) => action.id === 'new-folder'))
})

// DELETE IS LAST, ALONE. Pinned separately from the full order because this is
// the property that matters if somebody reorders the list later: a destructive
// entry must not drift up next to an everyday one.
test('delete is the final entry for every kind of object that offers it', () => {
  for (const object of [folder, request]) {
    const actions = sidebarActionsFor(object, context)
    assert.equal(actions.at(-1)?.id, 'delete', `delete is not last for a ${object.kind}`)
  }
})

test('the test ids every action carries are the ones already in the markup', () => {
  const collection: SidebarObject = { kind: 'collection', collectionId: 'c1', folder: '', itemId: '', label: 'Alpha' }
  const ids = new Map(
    [
      ...sidebarActionsFor(collection, context),
      ...sidebarActionsFor(folder, context),
      ...sidebarActionsFor(request, context)
    ].map((action) => [action.id, action.testId])
  )

  assert.deepEqual(Object.fromEntries(ids), {
    'new-request': 'collection-item-menu-new-request',
    'new-flow': 'collection-item-menu-new-flow',
    reveal: 'collection-item-menu-show-in-folder',
    'generate-code': 'collection-item-menu-generate-code',
    info: 'collection-item-menu-info',
    'open-terminal': 'collection-item-menu-open-terminal',
    'new-folder': 'collection-item-menu-new-folder',
    rename: 'collection-item-menu-rename',
    clone: 'collection-item-menu-clone',
    delete: 'collection-item-menu-delete'
  })
})

// ABSENT, NOT DISABLED. The shipped markup wrapped the code button in
// {#if requestSupportsGenerateCode(item)}, and a keyboard menu makes the
// distinction matter more than a pointer menu did: a greyed-out entry is still
// a row the user has to arrow past.
test('a request that cannot generate code omits the action rather than disabling it', () => {
  const actions = sidebarActionsFor(request, { ...context, supportsGenerateCode: false })

  assert.deepEqual(actions.map((action) => action.id), ['reveal', 'info', 'rename', 'clone', 'delete'])
  assert.ok(!actions.some((action) => action.id === 'generate-code'))
})

test('the reveal label follows the platform rather than being fixed', () => {
  const [reveal] = sidebarActionsFor(request, { ...context, revealLabel: 'Reveal in File Explorer' })
  assert.equal(reveal.label, 'Reveal in File Explorer')
})

test('delete is the only action marked dangerous', () => {
  const dangerous = [...sidebarActionsFor(folder, context), ...sidebarActionsFor(request, context)]
    .filter((action) => action.tone === 'danger')
    .map((action) => action.id)

  assert.deepEqual([...new Set(dangerous)], ['delete'])
})

// A MENU MUST NOT ADVERTISE A SHORTCUT NOBODY IMPLEMENTS. The registry and the
// key resolver read the SAME binding names, and this asserts they reach the
// rendered action.
test('the three directly bound actions carry their shortcut into the menu', () => {
  const byId = new Map(sidebarActionsFor(request, context).map((action) => [action.id, action]))

  assert.equal(byId.get('rename')?.shortcut, '⌘R')
  assert.equal(byId.get('clone')?.shortcut, '⌘D')
  assert.equal(byId.get('delete')?.shortcut, '⌘⌫')
  assert.equal(byId.get('info')?.shortcut, undefined, 'info gained a shortcut nothing binds')
})

// The bindings are the ones keybindings.ts already ships, not a second
// vocabulary invented beside them. renameItem and cloneItem have been sitting
// in the Preferences shortcuts sheet, bound and unimplemented.
test('the registry binds the keybinding actions Preferences already lists', () => {
  assert.deepEqual(SIDEBAR_ACTION_BINDINGS, {
    rename: 'renameItem',
    clone: 'cloneItem',
    delete: 'deleteItem'
  })
})

// A user who clears a binding gets no hint, rather than a blank column where
// the hint used to be.
test('an unbound action renders no shortcut hint at all', () => {
  const actions = sidebarActionsFor(request, { ...context, shortcutFor: () => '' })
  const rename = actions.find((action) => action.id === 'rename')
  assert.ok(rename, 'rename is missing')
  assert.equal(rename.shortcut, undefined)
})

// A collection is a container, so it offers the creating actions and nothing
// else. Rename, clone and delete exist for collections but move directories on
// disk and live in the settings pane; putting them on the row is a separate
// decision, not a free addition.
//
// New Flow is on the collection and ONLY on the collection: flows live in the
// collection's root config file and carry no folder path, so offering it on a
// folder would promise a placement that does not exist.
test('a collection offers the creating actions only, New Flow among them', () => {
  const collection: SidebarObject = { kind: 'collection', collectionId: 'c1', folder: '', itemId: '', label: 'Alpha' }
  assert.deepEqual(
    sidebarActionsFor(collection, context).map((action) => action.id),
    ['new-request', 'new-folder', 'new-flow']
  )
  for (const object of [folder, request]) {
    assert.ok(
      !sidebarActionsFor(object, context).some((action) => action.id === 'new-flow'),
      `New Flow should not be offered on a ${object.kind}`
    )
  }
})

test('a response example row maps to no object, because its actions belong to its request', () => {
  assert.equal(
    sidebarObjectForRow({ kind: 'example', collectionId: 'c1', folder: '', itemId: 'r1', label: 'OK' }),
    undefined
  )
  assert.deepEqual(
    sidebarObjectForRow({ kind: 'request', collectionId: 'c1', folder: 'auth', itemId: 'r1', label: 'Login' }),
    { kind: 'request', collectionId: 'c1', folder: 'auth', itemId: 'r1', label: 'Login' }
  )
})

// ── Flows ───────────────────────────────────────────────────────────────────
//
// The flow row was the one row type with no ⋯ menu at all, so a flow could not
// be deleted from the sidebar — only from inside its own open tab. It now
// answers the same question every other object answers.

const flow: SidebarObject = { kind: 'flow', collectionId: 'c1', folder: '', itemId: 'f1', label: 'Signup' }

test('a flow offers the two actions that have real handlers behind them', () => {
  assert.deepEqual(sidebarActionsFor(flow, context).map((action) => action.id), ['reveal', 'delete'])
})

// RENAME IS ABSENT ON PURPOSE, and this test is the guard on that reasoning
// rather than on the omission: there is no RenameFlowModal, so listing Rename
// would put an entry in the menu that opens nothing — the same dead-promise
// failure the ⌘R and ⌘D bindings in Preferences shipped with for a year. When
// the dialog lands, this test changes in the same commit.
test('a flow does not advertise an action the app cannot perform', () => {
  const ids = sidebarActionsFor(flow, context).map((action) => action.id)
  assert.ok(!ids.includes('rename'))
  assert.ok(!ids.includes('clone'))
  // Nor the container actions: nothing is created inside a flow from the tree.
  assert.ok(!ids.includes('new-request'))
  assert.ok(!ids.includes('new-folder'))
  assert.ok(!ids.includes('new-flow'))
})

test('delete is still last and alone once flows are in the registry', () => {
  const actions = sidebarActionsFor(flow, context)
  assert.equal(actions.at(-1)?.id, 'delete')
  assert.equal(actions.at(-1)?.tone, 'danger')
})

test('a flow row resolves to a flow object, carrying the flow id', () => {
  const object = sidebarObjectForRow({
    kind: 'flow', collectionId: 'c1', folder: '', itemId: 'f1', label: 'Signup'
  })

  assert.deepEqual(object, { kind: 'flow', collectionId: 'c1', folder: '', itemId: 'f1', label: 'Signup' })
})

// An example row still has no actions of its own: it is a view of a request,
// and the request's own menu is one row up. Re-pinned here because widening
// sidebarObjectForRow to admit flows is exactly the edit that would let an
// example slip through with it.
test('a response example row still resolves to no object', () => {
  assert.equal(
    sidebarObjectForRow({ kind: 'example', collectionId: 'c1', folder: '', itemId: 'r1', label: 'OK' }),
    undefined
  )
})
