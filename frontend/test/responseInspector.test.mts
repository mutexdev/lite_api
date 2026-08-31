// The response pane's chrome, asserted against its source.
//
// There is no render harness in this repo, so the pattern here is the one
// syntaxHighlight.test.mts and brandMark.test.mts established: read the file
// and assert about what it says. That is weaker than rendering it, and it is
// aimed accordingly -- at the findings whose whole content is "this file went
// its own way", which are exactly the ones a diff review keeps missing because
// each individual divergence looks reasonable on its own line.
//
// Every test below corresponds to a finding in
// docs/ux/audit-2026-08-31/02-response-inspector.md.
import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { keyBindingPresets, keyBindingSections, keyBindingSignature } from '../src/lib/keybindings.ts'

const read = (relative: string) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const inspector = read('../src/lib/workbench/ResponseInspector.svelte')
const treeView = read('../src/lib/workbench/JsonTreeView.svelte')
const notice = read('../src/lib/workbench/ResponseNotice.svelte')
const stylesheet = read('../src/style.css')

function styleBlock(source: string) {
  const open = source.indexOf('<style>')
  assert.ok(open >= 0, 'the component has no style block')
  // CSS comments are stripped before anything is parsed out of a style block:
  // the comments in these files quote the pixel values they replaced, and a
  // token test that reads its own rationale as a violation is useless.
  return source.slice(open, source.indexOf('</style>')).replace(/\/\*[\s\S]*?\*\//g, '')
}

// --- A2-02: the tree is a tree, and it is painted ---------------------------

test('the tree view is a component, not a list of stringify dumps in the pane', () => {
  assert.match(inspector, /<JsonTreeView\b/, 'the pane should render the tree component')
  assert.doesNotMatch(inspector, /<pre>\{entry\.text\}<\/pre>/, 'the flat per-key dump should be gone')
  assert.doesNotMatch(inspector, /jsonTree\.entries as entry/, 'the pane should not re-implement the tree inline')
})

test('the tree paints its values with the body highlighter, not a second one', () => {
  // The drift risk the whole bodyHighlight.ts design exists to close: a second
  // surface colouring JSON with its own idea of the palette. The tree calls the
  // same scanner over the same tokens, so there is nothing to keep in step.
  assert.match(treeView, /from '\.\/bodyHighlight'/)
  assert.match(treeView, /highlightSegments\(/)
  assert.doesNotMatch(treeView, /--syntax-string|--syntax-number|--syntax-boolean/, 'colours belong to style.css, not to a component')
  for (const kind of ['key', 'string', 'number', 'boolean', 'null', 'punctuation']) {
    assert.match(stylesheet, new RegExp(`\\.response-token-${kind}\\b`), `style.css has no .response-token-${kind} rule`)
  }
})

test('the tree renders its bounds rather than stopping silently', () => {
  assert.match(treeView, /JSON_TREE_BUDGET/)
  assert.match(treeView, /JSON_TREE_MAX_ENTRIES/)
  assert.match(treeView, /tree\.truncated/)
})

// --- A2-06: one toolbar shape across every sub-view -------------------------

test('every sub-view toolbar is the shared PaneToolbar', () => {
  const toolbars = inspector.match(/<PaneToolbar\b/g) ?? []
  assert.ok(toolbars.length >= 5, `expected body, headers, metadata/trailers, timeline and compare toolbars, found ${toolbars.length}`)
  for (const bespoke of ['response-inspector-toolbar', 'timeline-tools', 'class="response-search"', 'compare-toggle']) {
    assert.doesNotMatch(inspector, new RegExp(bespoke.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')), `${bespoke} is a hand-rolled toolbar that should be gone`)
  }
})

test('every in-pane search is the shared FindBar', () => {
  // A bare `<input aria-label="Search ...">` is how all seven of the audit's
  // search UIs started -- each one reasonable, none of them the same.
  assert.doesNotMatch(inspector, /<input[^>]*aria-label="Search/, 'a raw search input has come back')
  const bars = inspector.match(/<FindBar\b/g) ?? []
  assert.ok(bars.length >= 3, `expected body, headers and timeline find bars, found ${bars.length}`)
})

// --- A2-07: no live region re-announces on every keystroke ------------------

test('no aria-live region is driven by a search-derived count', () => {
  // The failure is specific: a count that recomputes per keystroke, inside an
  // element that carries aria-live, queues one announcement per character. The
  // fix is FindBar's -- aria-live on a wrapper that exists from first render
  // and is empty until there is a query -- so nothing in this file should be
  // pairing the two itself.
  const searchDerived = ['matches.length', 'treeMatches', 'filteredHeaders', 'filteredTimeline']
  for (const line of inspector.split('\n')) {
    if (!line.includes('aria-live')) continue
    for (const source of searchDerived) {
      assert.ok(
        !line.includes(source),
        `aria-live on a region driven by ${source} re-announces on every keystroke:\n${line.trim()}`
      )
    }
  }
})

test('the resting empty state does not announce itself', () => {
  // It said aria-live="polite" while containing static text, so switching
  // response tabs on an unsent request read the placeholder out again.
  const resting = inspector.slice(inspector.indexOf('response-resting'), inspector.indexOf('Ready for a response'))
  assert.doesNotMatch(resting, /aria-live/)
})

// --- A2-08: a keyboard path to the pane's search ----------------------------

const paneFindBinding = (() => {
  const match = /findInPaneBinding = \{ mac: '([^']+)', windows: '([^']+)' \}/.exec(inspector)
  assert.ok(match, 'the pane-find binding should be declared as a mac/windows pair, as keybindings.ts writes them')
  return { mac: match[1], windows: match[2] }
})()

test('the response pane has a find shortcut at all', () => {
  assert.match(inspector, /<svelte:window onkeydown=\{findInPane\}/, 'nothing listens for the shortcut')
  assert.match(inspector, /keyBindingComboFromEvent\(event\)/, 'the combo should be resolved by the app\'s own matcher, not by reading modifier flags again')
})

test('the pane-find shortcut does not take a combo any keybinding already owns', () => {
  // ⌘F is owned twice over -- Search Sidebar globally, CodeMirror's own find
  // inside an editor -- and shortcuts.ts resolved that conflict deliberately in
  // the editor's favour. A component quietly binding a third meaning to it
  // would undo that decision from the one place nothing in shortcuts.ts says so.
  const taken = new Set<string>()
  for (const section of keyBindingSections) {
    for (const definition of Object.values(section.bindings)) {
      taken.add(keyBindingSignature(definition.mac))
      taken.add(keyBindingSignature(definition.windows))
    }
  }
  for (const preset of Object.values(keyBindingPresets)) {
    for (const override of Object.values(preset)) {
      if (override.mac) taken.add(keyBindingSignature(override.mac))
      if (override.windows) taken.add(keyBindingSignature(override.windows))
    }
  }
  for (const combo of [paneFindBinding.mac, paneFindBinding.windows]) {
    assert.equal(taken.has(keyBindingSignature(combo)), false, `${combo} is already bound in keybindings.ts`)
  }
})

test('the pane-find shortcut does not fight the editor\'s own find', () => {
  // CodeMirror's searchKeymap binds Mod-f, Mod-Alt-f, Mod-g, Mod-d and F3. The
  // Shift variant is the one modifier combination it leaves alone, which is
  // also how editors spell "the wider find".
  for (const combo of [paneFindBinding.mac, paneFindBinding.windows]) {
    assert.match(combo, /shift/, 'without Shift this lands on the editor\'s own find')
    assert.match(combo, /f$/)
    assert.doesNotMatch(combo, /alt/, 'Mod-Alt-f is CodeMirror\'s replace')
  }
})

test('the shortcut is discoverable from the button that opens the same thing', () => {
  assert.match(inspector, /label=\{`Find in response \(\$\{findShortcutLabel\}\)`\}/)
})

// --- A2-09: the token scale, not pixel literals -----------------------------

test('no spacing, radius or type value in the response pane is a pixel literal', () => {
  // The audit's point was not that 7px is wrong -- it is that 7px is
  // --space-7 written in a way a future density change cannot reach. Sizes
  // that are genuinely one-off measurements (a pane's max-height clamp, a
  // table's minimum column width) are not on the scale and are not checked.
  const tokenised = /(^|[;{])\s*(margin|padding|gap|font-size|border-radius)(-[a-z]+)?\s*:\s*([^;{}]*)/g
  for (const [name, source] of [
    ['ResponseInspector.svelte', inspector],
    ['JsonTreeView.svelte', treeView],
    ['ResponseNotice.svelte', notice]
  ] as const) {
    for (const match of styleBlock(source).matchAll(tokenised)) {
      const property = `${match[2]}${match[3] ?? ''}`
      assert.doesNotMatch(
        match[4],
        /\d\s*px/,
        `${name} sets ${property}: ${match[4].trim()} — use the --space-*/--font-size-*/--radius-* scale`
      )
    }
  }
})

// --- A2-11: one set of "cannot show you a body" containers ------------------

test('the four one-off state containers are gone', () => {
  for (const container of ['binary-response-card', 'response-empty-state', 'response-warning', 'response-tls-warning']) {
    assert.doesNotMatch(inspector, new RegExp(container), `${container} is one of four containers saying the same thing`)
  }
})

test('a state with a cause and a remedy is a ResponseNotice, and nothing else is', () => {
  const notices = inspector.match(/<ResponseNotice\b/g) ?? []
  assert.ok(notices.length >= 4, `expected the send error, TLS failure, cancellation, binary body and oversized preview, found ${notices.length}`)
  // Three tones, and only three: the point of converging on one container is
  // lost the moment it grows a fourth appearance nobody can name.
  const tones = new Set([...notice.matchAll(/tone === '([a-z]+)'/g)].map((match) => match[1]))
  assert.deepEqual([...tones].sort(), ['error', 'warning'], 'info is the default, so only the other two are branched on')
})

test('a resting state uses the app\'s shared empty-state box', () => {
  assert.match(inspector, /class="empty-state response-resting"/)
  const emptyStates = inspector.match(/class="empty-state"/g) ?? []
  assert.ok(emptyStates.length >= 4, `expected headers, metadata/trailers, timeline, visualizer and console, found ${emptyStates.length}`)
})

// --- A9-12: tables acknowledge the pointer ----------------------------------

test('the response tables hover the way the DevTools network table does', () => {
  // Not a similar colour -- the same expression. The audit found the network
  // table was the only surface in the app that reacted to the pointer at all,
  // and a second hand-picked tint beside it is how "the same interaction"
  // becomes two interactions again.
  const devtools = /tr\[data-network-row\]:hover td \{\s*background:\s*([^;]+);/.exec(stylesheet)
  assert.ok(devtools, 'the network table lost its hover rule, so there is nothing to match')
  const expected = devtools[1].trim()
  const rule = /tbody tr:hover td \{ background: ([^;]+); \}/.exec(styleBlock(inspector))
  assert.ok(rule, 'the response tables have no hover rule')
  assert.equal(rule[1].trim(), expected)
  for (const table of ['response-kv-table', 'compare-section', 'timeline-detail']) {
    assert.match(styleBlock(inspector), new RegExp(`\\.${table} tbody tr:hover td`), `${table} rows give no hover feedback`)
  }
})

test('a row that does nothing when clicked is not dressed as though it did', () => {
  // The network table's rows are the click target for the details pane, so they
  // take cursor:pointer and a focus ring. These are read-only key/value lists.
  // Borrowing the affordance without the behaviour is a worse lie than no
  // feedback at all.
  assert.doesNotMatch(styleBlock(inspector), /response-kv-table[^{]*\{[^}]*cursor:\s*pointer/)
})

// --- the find highlight, which had no rule at all ---------------------------

test('a response search hit is painted with the same tokens as an editor search hit', () => {
  // CodeEditor.svelte's own comment says its find colours are "deliberately the
  // SAME pairing the response pane uses for `<mark>`". That was written against
  // a pairing that did not exist: `<mark>` had no rule in this component, none
  // in style.css, and none anywhere else, so every response search hit rendered
  // in the browser's default yellow — overriding the syntax colour underneath
  // it. This is the guard that makes the sentence true, and the same anti-drift
  // device bodyHighlight.test.mts uses for the token palette.
  const editor = read('../src/lib/workbench/CodeEditor.svelte')
  // Radius is excluded from the comparison, and only radius: the editor writes
  // its corner as the literal `2px` inside a JS theme object where the token
  // scale is not reachable, while this side uses --radius-2. What is being
  // guarded is the COLOUR pairing, which is where a drift would actually show.
  const tokensOf = (source: string) =>
    [...source.matchAll(/var\((--[a-z0-9-]+)\)/g)].map((match) => match[1]).filter((token) => !token.startsWith('--radius'))

  const editorMatch = /'\.cm-find-match':\s*\{([^}]*)\}/.exec(editor)
  const editorCurrent = /'\.cm-find-current':\s*\{([^}]*)\}/.exec(editor)
  assert.ok(editorMatch && editorCurrent, 'the editor find highlight moved; this pairing needs re-deriving')

  // The rule lives in style.css, GLOBALLY, rather than in this component.
  //
  // It arrived as two component-scoped copies — one here, one in JsonTreeView —
  // which is the duplication this campaign exists to remove, and which would
  // have left a third surface that grows a `<mark>` rendering browser-yellow
  // again. One `mark` rule covers every surface that highlights anything.
  const css = read('../src/style.css')
  const paneMatch = /\nmark \{([^}]*)\}/.exec(css)
  const paneCurrent = /\nmark\.current-match \{([^}]*)\}/.exec(css)
  assert.ok(paneMatch, '<mark> has no global rule, so search hits render browser-yellow')
  assert.ok(paneCurrent, '.current-match has no rule, so "3 of 12" points at nothing')

  assert.deepEqual(tokensOf(paneMatch[1]), tokensOf(editorMatch[1]))
  assert.deepEqual(tokensOf(paneCurrent[1]), tokensOf(editorCurrent[1]))

  // `color: inherit` is the load-bearing declaration: it is what lets a JSON
  // key keep its colour while highlighted, which is the entire reason the
  // scanner merges token and match into one span instead of layering them.
  assert.match(paneMatch[1], /color:\s*inherit/)

  // And neither component may reintroduce a local copy that could drift.
  assert.doesNotMatch(styleBlock(inspector), /mark\s*\{/)
  assert.doesNotMatch(styleBlock(treeView), /mark\s*\{/)
})
