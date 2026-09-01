// One row-action control for every editable table, and no glyphs pretending to
// be icons.
//
// The tables audit counted fifteen surfaces that delete a row and found two
// unrelated controls doing it — a 32px square holding the letter `x` in seven,
// a text button reading "Remove" in eight — with nothing about the data
// deciding which one a table got. The move and drag affordances were in the
// same state: `^`, `v` and `::` typed as literal characters into elements whose
// class already claimed they held an icon, sitting a pane away from the
// overflow menu's real SVGs.
//
// Replacing the glyphs is not the fix that holds. Three copies of the same
// action cell in three files drift apart again the next time one of them is
// touched, which is exactly how the split arose. So these tests are about the
// STRUCTURE: there is one component that renders the cell, the primitives
// delegate to it, and none of them can reach for a bare `.icon-button` again.
//
// Asserted against the source text because the repo has no component-rendering
// harness — the same approach as brandMark.test.mts and syntaxHighlight.test.mts.
// That makes these weak about pixels and strong about the one thing that
// regressed: which control the markup actually names.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

/**
 * These files explain what they replaced, so the markup they replaced is
 * quoted verbatim in their comments — `class="icon-button subtle"` among it.
 * A rule that reads the raw file therefore fails on a file that is correct, and
 * the "is this class defined anywhere" check passes for a dead class because
 * the comment saying it is dead mentions it. Both were live in the first draft
 * of this file. Every rule below reads the markup with comments removed.
 */
const withoutComments = (text: string) => text.replace(/<!--[\s\S]*?-->/g, '')

/** The three shared primitives behind every editable table in the app. */
const tablePrimitives = ['KeyValueTable', 'MultipartTable', 'FileBodyTable']

const primitiveMarkup = (name: string) => withoutComments(read(`../src/lib/${name}.svelte`))

const devToolsDirectory = fileURLToPath(new URL('../src/lib/views/devtools', import.meta.url))
const devToolsFiles = readdirSync(devToolsDirectory)
  .filter((name) => name.endsWith('.svelte'))
  .map((name) => ({ name, text: withoutComments(readFileSync(join(devToolsDirectory, name), 'utf8')) }))

/** Everything this change owns, i.e. everything these rules can hold true for. */
const ownedFiles = [
  ...tablePrimitives.map((name) => ({ name: `${name}.svelte`, text: withoutComments(read(`../src/lib/${name}.svelte`)) })),
  { name: 'RowActions.svelte', text: withoutComments(read('../src/lib/RowActions.svelte')) },
  { name: 'SuggestionListbox.svelte', text: withoutComments(read('../src/lib/SuggestionListbox.svelte')) },
  ...devToolsFiles
]

test('every editable table delegates its row actions to the one shared cell', () => {
  for (const name of tablePrimitives) {
    const markup = primitiveMarkup(name)
    assert.ok(
      /import RowActions from ['"]\.\/RowActions\.svelte['"]/.test(markup),
      `${name} does not import RowActions`,
    )
    const uses = markup.match(/<RowActions\b/g) ?? []
    assert.equal(uses.length, 1, `${name} should render RowActions exactly once, found ${uses.length}`)
  }
})

test('no table primitive hand-rolls a delete, move or drag control of its own', () => {
  // The four glyphs, exactly as they appeared: `x` to delete, `^`/`v` to move,
  // `::` for the drag handle. Matched as the whole text content of an element
  // so a `v` inside a word or an `x` inside an attribute cannot trip this.
  const glyphButton = />\s*(x|\^|v|::)\s*</
  for (const name of tablePrimitives) {
    const markup = primitiveMarkup(name)
    assert.ok(!glyphButton.test(markup), `${name} still renders a literal glyph as a row action`)
    assert.ok(
      !/onclick=\{\(\) => onRemove\(/.test(markup),
      `${name} wires its own remove button; the control belongs to RowActions`,
    )
  }
})

test('nothing this change owns reaches for the bare .icon-button class', () => {
  // `.icon-button` is a 32px square with `text-align: center` — a contract that
  // says "an icon lives here", which is why the audit found `Send` and `Gen`
  // wrapping inside one. IconButton makes the icon the content and the label a
  // required prop, so the two cannot come apart again. A file that writes the
  // class by hand has stepped around that.
  for (const file of ownedFiles) {
    assert.ok(
      !/class="[^"]*\bicon-button\b/.test(file.text),
      `${file.name} applies .icon-button directly; use the IconButton component`,
    )
  }
})

test('the delete control is a trash icon with a real accessible name', () => {
  const markup = withoutComments(read('../src/lib/RowActions.svelte'))
  assert.match(markup, /icon="trash"/, 'RowActions no longer deletes with a trash icon')
  assert.match(markup, /label=\{`Remove \$\{noun\}`\}/, 'the delete control lost its label')
  assert.match(markup, /icon="chevron-up"/, 'Move up is no longer a chevron')
  assert.match(markup, /icon="chevron-down"/, 'Move down is no longer a chevron')
})

test('the drag handle is not a control, because it never had a behaviour', () => {
  // It used to be a <button> with an aria-label and no click handler: a tab
  // stop on every row that did nothing when activated. The drag itself is on
  // the <tr>, and the Move buttons beside it are what keyboard users reorder
  // with, so the handle is decoration and is marked as such.
  const markup = withoutComments(read('../src/lib/RowActions.svelte'))
  const template = markup.slice(markup.indexOf('</script>'))
  assert.match(template, /<span class="row-drag-handle drag-handle" aria-hidden="true">/)
  assert.ok(
    !/<button[^>]*drag-handle/.test(template),
    'the drag handle is a button again; it has no activation behaviour to offer',
  )

  // The row is still what actually drags. If this goes, reordering is gone and
  // nothing else in these files would notice.
  for (const name of tablePrimitives) {
    assert.match(
      primitiveMarkup(name),
      /draggable=\{showMove && !readonly\}/,
      `${name} no longer makes its rows draggable`,
    )
  }
})

test('add-row copy is cased the same way in all three body tables', () => {
  // "Add row", "Add row", "Add File" — the file table was the only one
  // title-casing its noun, in the three tables that swap places as the body
  // mode changes. The noun differs because the rows differ; the casing must not.
  for (const name of tablePrimitives) {
    const markup = primitiveMarkup(name)
    const labels = [...markup.matchAll(/>(Add [^<]+)</g)].map((match) => match[1])
    assert.ok(labels.length > 0, `${name} has no add-row button`)
    for (const label of labels) {
      assert.match(label, /^Add [a-z]+$/, `${name}'s add button reads "${label}"`)
    }
  }
})

test('every table primitive can be given an accessible name', () => {
  // Not one <table> in the app had one, so a screen reader announced eleven
  // structurally identical name/value grids with nothing to tell them apart.
  // The prop is optional only because App.svelte owns the call sites.
  for (const name of tablePrimitives) {
    const markup = primitiveMarkup(name)
    assert.match(markup, /label\?: string/, `${name} takes no label prop`)
    assert.match(markup, /<table class="[^"]*" aria-label=\{label\}>/, `${name} does not apply label as aria-label`)
  }
})

test('row reordering is ready at the component level for every table that uses it', () => {
  // A9-09: `showMove` is switched on only in the Response Examples editor,
  // while Params and Headers — the tables edited on every single request — do
  // not get it. Turning it on is a prop change at call sites in App.svelte,
  // which this change cannot edit; what it CAN guarantee is that the component
  // side is complete, so the handoff is genuinely one prop per call site.
  for (const name of tablePrimitives) {
    const markup = primitiveMarkup(name)
    assert.match(markup, /showMove\?: boolean/, `${name} lost its showMove prop`)
    assert.match(markup, /onMove\?: \(index: number, direction: -1 \| 1\) => void/, `${name} lost onMove`)
    assert.match(markup, /onReorder\?: \(from: number, to: number\) => void/, `${name} lost onReorder`)
    assert.match(markup, /<RowActions[^>]*\{showMove\}/, `${name} does not pass showMove through to the action cell`)
  }
})

// --- DevTools ---------------------------------------------------------------
//
// The decision the audit asked someone to make and record: DevTools looks like
// DevTools. Denser, more tabular, more monospaced than the request editors,
// because it is a log inspector. What follows checks the panel is at least
// internally consistent about that, which is what it was not.

test('every code surface in the request details panel uses the app code font', () => {
  // `.console-row code` set --code-font-family; the details tables, the request
  // and response bodies and the network log lines set only colour and wrapping,
  // so they fell through to the browser's default monospace at the app's
  // PROPORTIONAL font size. Two faces at two sizes inside one panel, for the
  // same category of string.
  const markup = read('../src/lib/views/devtools/RequestDetailsPanel.svelte')
  const styles = markup.slice(markup.indexOf('<style>'))
  assert.match(styles, /font-family: var\(--code-font-family\)/)
  assert.match(styles, /font-size: var\(--code-font-size\)/)
  for (const selector of ['.details-table code', '.network-body', '.progress-row code', '.detail-list dd code']) {
    assert.ok(styles.includes(selector), `${selector} is no longer given the code font`)
  }
})

test('the request URL and method are monospaced like the headers beside them', () => {
  const markup = withoutComments(read('../src/lib/views/devtools/RequestDetailsPanel.svelte'))
  assert.match(markup, /<dd><code>\{selectedDevToolsNetworkRow\.url\}<\/code><\/dd>/)
  assert.match(markup, /<dd><code>\{normalizedNetworkMethod\(selectedDevToolsNetworkRow\)\}<\/code><\/dd>/)
})

test('the read-only header tables carry a header row and a name', () => {
  const markup = withoutComments(read('../src/lib/views/devtools/RequestDetailsPanel.svelte'))
  const headers = markup.match(/<thead><tr><th>Name<\/th><th>Value<\/th><\/tr><\/thead>/g) ?? []
  assert.equal(headers.length, 2, 'a details table lost its header row')
  assert.match(markup, /<table class="details-table" aria-label="Request headers">/)
  assert.match(markup, /<table class="details-table" aria-label="Response headers">/)
})

test('DevTools empty states pick one of the two shapes, never the bare class', () => {
  // Three shapes were in use across four files: the Console tab's centred
  // headline+detail, the details panel's one-line compact box, and — in the
  // Terminal tab only — `.empty-state` with no modifier at all, which is 24px
  // of padding and a dashed border wrapped around a sentence in a 200px rail.
  //
  // The rule: `devtools-empty` when a whole tab is empty, `compact` when a
  // section inside one is.
  for (const file of devToolsFiles) {
    for (const match of file.text.matchAll(/class="(empty-state[^"]*)"/g)) {
      const className = match[1]
      assert.ok(
        className === 'empty-state compact' || className === 'empty-state devtools-empty',
        `${file.name} uses "${className}"; DevTools empty states are compact or devtools-empty`,
      )
    }
  }
})

test('DevTools declares no class the stylesheet has never heard of', () => {
  // `.icon-button subtle` in the Terminal tab: `.subtle` matched no rule
  // anywhere, the same species of dead class name as `.empty-appState`. It
  // looked like a deliberate quiet variant in review and did nothing on screen.
  const css = read('../src/style.css')
  for (const file of devToolsFiles) {
    for (const match of file.text.matchAll(/class="([^"{]*)"/g)) {
      for (const className of match[1].split(/\s+/).filter(Boolean)) {
        assert.ok(
          css.includes(`.${className}`) || file.text.includes(`.${className}`),
          `${file.name} uses class "${className}" but no rule defines it`,
        )
      }
    }
  }
})

// ---------------------------------------------------------------------------
// A9-02 — one network table. A9-10 / A9-12 / A1-08 — one set of table
// behaviours across the three primitives.
// ---------------------------------------------------------------------------

/**
 * The row-feedback rules, lifted out of each primitive's <style> block.
 *
 * Compared rule by rule rather than block by block because KeyValueTable has a
 * fourth rule the other two do not (the description column). What must not
 * drift is these two.
 */
const rowFeedbackRules = (text: string) => [
  text.match(/tbody tr:hover td \{[^}]*\}/)?.[0],
  text.match(/tbody tr:focus-within td \{[^}]*\}/)?.[0]
]

test('every editable table gives a row the same hover and focus feedback', () => {
  // Before this, exactly one table in the app — the DevTools network log — had
  // any row feedback at all, which the audit read as one table having received
  // more attention rather than as a rule. These grids are rows of near-identical
  // inputs and the mistake they invite is editing the WRONG row, so hover marks
  // the row under the pointer and :focus-within marks the row with the caret.
  const reference = rowFeedbackRules(read(`../src/lib/${tablePrimitives[0]}.svelte`))
  assert.ok(reference[0] && reference[1], 'KeyValueTable has no row-feedback rules')
  for (const name of tablePrimitives) {
    assert.deepEqual(
      rowFeedbackRules(read(`../src/lib/${name}.svelte`)),
      reference,
      `${name}'s row feedback has drifted from the other tables'`,
    )
  }
})

test('row feedback is painted from tokens, never a literal colour', () => {
  for (const name of tablePrimitives) {
    for (const rule of rowFeedbackRules(read(`../src/lib/${name}.svelte`))) {
      assert.ok(rule, `${name} lost a row-feedback rule`)
      assert.match(rule as string, /var\(--selected-bg\)/, `${name} paints row feedback with something other than the selection token`)
      assert.ok(!/#[0-9a-f]{3,8}\b/i.test(rule as string), `${name} hard-codes a colour in its row feedback`)
    }
  }
})

test('the bulk-edit toggle is the shared segmented control, not a pill group', () => {
  // Two plain buttons with an `.active` class, no role, no grouping, and two
  // tab stops for a one-of-two choice — the exact hand-rolled shape
  // SegmentedControl exists to absorb, and the same control the body-mode and
  // response-view pickers already are.
  const markup = primitiveMarkup('KeyValueTable')
  assert.match(markup, /import SegmentedControl from '\.\/ui\/SegmentedControl\.svelte'/)
  assert.match(markup, /<SegmentedControl\b/)
  assert.ok(
    !/data-testid="kv-mode-rows"/.test(markup),
    'the hand-rolled mode buttons are back; the group is addressable by data-value now',
  )
})

test('bulk edit is offered only where the table permits renaming and adding rows', () => {
  // A9-10, and the half of it the audit read as a style inconsistency. The bulk
  // textarea is parsed back into a WHOLE NEW row array, so it can rename a row,
  // delete one and invent one. Path Params passes showBulkEdit next to
  // readonlyNames, showAddRow={false} and showActions={false} — and got a tab
  // that let a user rename `:id` in a grid otherwise locked against exactly
  // that. The condition belongs to the component so no future caller can pass
  // the same pair of props and reopen it.
  const markup = primitiveMarkup('KeyValueTable')
  assert.match(
    markup,
    /const bulkEditAvailable = \$derived\(showBulkEdit && !readonly && !readonlyNames && showAddRow\)/,
  )
  assert.ok(
    !/\{#if showBulkEdit\b/.test(markup),
    'a bulk-edit branch still tests the raw prop instead of what the table permits',
  )
})

test('the computed description finally has somewhere to render', () => {
  // A1-08. The Vars tab has mapped `description: v.dataType` onto every row
  // since the data-type feature landed, and this component had no rendering
  // path for `description` anywhere in its template — computed on every
  // keystroke, dropped on the floor. Read-only because it is DERIVED.
  const markup = primitiveMarkup('KeyValueTable')
  assert.match(markup, /\{#if showDescription\}/)
  assert.match(markup, /<td class="kv-description"><code>\{row\.description \|\| '—'\}<\/code><\/td>/)
  assert.ok(
    !/onChange\(index, 'description'/.test(markup),
    'the description became editable; it is recomputed from the value and an edit would be discarded',
  )
})

test('there is one network table, and it carries an accessible name', () => {
  // A9-02. `appState.networkLog` had two renderings three thousand lines apart
  // in App.svelte: this one — virtualised, sortable, resizable, filterable,
  // selectable into a detail pane — and a nine-line <table> reachable from the
  // command palette that printed the same fields raw. Same array, same rows,
  // opposite ends of the quality range, both on the main navigation.
  const markup = withoutComments(read('../src/lib/views/devtools/NetworkTable.svelte'))
  assert.match(markup, /<table class="devtools-network-table" aria-label=\{label\}/)
  assert.match(markup, /label = 'Network requests'/, 'the table name is no longer defaulted')
  // Everything a second call site needs in order to mount it: the rows go in,
  // the stored sort and widths go in and come back out. Nothing about how they
  // are saved is in here.
  for (const prop of ['rows', 'preferences', 'onPreferencesChange', 'detailsPanelWidth', 'onStartDetailsResize']) {
    assert.ok(markup.includes(prop), `NetworkTable no longer takes ${prop}`)
  }
})

test('the network table and its detail pane format cells from one module', () => {
  // Four of RequestDetailsPanel's props used to be the functions that turn a
  // NetworkLog into strings, handed down from App.svelte because that is where
  // they happened to live — so the pane could not render a header row without
  // its parent supplying a formatter, and the parent's own legacy table three
  // thousand lines below rendered the same fields raw because nobody passed
  // them to it.
  const table = withoutComments(read('../src/lib/views/devtools/NetworkTable.svelte'))
  const panel = withoutComments(read('../src/lib/views/devtools/RequestDetailsPanel.svelte'))
  assert.match(table, /from '\.\.\/\.\.\/networkSort'/)
  assert.match(panel, /from '\.\.\/\.\.\/networkSort'/)
  for (const helper of ['networkStatusDisplay', 'networkSizeDisplay', 'networkLogTime']) {
    assert.ok(table.includes(helper), `the network table stopped using ${helper}`)
  }
  // The four legacy formatter props must stay OPTIONAL: App.svelte still mounts
  // this panel and cannot be edited from here, so a required set it no longer
  // satisfies is a type error nobody in this change can fix. They go when that
  // mount does.
  for (const legacy of ['networkHeaderRows?:', 'networkLogBody?:', 'networkLogLines?:', 'normalizedNetworkMethod?:']) {
    assert.ok(panel.includes(legacy), `${legacy} is no longer optional; App.svelte still passes it`)
  }
})

test('an empty filter is a different empty state from an empty log', () => {
  // The old table said "No network requests" to both. An empty log means make a
  // request; an empty FILTERED log means the rows exist and the method filter
  // above is hiding them — and saying the first to the second reads as the log
  // having been cleared.
  const markup = withoutComments(read('../src/lib/views/devtools/NetworkTable.svelte'))
  assert.match(markup, /<strong>No network requests<\/strong>/)
  assert.match(markup, /<strong>No requests match the method filter<\/strong>/)
})
