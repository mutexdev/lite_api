// How the app writes a duration, a size, a status code and a time.
//
// The audit counted seven separate inline copies of `${ms} ms` across
// App.svelte, RunnerPanel, HistoryPanel, ResponseInspector and
// workbench/commandState.ts, plus one factored-out `flowDurationLabel` that
// only Flow used. They all happened to agree. That agreement was luck: nothing
// held it, and the first surface to write `200ms` or round differently would
// have shipped the drift silently, because no test anywhere compares one
// surface's wording to another's.
//
// Bytes had the opposite problem — exactly one implementation
// (`formatRuntimeBytes`, workbench/commandState.ts) and exactly one caller, so
// History rendered no size at all despite HistoryEntry.size arriving on the
// wire. A formatter nobody can reach is a formatter nobody uses.
//
// THE SPACE IS PART OF THE FORMAT. "413 ms", not "413ms": these numbers sit in
// dense rows next to status codes and byte counts, and a unit welded to its
// digits reads as one longer number at a glance. Same reason the byte
// formatter keeps its space.
//
// NOTHING HERE IS LOCALE-AWARE except formatWallClockTime, deliberately. A
// duration and a byte count are measurements the user compares between rows;
// grouping separators that change with the machine's locale would make two rows
// of the same table disagree about what a thousand looks like.

/**
 * "413 ms", or '' when there is no measurement to report.
 *
 * Blank rather than "0 ms" for the missing case, because the surfaces that use
 * this are LISTS: a flow step that has not run yet, a runner row for a request
 * that was skipped. "0 ms" in that slot claims the step ran and was
 * instantaneous, which is a different and wrong fact. A surface that must
 * always fill the slot — the response command strip has a fixed three-metric
 * row — wants formatDurationMsOrZero instead.
 *
 * Rounds rather than truncates so a 0.4 ms step still says "0 ms" and does not
 * silently become blank; sub-millisecond is a real measurement.
 */
export function formatDurationMs(value: number | undefined): string {
  const ms = Number(value ?? 0)
  if (!Number.isFinite(ms) || ms < 0) return ''
  return `${Math.round(ms)} ms`
}

/** The same wording, for a slot that must render something either way. */
export function formatDurationMsOrZero(value: number | undefined): string {
  return formatDurationMs(value) || '0 ms'
}

/**
 * "512 B", "1.5 KB", "10 KB", "1.0 MB".
 *
 * Byte-for-byte the behaviour of `formatRuntimeBytes` in
 * workbench/commandState.ts, which this is meant to replace: the response
 * strip is the surface people already read sizes on, so the rule it set is the
 * rule, and moving it here has to be a no-op there.
 *
 * The precision rule is the interesting part. Bytes are never fractional
 * ("512.0 B" is a measurement pretending to be more precise than a byte), and
 * past ten of any unit the decimal stops carrying information — "10.2 KB" and
 * "10 KB" tell a user the same thing, and the shorter one keeps the column
 * narrow.
 *
 * The largest unit absorbs everything above it rather than overflowing the
 * table, so an absurd size reads as "1048576 GB" instead of running off the end
 * of the unit list.
 */
export function formatBytes(value: number | undefined): string {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let amount = value
  let unitIndex = 0
  while (amount >= 1024 && unitIndex < units.length - 1) {
    amount /= 1024
    unitIndex += 1
  }
  const precision = amount >= 10 || unitIndex === 0 ? 0 : 1
  return `${amount.toFixed(precision)} ${units[unitIndex]}`
}

/**
 * What goes in a results row's status column.
 *
 * A row can carry a transport error INSTEAD of a code — a DNS failure, a TLS
 * refusal, a cancelled run — and those rows still need something in the column,
 * or the grid collapses and the row stops lining up with its neighbours. The
 * word, not the sentence: the full error belongs in the expanded detail, where
 * there is room for it.
 */
export function formatStatusCode(status: number | undefined, error?: string | undefined): string {
  const code = Number(status ?? 0)
  if (Number.isFinite(code) && code > 0) return String(code)
  if (error) return 'error'
  return '—'
}

/**
 * "12s ago", "4m ago", "3h ago", "6d ago".
 *
 * One of the app's two sanctioned timestamp styles, and the one for a LIST: on
 * a history row the question is "how long ago", and a wall-clock time makes the
 * reader do the subtraction. Ported from HistoryPanel, which was the only place
 * in the app that had one.
 *
 * `now` is an argument rather than a Date.now() call so this is testable at
 * all; every caller omits it.
 *
 * Clamped at zero: a machine whose clock stepped backwards between recording an
 * entry and rendering it would otherwise produce "-3s ago".
 */
export function formatRelativeTime(value: string | number | Date | undefined, now = Date.now()): string {
  if (value === undefined || value === null || value === '') return ''
  const at = new Date(value as string).getTime()
  if (Number.isNaN(at)) return ''
  const seconds = Math.max(0, Math.floor((now - at) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86400)}d ago`
}

/**
 * The other sanctioned style: an actual clock reading, in the user's locale.
 *
 * These two answer DIFFERENT QUESTIONS and both are legitimate — "how long ago"
 * for a row in a log the user is scanning, "at what time" for a single fact the
 * user may need to correlate with something outside the app (a server log, a
 * token expiry, a sync check). Naming them both here is the point: the audit
 * found three ad hoc timestamp call sites and no way to tell which style was
 * intended, so the next one had nothing to copy.
 */
export function formatWallClockTime(value: string | number | Date | undefined): string {
  if (value === undefined || value === null || value === '') return ''
  const at = new Date(value as string)
  if (Number.isNaN(at.getTime())) return ''
  return at.toLocaleTimeString()
}
