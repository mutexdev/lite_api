// The app's four value formats, pinned.
//
// These tests exist because the thing they guard is invisible. The audit found
// seven separate inline copies of `${ms} ms` and they all AGREED — by luck, not
// by anything holding them there. Nothing in the repo compared one surface's
// wording to another's, so the first `200ms` or the first `Math.floor` would
// have shipped, looked fine in its own pane, and only read as wrong beside the
// pane next to it.
//
// The byte formatter's cases are lifted from test/commandState.test.mts on
// purpose: formatBytes is meant to replace formatRuntimeBytes exactly, and two
// suites asserting the same outputs is how "exactly" gets proved rather than
// claimed.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  formatBytes,
  formatDurationMs,
  formatDurationMsOrZero,
  formatRelativeTime,
  formatStatusCode,
  formatWallClockTime,
} from '../src/lib/formatting.ts'

// ── Durations ───────────────────────────────────────────────────────────────

test('a duration is a rounded whole number and a space before the unit', () => {
  assert.equal(formatDurationMs(0), '0 ms')
  assert.equal(formatDurationMs(1), '1 ms')
  assert.equal(formatDurationMs(412), '412 ms')
  assert.equal(formatDurationMs(412.4), '412 ms')
  assert.equal(formatDurationMs(412.6), '413 ms', 'rounded, not truncated')
  assert.equal(formatDurationMs(0.4), '0 ms', 'sub-millisecond is a real measurement')
})

// The space is the format. Welded to its digits the number reads as one longer
// number in a row that already has a status code and a byte count in it.
test('the unit is separated by a space, in every case', () => {
  for (const value of [0, 1, 999, 1000, 123456]) {
    assert.match(formatDurationMs(value), /^\d+ ms$/, String(value))
  }
})

// Blank rather than "0 ms" for the unusable cases: these labels sit in LISTS,
// and "0 ms" on a flow step that has not run claims the step ran and was
// instantaneous.
test('there is no measurement to report for a missing or impossible duration', () => {
  assert.equal(formatDurationMs(undefined), '0 ms', 'absent reads as zero, which is what the wire sends')
  assert.equal(formatDurationMs(-1), '', 'negative is not a duration')
  assert.equal(formatDurationMs(Number.NaN), '')
  assert.equal(formatDurationMs(Infinity), '')
})

test('the OrZero variant always fills its slot', () => {
  assert.equal(formatDurationMsOrZero(undefined), '0 ms')
  assert.equal(formatDurationMsOrZero(-1), '0 ms')
  assert.equal(formatDurationMsOrZero(Number.NaN), '0 ms')
  assert.equal(formatDurationMsOrZero(413), '413 ms')
})

// ── Bytes ───────────────────────────────────────────────────────────────────

test('a byte size uses the response strip’s existing rule, unchanged', () => {
  assert.equal(formatBytes(undefined), '0 B')
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(512), '512 B', 'bytes are never fractional')
  assert.equal(formatBytes(1023), '1023 B', 'the last value before the unit changes')
  assert.equal(formatBytes(1024), '1.0 KB')
  assert.equal(formatBytes(1536), '1.5 KB')
  assert.equal(formatBytes(10 * 1024), '10 KB', 'past ten the decimal is noise')
  assert.equal(formatBytes(10 * 1024 - 1), '10.0 KB', 'just under ten still carries it')
  assert.equal(formatBytes(1024 * 1024), '1.0 MB')
  assert.equal(formatBytes(5 * 1024 * 1024 * 1024), '5.0 GB')
  assert.equal(formatBytes(1024 ** 5), '1048576 GB', 'the largest unit absorbs the rest rather than overflowing')
})

test('every byte size separates its unit with a space too', () => {
  for (const value of [1, 1024, 1024 ** 2, 1024 ** 3]) {
    assert.match(formatBytes(value), /^[\d.]+ (B|KB|MB|GB)$/, String(value))
  }
})

// ── Status codes in a results column ────────────────────────────────────────

// A row that carries a transport error instead of a code still needs something
// in the column, or the grid collapses and stops lining up with its neighbours.
test('a status column always has something in it', () => {
  assert.equal(formatStatusCode(200), '200')
  assert.equal(formatStatusCode(302), '302')
  assert.equal(formatStatusCode(500), '500')
  assert.equal(formatStatusCode(0, 'connection refused'), 'error')
  assert.equal(formatStatusCode(undefined, 'dial tcp'), 'error')
  assert.equal(formatStatusCode(undefined), '—', 'an em dash for a row that has not run')
  assert.equal(formatStatusCode(0), '—')
})

test('a real code wins over an error, because the code is the more specific fact', () => {
  assert.equal(formatStatusCode(404, 'not found'), '404')
})

// ── Relative time ───────────────────────────────────────────────────────────

const at = Date.UTC(2026, 7, 31, 12, 0, 0)

test('relative time steps through seconds, minutes, hours and days', () => {
  const cases: [number, string][] = [
    [0, '0s ago'],
    [1_000, '1s ago'],
    [59_000, '59s ago'],
    [60_000, '1m ago'],
    [3_599_000, '59m ago'],
    [3_600_000, '1h ago'],
    [86_399_000, '23h ago'],
    [86_400_000, '1d ago'],
    [86_400_000 * 400, '400d ago', ] as [number, string],
  ]
  for (const [elapsed, expected] of cases) {
    assert.equal(formatRelativeTime(new Date(at).toISOString(), at + elapsed), expected, `${elapsed}ms`)
  }
})

// A machine whose clock stepped backwards between recording an entry and
// rendering it would otherwise produce "-3s ago", which reads as a bug in the
// app rather than in the clock.
test('an entry from the future is clamped rather than shown as negative', () => {
  assert.equal(formatRelativeTime(new Date(at).toISOString(), at - 5_000), '0s ago')
})

test('an absent or unparseable timestamp says nothing at all', () => {
  assert.equal(formatRelativeTime(undefined), '')
  assert.equal(formatRelativeTime(''), '')
  assert.equal(formatRelativeTime('not a date'), '')
})

test('relative time accepts what the wire actually sends', () => {
  assert.equal(formatRelativeTime(new Date(at), at + 5_000), '5s ago')
  assert.equal(formatRelativeTime(new Date(at).toISOString(), at + 5_000), '5s ago')
})

// ── Wall-clock time ─────────────────────────────────────────────────────────

// The other sanctioned style. It is locale-dependent by design — it is the one
// value a user may need to correlate with something outside the app — so this
// asserts that it produces SOMETHING for a valid date and nothing for an
// invalid one, rather than pinning a locale the test machine does not control.
test('wall-clock time renders for a valid date and stays blank otherwise', () => {
  assert.notEqual(formatWallClockTime(new Date(at).toISOString()), '')
  assert.equal(formatWallClockTime(undefined), '')
  assert.equal(formatWallClockTime(''), '')
  assert.equal(formatWallClockTime('not a date'), '')
})

// The two styles answer different questions and must not be collapsed into one
// helper later by someone who reads them as duplicates.
test('the two timestamp styles are genuinely different answers', () => {
  const relative = formatRelativeTime(new Date(at).toISOString(), at + 5_000)
  assert.notEqual(relative, formatWallClockTime(new Date(at).toISOString()))
})
