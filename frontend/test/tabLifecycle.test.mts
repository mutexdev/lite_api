import assert from 'node:assert/strict'
import test from 'node:test'

import {
  requestDeletionAction,
  planUnsavedClose,
  type LifecycleOpenTab,
  type LifecycleRequest
} from '../src/lib/workbench/tabLifecycle.ts'

test('request deletion discards transient requests without recovery and preserves durable recovery deletes', () => {
  assert.equal(requestDeletionAction({ transient: true }), 'discard-draft')
  assert.equal(requestDeletionAction({ transient: false }), 'recoverable-delete')
  assert.equal(requestDeletionAction({}), 'recoverable-delete')
})

// planUnsavedClose — coverage found it entirely untested, and it is the
// function that decides whether closing tabs WARNS the user.
//
// If it reports requiresConfirmation: false when a draft is open, the tabs close
// silently and the edits are gone. There is no error and no undo prompt; the
// user finds out when they reopen the request. That makes this the frontend
// counterpart of the draft-guard validation on the Go side, and it deserves the
// same treatment.

const tab = (over: Partial<LifecycleOpenTab> = {}): LifecycleOpenTab => ({
  id: 't1', collectionId: 'c1', itemId: 'i1', kind: 'request', ...over
})
const req = (over: Partial<LifecycleRequest> = {}): LifecycleRequest => ({
  collectionId: 'c1', id: 'i1', name: 'Req', ...over
})

test('a draft request makes closing require confirmation', () => {
  const plan = planUnsavedClose([tab()], [req({ draft: true })])
  assert.equal(plan.requiresConfirmation, true)
  assert.equal(plan.affected.length, 1)
  assert.equal(plan.affected[0].draft, true)
  assert.equal(plan.affected[0].requestId, 'i1')
})

test('a transient request makes closing require confirmation', () => {
  const plan = planUnsavedClose([tab()], [req({ transient: true })])
  assert.equal(plan.requiresConfirmation, true)
  assert.equal(plan.affected[0].transient, true)
})

// The tab may carry transience even when the request record does not — a
// scratch request opened into a tab. Reading only the request would close it
// without asking.
test('transience on the tab alone still counts', () => {
  const plan = planUnsavedClose([tab({ transient: true })], [req()])
  assert.equal(plan.requiresConfirmation, true, 'a transient tab must be reported even if the request is not marked')
})

test('a saved, non-transient request closes without confirmation', () => {
  const plan = planUnsavedClose([tab()], [req()])
  assert.equal(plan.requiresConfirmation, false)
  assert.equal(plan.affected.length, 0)
})

// Closing a response-example tab does not discard the request it references, so
// it must not trigger a prompt — a spurious dialog trains people to dismiss it.
test('response-example tabs are ignored', () => {
  const plan = planUnsavedClose([tab({ kind: 'response-example' })], [req({ draft: true })])
  assert.equal(plan.requiresConfirmation, false)
})

// The same request open in two tabs is ONE piece of unsaved work. Listing it
// twice would show the user a dialog naming the same request twice.
test('a request open in two tabs is reported once', () => {
  const plan = planUnsavedClose(
    [tab({ id: 't1' }), tab({ id: 't2' })],
    [req({ draft: true })]
  )
  assert.equal(plan.affected.length, 1, 'the same request must not be listed twice')
  assert.equal(plan.affected[0].tabId, 't1', 'the first tab wins')
})

test('two different drafts are both reported', () => {
  const plan = planUnsavedClose(
    [tab({ id: 't1', itemId: 'i1' }), tab({ id: 't2', itemId: 'i2' })],
    [req({ id: 'i1', draft: true }), req({ id: 'i2', draft: true })]
  )
  assert.equal(plan.affected.length, 2)
})

// A tab whose request no longer exists cannot have unsaved edits to lose.
test('a tab with no matching request is skipped', () => {
  const plan = planUnsavedClose([tab({ itemId: 'gone' })], [req({ draft: true })])
  assert.equal(plan.requiresConfirmation, false)
})

// Same item id in two collections is two different requests; matching on the
// item id alone would attribute one collection's draft to the other.
test('the request is matched on collection AND item', () => {
  const plan = planUnsavedClose(
    [tab({ collectionId: 'c2', itemId: 'i1' })],
    [req({ collectionId: 'c1', id: 'i1', draft: true })]
  )
  assert.equal(plan.requiresConfirmation, false, 'a draft in another collection must not match')
})

test('tabs with no collection or item are skipped', () => {
  const plan = planUnsavedClose(
    [tab({ collectionId: '' }), tab({ id: 't2', itemId: '' })],
    [req({ draft: true })]
  )
  assert.equal(plan.affected.length, 0)
})

// The dialog lists names; a blank one would show an empty row.
test('an unnamed request gets a readable fallback', () => {
  const plan = planUnsavedClose([tab()], [req({ name: '', draft: true })])
  assert.equal(plan.affected[0].requestName, 'Untitled request')
})

test('no tabs means nothing to confirm', () => {
  const plan = planUnsavedClose([], [req({ draft: true })])
  assert.equal(plan.requiresConfirmation, false)
  assert.equal(plan.affected.length, 0)
})
