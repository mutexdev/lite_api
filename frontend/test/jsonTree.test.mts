// The bounded JSON tree, extracted from ResponseInspector.svelte so it can be
// tested at all.
//
// It previously short-circuited to zero entries for any array-rooted JSON,
// while the "JSON tree" button was shown for every parsed JSON body. A list
// endpoint -- the most ordinary JSON response there is -- therefore offered a
// button that swapped the body for a silently empty panel.
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  JSON_TREE_BUDGET,
  JSON_TREE_MAX_DEPTH,
  JSON_TREE_MAX_ENTRIES,
  JSON_TREE_MAX_NODES,
  boundedJsonTree,
  countJsonTreeMatches,
  unserializableText
} from '../src/lib/workbench/jsonTree.ts'

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

// This assertion used to read `JSON.parse(entries[1].text).id === 2`, because
// every entry carried a full `JSON.stringify` dump of its value -- which is
// precisely what made the "JSON tree" not a tree. A container now expands into
// child nodes and carries no text of its own; the dump has nowhere left to be.
test('an array of objects expands into child nodes rather than dumping each element', () => {
  const tree = boundedJsonTree([{ id: 1 }, { id: 2 }])
  assert.equal(tree.entries.length, 2)
  assert.equal(tree.entries[1].kind, 'object')
  assert.equal(tree.entries[1].text, '', 'a container renders its children, not a serialisation of itself')
  assert.deepEqual(tree.entries[1].children.map((child) => [child.name, child.text]), [['id', '2']])
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

// A single root field larger than the whole budget yields no entries AND
// truncation at once. The panel renders a message for each state, so it has to
// know this pair is reachable or it renders both and contradicts itself.
test('a first field larger than the budget reports empty and truncated together', () => {
  const tree = boundedJsonTree({ blob: 'x'.repeat(JSON_TREE_BUDGET + 1000) })
  assert.deepEqual(tree.entries, [])
  assert.equal(tree.truncated, true)
})

// --- the tree, once it became one -----------------------------------------
//
// Everything above dates from when this listed root keys and put a
// JSON.stringify dump inside each. The audit's A2-02 is that the control said
// "JSON tree" and shipped one level of accordion over the same flat text. The
// tests below are the other half of that fix: it recurses, and the recursion
// is bounded four separate ways, because "expand everything" over a body sized
// by a remote server is a frozen window -- a worse bug than the flat list.

test('nesting is walked to the bottom, not to the first level', () => {
  const tree = boundedJsonTree({ user: { name: { first: 'Ada' } } })
  const user = tree.entries[0]
  assert.equal(user.kind, 'object')
  const name = user.children[0]
  assert.equal(name.name, 'name')
  assert.deepEqual(name.children.map((child) => [child.name, child.text]), [['first', '"Ada"']])
})

test('a leaf carries JSON text, so the body highlighter can colour it unchanged', () => {
  // The text is handed to highlightSegments(text, 'json') verbatim. A bare
  // `Ada` would scan as nothing at all and render grey, which is the exact
  // complaint -- an uncoloured value -- that this view existed to reproduce.
  const tree = boundedJsonTree({ name: 'Ada', count: 7, ok: true, missing: null })
  assert.deepEqual(
    tree.entries.map((entry) => [entry.kind, entry.text]),
    [['string', '"Ada"'], ['number', '7'], ['boolean', 'true'], ['null', 'null']]
  )
})

test('a container names what is inside it without serialising it', () => {
  const tree = boundedJsonTree({ items: [1, 2, 3], meta: { a: 1, b: 2 } })
  assert.deepEqual(tree.entries.map((entry) => entry.summary), ['Array (3)', 'Object (2)'])
  assert.deepEqual(tree.entries.map((entry) => entry.childCount), [3, 2])
})

test('every node has a path unique across the whole tree', () => {
  // Names are unique only among siblings. The view renders the tree in one
  // recursive pass and keys each {#each} on the path, so a collision would let
  // Svelte reuse one row's <details> for another -- a field that opens itself
  // when an unrelated one is expanded.
  const tree = boundedJsonTree({ a: { id: 1, next: { id: 2 } }, b: { id: 3 } })
  const paths: string[] = []
  const visit = (nodes: typeof tree.entries) => {
    for (const node of nodes) {
      paths.push(node.path)
      visit(node.children)
    }
  }
  visit(tree.entries)
  assert.equal(new Set(paths).size, paths.length, `paths collide: ${paths.join(' ')}`)
})

test('the child cap applies at every level, not only at the root', () => {
  // The bound the flat version had was a root-entry count, and it was the ONLY
  // bound recursion could not inherit: one root key holding 200,000 children is
  // one root entry.
  const wide = Object.fromEntries(Array.from({ length: JSON_TREE_MAX_ENTRIES + 40 }, (_, index) => [`k${index}`, index]))
  const tree = boundedJsonTree({ nested: wide })
  assert.equal(tree.entries[0].children.length, JSON_TREE_MAX_ENTRIES)
  assert.equal(tree.entries[0].collapsed, true, 'the container should admit it is showing part of itself')
  assert.equal(tree.truncated, true)
})

test('depth is bounded, and the node where it stopped says so', () => {
  let deep: Record<string, unknown> = { end: 'bottom' }
  for (let level = 0; level < JSON_TREE_MAX_DEPTH + 6; level += 1) deep = { down: deep }
  const tree = boundedJsonTree(deep)
  let node = tree.entries[0]
  let levels = 1
  while (node.children.length > 0) {
    node = node.children[0]
    levels += 1
  }
  assert.ok(levels <= JSON_TREE_MAX_DEPTH, `built ${levels} levels, past the ${JSON_TREE_MAX_DEPTH} bound`)
  assert.equal(node.collapsed, true)
  assert.equal(tree.truncated, true)
})

test('the total node count is bounded even when no single bound is exceeded', () => {
  // 90 keys of 90 keys breaks neither the per-container cap (100) nor the depth
  // bound (12) nor the leaf budget (the values are one character), and is 8,190
  // rows. The product is the bound that catches it.
  const inner = Object.fromEntries(Array.from({ length: 90 }, (_, index) => [`i${index}`, 0]))
  const outer = Object.fromEntries(Array.from({ length: 90 }, (_, index) => [`o${index}`, inner]))
  const tree = boundedJsonTree(outer)
  assert.ok(tree.nodes <= JSON_TREE_MAX_NODES, `built ${tree.nodes} nodes, past the ${JSON_TREE_MAX_NODES} bound`)
  assert.equal(tree.truncated, true)
})

test('a cycle is caught by identity rather than left to the depth bound', () => {
  // Leaving it to JSON_TREE_MAX_DEPTH would render twelve identical levels of a
  // field that does not exist twelve times -- a tree that lies about the shape
  // of the document, which is worse than one that stops.
  const root: Record<string, unknown> = { name: 'root' }
  root.self = root
  const tree = boundedJsonTree(root)
  const self = tree.entries.find((entry) => entry.name === 'self')
  assert.equal(self?.kind, 'unserializable')
  assert.deepEqual(self?.children, [])
  assert.equal(self?.text, unserializableText)
})

test('the same object reached twice by different paths still renders twice', () => {
  // The ancestor set is popped on the way out. Held for the whole walk it would
  // be a "seen" set, and the second occurrence of a shared value -- which
  // JSON.parse never produces but a caller can hand us -- would be reported as
  // a cycle it is not.
  const shared = { id: 1 }
  const tree = boundedJsonTree({ left: shared, right: shared })
  assert.deepEqual(tree.entries.map((entry) => entry.kind), ['object', 'object'])
  assert.deepEqual(tree.entries.map((entry) => entry.children.length), [1, 1])
  assert.equal(tree.truncated, false)
})

test('the leaf budget is charged once per leaf, not once per level of depth', () => {
  // Charging a container the length of its own serialisation -- which is what
  // the flat version did, because every root entry WAS a serialisation --
  // counts every nested value again at each level, so an ordinary response
  // stops expanding at depth two for a reason nothing on screen explains.
  const leaf = 'x'.repeat(1024)
  const document = { a: { b: { c: { d: { e: leaf } } } } }
  const tree = boundedJsonTree(document)
  let node = tree.entries[0]
  while (node.children.length > 0) node = node.children[0]
  assert.equal(node.name, 'e')
  assert.equal(tree.truncated, false, 'a 1 KB document must not exhaust a 96 KB budget by being nested')
})

// --- the find bar over the tree -------------------------------------------

test('tree matches count names and values, and count nothing for an empty query', () => {
  const tree = boundedJsonTree({ created_at: '2026-01-01', updated: { at: 'later' } })
  assert.equal(countJsonTreeMatches(tree.entries, 'at'), 3)
  assert.equal(countJsonTreeMatches(tree.entries, '   '), 0)
})

test('tree matches are case-insensitive, as the body find bar is', () => {
  const tree = boundedJsonTree({ Name: 'Ada' })
  assert.equal(countJsonTreeMatches(tree.entries, 'name'), 1)
  assert.equal(countJsonTreeMatches(tree.entries, 'ADA'), 1)
})

test('a container is not matched by the values buried inside it', () => {
  // Its own text is '', so a hit on a descendant counts once, on the
  // descendant. Counting the ancestor too would report a number larger than
  // the number of marks the view paints.
  const tree = boundedJsonTree({ outer: { inner: 'needle' } })
  assert.equal(countJsonTreeMatches(tree.entries, 'needle'), 1)
})
