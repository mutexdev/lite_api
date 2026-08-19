import { test } from 'node:test'
import assert from 'node:assert/strict'
import { movedRows, normalizeBulkKeyValueRows, reorderedRows } from '../src/lib/rowEdits.ts'

const rows = ['a', 'b', 'c', 'd']

test('a row moves one position in either direction', () => {
  assert.deepEqual(movedRows(rows, 1, -1), ['b', 'a', 'c', 'd'])
  assert.deepEqual(movedRows(rows, 1, 1), ['a', 'c', 'b', 'd'])
})

// The up-arrow on the first row is visible and clickable. Wrapping it to the
// bottom would be a reorder the user did not ask for.
test('a move off either end leaves the order alone rather than wrapping', () => {
  assert.deepEqual(movedRows(rows, 0, -1), rows)
  assert.deepEqual(movedRows(rows, 3, 1), rows)
  assert.deepEqual(movedRows(rows, -1, 1), rows)
  assert.deepEqual(movedRows(rows, 99, -1), rows)
})

test('a row drags to an arbitrary position', () => {
  assert.deepEqual(reorderedRows(rows, 0, 2), ['b', 'c', 'a', 'd'])
  assert.deepEqual(reorderedRows(rows, 3, 0), ['d', 'a', 'b', 'c'])
})

// The array is one shorter once the dragged row is spliced out, so dropping
// onto the last position gives an insertion index equal to the new length.
test('dragging a row to the last position keeps it, at the end', () => {
  assert.deepEqual(reorderedRows(rows, 0, 3), ['b', 'c', 'd', 'a'])
  assert.equal(reorderedRows(rows, 0, 3).length, rows.length, 'a row was dropped')
})

test('an out-of-range or no-op drag changes nothing', () => {
  assert.deepEqual(reorderedRows(rows, 1, 1), rows)
  assert.deepEqual(reorderedRows(rows, -1, 2), rows)
  assert.deepEqual(reorderedRows(rows, 2, 99), rows)
})

// Svelte's reactivity is identity-based. A handler that returns the same array
// leaves the table showing its pre-drag order until something else happens to
// invalidate it — so even the no-op paths must return a new array.
// The table renders from state a background refresh can shorten between the
// render and the keystroke. Losing the character the user just typed is worse
// than gaining a row they can delete.
// The bulk editor reads "name: value" lines and has nothing to say about
// `secret` or `description`. Leaving them undefined writes a different file
// each time the same table is saved.
test('bulk rows gain the fields the bulk editor cannot parse', () => {
  const [row] = normalizeBulkKeyValueRows([{ name: 'a', value: 'b', enabled: true }])
  assert.equal(row.secret, false)
  assert.equal(row.description, '')
  assert.equal(row.enabled, true)
})

test('bulk rows keep the fields that were supplied', () => {
  const [row] = normalizeBulkKeyValueRows([
    { name: 'a', value: 'b', enabled: false, secret: true, description: 'note' },
  ])
  assert.equal(row.secret, true)
  assert.equal(row.description, 'note')
  assert.equal(row.enabled, false)
})

// enabled is NOT defaulted the way secret and description are — it is a real
// field of the bulk format (a commented-out line is a disabled row), so an
// absent value would be a parse bug rather than something to fill in.