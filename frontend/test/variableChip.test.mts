// One {{variable}} chip, one tooltip, and the states they can be in.
//
// A5-07. Nothing here throws when it breaks — that is the point. The chip had
// three implementations with three border radii, two different "valid"
// backgrounds and a secret treatment present in exactly one of them, and the
// only symptom was that the app looked like three apps. So the guard is
// structural: the state mapping is asserted directly, and the number of places
// that render a chip or a tooltip is asserted against the source, because "we
// unified these" is a claim that decays the moment someone needs a fourth one
// in a hurry.

import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { variableChipLabel, variableChipState } from '../src/lib/variableChipState.ts'

const read = (relative: string) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const resolved = { found: true, validName: true, secret: false }

test('a resolved variable is resolved, and a secret one is distinguishable from it', () => {
  assert.equal(variableChipState(resolved), 'resolved')
  assert.equal(variableChipState({ ...resolved, secret: true }), 'secret')
})

test('an invalid name outranks not being found', () => {
  // An invalid name cannot meaningfully be "found", and calling it missing would
  // send the user off to define a variable that can never resolve.
  assert.equal(variableChipState({ found: false, validName: false }), 'invalid')
  assert.equal(variableChipState({ found: true, validName: false }), 'invalid')
})

test('a usable name that nothing defines is missing, not invalid', () => {
  // Different problems, different fixes, and only one of them deserves the red.
  assert.equal(variableChipState({ found: false, validName: true }), 'missing')
})

test('a prompt variable is never reported as missing', () => {
  // It has no value until the user is asked at send time. Flagging it would be
  // flagging the feature working.
  assert.equal(variableChipState(undefined, true), 'prompt')
  assert.equal(variableChipState({ found: false, validName: true }, true), 'prompt')
})

test('an absent info is missing rather than a crash', () => {
  assert.equal(variableChipState(undefined), 'missing')
})

test('every state has an accessible name that says which state it is', () => {
  // The CodeMirror decoration built one of these; the two DOM chips built none,
  // so a screen reader read literal braces with no indication of state.
  const states = [
    variableChipLabel('token', 'resolved', 'Environment'),
    variableChipLabel('token', 'secret', 'Environment'),
    variableChipLabel('token', 'missing'),
    variableChipLabel('token', 'invalid'),
    variableChipLabel('token', 'prompt')
  ]
  for (const label of states) assert.match(label, /\{\{token\}\}/)
  assert.match(states[1], /secret/i)
  assert.match(states[2], /not defined/i)
  assert.match(states[3], /invalid/i)
  assert.match(states[4], /send time/i)
  assert.equal(new Set(states).size, states.length, 'two states read identically')
})

test('the chip and the tooltip each have one implementation', () => {
  // Three copies of the tooltip markup is how the chips ended up with three
  // appearances: nothing made them move together, so they stopped.
  const app = read('../src/App.svelte')
  const overlay = read('../src/lib/VariableTextOverlay.svelte')

  for (const [name, source] of [['App.svelte', app], ['VariableTextOverlay.svelte', overlay]] as const) {
    assert.ok(source.includes('<VariableChip'), `${name} no longer renders the shared chip`)
    assert.doesNotMatch(
      source,
      /class:cm-variable-valid/,
      `${name} is hand-rolling a variable chip again`
    )
    assert.doesNotMatch(
      source,
      /class="var-scope-badge"/,
      `${name} is hand-rolling the variable tooltip again`
    )
  }
})

test('the chip states are painted from tokens, never from literals', () => {
  // --accent-soft vs --accent-tint, 3px vs 6px vs a hardcoded 2px: the previous
  // three implementations disagreed precisely because two of them wrote values
  // instead of naming them.
  //
  // The rules live in style.css, GLOBALLY, not in this component's scoped
  // block. That is not tidiness: the CodeMirror decoration is created outside
  // Svelte's component tree, so a scoped block cannot reach it — which is
  // exactly how the third implementation came to exist. Scoping the chip again
  // would re-open that door, so the component is asserted to hold no styles at
  // all.
  const chip = read('../src/lib/VariableChip.svelte')
  assert.ok(!chip.includes('<style>'), 'the chip has scoped styles again, which the CodeMirror surface cannot reach')

  const styles = read('../src/style.css')
  const chipRules = styles.slice(styles.indexOf('.variable-chip-pill,'))

  assert.match(chipRules, /border-radius: var\(--radius-4\)/)
  assert.ok(!/border-radius:\s*\d/.test(chipRules.slice(0, chipRules.indexOf('.variable-chip-pill:focus-visible'))), 'a literal radius is back in the chip')
  for (const state of ['resolved', 'secret', 'missing', 'invalid', 'prompt']) {
    assert.ok(chipRules.includes(`data-state='${state}'`), `no rule paints the ${state} state`)
  }

  // The CodeMirror class names must be painted by the SAME rules, or the editor
  // silently goes back to having its own appearance.
  for (const cmClass of ['.cm-variable-valid', '.cm-variable-missing', '.cm-variable-invalid', '.cm-variable-prompt']) {
    assert.ok(chipRules.includes(cmClass), `${cmClass} is not covered by the shared chip rules`)
  }

  // And the editor must not reintroduce its own theme entry for them.
  const editor = read('../src/lib/workbench/CodeEditor.svelte')
  assert.ok(!/'\.cm-variable[^']*':\s*\{/.test(editor), 'CodeEditor is theming the variable chip again')
})
