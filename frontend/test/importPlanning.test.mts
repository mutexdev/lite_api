import assert from 'node:assert/strict'
import test from 'node:test'

import { hasReplaceImportSelection, selectedImportRows } from '../src/lib/importPlanning.ts'

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
