// The one rule for "is this status good", held to every boundary it has.
//
// A8-01 was not a bug anyone could see in one place: the app had four rules and
// they only disagreed if you carried the same request across four panels. A 302
// was green in History, amber in the response pane, uncoloured in the Runner and
// ungraded in a Flow step, and each of those looked perfectly reasonable on its
// own screen. Nothing threw, nothing logged, and no test compared them.
//
// So the table below is the comparison. It walks every status class and both
// sides of every boundary — 0, 199/200, 299/300, 399/400, 499/500, 599/600 —
// because a bucketing function is exactly the kind of code where the only bugs
// that survive review are off-by-one at a boundary nobody wrote a case for. The
// 2xx/3xx boundary in particular is the one the whole finding turns on.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  outcomeTone,
  resultTone,
  statusTone,
  toneClass,
  toneLabel,
  type StatusTone,
} from '../src/lib/statusTone.ts'

// ── The bucketing, boundary by boundary ─────────────────────────────────────

// Every entry names WHY it sits where it does, so a future change that moves a
// boundary has to argue with a sentence rather than edit a number.
const statusCases: [number | undefined, StatusTone, string][] = [
  [undefined, 'idle', 'no response at all'],
  [0, 'idle', 'the zero the wire type uses for "never ran"'],
  [-1, 'idle', 'a negative code is not a code'],
  [1, 'success', 'above zero and below 300'],
  [99, 'success', 'below the 1xx class but still a positive code'],
  [100, 'success', 'first informational — unreachable in practice, see the module note'],
  [199, 'success', 'last informational'],
  [200, 'success', 'first success'],
  [201, 'success', 'created'],
  [204, 'success', 'no content is still a success'],
  [299, 'success', 'last success'],
  [300, 'warning', 'FIRST REDIRECT — the boundary the whole finding turns on'],
  [301, 'warning', 'moved permanently: the payload is one hop away'],
  [302, 'warning', 'the exact code the audit carried across four panels'],
  [304, 'warning', 'not modified — nothing failed and nothing was delivered'],
  [308, 'warning', 'permanent redirect'],
  [399, 'warning', 'last redirect'],
  [400, 'danger', 'first client error'],
  [401, 'danger', 'unauthorised'],
  [404, 'danger', 'not found'],
  [418, 'danger', 'an unassigned 4xx is still a 4xx'],
  [499, 'danger', 'last client error'],
  [500, 'danger', 'first server error'],
  [503, 'danger', 'unavailable'],
  [599, 'danger', 'last server error'],
  [600, 'danger', 'past every assigned class — a code we cannot vouch for is not a pass'],
  [999, 'danger', 'nonsense high code'],
]

test('every status class and both sides of every boundary bucket the same way', () => {
  for (const [status, expected, why] of statusCases) {
    assert.equal(statusTone(status), expected, `${status}: ${why}`)
  }
})

// NaN is the case that makes the guard order matter. It is neither < 300 nor
// >= 400, so without the isFinite check ahead of the comparisons it falls
// through to the final else and a status we could not parse renders as a
// failure — a red row for a response that may well have been fine.
test('an unparseable status is idle, not a failure', () => {
  assert.equal(statusTone(Number.NaN), 'idle')
  assert.equal(statusTone(Number('nope')), 'idle')
  assert.equal(statusTone(Infinity), 'idle', 'an infinite status is not a status either')
})

// The behaviour A8-01 is actually about, stated as one assertion: whatever the
// panel, the same code gets the same grade.
test('a 3xx is a warning and a 2xx is a success, which is the disagreement A8-01 named', () => {
  assert.equal(statusTone(200), 'success')
  assert.equal(statusTone(302), 'warning')
  assert.notEqual(statusTone(200), statusTone(302), 'History used to grade these identically')
})

// ── Whole results, where the precedence lives ───────────────────────────────

test('a transport error outranks whatever status came with it', () => {
  assert.equal(resultTone({ status: 200, error: 'unexpected EOF' }), 'danger')
  assert.equal(resultTone({ status: 0, error: 'dial tcp: connection refused' }), 'danger')
  assert.equal(resultTone({ error: '' }), 'idle', 'an empty error is not an error')
})

// A cancel is the user's own doing. Painting it the same red as a 500 makes
// "I hit Cancel" show up in a run report as a fault.
test('cancellation outranks everything and is amber, not red', () => {
  assert.equal(resultTone({ status: 500, error: 'boom', cancelled: true }), 'warning')
  assert.equal(resultTone({ status: 200, cancelled: true }), 'warning')
})

test('a result with neither error nor cancellation is graded on its code alone', () => {
  for (const [status, expected] of statusCases) {
    assert.equal(resultTone({ status }), expected, `status ${status}`)
  }
})

// ── The Runner's verdict words ──────────────────────────────────────────────

// These are internal/core/app_runner.go's four, verbatim. A fifth added on the
// backend must show up uncoloured rather than mis-coloured, which is why the
// default is 'idle' and not one of the three grades.
test('every runner verdict the backend emits has a tone', () => {
  assert.equal(outcomeTone('passed'), 'success')
  assert.equal(outcomeTone('failed'), 'danger')
  assert.equal(outcomeTone('skipped'), 'warning')
  assert.equal(outcomeTone('cancelled'), 'warning')
})

test('an unknown or missing verdict is ungraded rather than guessed at', () => {
  assert.equal(outcomeTone('quarantined'), 'idle')
  assert.equal(outcomeTone(''), 'idle')
  assert.equal(outcomeTone(undefined), 'idle')
})

test('verdict matching is case-insensitive, because the wire word is not a style choice', () => {
  assert.equal(outcomeTone('Passed'), 'success')
  assert.equal(outcomeTone('CANCELLED'), 'warning')
})

// ── The mapping onto the stylesheet ─────────────────────────────────────────

// `.warning` is NOT a global class — style.css defines it only as
// `.runner-summary .warning`, scoped to that one bar. A tone that mapped to it
// would paint nothing on a row and the failure would be invisible: correct
// markup, no colour.
test('every tone maps to a class the global stylesheet actually defines', () => {
  assert.equal(toneClass('success'), 'ok')
  assert.equal(toneClass('warning'), 'warn')
  assert.equal(toneClass('danger'), 'bad')
  assert.equal(toneClass('idle'), '', 'no grade means no badge, not a grey one')
})

test('every tone that means something has a word for a screen reader', () => {
  assert.equal(toneLabel('success'), 'succeeded')
  assert.equal(toneLabel('warning'), 'needs attention')
  assert.equal(toneLabel('danger'), 'failed')
  assert.equal(toneLabel('idle'), '', 'nothing to announce about a row that has not run')
})

// The two mappings have to stay total: a tone added later with no class or no
// label would silently render an uncoloured, unannounced row.
test('the class and label mappings cover every tone in the union', () => {
  const tones: StatusTone[] = ['success', 'warning', 'danger', 'idle']
  for (const tone of tones) {
    assert.equal(typeof toneClass(tone), 'string', tone)
    assert.equal(typeof toneLabel(tone), 'string', tone)
  }
  const graded = tones.filter((tone) => tone !== 'idle')
  assert.equal(new Set(graded.map(toneClass)).size, graded.length, 'no two grades share a class')
  assert.equal(new Set(graded.map(toneLabel)).size, graded.length, 'no two grades share a word')
})
