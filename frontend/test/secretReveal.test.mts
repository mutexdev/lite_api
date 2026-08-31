// A masked field can be looked at, wherever it is.
//
// A5-08. The app had two postures on one concept. A `{{secretVar}}` reference
// got a tooltip with an explicit Show/Hide button. A literal secret typed into
// an Auth field — Bearer token, Client secret, AWS secret key, API key value,
// OAuth1 consumer secret — was a bare `type="password"` with no reveal at all,
// so the only way to check a paste was to clear it and paste again. The case
// with no affordance was the common one: most Auth-tab secrets are literal
// text.
//
// A5-10 is the same absence one screen over: the `.env` table, which is where
// people paste raw keys, had no secret concept whatsoever — not even the
// non-functional checkbox the Environment tables had.
//
// Nothing errors when either regresses. Asserted against source text for the
// same reason secretMasking.test.mts is: there is no component-rendering
// harness in this repo.

import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (relative: string) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

test('the masked input primitive can be revealed, and says so in the same words the tooltip does', () => {
  // Show/Hide rather than an eye glyph, deliberately: those are the exact words
  // the variable tooltip already uses for this exact gesture. One vocabulary
  // was the whole point.
  const input = read('../src/lib/SecretInput.svelte')

  assert.match(input, /type=\{revealed \? 'text' : 'password'\}/)
  assert.match(input, /\{revealed \? 'Hide' : 'Show'\}/)
  assert.match(input, /aria-pressed=\{revealed\}/)
})

test('a reveal never survives a remount', () => {
  // "I looked at this secret" is not worth persisting, and a reveal that
  // outlived a tab switch would put a credential back on screen unasked.
  const input = read('../src/lib/SecretInput.svelte')

  assert.match(input, /let revealed = \$state\(false\)/)
})

test('every auth secret goes through the primitive, not a bare password input', () => {
  // The form renders one control per field kind, so this is asserted at the
  // schema's rendering site rather than field by field.
  const form = read('../src/lib/AuthForm.svelte')

  assert.match(form, /\{#if field\.kind === 'secret'\}\s*<SecretInput/)
  assert.ok(
    !/type="password"/.test(form),
    'AuthForm is rendering a bare masked input again instead of SecretInput'
  )
})

test('the auth forms left no bare password input behind in App.svelte', () => {
  // Ten of these lived in the four hand-copied auth forms. Two remain, and both
  // are outside this pass's territory: the collection PROXY password and a
  // client certificate passphrase, which belong to the proxy and certificate
  // surfaces rather than to auth. They are listed rather than excused, so a
  // NEW bare masked input — the shape of the original bug — fails here.
  const app = read('../src/App.svelte')
  const remaining = [...app.matchAll(/aria-label="([^"]*)"[^>]*type="password"/g)].map((match) => match[1])

  assert.deepEqual(remaining.sort(), ['Client certificate passphrase', 'Collection proxy password'])
})

test('the .env table has a secret concept and it reaches the primitive', () => {
  // A5-10. A .env file has nowhere to store a per-row secret flag — it is
  // name=value lines — so masking is a property of the view. One switch hides
  // every value; SecretInput's own Show reveals one at a time.
  const app = read('../src/App.svelte')

  assert.match(app, /aria-label="Mask \.env values"/)
  assert.match(app, /\{#if dotEnvMaskValues\}[\s\S]{0,200}<SecretInput/)
})
