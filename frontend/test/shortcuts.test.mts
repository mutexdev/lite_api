import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  configurableShortcutActions,
  editorOwnedShortcutActions,
  resolveShortcut,
  shortcutTabNumber,
  type ShortcutContext,
} from '../src/lib/shortcuts.ts'

function context(over: Partial<ShortcutContext> = {}): ShortcutContext {
  return {
    commandPaletteOpen: false,
    requestActionMenuOpen: false,
    modalOpen: false,
    activeView: 'request',
    canCancel: false,
    keybindingsEnabled: true,
    editingInCodeEditor: false,
    matches: () => false,
    ...over,
  }
}

function bound(action: string): Pick<ShortcutContext, 'matches'> {
  return { matches: (candidate) => candidate === action }
}

const escape = { key: 'Escape', metaKey: false, ctrlKey: false }
const other = { key: 'k', metaKey: true, ctrlKey: false }

test('an unbound key resolves to nothing', () => {
  assert.equal(resolveShortcut(other, context()), undefined)
})

// The palette is a layer over everything. If Escape reached a lower handler
// first the palette would stay open with its input focused, swallowing every
// subsequent key.
test('escape closes the command palette before anything else can claim it', () => {
  const ctx = context({ commandPaletteOpen: true, canCancel: true, requestActionMenuOpen: true })
  assert.equal(resolveShortcut(escape, ctx), 'closeCommandPalette')
})

test('escape closes an open request-actions menu', () => {
  assert.equal(resolveShortcut(escape, context({ requestActionMenuOpen: true })), 'closeRequestActionMenus')
})

test('escape cancels a running request when nothing is layered over it', () => {
  assert.equal(resolveShortcut(escape, context({ canCancel: true })), 'cancelActiveRequest')
})

test('escape does nothing when there is nothing to cancel or close', () => {
  assert.equal(resolveShortcut(escape, context()), undefined)
})

// A modal owns Escape while it is open — that is how it is dismissed. Resolving
// to a cancel here would stop the request AND leave the dialog open, or close
// both at once depending on which handler ran first.
test('a modal keeps escape for itself rather than cancelling the request', () => {
  const ctx = context({ canCancel: true, modalOpen: true })
  assert.equal(resolveShortcut(escape, ctx), undefined)
})

// Returning rather than falling through matters: Escape inside a modal must do
// exactly one thing, and matching a user binding as well is how a dialog closes
// and fires an unrelated action on the same keypress.
test('escape in a modal does not fall through to a configured binding', () => {
  const ctx = context({ canCancel: true, modalOpen: true, matches: () => true })
  assert.equal(resolveShortcut(escape, ctx), undefined)
})

test('the url bar is focused on the command modifier plus L, on either platform', () => {
  for (const event of [
    { key: 'l', metaKey: true, ctrlKey: false },
    { key: 'L', metaKey: false, ctrlKey: true },
  ]) {
    assert.equal(resolveShortcut(event, context()), 'focusURL', event.key)
  }
})

test('the url shortcut only applies while a request is on screen', () => {
  const event = { key: 'l', metaKey: true, ctrlKey: false }
  assert.equal(resolveShortcut(event, context({ activeView: 'preferences' })), undefined)
})

// The whole point of the ordering: turning custom keybindings off must not
// leave the user with no way to dismiss the palette or stop a running request.
// Those keys are not configurable, so they are not the user's to disable.
test('the fixed keys keep working when custom keybindings are disabled', () => {
  const off = { keybindingsEnabled: false }
  assert.equal(resolveShortcut(escape, context({ ...off, commandPaletteOpen: true })), 'closeCommandPalette')
  assert.equal(resolveShortcut(escape, context({ ...off, canCancel: true })), 'cancelActiveRequest')
  assert.equal(
    resolveShortcut({ key: 'l', metaKey: true, ctrlKey: false }, context(off)),
    'focusURL',
  )
})

test('configured bindings stop resolving when keybindings are disabled', () => {
  const ctx = context({ keybindingsEnabled: false, ...bound('sendRequest') })
  assert.equal(resolveShortcut(other, ctx), undefined)
})

test('every configurable action resolves when its binding matches', () => {
  for (const action of configurableShortcutActions) {
    assert.equal(resolveShortcut(other, context(bound(action))), action, action)
  }
})

// The list is the behaviour. A dropped entry produces no error — the key simply
// stops doing anything, which reads as a binding the user mis-set.
test('the configurable list holds every action exactly once', () => {
  assert.equal(new Set(configurableShortcutActions).size, configurableShortcutActions.length)
  assert.equal(configurableShortcutActions.length, 33)
})

// The two groups can be bound to overlapping combos, and whichever is checked
// first wins. Reordering them silently changes which tab a keypress selects.
test('the last-tab binding is matched before the numbered tabs', () => {
  const ctx = context({ matches: (action) => action === 'switchToLastTab' || action === 'switchToTab1' })
  assert.equal(resolveShortcut(other, ctx), 'switchToLastTab')
  assert.ok(
    configurableShortcutActions.indexOf('switchToLastTab') <
      configurableShortcutActions.indexOf('switchToTab1'),
  )
})

// A context where several bindings match is not hypothetical: collisions are
// permitted and the preferences screen only warns about them.
test('the first matching binding in order wins', () => {
  const ctx = context({ matches: () => true })
  assert.equal(resolveShortcut(other, ctx), configurableShortcutActions[0])
})

test('a tab action carries its 1-based number', () => {
  assert.equal(shortcutTabNumber('switchToTab1'), 1)
  assert.equal(shortcutTabNumber('switchToTab8'), 8)
  assert.equal(shortcutTabNumber('switchToLastTab'), undefined)
  assert.equal(shortcutTabNumber('save'), undefined)
})

// switchToTab9 is not a binding. Accepting it would index one past the eight
// the UI offers, selecting a tab the shortcut sheet never claimed existed.
test('only tabs one through eight are numbered actions', () => {
  assert.equal(shortcutTabNumber('switchToTab9' as never), undefined)
  assert.equal(shortcutTabNumber('switchToTab10' as never), undefined)
})

// The caller passes the two DOM probes as getters, because this runs on every
// keystroke including ordinary typing and both selectors only matter on Escape.
// That only helps if the resolver actually leaves them unread — so it is pinned
// here rather than left as an assumption about the call site.
test('the DOM-probing fields are not read for a key that cannot use them', () => {
  const read: string[] = []
  const ctx = context({ ...bound('sendRequest') })
  const probed: ShortcutContext = {
    ...ctx,
    get requestActionMenuOpen() {
      read.push('requestActionMenuOpen')
      return false
    },
    get modalOpen() {
      read.push('modalOpen')
      return false
    },
  }
  assert.equal(resolveShortcut(other, probed), 'sendRequest')
  assert.deepEqual(read, [], 'a non-Escape key triggered a document query')
})

// And the modal probe stays unread even on Escape when there is nothing to
// cancel, since the check it guards is never reached.
test('the modal probe is only read when escape has a request to cancel', () => {
  let modalReads = 0
  const base = context()
  const probe = (over: Partial<ShortcutContext>): ShortcutContext => ({
    ...base,
    ...over,
    get modalOpen() {
      modalReads += 1
      return false
    },
  })
  resolveShortcut(escape, probe({}))
  assert.equal(modalReads, 0)
  resolveShortcut(escape, probe({ canCancel: true }))
  assert.equal(modalReads, 1)
})

// --- the code editor's claim on ⌘F ---------------------------------------
//
// ⌘F was bound to Search Sidebar globally AND to find-in-document by CodeMirror,
// with no guard on either side. Pressing it with the caret in a request body
// opened the editor's find and simultaneously threw focus to the sidebar
// filter — two search UIs answering one keypress.

test('the sidebar search shortcut yields to a focused code editor', () => {
  const ctx = context({ ...bound('sidebarSearch'), editingInCodeEditor: true })
  assert.equal(resolveShortcut(other, ctx), undefined)
})

test('the sidebar search shortcut still fires when focus is anywhere else', () => {
  // Including plain inputs: ⌘F in the URL bar should reach the sidebar filter,
  // which is why the guard tests for a code editor and not for "is typing".
  const ctx = context({ ...bound('sidebarSearch'), editingInCodeEditor: false })
  assert.equal(resolveShortcut(other, ctx), 'sidebarSearch')
})

test('a focused editor withholds only the actions it actually claims', () => {
  // The failure this guards against is over-correction: suppressing the whole
  // configurable set while typing would break ⌘S, ⌘Enter and ⌘W in the exact
  // place they are used most — inside a body being edited.
  for (const action of configurableShortcutActions) {
    if (editorOwnedShortcutActions.includes(action)) continue
    const ctx = context({ ...bound(action), editingInCodeEditor: true })
    assert.equal(resolveShortcut(other, ctx), action, `${action} must survive editor focus`)
  }
})

test('save and send in particular survive editor focus', () => {
  // Named explicitly rather than left to the loop above, because these three
  // are the ones a regression would be reported for.
  for (const action of ['save', 'sendRequest', 'closeTab']) {
    const ctx = context({ ...bound(action), editingInCodeEditor: true })
    assert.equal(resolveShortcut(other, ctx), action)
  }
})

test('escape still cancels a request from inside an editor', () => {
  // Escape is resolved before the configurable gate, so editor focus must not
  // reach it. If it did, a long request started from the body editor would
  // become uncancellable without first clicking away.
  const ctx = context({ canCancel: true, editingInCodeEditor: true })
  assert.equal(resolveShortcut(escape, ctx), 'cancelActiveRequest')
})

test('every editor-owned action is a real configurable action', () => {
  for (const action of editorOwnedShortcutActions) {
    assert.ok(configurableShortcutActions.includes(action), `${action} is not configurable, so the guard is dead code`)
  }
})
