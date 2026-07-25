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
