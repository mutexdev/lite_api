// The persistent half of the app's error surface.
//
// `runAction` owns a single `error` string and clears it at the START of every
// action. That is right for action errors — the next thing you do replaces the
// message about the last thing you did — and wrong for everything else, because
// the app raises errors from work the user did not just start: the auto-save
// timer, the collection watcher poll, devtools refreshes, history writes, a
// backend `notification` push, a request patch that failed to flush. Those all
// wrote into the same string, so the next click erased them. A save failure the
// user never saw is indistinguishable from no save failure at all.
//
// So there are two channels, not one. This module is the second: notices that
// stay until the user dismisses them, because nothing else in the UI is going
// to tell them again.
//
// Kept as pure functions over a plain array rather than a store, for the same
// reason the rest of frontend/src/lib is: `node --test` can run it without a
// browser, and the reducer logic — dedupe, count, cap, dismiss — is exactly the
// part with edge cases worth testing.

export type BackgroundNoticeTone = 'error' | 'warning'

export interface BackgroundNotice {
  /** Stable across repeats, so dismissing one does not renumber the others. */
  id: string
  tone: BackgroundNoticeTone
  message: string
  /** Longer context shown under the message. Not part of dedupe identity. */
  detail?: string
  /** How many times this notice has been raised. 1 until it repeats. */
  count: number
  /** Milliseconds, as from Date.now(). Newest first in the list. */
  at: number
}

export interface BackgroundNoticeInput {
  tone?: BackgroundNoticeTone
  message: string
  detail?: string
  at?: number
}

/**
 * The most notices kept at once.
 *
 * A cap is required, not tidiness: the collection watcher polls every two
 * seconds and the auto-save timer fires per keystroke burst, so a backend that
 * stays broken produces notices without bound. Dedupe (below) handles the
 * common case of the SAME failure repeating; the cap handles a broken backend
 * that manages to fail differently each time.
 */
export const MAX_BACKGROUND_NOTICES = 6

/**
 * Two notices are the same notice when they say the same thing at the same
 * severity.
 *
 * Detail is excluded deliberately. A failing auto-save reports the same message
 * with a timestamp or path that shifts between attempts, and treating those as
 * distinct is how a broken backend fills the banner with six copies of one
 * problem and pushes every other notice out.
 */
function sameNotice(notice: BackgroundNotice, input: BackgroundNoticeInput): boolean {
  return notice.tone === (input.tone ?? 'error') && notice.message === input.message
}

let nextNoticeID = 0

/**
 * Returns the list with `input` recorded.
 *
 * A repeat does not add a row: it bumps the existing one's count and freshens
 * its timestamp and detail, then moves it to the front. The user learns "this
 * is still happening, 12 times now" from one line instead of losing the rest of
 * the list to it.
 */
export function addBackgroundNotice(
  notices: readonly BackgroundNotice[],
  input: BackgroundNoticeInput
): BackgroundNotice[] {
  const message = input.message.trim()
  // Nothing useful to show, and a blank banner is worse than none — it takes up
  // the same space while telling the user only that something went wrong.
  if (!message) return [...notices]

  const at = input.at ?? Date.now()
  const tone = input.tone ?? 'error'
  const existing = notices.find((notice) => sameNotice(notice, { ...input, message }))
  if (existing) {
    const updated: BackgroundNotice = {
      ...existing,
      count: existing.count + 1,
      detail: input.detail ?? existing.detail,
      at
    }
    return [updated, ...notices.filter((notice) => notice.id !== existing.id)]
  }

  const added: BackgroundNotice = {
    id: `background-notice-${++nextNoticeID}`,
    tone,
    message,
    detail: input.detail,
    count: 1,
    at
  }
  return [added, ...notices].slice(0, MAX_BACKGROUND_NOTICES)
}

export function dismissBackgroundNotice(
  notices: readonly BackgroundNotice[],
  id: string
): BackgroundNotice[] {
  return notices.filter((notice) => notice.id !== id)
}

export function clearBackgroundNotices(): BackgroundNotice[] {
  return []
}

/** `Save failed (x3)` — the suffix appears only once there is something to count. */
export function backgroundNoticeLabel(notice: BackgroundNotice): string {
  return notice.count > 1 ? `${notice.message} (x${notice.count})` : notice.message
}

/**
 * Normalises the many shapes an error arrives in into one line of text.
 *
 * Wails binding rejections are frequently plain objects, and `String(err)` on
 * one of those produces "[object Object]" — a message that has told the user
 * nothing while looking like it told them something. That case is worth
 * detecting rather than displaying.
 */
export function errorMessage(err: unknown, fallback = 'Something went wrong'): string {
  if (err === null || err === undefined) return fallback
  if (err instanceof Error) return err.message || fallback
  if (typeof err === 'string') return err.trim() || fallback
  const text = String(err)
  return !text || text === '[object Object]' ? fallback : text
}
