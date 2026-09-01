// A variable marked Secret is masked wherever it is edited.
//
// The Secret checkbox existed in six variable tables — folder pre-request,
// folder post-response, collection variables (twice), the environment editor
// and the global environment editor — and in none of them did ticking it change
// the value field. `KeyValueTable.svelte` had done this correctly the whole
// time, so the app both knew the right answer and failed to apply it on the
// screens most likely to hold a real credential.
//
// Nothing errors when this breaks. The checkbox still ticks, the value is still
// stored with its secret flag, and masking still works everywhere else — the
// token simply sits on screen in plain text, in an app people run during screen
// shares and pair sessions.
//
// Asserted against source text because the repo has no component-rendering
// harness; see syntaxHighlight.test.mts and brandMark.test.mts for the same
// approach.
import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (relative: string) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')
const app = read('../src/App.svelte')

/**
 * Every table row that offers a Secret checkbox, and the value input beside it.
 *
 * Keyed by the checkbox's own update call so a renamed table still matches, and
 * so adding a seventh secret-bearing table without masking it fails the count
 * assertion at the bottom rather than passing unnoticed.
 */
const secretCheckboxes = [
  "updateFolderVariable('variables', index, 'secret'",
  "updateFolderVariable('resVariables', index, 'secret'",
  "updateCollectionVariable(index, 'secret'",
  "updateGlobalEnvironmentVariable(row.index, 'secret'",
  "updateEnvironmentVariable(row.index, 'secret'"
]

test('every secret-bearing variable table still exists', () => {
  for (const call of secretCheckboxes) {
    assert.ok(app.includes(call), `no table found for ${call} — was it renamed?`)
  }
})

test('every variable value input is masked when the row is secret', () => {
  // The rule, stated positively: an input whose value comes from a variable's
  // `.value` must carry a `type` bound to that same variable's `.secret`.
  const valueInputs = [...app.matchAll(/<input[^>]*value=\{String\((variable|row\.variable)\.value \?\? ''\)\}[^>]*>/g)]
  assert.ok(valueInputs.length >= 6, `expected at least six variable value inputs, found ${valueInputs.length}`)
  for (const match of valueInputs) {
    const element = match[0]
    const subject = match[1]
    assert.match(
      element,
      new RegExp(`type=\\{${subject.replace('.', '\\.')}\\.secret \\? 'password' : 'text'\\}`),
      `this value input is not masked for secrets:\n${element}`
    )
  }
})

test('the number of secret checkboxes and masked value inputs agree', () => {
  // The failure this catches is a NEW table added with a Secret checkbox and a
  // plain value field — the exact shape of the original bug, which arrived the
  // same way.
  const checkboxes = [...app.matchAll(/checked=\{(?:variable|row\.variable)\.secret\}/g)].length
  const masked = [...app.matchAll(/type=\{(?:variable|row\.variable)\.secret \? 'password' : 'text'\}/g)].length
  assert.equal(masked, checkboxes, `${checkboxes} tables offer a Secret checkbox but only ${masked} mask the value`)
})

test('the shared key/value table has not lost its masking either', () => {
  // This one was already correct. It is pinned so a future refactor that unifies
  // the tables cannot regress the working case while fixing the broken ones.
  const table = read('../src/lib/KeyValueTable.svelte')
  assert.match(table, /type=\{row\.secret \? 'password' : 'text'\}/)
})
