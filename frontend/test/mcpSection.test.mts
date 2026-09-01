// Source-text assertions over the AI access (MCP) settings section.
//
// Like brandMark.test.mts and proxyModeVocabularies.test.mts, these read the
// .svelte files rather than a rendered tree: the repo has no component-rendering
// harness. That makes them weak about layout and strong about the handful of
// wiring facts that are silent when they break — a section that exists but is
// never mounted, a Copy button that copies the masked string, and a stylesheet
// that quietly needs twelve theme blocks updated.

import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const readIfPresent = (relative: string) => {
  const path = fileURLToPath(new URL(relative, import.meta.url))
  return existsSync(path) ? readFileSync(path, 'utf8') : ''
}

const section = read('../src/lib/views/preferences/McpSection.svelte')
const app = read('../src/App.svelte')

// WHICH FILE HOLDS THE PANEL.
//
// These assertions were written against App.svelte because that is where the
// preferences settings-stack lived. A7-06 moved the panel shell — its header,
// its section index and the one loader that replaced eight independent await
// blocks — into PreferencesPanel.svelte, and App.svelte mounts that instead.
//
// The invariants below are about the SECTION, not about which file stacks it:
// that it is loaded lazily rather than sitting in the initial chunk, that it is
// rendered inside the preferences stack rather than loose on the page, and that
// it is handed the app's mutator and clipboard helper. Pinning them to a file
// path made them fail on a move that changed none of them. They now follow the
// stack to whichever file declares it, and still fail if no file does.
const panel = readIfPresent('../src/lib/views/preferences/PreferencesPanel.svelte')
const shell = panel.includes('settings-stack') ? panel : app

/** App.svelte plus the panel: wiring may be split across the two after the move. */
const wiring = `${app}\n${panel}`

test('the settings stack lazily imports the MCP section', () => {
  assert.ok(
    /import\(['"][^'"]*McpSection\.svelte['"]\)/.test(shell),
    'nothing dynamically imports McpSection.svelte; its markup is back in the initial chunk',
  )
  assert.ok(
    /<(McpSectionComponent|[A-Za-z_$][\w$]*\.Mcp)\b/.test(shell),
    'the panel imports McpSection but never renders it',
  )

  // Mounted inside the preferences settings-stack, not somewhere else on the
  // page: a section rendered outside it inherits none of the section styling
  // and would be unreachable from Preferences.
  const stackStart = shell.indexOf('<div class="settings-stack">')
  assert.ok(stackStart > 0, 'the preferences settings-stack is gone or was renamed')
  assert.ok(
    shell.slice(stackStart).includes('Mcp'),
    'McpSection is rendered outside the preferences settings-stack',
  )
})

test('App.svelte merges MCP patches rather than replacing the block', () => {
  const start = app.indexOf('async function updateMcpPreferences')
  assert.ok(start > 0, 'updateMcpPreferences is missing from App.svelte')
  const body = app.slice(start, start + 500)

  // Spreading the current block is what keeps a port the user set from being
  // written back as 0 when only the toggle changed.
  assert.ok(
    /\.\.\.\(appState\.preferences\.mcp/.test(body),
    'updateMcpPreferences does not merge into the existing mcp preferences',
  )
  assert.ok(
    /\.\.\.updates/.test(body),
    'updateMcpPreferences does not apply the patch',
  )
})

test('the section is wired to the app mutator and the clipboard helper', () => {
  // Either spelling: a prop on the section itself, or a key in the prop bag the
  // panel forwards to it.
  assert.ok(
    /onUpdateMcp[=:]\s*\{?updateMcpPreferences\}?/.test(wiring),
    'McpSection is not given updateMcpPreferences',
  )
  assert.ok(
    /onCopyCommand[=:]\s*\{?copyText\}?/.test(wiring),
    "McpSection is not given the app's copyText helper",
  )
})

test('the section exposes the testids the settings tests select on', () => {
  for (const testid of [
    'mcp-enabled-toggle',
    'mcp-port-input',
    'mcp-copy-command-btn',
    'mcp-write-tier-toggle',
  ]) {
    assert.ok(
      section.includes(`data-testid="${testid}"`),
      `McpSection is missing data-testid="${testid}"`,
    )
  }
})

// THE POINT OF THE WHOLE MASK. What is displayed and what is copied are
// deliberately two different strings, so the Copy handler must reach for the
// unmasked variable. Handing it the masked one produces a command that pastes
// cleanly and then fails authentication with no clue why.
test('the copy handler copies the full command, not the masked one', () => {
  assert.ok(
    /onCopyCommand\(pairingCommand\)/.test(section),
    'the copy handler does not pass the unmasked pairingCommand',
  )
  assert.ok(
    !/onCopyCommand\(\s*(maskedCommand|maskToken)/.test(section),
    'the copy handler copies the masked command',
  )
  assert.ok(
    /maskedCommand = maskToken\(pairingCommand\)/.test(section),
    'the display variant is no longer derived with maskToken',
  )
  assert.ok(
    /\{maskedCommand\}/.test(section),
    'the section renders something other than the masked command',
  )
})

test('the section re-reads the backend status after a write', () => {
  assert.ok(/GetMCPStatus/.test(section), 'McpSection never calls GetMCPStatus')
  assert.ok(/onMount\(refreshStatus\)/.test(section), 'McpSection does not fetch status on mount')

  const start = section.indexOf('async function applyMcp')
  assert.ok(start > 0, 'applyMcp is missing')
  const body = section.slice(start, section.indexOf('}', section.indexOf('refreshStatus()', start)))
  assert.ok(
    body.indexOf('await onUpdateMcp') < body.indexOf('await refreshStatus()'),
    'the status is re-read before the write round-trips, so it shows the old state',
  )
})

// A new `--name` here would have to be added to all 12 theme blocks in
// style.css to mean anything; defined in one component it resolves to nothing
// in every theme and the rule silently falls back.
test('the section defines no new CSS custom property', () => {
  const styleStart = section.indexOf('<style>')
  if (styleStart < 0) return
  const css = section.slice(styleStart, section.indexOf('</style>', styleStart))

  assert.ok(
    !/^\s*--[\w-]+\s*:/m.test(css),
    'McpSection defines a CSS custom property; it would resolve in none of the 12 themes',
  )
})
