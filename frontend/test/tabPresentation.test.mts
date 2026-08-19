import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  collectionIsScratch,
  findResponseExampleForTab,
  methodLabel,
  responseExampleIdentifier,
  sidebarFolderKey,
  tabLabel,
  tabMethod,
} from '../src/lib/workbench/tabPresentation.ts'
import type { types } from '../wailsjs/go/models'

const example = { id: 'ex-1', name: 'Success' } as types.ResponseExample
const legacyExample = { name: 'Legacy' } as types.ResponseExample

const collections = [
  {
    id: 'col-1',
    items: [
      { id: 'req-1', name: 'Get user', method: 'delete', examples: [example, legacyExample] },
    ],
  },
] as types.Collection[]

function tab(over: Partial<types.OpenTab> = {}): types.OpenTab {
  return { collectionId: 'col-1', itemId: 'req-1', kind: 'request', ...over } as types.OpenTab
}

// Examples written by earlier versions have no id at all. Dropping the fallback
// makes every one of them unaddressable — they render, and nothing can be
// opened or renamed.
test('an example with no id is identified by its name', () => {
  assert.equal(responseExampleIdentifier(example), 'ex-1')
  assert.equal(responseExampleIdentifier(legacyExample), 'Legacy')
})

test('a response-example tab resolves by id', () => {
  const found = findResponseExampleForTab(
    tab({ kind: 'response-example', exampleId: 'ex-1' }),
    collections,
  )
  assert.equal(found?.name, 'Success')
})

// A tab opened before the example gained an id must still resolve after an
// upgrade rewrites the file. One comparison leaves those tabs pointing at
// nothing, which renders as a blank example pane rather than as an error.
test('a response-example tab resolves by name when it has no id', () => {
  const found = findResponseExampleForTab(
    tab({ kind: 'response-example', exampleName: 'Success' }),
    collections,
  )
  assert.equal(found?.id, 'ex-1')
})

test('a request tab resolves to no example', () => {
  assert.equal(findResponseExampleForTab(tab(), collections), undefined)
  assert.equal(findResponseExampleForTab(undefined, collections), undefined)
})

test('a tab pointing at a collection that is gone resolves to nothing', () => {
  assert.equal(findResponseExampleForTab(tab({ kind: 'response-example', collectionId: 'gone', exampleId: 'ex-1' }), collections), undefined)
  assert.equal(tabMethod(tab({ collectionId: 'gone' }), collections), '')
})

test('a tab is labelled with its request name', () => {
  assert.equal(tabLabel(tab(), collections), 'Get user')
})

// "Scratch request" says the tab holds something never written to disk, which
// is the one thing a user needs to know before closing it.
test('an unresolvable transient tab says so, and a saved one does not', () => {
  assert.equal(tabLabel(tab({ itemId: 'gone', transient: true }), collections), 'Scratch request')
  assert.equal(tabLabel(tab({ itemId: 'gone' }), collections), 'Request')
  assert.notEqual(
    tabLabel(tab({ itemId: 'gone', transient: true }), collections),
    tabLabel(tab({ itemId: 'gone' }), collections),
  )
})

test('an example tab falls back through its stored name', () => {
  assert.equal(
    tabLabel(tab({ kind: 'response-example', exampleId: 'ex-1' }), collections),
    'Success',
  )
  assert.equal(
    tabLabel(tab({ kind: 'response-example', exampleName: 'Deleted one' }), collections),
    'Deleted one',
  )
  assert.equal(tabLabel(tab({ kind: 'response-example' }), collections), 'Example')
})

// Only DELETE and OPTIONS are long enough to widen the badge past the tab
// strip's fixed column. Abbreviating the rest trades legibility for nothing.
test('only the two long methods are abbreviated', () => {
  assert.equal(methodLabel('delete'), 'DEL')
  assert.equal(methodLabel('OPTIONS'), 'OPT')
  for (const method of ['get', 'post', 'put', 'patch', 'head', 'trace']) {
    assert.equal(methodLabel(method), method.toUpperCase(), method)
  }
  assert.equal(methodLabel(''), '')
})

test('a request tab shows its method and an example tab shows none', () => {
  assert.equal(tabMethod(tab(), collections), 'delete')
  assert.equal(tabMethod(tab({ kind: 'response-example', exampleId: 'ex-1' }), collections), '')
})

// NUL is the one byte that cannot appear in a collection id or a folder path. A
// printable separator would let collection "a" folder "b/c" and collection
// "a/b" folder "c" produce the same key, and the two folders would collapse and
// expand together.
test('the folder key cannot be produced by two different pairs', () => {
  assert.notEqual(sidebarFolderKey('a', 'b/c'), sidebarFolderKey('a/b', 'c'))
  assert.notEqual(sidebarFolderKey('a', 'b:c'), sidebarFolderKey('a:b', 'c'))
  assert.notEqual(sidebarFolderKey('a', 'b c'), sidebarFolderKey('a b', 'c'))
  assert.notEqual(sidebarFolderKey('a', 'b-c'), sidebarFolderKey('a-b', 'c'))
  assert.notEqual(sidebarFolderKey('a', ''), sidebarFolderKey('', 'a'))
})

test('the folder key separator is a byte no path can contain', () => {
  assert.ok(sidebarFolderKey('col', 'folder').includes('\u0000'))
})

test('the same pair always produces the same key', () => {
  assert.equal(sidebarFolderKey('col-1', 'auth'), sidebarFolderKey('col-1', 'auth'))
})

// Two sources, and both are needed: a collection loaded before the flag existed
// has only the workspace's id, and one belonging to another workspace has only
// its own flag.
test('a scratch collection is recognised from either source', () => {
  assert.equal(collectionIsScratch({ id: 'c', scratch: true } as types.Collection, undefined), true)
  assert.equal(collectionIsScratch({ id: 'c' } as types.Collection, 'c'), true)
  assert.equal(collectionIsScratch({ id: 'c' } as types.Collection, 'other'), false)
  assert.equal(collectionIsScratch(undefined, 'c'), false)
})

// An absent scratch id must not match a collection whose id is also absent —
// every collection would read as scratch, and scratch collections are excluded
// from git, export and the collection list.
test('an absent scratch id matches nothing', () => {
  assert.equal(collectionIsScratch({ id: '' } as types.Collection, ''), false)
  assert.equal(collectionIsScratch({ id: '' } as types.Collection, undefined), false)
})
