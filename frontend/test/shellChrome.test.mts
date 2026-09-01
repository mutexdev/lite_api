// The chrome budget, held in place.
//
// The complaint that started this was "clutter": four rows of chrome above the
// request tabs, three above the response body, and 130px of sidebar spent on a
// tagline, a button, a helper sentence and a labelled search box before the
// first collection. Every reference app spends 36-44px there and one row here.
//
// Rows do not come back all at once. They come back one at a time, each with a
// good local reason — a chip that "only" says the protocol, a label that "only"
// spells out what the icon means, a second New button because the first one was
// somewhere else. So the rule this file enforces is not "the layout looks like
// the screenshot"; it is the list of specific things that were removed and the
// specific place each one was moved to. See docs/ux/clutter-free-shell.md; the
// table there and the tests here are the same list.
//
// These read sources rather than a rendered tree, like nativeMenu.test.mts,
// brandMark.test.mts and layout.test.mts do, because the repo has no
// component-rendering harness. That makes them weak about pixels and strong
// about the one thing that regresses: whether the markup is still there.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { sidebarActionsFor, type SidebarObject } from '../src/lib/sidebar/sidebarActions.ts'
import { requestCommandState } from '../src/lib/workbench/commandState.ts'

const sourceRoot = fileURLToPath(new URL('../src', import.meta.url))

/**
 * Comments stripped before scanning.
 *
 * Not a convenience — a correctness requirement, and the same one
 * emptyState.test.mts documents. Every file touched by this campaign explains
 * what went away by naming it, so a scan over raw source would report each
 * explanation as the violation and the only way to pass would be to delete the
 * reasoning. The check is about what ships to the screen.
 */
function withoutComments(text: string): string {
  return text
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1')
}

const read = (relative: string) => readFileSync(join(sourceRoot, relative), 'utf8')
const source = (relative: string) => withoutComments(read(relative))

const sidebarHeader = source('lib/SidebarHeader.svelte')
const sidebarSearch = source('lib/SidebarSearch.svelte')
const commandBar = source('lib/workbench/WorkspaceCommandBar.svelte')
const requestStrip = source('lib/workbench/RequestCommandStrip.svelte')
const app = source('App.svelte')

/** Counts non-overlapping occurrences of a literal needle. */
const occurrences = (text: string, needle: string) => text.split(needle).length - 1

// ── D1 · the sidebar header is one row ──────────────────────────────────────

test('D1 the sidebar header carries no tagline and no helper sentence', () => {
  assert.ok(
    !/<p[\s>]/.test(sidebarHeader),
    'the "Local-first API workbench" tagline is back in SidebarHeader; a returning user reads it once',
  )
  assert.ok(
    !/<small[\s>]/.test(sidebarHeader),
    'a <small> helper is back under a control in SidebarHeader; D9 puts the hint in the tooltip',
  )
  assert.ok(
    !/<kbd[\s>]/.test(sidebarHeader),
    'a <kbd> hint is back in SidebarHeader; the shortcut belongs in the IconButton label, which is also the tooltip',
  )
})

// The full-width New button and the sentence under it were the second place to
// start a request. There is one New menu now and it is this one.
test('D1 the header is a brand plus exactly two controls', () => {
  const controls = occurrences(sidebarHeader, '<IconButton') + occurrences(sidebarHeader, '<CommandOverflowMenu')
  assert.equal(controls, 2, `the sidebar header renders ${controls} controls; D1 allows search and new, and nothing else`)

  assert.ok(
    !/new-request-button|rail-create/.test(sidebarHeader),
    'the full-width New button is back in the sidebar header',
  )
})

test('D1 the search control is a toggle that reports whether the bar is open', () => {
  assert.match(
    sidebarHeader,
    /icon="search"[\s\S]{0,200}?pressed=\{searchOpen\}/,
    'the sidebar header search button does not report searchOpen as its pressed state, so an open find bar leaves the toggle looking off',
  )
})

// The New menu must be the shared list, not a second copy of it. A copy is how
// the sidebar goes on offering WebSocket six months after the top bar stops.
test('D1 the header opens the shared New list rather than rebuilding it', () => {
  assert.match(
    sidebarHeader,
    /import \{[^}]*\bnewItems\b[^}]*\} from '\.\/workbench\/workbenchCommands'/,
    'SidebarHeader does not import newItems from workbenchCommands; a second New list has grown here',
  )
  assert.ok(
    !/'new-http'|'new-graphql'|'new-websocket'/.test(sidebarHeader),
    'SidebarHeader builds its own New entries instead of using the shared list',
  )
})

test('D1 the find bar is hidden at rest and has no field label', () => {
  assert.match(
    sidebarSearch,
    /\{#if [^}]+\}[\s\S]*<FindBar/,
    'SidebarSearch renders FindBar unconditionally; the box is meant to cost a row only while it is in use',
  )
  assert.ok(
    !/field-label/.test(sidebarSearch),
    'the "Search" field label is back above a box whose placeholder already says Search',
  )
})

// Escape twice must give the row back. FindBar clears a non-empty query on the
// first press; the second is this file's job, and it is easy to lose because
// FindBar stops propagation before deciding, so a bubbling listener never runs.
test('D1 Escape on an empty query closes the bar and hands focus back', () => {
  assert.match(
    sidebarSearch,
    /onkeydowncapture=/,
    'SidebarSearch listens for Escape in the bubble phase, where FindBar’s stopPropagation() means it never arrives',
  )
  assert.match(sidebarSearch, /onClose\?\.\(\)/, 'SidebarSearch closes without telling the parent, so focus is left on a removed element')
})

// ── D2 · tree rows show state, not properties ───────────────────────────────

test('D2 the collection row does not print the file format on every row', () => {
  assert.ok(
    !/\{collection\.format\}/.test(app),
    'the YML/BRU badge is back on every collection row; the format is on the collection page and in Info',
  )
})

// Run left the top bar, so it has to have landed somewhere. The collection is
// the object it acts on, and the sidebar menu is where a per-object action goes.
test('D2 the collection menu offers Run collection', () => {
  const collection: SidebarObject = { kind: 'collection', collectionId: 'c1', folder: '', itemId: '', label: 'Alpha' }
  const actions = sidebarActionsFor(collection, { revealLabel: 'Reveal in Finder', supportsGenerateCode: true })

  const run = actions.find((action) => action.id === 'run-collection')
  assert.ok(run, 'run-collection is not offered on a collection; the top bar’s Run button has nowhere to have gone')
  assert.equal(run.label, 'Run collection')
})

// ── D3 · the top bar is icons ───────────────────────────────────────────────

test('D3 no text label survives in the command bar’s controls', () => {
  for (const label of ['Cookies', 'Local', 'Git', 'Commands']) {
    assert.ok(
      !new RegExp(`>\\s*${label}\\s*<`).test(commandBar),
      `the ${label} text label is back in the command bar; D3 leaves the trailing cluster iconic`,
    )
  }
  // "Running" is the one word allowed, and only while a run is in flight: the
  // M3 QA contract requires a NAMED cancel in the global toolbar, and a bare
  // stop glyph cannot say "Cancelling".
  assert.ok(
    !/>\s*Run\s*</.test(commandBar),
    'the Run button is back on the command bar; it belongs to the collection that gets run',
  )
})

test('D3 the palette button wears the command icon, not the magnifier', () => {
  assert.match(
    commandBar,
    /icon="command"[\s\S]{0,200}?'command-palette'/,
    'the command palette button does not use the command icon; the magnifier belongs to ⌘K search',
  )
  assert.match(commandBar, /icon="search"[\s\S]{0,200}?'workspace-search'/, '⌘K search has no magnifier button')
})

test('D3 the orientation toggle is not duplicated in the command bar', () => {
  assert.ok(
    !/OrientationToggleButton/.test(commandBar),
    'the command bar imports the orientation toggle again; the request strip already has one',
  )
})

// ── D4 · the request strip is one row ───────────────────────────────────────

test('D4 the chip row under the request line is gone', () => {
  for (const marker of [
    'request-command-meta',
    'command-protocol',
    'command-environment',
    'command-saved',
    'command-scope-collection',
  ]) {
    assert.ok(
      !new RegExp(marker).test(requestStrip),
      `${marker} is back in the request strip; protocol is on the tab, environment is in the top bar, TLS and proxy are in Settings, saved state is on the tab`,
    )
  }
})

// A cue that renders on every request is wallpaper, and "TLS off" rendered in
// the same place, size and colour as "TLS verify" is the one state worth
// interrupting somebody over hiding inside the state that never changes.
test('D4 a default install produces no transport cue at all', () => {
  const cues = requestCommandState(
    undefined as never,
    undefined as never,
    undefined as never,
    '',
    false,
    false,
    undefined as never,
    false,
    false,
    undefined as never,
    undefined as never,
  ).transportCues

  assert.deepEqual(cues, [], 'TLS on and a system proxy still produce a cue, so every request shows chrome that never changes')
})

test('D4 the two states that are not the default still say so', () => {
  const cueFor = (request: unknown, preferences: unknown) =>
    requestCommandState(
      request as never,
      undefined as never,
      undefined as never,
      '',
      false,
      false,
      preferences as never,
      false,
      false,
      undefined as never,
      undefined as never,
    ).transportCues

  assert.deepEqual(cueFor({ settings: { verifyTls: false } }, undefined), ['TLS off'])
  assert.deepEqual(cueFor(undefined, { proxy: { source: 'manual' } }), ['Proxy: manual'])
})

// ── D5 · the response status lives in the tab row ───────────────────────────

test('D5 the response summary row is folded into the tab row', () => {
  assert.ok(
    !/response-summary/.test(app),
    'the response-summary row is back; the status belongs in the tab row’s middle slot, and shows nothing at rest',
  )
  assert.match(app, /<PaneToolbar[^>]*ariaLabel="Response"/, 'the response pane has no PaneToolbar carrying its tablist and status')
})

// Inside a pane called Response, a tab called Response names nothing.
test('D5 the first response tab is Body', () => {
  const lists = [...app.matchAll(/[Rr]esponseTabs[^=]*=\s*\[([\s\S]*?)\]/g)]
  assert.ok(lists.length > 0, 'no response tab list found in App.svelte; did it move or get renamed?')

  for (const [, body] of lists) {
    const firstLabel = body.match(/label:\s*'([^']+)'/)?.[1]
    assert.equal(firstLabel, 'Body', 'a response tab list still leads with a tab named after the pane it is inside')
    // Scanned inside the extracted list, not across the file. Dev Tools' network
    // inspector has its own Request / Response / Network tabs, and a Response tab
    // sitting beside a Request tab names exactly the half of the exchange it
    // shows — a whole-file search reported that correct tab as this violation.
    assert.ok(
      !/id: 'response', label: 'Response'/.test(body),
      'the Response tab inside the Response pane is back',
    )
  }
})

// ── D6 · tabs carry a dirty dot ─────────────────────────────────────────────

test('D6 an unsaved tab shows a dot where its close button sits', () => {
  assert.match(
    app,
    /<div class="tab"[\s\S]{0,1500}?tab-dirty[\s\S]{0,600}?class="tab-close"/,
    'the tab strip renders no tab-dirty marker between the tab and its close button; this is what let the SAVED chip go',
  )
  assert.match(
    app,
    /unsaved/,
    'the close button’s accessible name never says "unsaved", so the dot is visual-only',
  )
})

// ── D7 · every full-pane view uses PageHeader ───────────────────────────────

function svelteFiles(directory: string): string[] {
  return readdirSync(join(sourceRoot, directory), { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return svelteFiles(path)
    return entry.name.endsWith('.svelte') ? [path] : []
  })
}

test('D7 no view hand-rolls a page header any more', () => {
  const offenders = ['App.svelte', ...svelteFiles('lib')]
    // lib/ui owns the primitive, so it is the one place the class may be written.
    .filter((path) => !path.startsWith(join('lib', 'ui')))
    .filter((path) => /class="panel-header"/.test(source(path)))

  assert.deepEqual(offenders, [], 'these files render their own header instead of lib/ui/PageHeader.svelte')
})

// ── D8 · the App request tab is removed ─────────────────────────────────────

test('D8 the placeholder App request tab is gone', () => {
  const list = app.match(/const requestTabs[^=]*=\s*\[([\s\S]*?)\]/)
  assert.ok(list, 'the requestTabs list is gone or was renamed')
  assert.ok(
    !/id: 'app'/.test(list[1]),
    'the App request tab is back; it rendered the sentence "Request app runtime surface" and nothing else',
  )
})
