// The filter contract the three run-results surfaces share.
//
// A8-03 — the Runner, Flow's run panel and History each drew "a list of
// executed requests and what came back", and each grew its filtering (or in the
// Runner's case, did not) on its own. The user-visible cost was that "Failures
// only" meant one thing in History and did not exist in the Runner at all; the
// invisible cost is that a filter is a rule you cannot see working, so two
// slightly different rules look identical until someone notices a row missing.
//
// The rule pinned here is the BACKEND'S. internal/history/history.go:311 keeps
// an entry when `Error != "" || Status >= 400`, which is exactly tone ===
// 'danger'. History's checkbox already meant that; this makes the other two
// mean it too rather than inventing a client-side definition beside it.

import test from 'node:test'
import assert from 'node:assert/strict'
import { runResultMatches, runResultSearchText } from '../src/lib/runResults.ts'

const row = (tone: 'success' | 'warning' | 'danger' | 'idle', searchText: string) => ({ tone, searchText })

// ── The search text ─────────────────────────────────────────────────────────

test('search text is lowercased so the query does not have to be', () => {
  assert.equal(runResultSearchText(['GET', 'Create Terminal']), 'get\ncreate terminal')
})

test('numbers and absent fields are handled without the caller pre-stringifying', () => {
  assert.equal(runResultSearchText(['step', undefined, 404, '']), 'step\n404')
  assert.equal(runResultSearchText([]), '')
  assert.equal(runResultSearchText([undefined, '']), '')
})

// Joined with a newline, not a space. Two adjacent fields joined by a space
// would let "GET /users" match a row whose method is GET and whose next field
// merely starts with /users — a hit on a phrase that appears nowhere.
test('two fields never match as if they were one phrase', () => {
  const text = runResultSearchText(['GET', '/users'])
  assert.ok(!text.includes('get /users'), 'the join must not manufacture a phrase')
  assert.ok(text.includes('get'))
  assert.ok(text.includes('/users'))
})

// ── The filter ──────────────────────────────────────────────────────────────

test('an empty filter keeps everything', () => {
  for (const tone of ['success', 'warning', 'danger', 'idle'] as const) {
    assert.equal(runResultMatches(row(tone, 'anything'), { query: '', onlyFailures: false }), true, tone)
  }
})

test('a query matches on a substring of any searched field', () => {
  const subject = row('success', runResultSearchText(['GET', 'https://example.com/orders', 200]))
  assert.equal(runResultMatches(subject, { query: 'orders', onlyFailures: false }), true)
  assert.equal(runResultMatches(subject, { query: 'GET', onlyFailures: false }), true, 'case-insensitive')
  assert.equal(runResultMatches(subject, { query: '200', onlyFailures: false }), true)
  assert.equal(runResultMatches(subject, { query: 'invoices', onlyFailures: false }), false)
})

test('whitespace around a query is ignored rather than matched literally', () => {
  const subject = row('success', 'create terminal')
  assert.equal(runResultMatches(subject, { query: '  terminal  ', onlyFailures: false }), true)
  assert.equal(runResultMatches(subject, { query: '   ', onlyFailures: false }), true, 'blank is no filter at all')
})

// The definition, stated as a test so a future change to it is a change to a
// sentence rather than to a comparison operator.
test('"failures only" means exactly what the backend means by it', () => {
  assert.equal(runResultMatches(row('danger', ''), { query: '', onlyFailures: true }), true)
  assert.equal(runResultMatches(row('success', ''), { query: '', onlyFailures: true }), false)
  assert.equal(runResultMatches(row('idle', ''), { query: '', onlyFailures: true }), false)
})

// Deliberate: a run the user stopped is amber, not red, and a "failures only"
// list that showed it would make every cancelled run look like a broken
// collection.
test('a cancelled or skipped row is not a failure', () => {
  assert.equal(runResultMatches(row('warning', ''), { query: '', onlyFailures: true }), false)
})

test('the two filters are ANDed, not ORed', () => {
  const failing = row('danger', 'create terminal')
  const passing = row('success', 'create terminal')
  assert.equal(runResultMatches(failing, { query: 'terminal', onlyFailures: true }), true)
  assert.equal(runResultMatches(passing, { query: 'terminal', onlyFailures: true }), false, 'tone still gates it')
  assert.equal(runResultMatches(failing, { query: 'nothing', onlyFailures: true }), false, 'query still gates it')
})
