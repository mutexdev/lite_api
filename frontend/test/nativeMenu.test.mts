import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  handledNativeMenuCommands,
  resolveNativeMenuCommand,
} from '../src/lib/nativeMenu.ts'

const goSource = readFileSync(
  fileURLToPath(new URL('../../native_menu.go', import.meta.url)),
  'utf8',
)

/** The command strings native_menu.go declares, read from the Go source. */
function goMenuCommands(): string[] {
  const block = /const \(([\s\S]*?)\n\)/.exec(goSource)
  assert.ok(block, 'could not find the command constant block in native_menu.go')
  const commands = [...block[1].matchAll(/menuCommand\w+\s+= "([a-z0-9-]+)"/g)].map((m) => m[1])
  assert.ok(commands.length > 10, `only found ${commands.length} commands; the parse is wrong`)
  return commands
}

// The contract this file exists for. Nothing type-checks across the Go/TS
// boundary: a menu item whose string the frontend does not handle is still
// drawn, still enabled, and still clickable — it just does nothing, with no
// error anywhere.
test('every command the Go menu declares is handled', () => {
  const handled = new Set(handledNativeMenuCommands())
  const missing = goMenuCommands().filter((command) => !handled.has(command))
  assert.deepEqual(missing, [], 'these native menu items would click and do nothing')
})

// The reverse direction is a warning, not a failure: a handler for a command
// nothing emits is dead, but harmless. Asserted anyway so it is a decision
// rather than an accumulation.
test('the only handled command the Go menu does not declare is open-history', () => {
  const declared = new Set(goMenuCommands())
  const extra = handledNativeMenuCommands().filter((command) => !declared.has(command))
  assert.deepEqual(extra, ['open-history'])
})

// Three names differ across the boundary, and only those three break on a
// rename. Matching names would survive a careless find-and-replace; these will
// not, and nothing but a runtime click would say so.
test('the three commands whose names differ map correctly', () => {
  const view = { activeView: 'request' }
  assert.deepEqual(resolveNativeMenuCommand('open-git', view), {
    kind: 'workbench',
    command: 'open-git-workbench',
  })
  assert.deepEqual(resolveNativeMenuCommand('open-workspace-in-new-window', view), {
    kind: 'workbench',
    command: 'open-workspace',
  })
})

// Two menu items, one command: "Import…" and "Open Collection…" are the same
// flow reached from two places in the menu.
test('import and open-collection reach the same workbench command', () => {
  const view = { activeView: 'request' }
  assert.deepEqual(
    resolveNativeMenuCommand('import', view),
    resolveNativeMenuCommand('open-collection', view),
  )
})

// send-or-start backs a single menu item that reads "Send" over a request and
// "Run" over the collection runner. Two items would be the alternative, and one
// of them would always be greyed out.
test('send-or-start branches on the active view', () => {
  assert.deepEqual(resolveNativeMenuCommand('send-or-start', { activeView: 'runner' }), {
    kind: 'direct',
    action: 'run-collection',
  })
  assert.deepEqual(resolveNativeMenuCommand('send-or-start', { activeView: 'request' }), {
    kind: 'direct',
    action: 'send-request',
  })
  assert.deepEqual(resolveNativeMenuCommand('send-or-start', { activeView: 'preferences' }), {
    kind: 'direct',
    action: 'send-request',
  })
})

test('the direct actions do not resolve to workbench commands', () => {
  const view = { activeView: 'request' }
  for (const command of ['new-window', 'save', 'save-all', 'close-tab', 'reopen-tab', 'cancel-active']) {
    assert.equal(resolveNativeMenuCommand(command, view)?.kind, 'direct', command)
  }
})

// save and save-all are distinct menu items with distinct consequences: one
// writes the active request, the other writes every open tab. Collapsing them
// would silently write files the user did not have in front of them.
test('save and save-all resolve to different actions', () => {
  const view = { activeView: 'request' }
  assert.notDeepEqual(
    resolveNativeMenuCommand('save', view),
    resolveNativeMenuCommand('save-all', view),
  )
})

test('an unknown command resolves to nothing', () => {
  assert.equal(resolveNativeMenuCommand('quit', { activeView: 'request' }), undefined)
  assert.equal(resolveNativeMenuCommand('', { activeView: 'request' }), undefined)
})

// A forward to a command id the runner has no case for is the same silent
// nothing as an unhandled menu string, one layer further in. The type system
// only guarantees the id is in the WorkbenchCommandID union — not that anyone
// acts on it.
//
// This deliberately does NOT check commandPaletteCommandIDs: that list is what
// the palette DISPLAYS, and several real commands (import, new-collection,
// command-palette itself) are correctly absent from it. Checking against it
// would have failed for commands that work.
test('every workbench target has a case in the runner', () => {
  const appSource = readFileSync(
    fileURLToPath(new URL('../src/App.svelte', import.meta.url)),
    'utf8',
  )
  const runner = /async function runWorkbenchCommand[\s\S]*?\n {2}\}\n/.exec(appSource)
  assert.ok(runner, 'could not find runWorkbenchCommand in App.svelte')
  const cases = new Set([...runner[0].matchAll(/case '([a-z0-9-]+)':/g)].map((m) => m[1]))
  assert.ok(cases.size > 20, `only found ${cases.size} cases; the parse is wrong`)

  for (const command of handledNativeMenuCommands()) {
    const resolved = resolveNativeMenuCommand(command, { activeView: 'request' })
    if (resolved?.kind !== 'workbench') continue
    assert.ok(cases.has(resolved.command), `${command} forwards to unhandled ${resolved.command}`)
  }
})
