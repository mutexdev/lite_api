// Presenting notifications.
//
// A notification is the app's only way to report something the user did not ask
// about — a failed background write, an expired token. Every fallback here
// exists because a notification that renders blank is one nobody can act on.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  notificationTime,
  notificationsForDisplay,
  notificationTitle,
  notificationDescription,
  notificationType,
  notificationDate,
  notificationLevelClass
} from '../src/lib/notificationView.ts'

const note = (o: Record<string, unknown>) => o as never

test('notifications sort newest first', () => {
  const sorted = notificationsForDisplay([
    note({ title: 'old', at: '2020-01-01T00:00:00Z' }),
    note({ title: 'new', at: '2030-01-01T00:00:00Z' }),
    note({ title: 'mid', at: '2025-01-01T00:00:00Z' })
  ])
  assert.deepEqual(sorted.map((n) => n.title), ['new', 'mid', 'old'])
})

// Sorting in place would reorder live app state under the UI.
test('sorting does not mutate the input array', () => {
  const original = [note({ title: 'a', at: '2020-01-01T00:00:00Z' }), note({ title: 'b', at: '2030-01-01T00:00:00Z' })]
  notificationsForDisplay(original)
  assert.deepEqual(original.map((n) => n.title), ['a', 'b'])
})

// An inconsistent comparator can reorder unrelated entries in V8, so a bad
// timestamp has to produce a stable number rather than NaN.
test('an unusable timestamp sorts to the bottom rather than breaking the sort', () => {
  assert.equal(notificationTime(note({ at: 'rubbish' })), 0)
  assert.equal(notificationTime(note({})), 0)

  const sorted = notificationsForDisplay([
    note({ title: 'broken', at: 'rubbish' }),
    note({ title: 'good', at: '2025-01-01T00:00:00Z' })
  ])
  assert.deepEqual(sorted.map((n) => n.title), ['good', 'broken'])
})

// A row with no heading is not clickable-looking and reads as a rendering bug.
test('the title falls back to the message and then to a word', () => {
  assert.equal(notificationTitle(note({ title: 'T', message: 'M' })), 'T')
  assert.equal(notificationTitle(note({ message: 'M' })), 'M')
  assert.equal(notificationTitle(note({})), 'Notification')
  assert.equal(notificationTitle(undefined), 'Notification')
})

// An empty description is legitimate when the title says everything; inventing
// text would put words in the notification's mouth.
test('the description falls back to the message but not to a placeholder', () => {
  assert.equal(notificationDescription(note({ description: 'D', message: 'M' })), 'D')
  assert.equal(notificationDescription(note({ message: 'M' })), 'M')
  assert.equal(notificationDescription(note({})), '')
  assert.equal(notificationDescription(undefined), '')
})

test('the type falls back to the level and then to Info', () => {
  assert.equal(notificationType(note({ type: 'Run', level: 'error' })), 'Run')
  assert.equal(notificationType(note({ level: 'error' })), 'error')
  assert.equal(notificationType(note({})), 'Info')
})

// "Invalid Date" reads as a bug in the app; blank reads as "no time recorded",
// which is what is actually true.
test('an unusable date renders blank, never Invalid Date', () => {
  assert.equal(notificationDate(note({ at: '' })), '')
  assert.equal(notificationDate(note({ at: 'rubbish' })), '')
  assert.equal(notificationDate(note({})), '')
  assert.equal(notificationDate(undefined), '')
  assert.notEqual(notificationDate(note({ at: 'rubbish' })), 'Invalid Date')
})

test('a real date renders localised', () => {
  const shown = notificationDate(note({ at: '2030-06-01T12:00:00Z' }))
  assert.ok(shown.includes('2030'))
})

// The Go side and the script runtime disagree on spelling, and a severity that
// fell through to "info" would colour an error like a routine message.
test('both spellings of each severity map to the same class', () => {
  assert.equal(notificationLevelClass(note({ level: 'warning' })), 'warning')
  assert.equal(notificationLevelClass(note({ level: 'warn' })), 'warning')
  assert.equal(notificationLevelClass(note({ level: 'error' })), 'danger')
  assert.equal(notificationLevelClass(note({ level: 'danger' })), 'danger')
  assert.equal(notificationLevelClass(note({ level: 'success' })), 'success')
})

test('severity is matched case-insensitively', () => {
  assert.equal(notificationLevelClass(note({ level: 'ERROR' })), 'danger')
  assert.equal(notificationLevelClass(note({ level: 'Warn' })), 'warning')
})

test('an unknown or absent severity is info', () => {
  assert.equal(notificationLevelClass(note({ level: 'chatty' })), 'info')
  assert.equal(notificationLevelClass(note({})), 'info')
  assert.equal(notificationLevelClass(undefined), 'info')
})
