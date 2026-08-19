// The command palette's command list, and the shortcuts it advertises.
//
// The palette renders `commandPaletteCommandIDs` and labels each entry from
// `commandMetadata`, while the keys that actually DO anything live in a
// separate table in keybindings.ts. Two tables describing the same thing drift,
// and when they do the palette advertises a shortcut that is bound to nothing —
// the user presses it, nothing happens, and the palette is still telling them
// it should have.
//
// TypeScript already guarantees every command id has metadata (the record is
// keyed by the union). What it cannot check is any of what follows.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  commandPaletteCommandIDs,
  workbenchCommandMetadata,
  type WorkbenchCommandID
} from '../src/lib/workbench/workbenchCommands.ts'
import { keyBindingSections } from '../src/lib/keybindings.ts'

// The palette's own shortcut hint is written with the glyphs a Mac user sees;
// the binding table spells the same combo out. This is the translation between
// them, and it is deliberately strict — an unrecognised glyph must not quietly
// pass through as itself and match nothing.
function toBindingCombo(shortcut: string): string {
  const glyphs: Record<string, string> = {
    '⌘': 'command',
    '⇧': 'shift',
    '⌥': 'alt',
    '⌃': 'ctrl',
    '↵': 'enter'
  }
  return [...shortcut].map((char) => glyphs[char] ?? char.toLowerCase()).join('+bind+')
}

function macBindings(): Map<string, string> {
  const bindings = new Map<string, string>()
  for (const section of keyBindingSections) {
    for (const [id, binding] of Object.entries(section.bindings)) {
      if (binding.mac) bindings.set(binding.mac, id)
    }
  }
  return bindings
}

// Bound by the native application menu rather than the web keybinding table,
// because the renderer never sees it — Cmd+Alt+I opens the webview's own
// inspector before any JavaScript runs. See native_menu.go.
const nativeMenuOnly: WorkbenchCommandID[] = ['toggle-devtools']

test('every shortcut the palette advertises is actually bound', () => {
  const bindings = macBindings()
  const unbound: string[] = []

  for (const id of commandPaletteCommandIDs) {
    const shortcut = workbenchCommandMetadata(id).shortcut
    if (!shortcut || nativeMenuOnly.includes(id)) continue
    const combo = toBindingCombo(shortcut)
    if (!bindings.has(combo)) unbound.push(`${id} advertises ${shortcut} (${combo})`)
  }

  assert.deepEqual(unbound, [], 'the palette would show a shortcut that does nothing')
})

// The allowlist is asserted exactly so that adding another unbound shortcut is a
// decision someone has to write down, rather than a test that quietly widens.
test('only the documented commands are bound outside the keybinding table', () => {
  const bindings = macBindings()
  for (const id of nativeMenuOnly) {
    const shortcut = workbenchCommandMetadata(id).shortcut
    assert.ok(shortcut, `${id} is listed as native-menu-bound but advertises no shortcut`)
    assert.ok(
      !bindings.has(toBindingCombo(shortcut!)),
      `${id} now has a keybinding too; remove it from the native-menu list`
    )
  }
})

test('the palette lists no command twice', () => {
  const seen = new Set(commandPaletteCommandIDs)
  assert.equal(seen.size, commandPaletteCommandIDs.length, 'a duplicate renders the same row twice')
})

test('every listed command has a label', () => {
  for (const id of commandPaletteCommandIDs) {
    const label = workbenchCommandMetadata(id).label
    assert.ok(label && label.trim().length > 0, `${id} has no label`)
  }
})

// Two commands sharing a combo means one of them never fires, and which one is
// down to listener order — invisible until a user reports that a menu item does
// something else.
test('no two commands advertise the same shortcut', () => {
  const byShortcut = new Map<string, WorkbenchCommandID>()
  for (const id of commandPaletteCommandIDs) {
    const shortcut = workbenchCommandMetadata(id).shortcut
    if (!shortcut) continue
    const existing = byShortcut.get(shortcut)
    assert.equal(existing, undefined, `${id} and ${existing} both claim ${shortcut}`)
    byShortcut.set(shortcut, id)
  }
})

// The binding table is the one the settings UI edits, and a duplicate there
// shadows an action for every user.
test('no two keybindings share a combo', () => {
  for (const platform of ['mac', 'windows'] as const) {
    const seen = new Map<string, string>()
    for (const section of keyBindingSections) {
      for (const [id, binding] of Object.entries(section.bindings)) {
        const combo = binding[platform]
        // The per-position tab bindings deliberately restate the range that
        // switchToTabAtPosition displays, so they are expected to overlap it.
        if (!combo || binding.hidden) continue
        const existing = seen.get(combo)
        assert.equal(existing, undefined, `${platform}: ${id} and ${existing} both use ${combo}`)
        seen.set(combo, id)
      }
    }
  }
})

test('the glyph translation covers every glyph in use', () => {
  for (const id of commandPaletteCommandIDs) {
    const shortcut = workbenchCommandMetadata(id).shortcut
    if (!shortcut) continue
    const combo = toBindingCombo(shortcut)
    assert.ok(
      !/[^\x20-\x7e+]/.test(combo.replace(/\+bind\+/g, '')),
      `${shortcut} translated to ${combo}, which still contains an untranslated glyph`
    )
  }
})
