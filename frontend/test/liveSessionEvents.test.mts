// US-021 / US-022 — tests for the frontend half of live-session event push.
//
// The valuable assertions here are the ones about a log that is WRONG. A naive
// implementation that appends every push it receives passes every happy-path
// test and then silently shows an incomplete event log to a user who reopened a
// tab mid-stream — the failure mode this module exists to make impossible.

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applyLiveSessionPush,
  emptyLiveSessionLog,
  liveSessionKey,
  resolveLiveSessionEvents,
  type LiveSessionPush,
} from '../src/lib/liveSessionEvents.ts'

const push = (index: number, data: string, total = index + 1): LiveSessionPush => ({
  collectionId: 'c1',
  itemId: 'i1',
  index,
  total,
  event: { direction: 'received', data },
})

test('a contiguous run of pushes accumulates in order', () => {
  let log = emptyLiveSessionLog()
  for (let i = 0; i < 5; i += 1) log = applyLiveSessionPush(log, push(i, `e${i}`))

  assert.equal(log.events.length, 5)
  assert.equal(log.contiguous, true)
  assert.equal(log.lastIndex, 4)
  assert.deepEqual(
    log.events.map((event) => event.data),
    ['e0', 'e1', 'e2', 'e3', 'e4'],
  )
})

test('a redelivered push is ignored rather than duplicated', () => {
  let log = emptyLiveSessionLog()
  log = applyLiveSessionPush(log, push(0, 'a'))
  log = applyLiveSessionPush(log, push(1, 'b'))
  const before = log
  log = applyLiveSessionPush(log, push(1, 'b'))

  assert.equal(log.events.length, 2, 'a duplicate must not extend the log')
  assert.equal(log, before, 'an ignored push must return the same object, so reactivity does not re-run')
})

test('a log that starts mid-stream is marked non-contiguous', () => {
  // The listener attached after the session had already produced 40 events.
  // Appending blindly would produce a 3-event log that LOOKS complete.
  let log = emptyLiveSessionLog()
  log = applyLiveSessionPush(log, push(40, 'x'))
  log = applyLiveSessionPush(log, push(41, 'y'))
  log = applyLiveSessionPush(log, push(42, 'z'))

  assert.equal(log.events.length, 3, 'live traffic is still recorded')
  assert.equal(log.contiguous, false, 'but the log must know it is missing its beginning')
})

test('a hole in the middle is detected and never repaired by later pushes', () => {
  let log = emptyLiveSessionLog()
  log = applyLiveSessionPush(log, push(0, 'a'))
  log = applyLiveSessionPush(log, push(2, 'c'))
  assert.equal(log.contiguous, false)

  log = applyLiveSessionPush(log, push(3, 'd'))
  assert.equal(log.contiguous, false, 'contiguity is not recoverable by subsequent in-order pushes')
})

test('resolve prefers the accumulated log when it is complete and at least as long', () => {
  let log = emptyLiveSessionLog()
  for (let i = 0; i < 6; i += 1) log = applyLiveSessionPush(log, push(i, `e${i}`))
  const bodyWindow = [{ data: 'e4' }, { data: 'e5' }]

  const resolved = resolveLiveSessionEvents(log, bodyWindow)
  assert.equal(resolved.length, 6, 'the full accumulated log beats the trailing window')
})

test('resolve falls back to the response body when the log has a gap', () => {
  let log = emptyLiveSessionLog()
  log = applyLiveSessionPush(log, push(40, 'x'))
  const bodyWindow = [{ data: 'a' }, { data: 'b' }, { data: 'c' }]

  assert.deepEqual(
    resolveLiveSessionEvents(log, bodyWindow),
    bodyWindow,
    'an incomplete log must never be shown in preference to the backend body',
  )
})

test('resolve falls back when the log is shorter than the body window', () => {
  // Right after a reconnect the log is clean but has fewer events than the
  // backend still holds. Showing it would drop history.
  let log = emptyLiveSessionLog()
  log = applyLiveSessionPush(log, push(0, 'a'))
  const bodyWindow = [{ data: 'a' }, { data: 'b' }, { data: 'c' }]

  assert.deepEqual(resolveLiveSessionEvents(log, bodyWindow), bodyWindow)
})

test('resolve falls back when there is no log at all', () => {
  const bodyWindow = [{ data: 'a' }]
  assert.deepEqual(resolveLiveSessionEvents(undefined, bodyWindow), bodyWindow)
})

test('keys distinguish requests and collections', () => {
  assert.notEqual(liveSessionKey('c1', 'i1'), liveSessionKey('c1', 'i2'))
  assert.notEqual(liveSessionKey('c1', 'i1'), liveSessionKey('c2', 'i1'))
  assert.equal(liveSessionKey('c1', 'i1'), liveSessionKey('c1', 'i1'))
})
