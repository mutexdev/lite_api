// Pure logic for the OpenAPI sync panel: the auto-check cadence, the status
// line, and the per-endpoint accept/keep decisions.
//
// The decisions are the part worth isolating. Each one chooses whether an
// endpoint's local edits survive the next apply, so a decision map that carries
// a stale or unrecognised value silently changes what gets overwritten — and the
// only visible symptom is a request whose body the user had customised coming
// back as the spec's version.

import type { types } from '../../wailsjs/go/models'

/** The cadences the settings dialog offers. */
export const OPENAPI_SYNC_CHECK_INTERVALS = [5, 15, 30, 60] as const

/** The cadence used whenever the stored one is missing or unusable. */
export const DEFAULT_OPENAPI_SYNC_INTERVAL = 5

/** The two decisions an endpoint change can carry. */
export const OPENAPI_SYNC_DECISIONS = ['accept-incoming', 'keep-mine'] as const
export type OpenAPISyncDecision = (typeof OPENAPI_SYNC_DECISIONS)[number]

export type EndpointDecisions = Record<string, string>

export function openAPISyncConfigFor(
  collection: types.Collection | undefined,
): types.OpenAPISyncConfig | undefined {
  return collection?.openapi?.[0]
}

/**
 * The polling cadence in minutes.
 *
 * Anything non-positive or unparseable falls back to the default rather than
 * being used: a 0 or negative interval becomes a `setInterval` that fires
 * continuously, which would hammer the spec's host.
 */
export function openAPISyncIntervalMinutes(config: types.OpenAPISyncConfig | undefined): number {
  const minutes = Number(config?.autoCheckInterval || DEFAULT_OPENAPI_SYNC_INTERVAL)
  return Number.isFinite(minutes) && minutes > 0 ? minutes : DEFAULT_OPENAPI_SYNC_INTERVAL
}

/**
 * Whether the collection polls its spec.
 *
 * The comparison is `!== false` rather than truthy, so a config saved before
 * the flag existed — where `autoCheck` is undefined — keeps polling. Only an
 * explicit `false` turns it off.
 */
export function openAPISyncAutoCheckEnabled(config: types.OpenAPISyncConfig | undefined): boolean {
  return Boolean(config?.sourceUrl && config.autoCheck !== false)
}

/**
 * Snaps a stored interval onto one the settings dialog can display.
 *
 * A value not in the list would leave the `<select>` with no matching option,
 * which renders as blank and then saves whatever the user's next interaction
 * happens to land on.
 */
export function normalizedOpenAPISyncSettingsInterval(value: number | undefined): number {
  const minutes = Number(value || DEFAULT_OPENAPI_SYNC_INTERVAL)
  return (OPENAPI_SYNC_CHECK_INTERVALS as readonly number[]).includes(minutes)
    ? minutes
    : DEFAULT_OPENAPI_SYNC_INTERVAL
}

/**
 * Formats a check timestamp as a local time, falling back to the raw string.
 *
 * Returning the raw value for an unparseable timestamp beats returning
 * "Invalid Date": it at least shows what the backend sent.
 */
export function formatOpenAPISyncCheckedAt(value: string | undefined): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleTimeString()
}

export interface AutoCheckStatusInput {
  config: types.OpenAPISyncConfig | undefined
  /** Absent when no collection is selected — the cadence still renders. */
  hasCollection: boolean
  errorMessage?: string
  status?: { checkedAt?: string; hasUpdates?: boolean }
}

/**
 * The one-line cadence/result summary under the sync controls.
 *
 * A failed check reports the failure and nothing else. Showing the last
 * successful result alongside it would read as though the spec were still being
 * tracked, which is the opposite of what a failing check means.
 */
export function openAPISyncAutoCheckStatusLine(input: AutoCheckStatusInput): string {
  const { config, hasCollection, errorMessage, status } = input
  if (!config?.sourceUrl) return ''
  const enabled = openAPISyncAutoCheckEnabled(config)
  const cadence = enabled
    ? `Auto Check for Updates: Every ${openAPISyncIntervalMinutes(config)} min`
    : 'Auto Check for Updates: Disabled'
  if (!hasCollection || !enabled) return cadence
  if (errorMessage) return `${cadence} · Last check failed`
  if (!status?.checkedAt) return cadence
  const checkedAt = formatOpenAPISyncCheckedAt(status.checkedAt)
  const updateState = status.hasUpdates ? 'Updates found' : 'No updates'
  return `${cadence} · ${updateState}${checkedAt ? ` ${checkedAt}` : ''}`
}

/**
 * Pretty-prints a JSON spec, leaving YAML and unparseable JSON as they are.
 *
 * The leading `{` test is a cheap pre-filter, not the correctness boundary — a
 * YAML mapping fails `JSON.parse` and reaches the same `catch`. It earns its
 * place by not running the parser over every large YAML spec the viewer opens,
 * and its one observable effect is on top-level JSON that is not an object (an
 * array or a bare scalar), which is left alone rather than reformatted.
 *
 * The `trimStart` is load-bearing in a way the `{` test is not: a spec served
 * with leading whitespace or a newline is ordinary, and without it every such
 * document would render as the unformatted single line it arrived as.
 */
export function formattedOpenAPISpecContent(content: string | undefined): string {
  const value = content ?? ''
  if (value.trimStart().startsWith('{')) {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  return value
}

export function openAPISyncSpecDiffSummary(
  result: types.OpenAPISyncSpecDiffResult | undefined,
): string {
  if (!result) return ''
  return `${result.added ?? 0} added · ${result.updated ?? 0} updated · ${result.removed ?? 0} removed · ${result.unchanged ?? 0} unchanged`
}

/** The decision applied to a change the user has not touched. */
export function defaultOpenAPISyncDecision(change: types.OpenAPISyncEndpointChange): string {
  return change.defaultDecision || 'accept-incoming'
}

function isKnownDecision(value: string | undefined): value is OpenAPISyncDecision {
  return value === 'accept-incoming' || value === 'keep-mine'
}

/**
 * Rebuilds the decision map against a fresh set of changes.
 *
 * Three properties, each of which has a way to go wrong that no error reports:
 *
 * - A decision the user made is KEPT, keyed by change id, so re-running the
 *   check does not throw away their choices.
 * - A value that is not one of the two known decisions is REPLACED by the
 *   default. The map is rebuilt from whatever it held before, so a value left
 *   by an older build or a renamed decision would otherwise ride along and be
 *   handed back to the backend as an endpoint's disposition.
 * - Ids no longer present are DROPPED, because the map is built fresh rather
 *   than merged. A decision for an endpoint that no longer exists would
 *   accumulate forever, and could re-apply if the id came back.
 */
export function reconcileEndpointDecisions(
  changes: types.OpenAPISyncEndpointChange[] | undefined,
  existing: EndpointDecisions,
): EndpointDecisions {
  const list = changes ?? []
  if (list.length === 0) return {}
  const next: EndpointDecisions = {}
  for (const change of list) {
    const previous = existing[change.id]
    next[change.id] = isKnownDecision(previous) ? previous : defaultOpenAPISyncDecision(change)
  }
  return next
}

/** The map produced by the "accept all" / "keep all" buttons. */
export function allEndpointDecisions(
  changes: types.OpenAPISyncEndpointChange[] | undefined,
  decision: string,
): EndpointDecisions {
  const next: EndpointDecisions = {}
  for (const change of changes ?? []) {
    next[change.id] = decision
  }
  return next
}

/** The ids of local-drift changes of one kind, for the bulk drift actions. */
export function openAPILocalDriftIDs(
  result: types.OpenAPILocalDriftResult | undefined,
  changeType: string,
): string[] {
  return (result?.changes ?? [])
    .filter((change) => change.change === changeType)
    .map((change) => change.id)
}

/**
 * The user-facing word for a drift kind.
 *
 * "missing" and "local-only" are written from the SPEC's point of view; the
 * panel reads from the collection's, where a request the spec lacks was added
 * and one the collection lacks was deleted. Rendering the raw values would
 * invert both.
 */
export function openAPILocalDriftLabel(changeType: string): string {
  if (changeType === 'missing') return 'deleted'
  if (changeType === 'local-only') return 'added'
  return changeType
}
