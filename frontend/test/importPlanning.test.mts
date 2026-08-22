import assert from 'node:assert/strict'
import test from 'node:test'

import {
  defaultImportDecision,
  hasReplaceImportSelection,
  importOutcomeSummary,
  importSelectionFor,
  reconcileImportDecision,
  selectedImportRows,
  toggleImportChildID,
  type ImportPreviewRowDetail
} from '../src/lib/importPlanning.ts'

const rows = [
  { candidateId: 'openapi', conflict: 'ready' },
  { candidateId: 'already-open', conflict: 'already-open' },
  { candidateId: 'invalid', error: 'Unsupported source' }
]

test('selected import rows update when a preview checkbox is deselected', () => {
  const decisions = {
    openapi: { selected: true },
    'already-open': { selected: true, conflictAction: 'replace' },
    invalid: { selected: true }
  }

  assert.deepEqual(selectedImportRows(rows, decisions).map((row) => row.candidateId), ['openapi', 'already-open'])

  const deselected = { ...decisions, openapi: { selected: false } }
  const applySelections = selectedImportRows(rows, deselected).map((row) => row.candidateId)
  assert.deepEqual(applySelections, ['already-open'], 'Apply must receive only the current ready rows')
})

test('replace confirmation is required only for selected replace rows', () => {
  const decisions = {
    openapi: { selected: true },
    'already-open': { selected: true, conflictAction: 'replace' }
  }
  assert.equal(hasReplaceImportSelection(rows, decisions), true)
  assert.equal(hasReplaceImportSelection(rows, { ...decisions, 'already-open': { selected: false, conflictAction: 'replace' } }), false)
})

function detailRow(over: Partial<ImportPreviewRowDetail> = {}): ImportPreviewRowDetail {
  return {
    candidateId: 'c1',
    sourceId: 's1',
    contentHash: 'hash-1',
    defaultSelect: true,
    collectionName: 'Users API',
    environments: [{ selectionId: 'e1' }, { selectionId: 'e2' }],
    folders: [{ selectionId: 'f1' }],
    requests: [{ selectionId: 'r1' }, { selectionId: 'r2' }],
    ...over,
  }
}

test('a healthy row starts selected with every child included', () => {
  const decision = defaultImportDecision(detailRow())
  assert.equal(decision.selected, true)
  assert.deepEqual(decision.environments, ['e1', 'e2'])
  assert.deepEqual(decision.requests, ['r1', 'r2'])
  assert.equal(decision.outputName, 'Users API')
})

// Apply would otherwise write a half-read source, or write into a place the app
// already knows it cannot use.
test('a row that failed to parse or has nowhere to go is not selected', () => {
  assert.equal(defaultImportDecision(detailRow({ error: 'unreadable' })).selected, false)
  assert.equal(defaultImportDecision(detailRow({ conflict: 'unavailable' })).selected, false)
  assert.equal(defaultImportDecision(detailRow({ defaultSelect: false })).selected, false)
})

// Replace destroys a collection already on disk. No default should be able to
// do that, so a conflict defaults to rename and never to replace.
test('a conflict defaults to rename, never to replace', () => {
  for (const conflict of ['exists', 'already-open']) {
    assert.equal(defaultImportDecision(detailRow({ conflict })).conflictAction, 'rename', conflict)
  }
  assert.equal(defaultImportDecision(detailRow()).conflictAction, '')
  assert.notEqual(defaultImportDecision(detailRow({ conflict: 'exists' })).conflictAction, 'replace')
})

// A source re-read as a different format has entirely different children, and
// its old selection ids mean nothing.
test('a decision is discarded when the source kind changed', () => {
  const prior = { ...defaultImportDecision(detailRow()), kindOverride: 'postman', outputName: 'Renamed' }
  const fresh = reconcileImportDecision(prior, detailRow(), 'openapi')
  assert.equal(fresh.outputName, 'Users API')
  assert.equal(fresh.kindOverride, 'openapi')
})

test('a decision is discarded when the row no longer parses', () => {
  const prior = { ...defaultImportDecision(detailRow()), outputName: 'Renamed' }
  const fresh = reconcileImportDecision(prior, detailRow({ error: 'broken' }))
  assert.equal(fresh.outputName, 'Users API')
  assert.equal(fresh.selected, false)
})

test('a compatible decision survives a re-preview', () => {
  const prior = { ...defaultImportDecision(detailRow()), outputName: 'Renamed', requests: ['r2'] }
  const next = reconcileImportDecision(prior, detailRow())
  assert.equal(next.outputName, 'Renamed')
  assert.deepEqual(next.requests, ['r2'])
})

// Passing an id the backend no longer knows is not an error it reports; the
// import simply proceeds without that item, so a stale id must be dropped here.
//
// EVERY KIND, not just requests. The filter is written three times, once per
// list, and a version checking only `requests` let the environments and folders
// lines be deleted with nothing failing — found by controlling all three rather
// than by reading them.
test('ids the new preview no longer contains are dropped, for every kind', () => {
  const kinds = [
    { key: 'environments', keep: 'e1' },
    { key: 'folders', keep: 'f1' },
    { key: 'requests', keep: 'r1' }
  ] as const

  for (const { key, keep } of kinds) {
    const prior = { ...defaultImportDecision(detailRow()), [key]: [keep, `${key}-gone`] }
    const next = reconcileImportDecision(prior as never, detailRow())
    assert.deepEqual(next[key], [keep], `${key}: a stale id survived`)

    // And the other two lists are untouched by that kind's filtering.
    for (const other of kinds) {
      if (other.key === key) continue
      assert.ok(
        next[other.key].includes(other.keep),
        `filtering ${key} also dropped from ${other.key}`
      )
    }
  }
})

// The user's choice was made when importing was still possible.
test('a row that became unavailable is deselected but keeps the rest of its decision', () => {
  const prior = { ...defaultImportDecision(detailRow()), outputName: 'Renamed' }
  const next = reconcileImportDecision(prior, detailRow({ conflict: 'unavailable' }))
  assert.equal(next.selected, false)
  assert.equal(next.outputName, 'Renamed')
})

test('toggling a child adds and removes without duplicating', () => {
  assert.deepEqual(toggleImportChildID(['a'], 'b', true), ['a', 'b'])
  assert.deepEqual(toggleImportChildID(['a', 'b'], 'a', false), ['b'])
  assert.deepEqual(toggleImportChildID(['a'], 'a', true), ['a'])
  assert.deepEqual(toggleImportChildID([], 'a', false), [])
})

// The backend compares this against the file as it is at APPLY time and refuses
// when it changed since the preview. Without it, a source edited between the
// two steps is imported as something the user never previewed.
test('the selection carries the previewed content hash', () => {
  const row = detailRow()
  assert.equal(importSelectionFor(row, defaultImportDecision(row)).expectedContentHash, 'hash-1')
})

// A false flag tells the backend to take everything. It must be false only when
// nothing was removed — sending false with a partial id list would import the
// items the user unchecked.
test('the filter flags are false only when nothing was deselected', () => {
  const row = detailRow()
  const full = importSelectionFor(row, defaultImportDecision(row))
  assert.equal(full.filterEnvironments, false)
  assert.equal(full.filterFolders, false)
  assert.equal(full.filterRequests, false)

  // Each kind must drive ITS OWN flag. Checking only one leaves the others free
  // to be wired to the wrong list: a flag reading another kind's counts is
  // false whenever that kind happens to be untouched, which is most of the
  // time.
  const kinds = [
    { key: 'environments', flag: 'filterEnvironments', keep: ['e1'] },
    { key: 'folders', flag: 'filterFolders', keep: [] as string[] },
    { key: 'requests', flag: 'filterRequests', keep: ['r1'] }
  ] as const
  for (const { key, flag, keep } of kinds) {
    const selection = importSelectionFor(row, { ...defaultImportDecision(row), [key]: keep })
    for (const other of kinds) {
      assert.equal(
        (selection as unknown as Record<string, boolean>)[other.flag],
        other.flag === flag,
        `deselecting ${key} set ${other.flag}`
      )
    }
  }
})

// Deselecting everything is not the same as selecting everything, and the count
// comparison is what tells them apart.
test('deselecting every child still filters', () => {
  const row = detailRow()
  const none = importSelectionFor(row, { ...defaultImportDecision(row), requests: [] })
  assert.equal(none.filterRequests, true)
  assert.deepEqual(none.requestIds, [])
})

// A row with no children of a kind has an empty list on both sides, so the
// counts match and the flag stays false — the backend is told to take all zero
// of them rather than to filter a list that does not exist.
test('a kind with no children does not read as filtered', () => {
  const row = detailRow({ folders: [] })
  assert.equal(importSelectionFor(row, defaultImportDecision(row)).filterFolders, false)
})

// The output name seeds the rename field. A source whose collection name the
// backend could not determine must seed it EMPTY rather than "undefined" —
// which is what the user would otherwise have to notice and clear before the
// import could be named anything sensible.
test('a row with no collection name seeds an empty output name', () => {
  const decision = defaultImportDecision(detailRow({ collectionName: undefined }))
  assert.equal(decision.outputName, '')
})

// A preview row for a source with no environments, folders or requests at all —
// an empty or unreadable collection — must still produce a usable decision
// rather than throwing while the preview list renders.
test('a row with no child lists still yields a decision and a selection', () => {
  const bare = {
    candidateId: 'c1',
    sourceId: 's1',
    defaultSelect: true
  } as ImportPreviewRowDetail
  const decision = defaultImportDecision(bare)
  assert.deepEqual(decision.environments, [])
  assert.deepEqual(decision.folders, [])
  assert.deepEqual(decision.requests, [])

  const selection = importSelectionFor(bare, decision)
  assert.equal(selection.filterEnvironments, false, 'nothing was deselected, so nothing is filtered')
  assert.equal(selection.filterFolders, false)
  assert.equal(selection.filterRequests, false)
})

// Reconciling against a row that lost its child lists drops the stale ids
// rather than passing them to a backend that no longer knows them.
test('reconciling against a row with no child lists drops every id', () => {
  const prior = { ...defaultImportDecision(detailRow()), requests: ['r1'] }
  const next = reconcileImportDecision(prior, { candidateId: 'c1', sourceId: 's1' } as ImportPreviewRowDetail)
  assert.deepEqual(next.requests, [])
  assert.deepEqual(next.environments, [])
})

test('importOutcomeSummary mentions only what happened', () => {
  assert.equal(importOutcomeSummary({ applied: [{ candidateId: 'a' }] }), '1 collection imported')
  assert.equal(
    importOutcomeSummary({ applied: [{ candidateId: 'a' }, { candidateId: 'b' }], skipped: [{ candidateId: 'c' }] }),
    '2 collections imported, 1 skipped'
  )
})

test('importOutcomeSummary counts importer warnings so they are not missed', () => {
  assert.equal(
    importOutcomeSummary({ applied: [{ candidateId: 'a', warnings: ['skipped "bad"'] }] }),
    '1 collection imported, 1 warning'
  )
})

test('importOutcomeSummary names the first failure instead of only counting it', () => {
  const summary = importOutcomeSummary({
    applied: [],
    errors: [{ candidateId: 'x', sourceName: 'broken.json', error: 'invalid JSON at line 3, column 9' }]
  })
  assert.match(summary, /broken\.json: invalid JSON at line 3, column 9/)
  assert.match(summary, /1 failure/)
})

test('importOutcomeSummary handles an empty result', () => {
  assert.equal(importOutcomeSummary(undefined), 'Nothing was imported.')
})
