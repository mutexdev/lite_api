// The bounded JSON tree, extracted from ResponseInspector.svelte so it can be
// tested at all.
//
// It previously short-circuited to zero entries for any array-rooted JSON,
// while the "JSON tree" button was shown for every parsed JSON body. A list
// endpoint -- the most ordinary JSON response there is -- therefore offered a
// button that swapped the body for a silently empty panel.
import assert from 'node:assert/strict'
import test from 'node:test'

import { JSON_TREE_BUDGET, JSON_TREE_MAX_ENTRIES, boundedJsonTree } from '../src/lib/workbench/jsonTree.ts'

test('an object is listed by key', () => {
  const tree = boundedJsonTree({ name: 'Ada', id: 7 })
  assert.deepEqual(tree.entries.map((entry) => entry.name), ['name', 'id'])
  assert.equal(tree.truncated, false)
})

test('an array is listed by index rather than rendering nothing', () => {
  const tree = boundedJsonTree([1, 2, 3])
  assert.equal(tree.entries.length, 3)
  assert.deepEqual(tree.entries.map((entry) => entry.name), ['0', '1', '2'])
  assert.deepEqual(tree.entries.map((entry) => entry.value), [1, 2, 3])
})

test('an array of objects keeps each element serialised', () => {
  const tree = boundedJsonTree([{ id: 1 }, { id: 2 }])
  assert.equal(tree.entries.length, 2)
  assert.equal(JSON.parse(tree.entries[1].text).id, 2)
})

test('an empty array reports empty rather than truncated', () => {
  const tree = boundedJsonTree([])
  assert.deepEqual(tree.entries, [])
  assert.equal(tree.truncated, false)
})

test('an empty object reports empty rather than truncated', () => {
  const tree = boundedJsonTree({})
  assert.deepEqual(tree.entries, [])
  assert.equal(tree.truncated, false)
})

test('a null value yields nothing to render', () => {
  const tree = boundedJsonTree(null)
  assert.deepEqual(tree.entries, [])
  assert.equal(tree.truncated, false)
})

test('the entry count is bounded, and says so', () => {
  const tree = boundedJsonTree(Array.from({ length: JSON_TREE_MAX_ENTRIES + 25 }, (_, index) => index))
  assert.equal(tree.entries.length, JSON_TREE_MAX_ENTRIES)
  assert.equal(tree.truncated, true)
})

test('the byte budget is bounded, and says so', () => {
  const wide = 'x'.repeat(Math.ceil(JSON_TREE_BUDGET / 4))
  const tree = boundedJsonTree([wide, wide, wide, wide, wide, wide])
  assert.equal(tree.truncated, true)
  assert.ok(tree.entries.length < 6, `expected the budget to stop the render, got ${tree.entries.length} entries`)
})

// A value JSON.stringify refuses -- a circular structure reached through a
// parsed body's prototype, say -- must not take the whole panel down with it.
test('a value that will not serialise is labelled rather than thrown', () => {
  const circular: Record<string, unknown> = { name: 'root' }
  circular.self = circular
  const tree = boundedJsonTree(circular)
  const self = tree.entries.find((entry) => entry.name === 'self')
  assert.ok(self, 'the unserialisable key should still be listed')
  assert.equal(self?.text, '[Unserializable value]')
})

// A primitive is valid JSON, so the button can be shown for one. It has no
// children, and the caller needs to be able to tell that apart from a failure.
test('a primitive body yields no entries', () => {
  assert.deepEqual(boundedJsonTree(42 as unknown as Record<string, unknown>).entries, [])
  assert.deepEqual(boundedJsonTree('text' as unknown as Record<string, unknown>).entries, [])
})
