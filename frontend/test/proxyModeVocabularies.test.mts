import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { preferenceProxyModeValue, proxyPreferencesWithDefaults } from '../src/lib/authDefaults.ts'
import { preferencesProxyMode } from '../src/lib/workbench/commandState.ts'
import type { types } from '../wailsjs/go/models'

// TWO FUNCTIONS, TWO VOCABULARIES, AND THEY DO NOT USE THE SAME WORD FOR THE
// SAME STATE. A near-name scan flagged them: preferenceProxyModeValue and
// preferencesProxyMode differ by two letters, both derive "which proxy mode is
// this", and their default arms return DIFFERENT STRINGS — "system" and
// "inherit".
//
// That is not a bug. They serve different sides of a boundary:
//
//   preferencesProxyMode    -> the <select>'s value. Its options are
//                              off / manual / inherit / pac.
//   preferenceProxyModeValue -> the PERSISTED legacy `preferences.proxyMode`
//                              field, whose vocabulary is off / manual / pac /
//                              system.
//
// The two only stay consistent because "system" round-trips through the legacy
// reader's default back to "inherit". Nothing checked that, and unifying the
// two defaults — the obvious tidy-up when the names look this alike — would
// leave the select with a value matching none of its options, rendering blank.

/** Mirrors updatePreferencesProxyMode in App.svelte. */
function applyUIMode(mode: string): types.ProxyPreferences {
  if (mode === 'off') return { disabled: true, source: 'manual' } as types.ProxyPreferences
  if (mode === 'manual') return { disabled: false, source: 'manual' } as types.ProxyPreferences
  if (mode === 'pac') return { disabled: false, source: 'pac' } as types.ProxyPreferences
  return { disabled: false, source: 'inherit' } as types.ProxyPreferences
}

const uiModes = ['off', 'manual', 'inherit', 'pac']

// THE ROUND TRIP. Pick a mode in the select, persist it, read it back, and the
// select must show the same mode. This walks both functions and the legacy
// field between them.
test('every proxy mode survives a save and reload', () => {
  for (const mode of uiModes) {
    const chosen = applyUIMode(mode)

    // What gets written to the legacy field.
    const persisted = preferenceProxyModeValue(chosen)

    // What comes back on reload: the stored proxy plus the legacy field.
    const restored = proxyPreferencesWithDefaults(chosen, persisted)
    const displayed = preferencesProxyMode({ proxy: restored } as types.Preferences)

    assert.equal(displayed, mode, `${mode} came back as ${mode === displayed ? '' : displayed} after a round trip`)
  }
})

// And with the stored proxy LOST — the migration case, where only the legacy
// field survives. This is the path the legacy field exists for.
test('a mode survives when only the legacy field remains', () => {
  for (const [mode, expected] of [['manual', 'manual'], ['pac', 'pac'], ['system', 'inherit'], ['', 'inherit']]) {
    const restored = proxyPreferencesWithDefaults(undefined, mode)
    const displayed = preferencesProxyMode({ proxy: restored } as types.Preferences)
    assert.equal(displayed, expected, `legacy proxyMode ${JSON.stringify(mode)} displayed as ${displayed}`)
  }
})

// THE SELECT MUST OFFER EVERY VALUE THE FUNCTION CAN RETURN. A value with no
// matching option renders as a blank control, and the next change event sends
// whatever the user lands on.
test('the proxy select offers exactly the modes the reader can produce', () => {
  const markup = readFileSync(
    fileURLToPath(new URL('../src/lib/views/preferences/ProxySection.svelte', import.meta.url)),
    'utf8',
  )
  const selectStart = markup.indexOf('aria-label="App proxy mode"')
  assert.ok(selectStart > 0, 'the proxy select is gone or was renamed')
  const selectEnd = markup.indexOf('</select>', selectStart)
  const options = new Set(
    [...markup.slice(selectStart, selectEnd).matchAll(/<option value="([a-z]+)"/g)].map((m) => m[1]),
  )

  assert.deepEqual([...options].sort(), [...uiModes].sort(), 'the select options and the reader vocabulary have drifted')

  // Every mode the reader can return is offered.
  for (const mode of uiModes) {
    const restored = proxyPreferencesWithDefaults(applyUIMode(mode), preferenceProxyModeValue(applyUIMode(mode)))
    const displayed = preferencesProxyMode({ proxy: restored } as types.Preferences)
    assert.ok(options.has(displayed), `the reader returns ${displayed}, which the select does not offer`)
  }
})

// The two defaults are DIFFERENT ON PURPOSE. Pinned so that unifying them is a
// decision rather than a tidy-up.
test('the two readers deliberately disagree on their default word', () => {
  assert.equal(preferenceProxyModeValue({} as types.ProxyPreferences), 'system')
  assert.equal(preferencesProxyMode({ proxy: {} } as types.Preferences), 'inherit')
  assert.notEqual(
    preferenceProxyModeValue({} as types.ProxyPreferences),
    preferencesProxyMode({ proxy: {} } as types.Preferences),
  )
})
