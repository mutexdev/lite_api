// Presenting notifications.
//
// A notification is the app's only way to report something that happened
// without the user asking — a failed background write, a completed run, an
// expired token. Every field here has a fallback chain, and the reason is the
// same each time: a notification that renders blank is one the user cannot act
// on and cannot dismiss with any confidence about what it said.

import type { types } from '../../wailsjs/go/models'

/**
 * Sort key. An unparseable or absent timestamp sorts to 0 — the BOTTOM of a
 * newest-first list — rather than throwing the whole sort into an inconsistent
 * comparator, which in V8 can reorder unrelated entries.
 */
export function notificationTime(notification: types.Notification): number {
  const value = new Date(notification.at)
  return Number.isNaN(value.getTime()) ? 0 : value.getTime()
}

/** Newest first, over a copy: sorting in place would reorder live app state. */
export function notificationsForDisplay(notifications: types.Notification[]): types.Notification[] {
  return [...notifications].sort((a, b) => notificationTime(b) - notificationTime(a))
}

/**
 * The heading. Falls back to the message and then to the literal word, because
 * a row with no heading is not clickable-looking and reads as a rendering bug.
 */
export function notificationTitle(notification: types.Notification | undefined): string {
  return notification?.title || notification?.message || 'Notification'
}

/**
 * The body. Falls back to the message but NOT to a placeholder: an empty
 * description is legitimate when the title already says everything, and
 * inventing text there would put words in the notification's mouth.
 */
export function notificationDescription(notification: types.Notification | undefined): string {
  return notification?.description || notification?.message || ''
}

/** The category chip. "Info" is the honest default for an unlabelled event. */
export function notificationType(notification: types.Notification | undefined): string {
  return notification?.type || notification?.level || 'Info'
}

/**
 * The timestamp, localised, or "" when there is not a usable one.
 *
 * Empty rather than "Invalid Date": a blank timestamp reads as "no time
 * recorded", which is true, while "Invalid Date" reads as a bug in the app and
 * tells the user nothing about their notification.
 */
export function notificationDate(notification: types.Notification | undefined): string {
  if (!notification?.at) return ''
  const value = new Date(notification.at)
  if (Number.isNaN(value.getTime())) return ''
  return value.toLocaleString()
}

/**
 * The severity class driving the row's colour.
 *
 * Accepts both spellings the codebase produces — "warn"/"warning" and
 * "error"/"danger" — because the Go side and the script runtime disagree, and a
 * severity that falls through to "info" would render an error in the same
 * colour as a routine message.
 */
export function notificationLevelClass(notification: types.Notification | undefined): string {
  const level = (notification?.level || '').toLowerCase()
  if (level === 'success') return 'success'
  if (level === 'warning' || level === 'warn') return 'warning'
  if (level === 'error' || level === 'danger') return 'danger'
  return 'info'
}
