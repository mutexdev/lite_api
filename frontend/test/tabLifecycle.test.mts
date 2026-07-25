import assert from 'node:assert/strict'
import test from 'node:test'

import { requestDeletionAction } from '../src/lib/workbench/tabLifecycle.ts'

test('request deletion discards transient requests without recovery and preserves durable recovery deletes', () => {
  assert.equal(requestDeletionAction({ transient: true }), 'discard-draft')
  assert.equal(requestDeletionAction({ transient: false }), 'recoverable-delete')
  assert.equal(requestDeletionAction({}), 'recoverable-delete')
})
