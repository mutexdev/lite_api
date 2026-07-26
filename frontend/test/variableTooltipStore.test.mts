// US-026 — tests for the variable-tooltip store.
//
// These pin the transitions that used to live as five separate props threaded
// through three components. Each is a small rule with a user-visible reason,
// and each fails quietly if broken: a tooltip that will not close, an edit
// seeded with the wrong text, or a "copied" badge left showing over a field the
// user is now typing into.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  emptyTooltipState,
  toggleActive,
  closeTooltip,
  beginEdit,
  cancelEdit,
  toggleRevealed,
  markCopied,
  resetTooltips
} from '../src/lib/stores/variableTooltipState.ts'

// The pure transitions, not the rune shell: a .svelte.ts module cannot be
// imported here because $state is a compiler construct. Testing the shell would
// mean a second test runner; testing the behaviour needs neither.
function createVariableTooltipStore() {
  let state = emptyTooltipState()
  return {
    get active() { return state.active },
    get editing() { return state.editing },
    get draft() { return state.draft },
    get revealed() { return state.revealed },
    get copied() { return state.copied },
    toggleActive: (name: string) => { state = toggleActive(state, name) },
    close: () => { state = closeTooltip(state) },
    beginEdit: (name: string, raw: string, found: boolean, editable: boolean) => {
      state = beginEdit(state, name, raw, found, editable)
    },
    cancelEdit: () => { state = cancelEdit(state) },
    toggleRevealed: (name: string) => { state = toggleRevealed(state, name) },
    isRevealed: (name: string) => Boolean(state.revealed[name]),
    markCopied: (name: string, copied: boolean) => { state = markCopied(state, name, copied) },
    isCopied: (name: string) => Boolean(state.copied[name]),
    reset: () => { state = resetTooltips() }
  }
}

test('toggleActive opens a tooltip and closes it on a second call', () => {
  const store = createVariableTooltipStore()
  assert.equal(store.active, '')

  store.toggleActive('host')
  assert.equal(store.active, 'host')

  // Closing on a second click is what makes the tooltip dismissable without a
  // separate close target.
  store.toggleActive('host')
  assert.equal(store.active, '', 'a second call on the same name must close it')
})

test('toggleActive switches directly between two tooltips', () => {
  const store = createVariableTooltipStore()
  store.toggleActive('host')
  store.toggleActive('token')
  assert.equal(store.active, 'token', 'opening another must not require closing the first')
})

test('close always closes regardless of what is open', () => {
  const store = createVariableTooltipStore()
  store.toggleActive('host')
  store.close()
  assert.equal(store.active, '')
  store.close()
  assert.equal(store.active, '', 'closing when nothing is open is not an error')
})

test('beginEdit opens the tooltip in edit mode seeded with the raw value', () => {
  const store = createVariableTooltipStore()
  store.beginEdit('host', 'https://api.test', true, true)
  assert.equal(store.active, 'host')
  assert.equal(store.editing, 'host')
  assert.equal(store.draft, 'https://api.test')
})

// The placeholder is not what the user typed. Seeding it would have them
// accidentally save a rendered default as a real value.
test('beginEdit seeds an empty draft when the variable is not found', () => {
  const store = createVariableTooltipStore()
  store.beginEdit('missing', 'a-rendered-placeholder', false, true)
  assert.equal(store.draft, '', 'an unfound variable must not offer its placeholder as the starting text')
  assert.equal(store.editing, 'missing')
})

test('beginEdit refuses a non-editable variable', () => {
  const store = createVariableTooltipStore()
  store.toggleActive('other')
  store.beginEdit('readonly', 'value', true, false)
  assert.equal(store.editing, '', 'a read-only variable must not enter edit mode')
  assert.equal(store.active, 'other', 'and must not steal the open tooltip')
})

// The confirmation would otherwise still be showing over a field the user is
// now editing.
test('beginEdit clears a lingering copied confirmation for that name', () => {
  const store = createVariableTooltipStore()
  store.markCopied('host', true)
  assert.equal(store.isCopied('host'), true)

  store.beginEdit('host', 'value', true, true)
  assert.equal(store.isCopied('host'), false)
})

test('beginEdit leaves other names copied flags alone', () => {
  const store = createVariableTooltipStore()
  store.markCopied('other', true)
  store.beginEdit('host', 'value', true, true)
  assert.equal(store.isCopied('other'), true, 'only the edited name should be cleared')
})

test('cancelEdit leaves the tooltip open but exits edit mode', () => {
  const store = createVariableTooltipStore()
  store.beginEdit('host', 'value', true, true)
  store.cancelEdit()
  assert.equal(store.editing, '')
  assert.equal(store.draft, '')
  // Deliberate: cancelling an edit should not also dismiss the tooltip the
  // user is still reading.
  assert.equal(store.active, 'host', 'cancelling an edit must not close the tooltip')
})

test('toggleRevealed flips a secret independently per name', () => {
  const store = createVariableTooltipStore()
  assert.equal(store.isRevealed('token'), false)

  store.toggleRevealed('token')
  assert.equal(store.isRevealed('token'), true)
  assert.equal(store.isRevealed('other'), false, 'revealing one secret must not reveal them all')

  store.toggleRevealed('token')
  assert.equal(store.isRevealed('token'), false)
})

test('markCopied sets and clears the confirmation flag', () => {
  const store = createVariableTooltipStore()
  store.markCopied('host', true)
  assert.equal(store.isCopied('host'), true)
  store.markCopied('host', false)
  assert.equal(store.isCopied('host'), false)
})

// A tooltip left open across a request switch would show a variable from the
// previous request, resolved against its scope.
test('reset clears everything', () => {
  const store = createVariableTooltipStore()
  store.beginEdit('host', 'value', true, true)
  store.toggleRevealed('token')
  store.markCopied('other', true)

  store.reset()
  assert.equal(store.active, '')
  assert.equal(store.editing, '')
  assert.equal(store.draft, '')
  assert.deepEqual(store.revealed, {})
  assert.deepEqual(store.copied, {})
})

test('each store instance is independent', () => {
  const a = createVariableTooltipStore()
  const b = createVariableTooltipStore()
  a.toggleActive('host')
  assert.equal(b.active, '', 'instances must not share state')
})
