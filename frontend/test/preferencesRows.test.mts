// Source-text assertions over the Preferences panel's row anatomy.
//
// Like mcpSection.test.mts and brandMark.test.mts, these read the .svelte files
// rather than a rendered tree: the repo has no component-rendering harness. The
// findings they guard are all of the same shape — a section quietly growing its
// own way of drawing a setting — and every one of them was invisible in review
// because each file looked perfectly reasonable on its own. It was only the
// eight of them side by side that were wrong.
//
// A7-01 (no shared row primitive), A7-02 (a hand-rolled pill group next to the
// real one), A7-03 (a severity attribute no stylesheet read) and A7-04 (five
// different content widths) are each pinned here.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const directory = fileURLToPath(new URL('../src/lib/views/preferences', import.meta.url))

// Not sections: the two row primitives, plus the panel shell that stacks them.
// PreferencesPanel owns the header, the section index and the one loader — it
// renders no settings of its own, so every assertion below about how a section
// draws a setting would be meaningless applied to it.
const primitives = new Set(['SettingRow.svelte', 'SettingSection.svelte', 'PreferencesPanel.svelte'])

const files = readdirSync(directory)
  .filter((name) => name.endsWith('.svelte'))
  .map((name) => ({ name, text: readFileSync(join(directory, name), 'utf8') }))

const sections = files.filter((file) => !primitives.has(file.name))
const settingRow = files.find((file) => file.name === 'SettingRow.svelte')!.text
const settingSection = files.find((file) => file.name === 'SettingSection.svelte')!.text

// The floor exists for the same reason verify-inputs.mjs does: every assertion
// below is a `for` loop over `sections`, and an empty or halved list passes all
// of them while checking nothing.
test('every preferences section is present to be checked', () => {
  assert.ok(sections.length >= 8, `found ${sections.length} preferences sections, expected at least 8`)
})

// A7-01. The finding was not "some sections are ugly" — it was that there was
// no component to be consistent WITH, so four row anatomies grew independently
// and nothing in the code said they were all settings.
test('every section routes its settings through the shared row primitive', () => {
  for (const section of sections) {
    assert.ok(
      section.text.includes("from './SettingRow.svelte'"),
      `${section.name} does not import SettingRow`,
    )
    assert.ok(
      section.text.includes('<SettingRow'),
      `${section.name} imports SettingRow but never renders one`,
    )
    assert.ok(
      section.text.includes("from './SettingSection.svelte'"),
      `${section.name} does not import SettingSection, so it owns its own width again`,
    )
  }
})

// The retired anatomies, named so that reintroducing one is a test failure
// rather than a code review someone has to notice.
test('no section reintroduces one of the retired row grids', () => {
  const retired = [
    'theme-mode-selector',
    'theme-variant-section',
    'font-preference-grid',
    'zoom-preference-row',
    'general-preferences-grid',
    'compact-preference-grid',
    'default-location-grid',
    'default-location-control',
    'cache-preference-card',
    'path-picker-row',
    'oauth2-browser-toggle',
    'field-grid',
  ]
  // Matched inside a class attribute rather than anywhere in the file: the
  // comments in these sections name the anatomy they replaced, on purpose, and
  // a test that forbade saying the name would delete the record of why.
  for (const section of sections) {
    for (const name of retired) {
      const applied = new RegExp(`class="[^"]*\\b${name}\\b|class:${name}\\b`)
      assert.ok(
        !applied.test(section.text),
        `${section.name} uses the retired "${name}" layout instead of SettingRow`,
      )
    }
  }
})

// A boolean is a checkbox, always, and the row owns it. A section that renders
// its own `<input type="checkbox">` has by definition escaped the one place
// that decides what a boolean looks like — which is how Proxy ended up with the
// panel's only bare checkbox sitting in a value column.
test('no section renders its own checkbox', () => {
  for (const section of sections) {
    assert.ok(
      !/type="checkbox"/.test(section.text),
      `${section.name} renders a raw checkbox instead of a SettingRow boolean`,
    )
  }
  assert.ok(/type="checkbox"/.test(settingRow), 'SettingRow no longer owns the checkbox')
})

// A7-02. `.segmented` already existed and was already used correctly elsewhere
// when AppearanceSection built a second one out of plain buttons.
test('no section hand-rolls a pill group', () => {
  for (const section of sections) {
    assert.ok(
      !/class="segmented/.test(section.text) && !/class:selected/.test(section.text.replace(/theme-variant-card[\s\S]{0,200}?class:selected/g, '')),
      `${section.name} styles its own selected-pill state instead of using SegmentedControl`,
    )
  }

  // Exactly one segmented control in the whole panel: the enum rule is "native
  // <select> everywhere", and theme mode is its single documented exception.
  const segmented = sections.filter((section) => section.text.includes('SegmentedControl'))
  assert.deepEqual(
    segmented.map((section) => section.name),
    ['AppearanceSection.svelte'],
    'the enum control rule has drifted: a segmented group appeared outside theme mode, or theme mode lost its',
  )
})

// A7-04. The number itself does not matter; that there is exactly one of it
// does. Five sections each choosing their own is what made the panel's right
// edge zig-zag.
test('the settings content width is declared once, in SettingSection', () => {
  // Anchored to the start of a line so an `@media (max-width: 720px)` query —
  // which is a breakpoint, not a content cap — is not counted as one.
  const contentWidths = (text: string) =>
    [...text.matchAll(/^\s*max-width:\s*(\d+)px/gm)].map((match) => Number(match[1])).filter((value) => value >= 400)

  for (const file of files) {
    const widths = contentWidths(file.text)
    if (file.name === 'SettingSection.svelte') {
      assert.equal(widths.length, 1, 'SettingSection should declare exactly one content width')
      continue
    }
    assert.deepEqual(widths, [], `${file.name} caps its own content width at ${widths.join(', ')}px`)
  }

  assert.match(settingSection, /max-width:\s*\d+px/, 'SettingSection declares no content width at all')
})

// The label column is the other half of "one column": rows whose labels do not
// share a right edge do not read as one list however wide the section is.
test('the label column width is declared once, in SettingRow', () => {
  const gridColumns = files.flatMap((file) =>
    [...file.text.matchAll(/grid-template-columns:\s*(\d+)px/g)].map(() => file.name),
  )
  assert.deepEqual(gridColumns, ['SettingRow.svelte'], 'a section declares its own label column width')
})

// A7-03. `grep -n "data-tone" style.css` returned nothing while the attribute
// was being written on every render, so the listener state — including the
// dangerous "enabled but not listening" middle case — rendered as plain grey
// text three lines above badges that colour-code the same kind of information.
test('every severity tone the MCP section emits is actually styled', () => {
  const mcp = files.find((file) => file.name === 'McpSection.svelte')!.text
  const styleStart = mcp.indexOf('<style>')
  assert.ok(styleStart > 0, 'McpSection has no style block')
  const css = mcp.slice(styleStart)

  // mcpStatusSummary's three tones and mcpAuditRows' three outcomes, which now
  // share one element and one vocabulary.
  for (const tone of ['running', 'warning', 'off', 'ok', 'denied', 'error']) {
    assert.ok(
      css.includes(`[data-tone='${tone}']`),
      `data-tone="${tone}" is emitted but no rule reads it, so it renders colourless`,
    )
  }

  // The status line and the badges must be the same element, or the two will
  // drift apart again the next time one of them is restyled.
  assert.ok(
    !/data-outcome/.test(mcp),
    'the audit badges are back on their own attribute instead of the shared tone vocabulary',
  )
})

// Save semantics are a genuine strength of this panel and the audit said so:
// every control writes straight through on native change. This is here to stop
// a well-meaning later change from introducing the Save button the panel has
// never had.
test('Preferences still has no Save or Cancel button anywhere', () => {
  for (const section of sections) {
    assert.ok(
      !/>\s*(Save|Apply|Cancel)\s*</.test(section.text),
      `${section.name} has grown a Save/Apply/Cancel button; Preferences writes instantly`,
    )
  }
})

// The testids the settings suites select on live in props now rather than in
// literal attributes on the input, so this pins that the migration carried
// every one of them across.
test('the migration kept every testid the panel is selected by', () => {
  const all = sections.map((section) => section.text).join('\n')
  for (const testid of [
    'ssl-verification-toggle',
    'custom-ca-enabled-toggle',
    'keep-default-ca-toggle',
    'store-cookies-toggle',
    'send-cookies-toggle',
    'request-timeout-input',
    'autosave-enabled-toggle',
    'autosave-interval-input',
    'default-location-input',
    'default-location-browse-btn',
    'default-location-clear-btn',
    'custom-ca-select-btn',
    'custom-ca-file-name',
    'code-font-input',
    'code-font-size-input',
    'zoom-percentage-select',
    'zoom-percentage-value',
    'zoom-reset-btn',
    'cache.file.enabled',
    'sslSession.enabled',
    'file-cache-clear-btn',
    'ssl-session-clear-btn',
    'keybinding-preset',
    'mcp-enabled-toggle',
    'mcp-port-input',
    'mcp-status',
    'mcp-copy-command-btn',
    'mcp-write-tier-toggle',
  ]) {
    assert.ok(all.includes(`data-testid="${testid}"`), `data-testid="${testid}" was lost in the row migration`)
  }
})

// App.svelte's openKeyboardShortcuts() finds this markup by selector, opens the
// details and focuses the summary. Renaming either would break the Keyboard
// Shortcuts command with no compile error and no other test failure — the exact
// kind of silent break the source-text suites exist for.
test('the keybindings disclosure keeps the shape App.svelte reaches for', () => {
  const keybindings = files.find((file) => file.name === 'KeybindingsSection.svelte')!.text
  assert.ok(
    /<details class="keybindings-disclosure">/.test(keybindings),
    'openKeyboardShortcuts() queries details.keybindings-disclosure and would find nothing',
  )
  assert.ok(/<summary>/.test(keybindings), 'the disclosure has no summary for openKeyboardShortcuts() to focus')

  // The 400px bounded scroller on this wrapper is the fix for the 1024x768
  // overflow bug, and it lives in style.css keyed on this class name.
  assert.ok(
    /class="keybindings-table-wrap"/.test(keybindings),
    'the keybindings table lost its bounded scroll region; the overflow bug comes back',
  )
})


// ── A7-06 — the section index, and the eight-await stack ─────────────────────

const panel = files.find((file) => file.name === 'PreferencesPanel.svelte')!.text

// The audit's headline number was "8 sequential dynamic-import waterfalls".
// They were never sequential — `{#await}` compiles to `$.await`, `$.await` runs
// its thunk inside `block()`, and a block effect runs as the parent fragment is
// built, so all eight `import()` calls went out in one synchronous pass. What
// was true is that eight INDEPENDENT await blocks settle independently, so the
// stack painted in up to eight passes in chunk-arrival order, and re-entered
// its pending branch on every reopen even though the modules had long since
// resolved. One block is the fix, and this is the measurement that keeps it.
test('the panel loads every section in one await block, not eight', async () => {
  const { compile } = await import('svelte/compiler')
  const { js } = compile(panel, { generate: 'client', name: 'PreferencesPanel' })

  const awaitBlocks = (js.code.match(/\$\.await\(/g) ?? []).length
  assert.equal(
    awaitBlocks,
    1,
    `the panel compiles to ${awaitBlocks} await blocks; each one settles on its own and repaints the stack separately`,
  )

  const dynamicImports = (js.code.match(/import\(/g) ?? []).length
  assert.equal(
    dynamicImports,
    sections.length,
    'the panel no longer dynamically imports exactly one module per section',
  )

  // One Promise.all, so the eight resolve as one result rather than eight.
  assert.match(panel, /Promise\.all\(\[/, 'the sections are not loaded together')
})

// The other half of "repopulates from a blank state on every open": the cache
// has to live on the MODULE. An instance field is recreated with the component,
// which is exactly the case being fixed.
test('the resolved sections are cached at module scope, not per instance', () => {
  const moduleStart = panel.indexOf('<script module')
  const moduleEnd = panel.indexOf('</script>', moduleStart)
  assert.ok(moduleStart >= 0 && moduleEnd > moduleStart, 'PreferencesPanel has no module script to cache in')
  const moduleScript = panel.slice(moduleStart, moduleEnd)

  assert.match(moduleScript, /let\s+settled/, 'the loaded components are not held at module scope')
  assert.match(moduleScript, /let\s+pending/, 'the in-flight load is not shared, so two mounts would each load')

  // And the instance renders them synchronously when they are already in hand,
  // rather than going back through an await block that flashes empty first.
  assert.match(panel, /\{#if alreadyLoaded\}/, 'a warm open still goes through the await block')
})

// A7-06's other half. The finding was one uninterrupted scroll from Appearance
// to MCP with no anchor list, so reaching Proxy meant scrolling past five other
// sections.
test('every section has an index entry and an anchor to scroll to', () => {
  const ids = [...panel.matchAll(/\{ id: '([a-z0-9]+)', label: '[^']+' \}/g)].map((match) => match[1])
  assert.equal(ids.length, sections.length, `the index lists ${ids.length} sections but the directory has ${sections.length}`)
  assert.deepEqual([...new Set(ids)], ids, 'the index lists a section twice')

  for (const id of ids) {
    assert.ok(
      panel.includes(`preferenceSectionAnchorId('${id}')`),
      `the index offers "${id}" but nothing in the stack carries its anchor`,
    )
  }

  // The strip has to stay sticky or it stops being navigation the moment the
  // reader scrolls, which is the only moment it is wanted.
  assert.match(panel, /position:\s*sticky/, 'the section index scrolls away with the content')
})

// A7-08. The summary line reported theme (which the user is looking at) and
// proxy (off for almost everyone) and said nothing about whether AI tools can
// reach into this workspace — the one piece of state in the panel that is both
// invisible and consequential.
//
// The clutter-free-shell pass moved it from a `<p class="panel-subtitle">` into
// PageHeader's `meta` snippet, which is where live state goes now. The element
// changed; the finding this pins did not, so the match is on the testid the
// line has always carried rather than on the tag it happens to be in.
test('the panel header reports AI access, not just theme and proxy', () => {
  assert.match(panel, /<PageHeader\s+title="Preferences"/, 'Preferences no longer uses the shared PageHeader')

  const line = panel.match(/<span data-testid="preferences-subtitle"[^>]*>([\s\S]*?)<\/span>/)
  assert.ok(line, 'the preferences summary line is gone')
  assert.match(line[1], /AI access/, 'the summary line still ignores the MCP state')

  // "enabled", never "running": whether the listener actually bound a port is
  // only known to GetMCPStatus, which the MCP section fetches on mount. A
  // header claiming "running" would be the exact lie that status line exists
  // to prevent.
  assert.ok(
    !/running/i.test(line[1]),
    'the summary line claims the MCP listener is running, which the panel header cannot know',
  )
})

// ── A6-11's equivalent: an async write that says nothing ─────────────────────
//
// Preferences writes instantly and has no Save button, which is right. But
// several of these settings round-trip to Go — the file cache re-measures, the
// clears walk a directory, AI access binds a socket — and until the round trip
// lands the control still renders the OLD value, so the checkbox springs back
// under the user's finger with nothing on screen to say why.
test('the row primitive can report a write that has not landed yet', () => {
  assert.match(settingRow, /busy\?:\s*boolean/, 'SettingRow cannot express a pending write')
  assert.match(settingRow, /aria-busy=/, 'the pending state is visual only and is not announced')
})

test('every section with a backend round-trip shows that it is working', () => {
  for (const name of ['CacheSection.svelte', 'McpSection.svelte']) {
    const text = files.find((file) => file.name === name)!.text
    assert.match(text, /busy=\{/, `${name} awaits the backend but never tells the user it is waiting`)
  }

  // The flag must be cleared on failure too. A refused write that left the row
  // reading "Saving..." forever is a worse lie than the silence it replaced.
  for (const name of ['CacheSection.svelte', 'McpSection.svelte']) {
    const text = files.find((file) => file.name === name)!.text
    assert.match(text, /\}\s*finally\s*\{/, `${name} does not clear its busy flag when the write fails`)
  }
})
