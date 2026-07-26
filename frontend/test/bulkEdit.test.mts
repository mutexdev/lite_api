// US-056 — tests for bulk edit round-tripping.
//
// The story's criterion is round-tripping "without losing disabled state", and
// these tests take that literally AND cover the thing it does not name: a text
// format has nowhere to put `secret`, so the obvious implementation resets it
// and a credential quietly starts being written in the clear.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { rowsToBulkText, parseBulkText, bulkTextIsLossy, type BulkRow } from '../src/lib/bulkEdit.ts'

const rows: BulkRow[] = [
  { name: 'Accept', value: 'application/json', enabled: true, secret: false, description: 'content negotiation' },
  { name: 'Authorization', value: 'Bearer abc123', enabled: true, secret: true, description: '' },
  { name: 'X-Debug', value: 'true', enabled: false, secret: false, description: 'off for now' }
]

test('rows round-trip through text unchanged', () => {
  const parsed = parseBulkText(rowsToBulkText(rows), rows)
  assert.deepEqual(parsed, rows)
})

// The named criterion.
test('disabled state survives the round trip', () => {
  const parsed = parseBulkText(rowsToBulkText(rows), rows)
  assert.equal(parsed[0].enabled, true)
  assert.equal(parsed[1].enabled, true)
  assert.equal(parsed[2].enabled, false, 'the disabled row came back enabled')
})

// The criterion it does not name, and the one with real consequences: without
// carrying it over, a secret row silently becomes non-secret and its value
// starts being written out in the clear.
test('secret and description survive the round trip', () => {
  const parsed = parseBulkText(rowsToBulkText(rows), rows)
  assert.equal(parsed[1].secret, true, 'a secret row came back non-secret')
  assert.equal(parsed[0].description, 'content negotiation')
  assert.equal(parsed[2].description, 'off for now')
})

test('metadata is carried per occurrence for duplicated names', () => {
  const duplicated: BulkRow[] = [
    { name: 'X-Trace', value: 'one', enabled: true, secret: true, description: 'first' },
    { name: 'X-Trace', value: 'two', enabled: true, secret: false, description: 'second' }
  ]
  const parsed = parseBulkText(rowsToBulkText(duplicated), duplicated)
  // Matching by name alone would give both rows the first row's metadata.
  assert.equal(parsed[0].secret, true)
  assert.equal(parsed[1].secret, false)
  assert.equal(parsed[0].description, 'first')
  assert.equal(parsed[1].description, 'second')
})

test('a new row added in the text gets defaults rather than borrowed metadata', () => {
  const parsed = parseBulkText(rowsToBulkText(rows) + '\nX-New: value', rows)
  const added = parsed[parsed.length - 1]
  assert.equal(added.name, 'X-New')
  assert.equal(added.secret, false)
  assert.equal(added.description, '')
})

// A colon inside a value is ordinary — a URL, a timestamp — and splitting at
// the wrong one truncates the value at its own punctuation.
test('only the first separator splits the line', () => {
  const parsed = parseBulkText('Referer: https://example.test:8443/a?b=c')
  assert.equal(parsed[0].name, 'Referer')
  assert.equal(parsed[0].value, 'https://example.test:8443/a?b=c')
})

test('an equals sign works as a separator when it comes first', () => {
  const parsed = parseBulkText('page=2')
  assert.equal(parsed[0].name, 'page')
  assert.equal(parsed[0].value, '2')
})

test('a colon before an equals still wins', () => {
  const parsed = parseBulkText('Cookie: a=1; b=2')
  assert.equal(parsed[0].name, 'Cookie')
  assert.equal(parsed[0].value, 'a=1; b=2')
})

// Postman writes `//`; this app previously wrote `~`. Both must parse or
// existing text stops round-tripping and pasted Postman text does not work.
test('both disabled markers are accepted on input', () => {
  const parsed = parseBulkText('// A: 1\n~B: 2\nC: 3')
  assert.deepEqual(
    parsed.map((row) => [row.name, row.enabled]),
    [
      ['A', false],
      ['B', false],
      ['C', true]
    ]
  )
})

test('the emitted marker is Postman’s', () => {
  const text = rowsToBulkText([{ name: 'X', value: '1', enabled: false }])
  assert.ok(text.startsWith('//'), `expected a Postman-style marker, got ${text}`)
})

// A line-based format splits a multi-line value into bogus rows. This table
// supports multi-line values, so that would be silent data loss the user meets
// much later.
test('a value containing a newline round-trips instead of splitting the row', () => {
  const multiline: BulkRow[] = [{ name: 'X-Body', value: 'line one\nline two', enabled: true }]
  const text = rowsToBulkText(multiline)
  assert.equal(text.split('\n').length, 1, 'the value split the row in two')
  const parsed = parseBulkText(text, multiline)
  assert.equal(parsed.length, 1)
  assert.equal(parsed[0].value, 'line one\nline two')
})

test('a backslash survives and does not grow on repeated round trips', () => {
  const withBackslash: BulkRow[] = [{ name: 'Path', value: 'C:\\Users\\ada', enabled: true }]
  let current = withBackslash
  for (let i = 0; i < 5; i++) {
    current = parseBulkText(rowsToBulkText(current), current)
  }
  assert.equal(current[0].value, 'C:\\Users\\ada', 'the backslash grew or shrank across round trips')
})

test('an escaped newline and a literal backslash-n are distinguishable', () => {
  const both: BulkRow[] = [
    { name: 'real', value: 'a\nb', enabled: true },
    { name: 'literal', value: 'a\\nb', enabled: true }
  ]
  const parsed = parseBulkText(rowsToBulkText(both), both)
  assert.equal(parsed[0].value, 'a\nb')
  assert.equal(parsed[1].value, 'a\\nb')
})

test('blank lines are dropped', () => {
  const parsed = parseBulkText('A: 1\n\n   \nB: 2')
  assert.equal(parsed.length, 2)
})

test('a line with no separator becomes a name with an empty value', () => {
  const parsed = parseBulkText('Half-typed')
  assert.deepEqual(parsed, [{ name: 'Half-typed', value: '', enabled: true, secret: false, description: '' }])
})

test('an empty text produces no rows', () => {
  assert.deepEqual(parseBulkText(''), [])
  assert.deepEqual(parseBulkText('\n\n'), [])
})

test('parsing without previous rows still works', () => {
  const parsed = parseBulkText('A: 1')
  assert.equal(parsed[0].secret, false)
  assert.equal(parsed[0].description, '')
})

// The one genuinely unrepresentable case. It is reported rather than blocked:
// silently refusing bulk edit on a table is more confusing than a note.
test('bulkTextIsLossy flags names the format cannot carry', () => {
  assert.equal(bulkTextIsLossy(rows), false)
  assert.equal(bulkTextIsLossy([{ name: 'has:colon', value: '1', enabled: true }]), true)
  assert.equal(bulkTextIsLossy([{ name: 'has=equals', value: '1', enabled: true }]), true)
  assert.equal(bulkTextIsLossy([{ name: '//looks-disabled', value: '1', enabled: true }]), true)
  assert.equal(bulkTextIsLossy([{ name: '~also', value: '1', enabled: true }]), true)
})

test('bulkTextIsLossy ignores empty names and normal values', () => {
  assert.equal(bulkTextIsLossy([{ name: '', value: 'a:b', enabled: true }]), false)
  assert.equal(bulkTextIsLossy([{ name: 'Accept', value: 'text/html;q=0.9', enabled: true }]), false)
})

test('repeated round trips are stable', () => {
  let current = rows
  const first = rowsToBulkText(current)
  for (let i = 0; i < 5; i++) {
    current = parseBulkText(rowsToBulkText(current), current)
  }
  assert.equal(rowsToBulkText(current), first, 'the text drifted across round trips')
  assert.deepEqual(current, rows)
})
