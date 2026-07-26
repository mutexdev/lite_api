import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  appendedRow,
  blankKeyValueRow,
  movedRows,
  normalizeBulkKeyValueRows,
  removedRow,
  reorderedRows,
  updatedRow,
} from '../src/lib/rowEdits.ts'

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
test('every path returns a new array, including the ones that change nothing', () => {
  assert.notEqual(movedRows(rows, 0, -1), rows)
  assert.notEqual(reorderedRows(rows, 1, 1), rows)
  assert.notEqual(removedRow(rows, 99), rows)
  assert.notEqual(updatedRow([{ a: 1 }], 0, 'a', 2, () => ({ a: 0 })), rows as never)
})

test('none of them mutate the input', () => {
  const original = [...rows]
  movedRows(original, 1, 1)
  reorderedRows(original, 0, 3)
  removedRow(original, 0)
  appendedRow(original, 'e')
  assert.deepEqual(original, rows)
})

test('an absent list is treated as empty', () => {
  assert.deepEqual(movedRows(undefined, 0, 1), [])
  assert.deepEqual(reorderedRows(undefined, 0, 1), [])
  assert.deepEqual(removedRow(undefined, 0), [])
  assert.deepEqual(appendedRow(undefined, 'a'), ['a'])
})

test('a row is removed by index', () => {
  assert.deepEqual(removedRow(rows, 1), ['a', 'c', 'd'])
  assert.deepEqual(removedRow(rows, 0), ['b', 'c', 'd'])
  assert.deepEqual(removedRow(rows, 3), ['a', 'b', 'c'])
})

test('removing an index that is not there leaves the list intact', () => {
  assert.deepEqual(removedRow(rows, -1), rows)
  assert.deepEqual(removedRow(rows, 4), rows)
})

test('one field of one row is written without touching the others', () => {
  const table = [blankKeyValueRow(), { ...blankKeyValueRow(), name: 'keep' }]
  const next = updatedRow(table, 0, 'name', 'set', blankKeyValueRow)
  assert.equal(next[0].name, 'set')
  assert.equal(next[0].value, '')
  assert.equal(next[1].name, 'keep')
})

// The table renders from state a background refresh can shorten between the
// render and the keystroke. Losing the character the user just typed is worse
// than gaining a row they can delete.
test('writing past the end fills a blank row rather than dropping the edit', () => {
  const next = updatedRow([blankKeyValueRow()], 2, 'name', 'typed', blankKeyValueRow)
  assert.equal(next[2].name, 'typed')
  assert.equal(next[2].enabled, true, 'the filled row carries the blank defaults')
})

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
test('a blank row is enabled and not secret', () => {
  const blank = blankKeyValueRow()
  assert.equal(blank.enabled, true)
  assert.equal(blank.secret, false)
  assert.equal(blank.name, '')
  assert.equal(blank.description, '')
})
