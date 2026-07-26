// US-057 — tests for keybinding defaults and the Postman preset.
//
// The assertion that carries the story is that applying a preset introduces no
// COLLISION. A preset shipped with a duplicate combo does not fail loudly: it
// shadows one of the two actions for every user who selects it, and the action
// simply stops responding to its shortcut.
//
// These run against the real default table, which is why it was moved out of
// App.svelte. A test that restated the defaults would drift from them and then
// pass while the shipped table collided.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  isKeyBindingModifier,
  validateKeyBinding,
  keyBindingParts,
  keyBindingSections,
  keyBindingPresets,
  effectiveKeyBindings,
  findKeyBindingCollisions,
  keyBindingSignature,
  normalizeKeyBindingPreset,
  type KeyBindingOS
} from '../src/lib/keybindings.ts'

const operatingSystems: KeyBindingOS[] = ['mac', 'windows']

test('the shipped defaults contain no collisions', () => {
  for (const os of operatingSystems) {
    const effective = effectiveKeyBindings(keyBindingSections, keyBindingPresets.default)
    const collisions = findKeyBindingCollisions(effective, os)
    assert.deepEqual(collisions, [], `${os} defaults collide: ${JSON.stringify(collisions)}`)
  }
})

// The story's criterion. A preset that collides silently disables an action.
test('the Postman preset introduces no collisions on either OS', () => {
  for (const os of operatingSystems) {
    const effective = effectiveKeyBindings(keyBindingSections, keyBindingPresets.postman)
    const collisions = findKeyBindingCollisions(effective, os)
    assert.deepEqual(collisions, [], `${os} Postman preset collides: ${JSON.stringify(collisions)}`)
  }
})

test('every preset entry names an action that actually exists', () => {
  const known = new Set(Object.keys(effectiveKeyBindings(keyBindingSections, {})))
  for (const [presetID, preset] of Object.entries(keyBindingPresets)) {
    for (const action of Object.keys(preset)) {
      assert.ok(known.has(action), `preset ${presetID} binds unknown action ${action}`)
    }
  }
})

// A preset entry that matches the default is dead weight: it implies a change
// where there is none and has to be kept in sync with the defaults forever.
test('every Postman preset entry actually differs from the default', () => {
  const defaults = effectiveKeyBindings(keyBindingSections, {})
  for (const [action, override] of Object.entries(keyBindingPresets.postman)) {
    const base = defaults[action]
    assert.ok(base, `${action} is not in the defaults`)
    const changed = (['mac', 'windows'] as KeyBindingOS[]).some((os) => override[os] && override[os] !== base[os])
    assert.ok(changed, `${action} restates its default and should be dropped from the preset`)
  }
})

// The point of the Postman preset: Postman reserves Cmd/Ctrl+T for New Tab,
// while this app binds Open in Terminal to it.
test('the Postman preset frees Cmd/Ctrl+T', () => {
  const effective = effectiveKeyBindings(keyBindingSections, keyBindingPresets.postman)
  for (const os of operatingSystems) {
    const reserved = os === 'mac' ? 'command+bind+t' : 'ctrl+bind+t'
    const signature = keyBindingSignature(reserved)
    for (const [action, definition] of Object.entries(effective)) {
      const combo = definition[os]
      if (!combo) continue
      assert.notEqual(
        keyBindingSignature(combo),
        signature,
        `${action} still holds ${reserved} under the Postman preset`
      )
    }
  }
})

test('the default preset changes nothing', () => {
  const plain = effectiveKeyBindings(keyBindingSections, {})
  const withDefault = effectiveKeyBindings(keyBindingSections, keyBindingPresets.default)
  assert.deepEqual(withDefault, plain)
})

test('a preset overrides only the OS keys it specifies', () => {
  const effective = effectiveKeyBindings(keyBindingSections, {
    openTerminal: { mac: 'command+bind+alt+bind+t' }
  })
  const plain = effectiveKeyBindings(keyBindingSections, {})
  assert.equal(effective.openTerminal.mac, 'command+bind+alt+bind+t')
  assert.equal(effective.openTerminal.windows, plain.openTerminal.windows, 'the untouched OS changed')
  assert.equal(effective.openTerminal.name, plain.openTerminal.name, 'the display name was lost')
})

test('effectiveKeyBindings does not mutate the source sections', () => {
  const before = JSON.stringify(keyBindingSections)
  effectiveKeyBindings(keyBindingSections, keyBindingPresets.postman)
  assert.equal(JSON.stringify(keyBindingSections), before, 'the defaults were mutated in place')
})

test('keyBindingSignature makes modifier order irrelevant', () => {
  assert.equal(
    keyBindingSignature('shift+bind+command+bind+p'),
    keyBindingSignature('command+bind+shift+bind+p')
  )
  assert.notEqual(keyBindingSignature('command+bind+p'), keyBindingSignature('command+bind+shift+bind+p'))
})

// The tab-number row is declared once as a hidden display range (Cmd+1 - Cmd+8)
// and again as eight individual actions. Counting the hidden entry would report
// a collision that does not exist and make the real check useless.
test('hidden entries are excluded from collision detection', () => {
  const collisions = findKeyBindingCollisions(
    {
      real: { name: 'Real', mac: 'command+bind+1' },
      hiddenRange: { name: 'Range', mac: 'command+bind+1', hidden: true }
    },
    'mac'
  )
  assert.deepEqual(collisions, [])
})

test('findKeyBindingCollisions reports a genuine duplicate', () => {
  const collisions = findKeyBindingCollisions(
    {
      first: { name: 'First', mac: 'command+bind+j' },
      second: { name: 'Second', mac: 'command+bind+j' }
    },
    'mac'
  )
  assert.equal(collisions.length, 1)
  assert.deepEqual(collisions[0].sort(), ['first', 'second'])
})

test('an action with no combo for an OS is not a collision', () => {
  const collisions = findKeyBindingCollisions(
    {
      first: { name: 'First', mac: 'command+bind+j' },
      second: { name: 'Second', windows: 'ctrl+bind+j' }
    },
    'mac'
  )
  assert.deepEqual(collisions, [])
})

test('normalizeKeyBindingPreset accepts only known ids', () => {
  assert.equal(normalizeKeyBindingPreset('postman'), 'postman')
  assert.equal(normalizeKeyBindingPreset('default'), 'default')
  assert.equal(normalizeKeyBindingPreset(undefined), 'default')
  assert.equal(normalizeKeyBindingPreset(''), 'default')
  assert.equal(normalizeKeyBindingPreset('nonsense'), 'default')
})

// There used to be a second keyBindingSignature in App.svelte that lowercased
// where this one did not, and did not sort the non-modifier keys. They agreed
// only because every combo reaching them happened to be lowercase and
// single-keyed — a coincidence of input, not of behaviour. These pin the two
// properties that differed, so one implementation stays one implementation.
test('signatures are case-insensitive', () => {
  assert.equal(
    keyBindingSignature('Ctrl+bind+K'),
    keyBindingSignature('ctrl+bind+k'),
    'a case-sensitive signature makes Ctrl+K and ctrl+k different shortcuts, and the collision check waves the duplicate through'
  )
})

test('modifier order does not change a signature', () => {
  assert.equal(keyBindingSignature('shift+bind+command+bind+k'), keyBindingSignature('command+bind+shift+bind+k'))
})

test('non-modifier keys are ordered too', () => {
  assert.equal(
    keyBindingSignature('command+bind+1+bind+command+bind+8'),
    keyBindingSignature('command+bind+8+bind+command+bind+1'),
    'the hidden tab-range binding lists several keys; their order must not create a false distinction'
  )
})

test('different shortcuts still produce different signatures', () => {
  assert.notEqual(keyBindingSignature('ctrl+bind+k'), keyBindingSignature('ctrl+bind+j'))
  assert.notEqual(keyBindingSignature('ctrl+bind+k'), keyBindingSignature('shift+bind+k'))
})

test('keyBindingParts trims and drops empties', () => {
  assert.deepEqual(keyBindingParts(' ctrl +bind+ k '), ['ctrl', 'k'])
  assert.deepEqual(keyBindingParts(''), [])
})

test('isKeyBindingModifier knows exactly the four modifiers', () => {
  for (const modifier of ['ctrl', 'command', 'alt', 'shift']) {
    assert.equal(isKeyBindingModifier(modifier), true, modifier)
  }
  for (const key of ['k', 'enter', 'meta', 'super', '']) {
    assert.equal(isKeyBindingModifier(key), false, key)
  }
})

const validationBindings: Record<string, { mac?: string; windows?: string; name: string; hidden?: boolean }> = {
  save: { mac: 'command+bind+s', windows: 'ctrl+bind+s', name: 'Save' },
  find: { mac: 'command+bind+f', windows: 'ctrl+bind+f', name: 'Find' },
  switchToTab1: { mac: 'command+bind+1', windows: 'ctrl+bind+1', name: 'Tab 1', hidden: true }
}

// A combo with no modifier would swallow a plain keystroke everywhere in the
// app, so the shape rules come before the collision check.
test('a binding needs exactly one key and at least one modifier', () => {
  const check = (combo: string) => validateKeyBinding('new', combo, validationBindings as never, 'mac')
  assert.match(check('k'), /at least one modifier/, 'a bare key is not a shortcut')
  assert.match(check('command+bind+k+bind+j'), /one key/, 'two non-modifier keys is not a shortcut')
  assert.match(check('command+bind+shift+bind+alt+bind+ctrl+bind+k'), /one key/, 'four modifiers plus a key is too long')
  assert.equal(check('command+bind+k'), '', 'one modifier and one key is valid')
  assert.equal(check('command+bind+shift+bind+k'), '', 'two modifiers are fine')
})

test('a combo already in use is rejected', () => {
  assert.match(
    validateKeyBinding('new', 'command+bind+s', validationBindings as never, 'mac'),
    /already in use/
  )
  assert.equal(
    validateKeyBinding('save', 'command+bind+s', validationBindings as never, 'mac'),
    '',
    'an action keeping its own combo is not a collision with itself'
  )
})

test('collision detection is per operating system', () => {
  assert.match(validateKeyBinding('new', 'ctrl+bind+s', validationBindings as never, 'windows'), /already in use/)
  assert.equal(
    validateKeyBinding('new', 'ctrl+bind+s', validationBindings as never, 'mac'),
    '',
    'a Windows combo does not collide on mac, where the actions use command'
  )
})

// findKeyBindingCollisions skips hidden entries to avoid double-reporting the
// tab range; this must NOT, because a hidden binding still owns its combo and
// saying otherwise hands the user a shortcut that never fires.
test('a hidden binding still occupies its combo', () => {
  assert.match(
    validateKeyBinding('new', 'command+bind+1', validationBindings as never, 'mac'),
    /already in use/,
    'switchToTab1 is hidden but owns Cmd+1'
  )
})

// The stored combo and the typed one must compare by SIGNATURE, not as raw
// strings. My first attempt at this test built the "different order" combo with
// a .replace that produced a raw-identical string, so it passed with either
// comparison and proved nothing — the control caught that.
test('modifier order does not defeat the collision check', () => {
  const ordered = { reorder: { mac: 'alt+bind+command+bind+k', name: 'Reorder' } }
  assert.match(
    validateKeyBinding('new', 'command+bind+alt+bind+k', ordered as never, 'mac'),
    /already in use/,
    'the same modifiers in a different order are the same shortcut'
  )
})
