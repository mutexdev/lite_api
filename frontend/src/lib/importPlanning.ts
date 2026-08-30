export type ImportPreviewRowLike = {
  candidateId: string
  error?: string
  conflict?: string
}

export type ImportDecisionLike = {
  selected: boolean
  conflictAction?: string
}

/** Returns the preview rows that are still safe and selected for Apply. */
export function selectedImportRows<T extends ImportPreviewRowLike>(rows: readonly T[], decisions: Readonly<Record<string, ImportDecisionLike>>): T[] {
  return rows.filter((row) => decisions[row.candidateId]?.selected === true && !row.error && row.conflict !== 'unavailable')
}

export function hasReplaceImportSelection(rows: readonly ImportPreviewRowLike[], decisions: Readonly<Record<string, ImportDecisionLike>>): boolean {
  return selectedImportRows(rows, decisions).some((row) => decisions[row.candidateId]?.conflictAction === 'replace')
}

export type ImportChildKind = 'environments' | 'folders' | 'requests'

export type ImportChildEntry = { selectionId: string }

export type ImportPreviewRowDetail = ImportPreviewRowLike & {
  sourceId: string
  contentHash?: string
  defaultSelect?: boolean
  collectionName?: string
  environments?: ImportChildEntry[]
  folders?: ImportChildEntry[]
  requests?: ImportChildEntry[]
}

export type ImportDecision = {
  selected: boolean
  environments: string[]
  folders: string[]
  requests: string[]
  outputName: string
  kindOverride: string
  conflictAction: string
}

function allChildIDs(entries: ImportChildEntry[] | undefined): string[] {
  return (entries ?? []).map((entry) => entry.selectionId)
}

/**
 * The decision a row starts with before the user touches anything.
 *
 * Three defaults, each chosen against the destructive direction:
 *
 * - A row that failed to parse, or whose destination is unavailable, is NOT
 *   selected. Apply would otherwise write a half-read source, or write into a
 *   place the app already knows it cannot use.
 * - Every child IS selected. Importing a collection minus a request the user
 *   never saw is worse than importing one they must then prune.
 * - A conflict defaults to RENAME, never to replace. Replace destroys a
 *   collection already on disk, and no default should be able to do that.
 */
export function defaultImportDecision(
  row: ImportPreviewRowDetail,
  kindOverride = ''
): ImportDecision {
  return {
    selected: Boolean(row.defaultSelect) && !row.error && row.conflict !== 'unavailable',
    environments: allChildIDs(row.environments),
    folders: allChildIDs(row.folders),
    requests: allChildIDs(row.requests),
    outputName: row.collectionName ?? '',
    kindOverride,
    conflictAction: row.conflict === 'exists' || row.conflict === 'already-open' ? 'rename' : ''
  }
}

/**
 * Carries a decision across a re-preview, or starts fresh when it cannot.
 *
 * A prior decision is only reusable when the row still parses AND the source's
 * kind override is unchanged — a source re-read as a different format has
 * entirely different children, and its old selection ids mean nothing.
 *
 * Surviving ids are filtered against what the new preview actually contains.
 * Passing an id the backend no longer knows is not an error it reports; the
 * import simply proceeds without that item.
 */
export function reconcileImportDecision(
  prior: ImportDecision | undefined,
  row: ImportPreviewRowDetail,
  kindOverride = ''
): ImportDecision {
  const compatible = prior && !row.error && prior.kindOverride === kindOverride
  if (!compatible) return defaultImportDecision(row, kindOverride)
  const stillPresent = (ids: string[], entries: ImportChildEntry[] | undefined) =>
    ids.filter((id) => (entries ?? []).some((entry) => entry.selectionId === id))
  return {
    ...prior,
    // A row that has become unavailable is deselected even though the rest of
    // the decision survives: the user's choice was made when importing was
    // still possible.
    selected: prior.selected && row.conflict !== 'unavailable',
    environments: stillPresent(prior.environments, row.environments),
    folders: stillPresent(prior.folders, row.folders),
    requests: stillPresent(prior.requests, row.requests)
  }
}

/** Adds or removes one child id, keeping the list free of duplicates. */
export function toggleImportChildID(current: readonly string[], id: string, checked: boolean): string[] {
  return checked ? [...new Set([...current, id])] : current.filter((entry) => entry !== id)
}

export type ImportSelection = {
  sourceId: string
  candidateId: string
  expectedContentHash?: string
  environmentIds: string[]
  folderIds: string[]
  requestIds: string[]
  outputName: string
  kindOverride: string
  conflictAction: string
  filterEnvironments: boolean
  filterFolders: boolean
  filterRequests: boolean
}

/**
 * The payload Apply sends for one row.
 *
 * Two things here are not obvious from the shape:
 *
 * `expectedContentHash` is an optimistic-concurrency guard. The backend
 * compares it against the file as it is at APPLY time and refuses when it has
 * changed since the preview — otherwise a source edited between the two steps
 * would be imported as something the user never previewed.
 *
 * The `filter*` flags say "the user deselected something", and are computed by
 * COMPARING COUNTS against the full child list rather than by checking for an
 * empty selection. A false flag tells the backend to take everything, which is
 * why it must be false only when nothing was removed: sending false with a
 * partial id list would import the items the user unchecked.
 */
export function importSelectionFor(
  row: ImportPreviewRowDetail,
  decision: ImportDecision
): ImportSelection {
  const deselected = (chosen: string[], entries: ImportChildEntry[] | undefined) =>
    chosen.length !== allChildIDs(entries).length
  return {
    sourceId: row.sourceId,
    candidateId: row.candidateId,
    expectedContentHash: row.contentHash,
    environmentIds: decision.environments,
    folderIds: decision.folders,
    requestIds: decision.requests,
    outputName: decision.outputName,
    kindOverride: decision.kindOverride,
    conflictAction: decision.conflictAction,
    filterEnvironments: deselected(decision.environments, row.environments),
    filterFolders: deselected(decision.folders, row.folders),
    filterRequests: deselected(decision.requests, row.requests)
  }
}

export interface ImportOutcomeRow {
  candidateId?: string
  sourceName?: string
  error?: string
  warnings?: string[]
}

export interface ImportOutcome {
  applied?: ImportOutcomeRow[]
  skipped?: ImportOutcomeRow[]
  errors?: ImportOutcomeRow[]
}

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? '' : 's'}`

/**
 * Summarises an apply for the panel's live region.
 *
 * US-055. The old wording was "N imported, N skipped, N errors" whatever the
 * numbers were, so a clean import announced "0 skipped, 0 errors" and a total
 * failure looked much like a success. Only what happened is mentioned, and a
 * warning raised by an importer -- a skipped request, a rebuilt URL -- is
 * counted, because until now nothing in the panel drew the eye to one.
 */
export function importOutcomeSummary(outcome: ImportOutcome | undefined): string {
  const applied = outcome?.applied ?? []
  const skipped = outcome?.skipped ?? []
  const failed = outcome?.errors ?? []
  if (applied.length === 0 && skipped.length === 0 && failed.length === 0) return 'Nothing was imported.'
  const parts = [`${plural(applied.length, 'collection')} imported`]
  if (skipped.length > 0) parts.push(`${skipped.length} skipped`)
  if (failed.length > 0) parts.push(`${plural(failed.length, 'failure')}`)
  const warnings = applied.reduce((total, row) => total + (row.warnings?.length ?? 0), 0)
  if (warnings > 0) parts.push(plural(warnings, 'warning'))
  let summary = parts.join(', ')
  const firstFailure = failed.find((row) => (row.error ?? '').trim() !== '')
  if (firstFailure) summary += `. ${firstFailure.sourceName || 'A source'}: ${firstFailure.error}`
  return summary
}
