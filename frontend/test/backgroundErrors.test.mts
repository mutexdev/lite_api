// Tests for the persistent error channel.
//
// The property under test throughout is that a notice raised by background work
// SURVIVES. Every one of these would pass trivially against the old single
// `error` string at the moment it was set — the bug was only ever visible one
// interaction later, when `runAction` cleared it. So the tests here are about
// accumulation and dismissal rather than about display: does raising a second
// notice keep the first, does a repeat stay one row, does the list stay bounded
// when a poller fails forever, and does dismissing one leave the rest alone.

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MAX_BACKGROUND_NOTICES,
  addBackgroundNotice,
  backgroundNoticeLabel,
  clearBackgroundNotices,
  dismissBackgroundNotice,
  errorMessage,
  type BackgroundNotice
} from '../src/lib/backgroundErrors.ts'

test('a notice is recorded newest first', () => {
  let notices: BackgroundNotice[] = []
  notices = addBackgroundNotice(notices, { message: 'Auto-save failed' })
  notices = addBackgroundNotice(notices, { message: 'Collection watch failed' })

  assert.deepEqual(
    notices.map((notice) => notice.message),
    ['Collection watch failed', 'Auto-save failed']
  )
})

test('a second background failure does not erase the first', () => {
  // The whole point of the channel. Two unrelated background subsystems failing
  // in the same session must both still be readable.
  let notices: BackgroundNotice[] = []
  notices = addBackgroundNotice(notices, { message: 'Auto-save failed' })
  notices = addBackgroundNotice(notices, { message: 'History write failed' })

  assert.equal(notices.length, 2)
  assert.ok(notices.some((notice) => notice.message === 'Auto-save failed'))
})

test('a repeated failure counts up instead of adding a row', () => {
  // The collection watcher polls every two seconds. Without dedupe, a backend
  // that stays broken evicts every other notice within twelve seconds.
  let notices: BackgroundNotice[] = []
  for (let i = 0; i < 12; i++) {
    notices = addBackgroundNotice(notices, { message: 'Collection watch failed' })
  }

  assert.equal(notices.length, 1)
  assert.equal(notices[0].count, 12)
  assert.equal(backgroundNoticeLabel(notices[0]), 'Collection watch failed (x12)')
})

test('a single occurrence carries no count suffix', () => {
  const notices = addBackgroundNotice([], { message: 'Auto-save failed' })
  assert.equal(backgroundNoticeLabel(notices[0]), 'Auto-save failed')
})

test('a repeat keeps its id so dismissing it is stable', () => {
  let notices = addBackgroundNotice([], { message: 'Auto-save failed' })
  const id = notices[0].id
  notices = addBackgroundNotice(notices, { message: 'Auto-save failed' })

  assert.equal(notices[0].id, id)
})

test('a repeat refreshes detail and timestamp and moves to the front', () => {
  let notices: BackgroundNotice[] = []
  notices = addBackgroundNotice(notices, { message: 'Auto-save failed', detail: 'attempt 1', at: 100 })
  notices = addBackgroundNotice(notices, { message: 'Something else', at: 200 })
  notices = addBackgroundNotice(notices, { message: 'Auto-save failed', detail: 'attempt 2', at: 300 })

  assert.equal(notices[0].message, 'Auto-save failed')
  assert.equal(notices[0].detail, 'attempt 2', 'the newest detail is the useful one')
  assert.equal(notices[0].at, 300)
  assert.equal(notices.length, 2)
})

test('a repeat without detail keeps the detail it had', () => {
  let notices = addBackgroundNotice([], { message: 'Auto-save failed', detail: 'disk full' })
  notices = addBackgroundNotice(notices, { message: 'Auto-save failed' })

  assert.equal(notices[0].detail, 'disk full')
})

test('the same message at a different tone is a different notice', () => {
  let notices: BackgroundNotice[] = []
  notices = addBackgroundNotice(notices, { message: 'Refresh skipped', tone: 'warning' })
  notices = addBackgroundNotice(notices, { message: 'Refresh skipped', tone: 'error' })

  assert.equal(notices.length, 2, 'severity is part of what a notice says')
})

test('the list stays bounded when a broken backend fails differently every time', () => {
  let notices: BackgroundNotice[] = []
  for (let i = 0; i < MAX_BACKGROUND_NOTICES * 3; i++) {
    notices = addBackgroundNotice(notices, { message: `failure ${i}` })
  }

  assert.equal(notices.length, MAX_BACKGROUND_NOTICES)
  assert.equal(notices[0].message, `failure ${MAX_BACKGROUND_NOTICES * 3 - 1}`, 'the newest survives')
})

test('a blank message is not recorded', () => {
  // An empty banner occupies the same space as a real one while telling the
  // user only that something, somewhere, went wrong.
  const notices = addBackgroundNotice([], { message: '   ' })
  assert.deepEqual(notices, [])
})

test('dismissing one notice leaves the others', () => {
  let notices: BackgroundNotice[] = []
  notices = addBackgroundNotice(notices, { message: 'first' })
  notices = addBackgroundNotice(notices, { message: 'second' })

  notices = dismissBackgroundNotice(notices, notices[0].id)

  assert.deepEqual(notices.map((notice) => notice.message), ['first'])
})

test('dismissing an unknown id changes nothing', () => {
  const notices = addBackgroundNotice([], { message: 'first' })
  assert.deepEqual(dismissBackgroundNotice(notices, 'nope').map((n) => n.message), ['first'])
})

test('clearing empties the channel', () => {
  assert.deepEqual(clearBackgroundNotices(), [])
})

test('errorMessage unwraps the shapes a Wails rejection actually arrives in', () => {
  assert.equal(errorMessage(new Error('boom')), 'boom')
  assert.equal(errorMessage('plain string'), 'plain string')
  assert.equal(errorMessage(404), '404')
})

test('errorMessage refuses to show [object Object]', () => {
  // Wails binding rejections are frequently plain objects. "[object Object]"
  // looks like a diagnosis and is not one.
  assert.equal(errorMessage({ code: 500 }, 'Save failed'), 'Save failed')
  assert.equal(errorMessage(new Error(''), 'Save failed'), 'Save failed')
  assert.equal(errorMessage('   ', 'Save failed'), 'Save failed')
  assert.equal(errorMessage(undefined, 'Save failed'), 'Save failed')
})
