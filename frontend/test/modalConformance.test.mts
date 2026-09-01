// The rules every dialog in the app follows, asserted against the source.
//
// The A6 audit measured 32 dialogs and found the same three mistakes spread
// across them, each one cheap to make and invisible in review:
//
//   * 26 close buttons whose entire content was the letter `x`, so the
//     accessible name computed to "x, button". The title="Close" beside it is a
//     hover tooltip and nothing else — visible text content wins over title in
//     the name computation, so every one of those buttons announced a letter.
//   * Two footers with a destructive button first in DOM order, one of them
//     (UnsavedTabsModal) with no header close button at all, which made "Discard
//     & Close" the first focusable element in a dialog the user had not read.
//   * One delete-family confirmation, RemoveCollectionModal, painted in the
//     filled accent instead of the danger outline — identical in weight and
//     colour to every Save button in the app.
//
// None of those is caught by a type checker, and the repo has no component
// rendering harness, so these read the .svelte sources directly — the pattern
// brandMark.test.mts and nativeMenu.test.mts already use. That makes them weak
// about what renders and strong about the one thing that regressed: which
// markup is written.
import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, relative } from 'node:path'

const sourceRoot = fileURLToPath(new URL('../src', import.meta.url))
const modalRoot = join(sourceRoot, 'lib', 'modals')

function svelteFiles(directory: string): string[] {
  const found: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) found.push(...svelteFiles(path))
    else if (entry.name.endsWith('.svelte')) found.push(path)
  }
  return found
}

// Every dialog in the app: the modals tree, plus the two that live beside the
// feature they belong to. Modal.svelte itself is the shell, not a dialog.
const dialogPaths = [
  ...svelteFiles(modalRoot).filter((path) => path !== join(modalRoot, 'Modal.svelte')),
  join(sourceRoot, 'lib', 'views', 'mcp', 'McpApprovalModal.svelte'),
  join(sourceRoot, 'lib', 'workbench', 'WorkspaceWindowPicker.svelte'),
]

const dialogs = dialogPaths.map((path) => ({
  name: relative(sourceRoot, path),
  text: readFileSync(path, 'utf8'),
}))

test('the sweep sees every dialog', () => {
  // A directory walk that quietly finds nothing turns every assertion below
  // into a loop over an empty array, and the file still reports all green.
  // 32 when the audit counted them, 33 now: WorkspaceWindowPicker was the one
  // dialog outside the shell, and joining it made it the 33rd.
  assert.equal(dialogs.length, 33, 'the dialog count changed; a new dialog must satisfy the rules below')
})

/**
 * The opening tags of every `<button>` in `markup`.
 *
 * Written as a scanner rather than a regex because the attribute values here
 * contain `>`: an arrow function in `onclick={() => close()}` ends a naive
 * `<button[^>]*>` match halfway through the tag, which is how an earlier
 * version of this file silently skipped the buttons it most needed to read.
 */
function buttonTags(markup: string): string[] {
  const tags: string[] = []
  let index = markup.indexOf('<button')
  while (index !== -1) {
    let depth = 0
    let cursor = index
    while (cursor < markup.length) {
      const character = markup[cursor]
      if (character === '{') depth += 1
      else if (character === '}') depth -= 1
      else if (character === '>' && depth === 0) break
      cursor += 1
    }
    tags.push(markup.slice(index, cursor + 1))
    index = markup.indexOf('<button', cursor)
  }
  return tags
}

/** Each `class="button-row …"` footer in `markup`, as its inner source. */
function buttonRows(markup: string): string[] {
  const rows: string[] = []
  // <footer> as well as <div>, because NewRequestModal writes its footer as a
  // <footer class="button-row"> and the first version of this scanner looked
  // only for <div> — so the one dialog whose footer markup differs was the one
  // dialog none of the footer rules below were ever checked against.
  const opener = /<(div|footer) class="button-row[^"]*"/g
  let match = opener.exec(markup)
  while (match) {
    // These rows hold buttons and at most a hint span; none nests another
    // element of the same tag, so the first closing tag is the right one.
    const end = markup.indexOf(`</${match[1]}>`, match.index)
    rows.push(markup.slice(match.index, end === -1 ? markup.length : end))
    match = opener.exec(markup)
  }
  return rows
}

/** The opening `<Modal …>` tag of a dialog, attribute expressions intact. */
function modalTag(markup: string): string {
  const index = markup.indexOf('<Modal')
  if (index === -1) return ''
  let depth = 0
  let cursor = index
  while (cursor < markup.length) {
    const character = markup[cursor]
    if (character === '{') depth += 1
    else if (character === '}') depth -= 1
    else if (character === '>' && depth === 0) break
    cursor += 1
  }
  return markup.slice(index, cursor + 1)
}

/** The opening tags of every `<input>` in `markup`. */
function inputTags(markup: string): string[] {
  const tags: string[] = []
  let index = markup.indexOf('<input')
  while (index !== -1) {
    let depth = 0
    let cursor = index
    while (cursor < markup.length) {
      const character = markup[cursor]
      if (character === '{') depth += 1
      else if (character === '}') depth -= 1
      else if (character === '>' && depth === 0) break
      cursor += 1
    }
    tags.push(markup.slice(index, cursor + 1))
    index = markup.indexOf('<input', cursor)
  }
  return tags
}

/** `markup` with comments removed, so a file's own prose is never the subject. */
function withoutComments(markup: string): string {
  return markup
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^[ \t]*\/\/.*$/gm, '')
}

/** The `<style>` block of a dialog that has one, comments stripped. */
function styleBlock(markup: string): string {
  const start = markup.indexOf('<style>')
  if (start === -1) return ''
  return withoutComments(markup.slice(start, markup.indexOf('</style>', start)))
}

/** The text of every `<h2>` in `markup`, as the strings a user can read. */
function headingTexts(markup: string): string[] {
  const texts: string[] = []
  const heading = /<h2[^>]*>([\s\S]*?)<\/h2>/g
  let match = heading.exec(markup)
  while (match) {
    const inner = match[1].trim()
    // A heading whose whole body is an expression — McpApprovalModal picks its
    // title from the subject it is asking about — contributes each of the
    // literals that expression can produce, not the expression's source.
    const literals = [...inner.matchAll(/'([^']+)'/g)].map((found) => found[1])
    if (inner.startsWith('{') && literals.length > 0) texts.push(...literals)
    else texts.push(inner.replace(/\{[^}]*\}/g, '').trim())
    match = heading.exec(markup)
  }
  return texts.filter((text) => text.length > 0)
}

test('every dialog is built on the shared Modal shell', () => {
  for (const dialog of dialogs) {
    assert.ok(
      /import Modal from ['"][^'"]*Modal\.svelte['"]/.test(dialog.text),
      `${dialog.name} does not import the shared Modal shell`,
    )
    assert.ok(
      !/role="dialog"/.test(dialog.text),
      `${dialog.name} declares its own role="dialog"; Modal.svelte owns that element`,
    )
    assert.ok(
      !/aria-modal=/.test(dialog.text),
      `${dialog.name} declares its own aria-modal; Modal.svelte owns that element`,
    )
  }
})

test('no dialog uses a bare glyph as its close button', () => {
  for (const dialog of dialogs) {
    for (const tag of buttonTags(dialog.text)) {
      if (!/class="[^"]*icon-button/.test(tag)) continue
      assert.fail(
        `${dialog.name} hand-writes a close button (${tag.slice(0, 60)}…); use <IconButton icon="close" label="Close" />`,
      )
    }
    // The letter x and the multiplication sign both computed to a one-character
    // accessible name. Neither may come back as button content.
    assert.ok(
      !/>\s*[x×]\s*<\/button>/.test(dialog.text),
      `${dialog.name} uses a bare glyph as a button's only content`,
    )
  }
})

test('every close affordance is an IconButton labelled "Close"', () => {
  const withClose = dialogs.filter((dialog) => dialog.text.includes('<IconButton'))

  // ImportReplaceModal, UnsavedTabsModal and McpApprovalModal deliberately offer
  // no close affordance: every way out of them has to be one of the answers.
  assert.equal(
    dialogs.length - withClose.length,
    3,
    'the set of dialogs without a header close button changed; confirm the new one means to have no way out but an answer',
  )

  for (const dialog of withClose) {
    assert.ok(
      /import IconButton from ['"][^'"]*ui\/IconButton\.svelte['"]/.test(dialog.text),
      `${dialog.name} renders <IconButton> without importing the shared one`,
    )
    assert.ok(
      /<IconButton icon="close" label="Close"/.test(dialog.text),
      `${dialog.name}'s close button is not <IconButton icon="close" label="Close" …>`,
    )
  }
})

test('a footer puts every neutral action before the primary or destructive one', () => {
  for (const dialog of dialogs) {
    for (const row of buttonRows(dialog.text)) {
      // McpApprovalModal is the one deliberate exception, and its own comments
      // argue for it: Deny leads the row because it must be the easiest target
      // to hit on purpose, and "Allow and remember" trails the primary because
      // it is the only answer whose consequences outlive the run.
      if (row.includes('mcp-approval-actions')) continue

      const kinds = buttonTags(row).map((tag) =>
        /class="[^"]*danger-button/.test(tag) || /class="[^"]*\bprimary\b/.test(tag) ? 'decided' : 'neutral',
      )
      const firstDecided = kinds.indexOf('decided')
      const lastNeutral = kinds.lastIndexOf('neutral')
      if (firstDecided === -1 || lastNeutral === -1) continue
      // Every neutral before every decided, not merely "the last button is
      // decided" — the first version of this assertion checked only the tail,
      // and so passed a footer reading danger, Cancel, primary, which is the
      // exact shape UnsavedTabsModal shipped.
      assert.ok(
        lastNeutral < firstDecided,
        `${dialog.name} puts a primary or destructive action before a Cancel/Close: ${kinds.join(', ')}`,
      )
    }
  }
})

test('every delete-family confirmation is styled as destructive, not primary', () => {
  const destructive = [
    'lib/modals/confirm/RemoveCollectionModal.svelte',
    'lib/modals/confirm/DeleteRequestModal.svelte',
    'lib/modals/confirm/DeleteFolderModal.svelte',
    'lib/modals/confirm/DeleteFlowModal.svelte',
    'lib/modals/confirm/ImportReplaceModal.svelte',
  ]

  for (const name of destructive) {
    const dialog = dialogs.find((candidate) => candidate.name === name)
    assert.ok(dialog, `${name} is no longer where this test looks for it`)

    const confirms = buttonRows(dialog.text)
      .flatMap(buttonTags)
      .filter((tag) => !/onclick=\{[^}]*[Cc]ancel/.test(tag) && !/on:click=\{[^}]*[Cc]ancel/.test(tag))

    assert.ok(
      confirms.some((tag) => /class="danger-button"/.test(tag)),
      `${name} has no .danger-button in its footer`,
    )
    assert.ok(
      !confirms.some((tag) => /class="[^"]*\bprimary\b/.test(tag)),
      `${name} styles a destructive action as .primary; it will look identical to Save`,
    )
  }
})

test('the name-entry dialogs name their own first field', () => {
  // The three mechanisms the audit found were: this attribute, an imperative
  // .focus() reaching in from App.svelte, and "whatever is first in DOM order" —
  // which for a dialog with a header close button is the close button. Opening
  // Rename Collection put the caret nowhere at all.
  const nameEntry = [
    'lib/modals/collection/CloneCollectionModal.svelte',
    'lib/modals/collection/CloneFolderModal.svelte',
    'lib/modals/collection/CloneRequestModal.svelte',
    'lib/modals/collection/NewFolderModal.svelte',
    'lib/modals/collection/RenameCollectionModal.svelte',
    'lib/modals/collection/RenameFolderModal.svelte',
    'lib/modals/collection/RenameRequestModal.svelte',
    'lib/modals/confirm/CreateExampleModal.svelte',
    'lib/modals/confirm/NewRequestModal.svelte',
    'lib/modals/confirm/PromptDialogModal.svelte',
  ]

  for (const name of nameEntry) {
    const dialog = dialogs.find((candidate) => candidate.name === name)
    assert.ok(dialog, `${name} is no longer where this test looks for it`)
    assert.ok(
      dialog.text.includes('data-modal-autofocus'),
      `${name} does not mark its name field with data-modal-autofocus`,
    )
  }
})

test('the dialogs that open on Cancel say so themselves', () => {
  // Both of these were held safe only by a tick().then(button.focus()) in
  // App.svelte — a second focus mechanism, living outside the component that
  // needs it, which the next refactor of App.svelte could quietly drop.
  for (const name of [
    'lib/modals/confirm/ImportReplaceModal.svelte',
    'lib/modals/confirm/UnsavedTabsModal.svelte',
  ]) {
    const dialog = dialogs.find((candidate) => candidate.name === name)
    assert.ok(dialog, `${name} is no longer where this test looks for it`)

    const cancel = buttonTags(dialog.text).find((tag) => /Cancel/.test(tag) || /[Cc]ancel/.test(tag))
    assert.ok(cancel, `${name} has no cancel button`)
    assert.ok(
      cancel.includes('data-modal-autofocus'),
      `${name} does not put data-modal-autofocus on its cancel button`,
    )
  }
})

test('modal-footer, which never had a CSS rule, stays deleted', () => {
  // Four files carried class="button-row modal-footer" and `modal-footer`
  // matched nothing in style.css — copy-pasted forward from file to file, or
  // left behind when a rule was removed. Either way it read as a deliberate
  // footer treatment that did not exist.
  for (const dialog of dialogs) {
    assert.ok(
      !dialog.text.includes('modal-footer'),
      `${dialog.name} applies modal-footer, which has no CSS rule`,
    )
  }
})

test('the workspace picker no longer re-implements the modal shell', () => {
  const source = readFileSync(
    join(sourceRoot, 'lib', 'workbench', 'WorkspaceWindowPicker.svelte'),
    'utf8',
  )
  // Comments are stripped first: the file's own header explains what it used to
  // carry, naming every fragment below, and an earlier version of this test
  // failed on that explanation rather than on any code.
  const picker = source
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^[ \t]*\/\/.*$/gm, '')

  // Each of these was a private copy of something Modal.svelte already owns and
  // already has tests for. The picker was written after that consolidation, so
  // a fix to the shared trap reached 28 dialogs and not this one.
  for (const [fragment, what] of [
    ['trapTab', 'its own Tab trap'],
    ["key === 'Escape'", 'its own Escape handler'],
    ['workspace-picker-backdrop', 'its own backdrop'],
    ['z-index:', 'its own stacking order'],
    ['backdrop-filter:', 'a backdrop blur no other dialog has'],
    ['@keyframes', 'a private copy of the app-wide spin animation'],
    ['--surface-raised', 'a token declared nowhere in style.css'],
    ['--surface-hover', 'a token declared nowhere in style.css'],
  ] as const) {
    assert.ok(!picker.includes(fragment), `WorkspaceWindowPicker still carries ${what} (${fragment})`)
  }

  assert.ok(
    /dialogClass="prompt-dialog /.test(picker),
    'WorkspaceWindowPicker does not compose with .prompt-dialog, so a shell restyle would miss it',
  )
})

// ---------------------------------------------------------------------------
// The second pass: A6-06 through A6-12, A8-08 and A8-10.
//
// The first pass closed the mistakes that were invisible on screen. These close
// the ones that are visible and were simply never decided: which word a confirm
// button uses, how a title is capitalised, how wide a dialog is, whether the
// shell is told the dialog is working, and whether Return does anything.
// ---------------------------------------------------------------------------

/** The one sanctioned width vocabulary. */
const sizeSteps = ['small', 'medium', 'large', 'xlarge']

test('every dialog takes its width from the named scale', () => {
  // Width was the last part of the box treatment still decided per dialog, in
  // style.css, in pixels: twelve values between 420 and 1120, none of which
  // referred to any of the others. `size` replaced them with four steps, and
  // this is what keeps a thirteenth number from being added — there is no
  // per-dialog width rule left for a new dialog to copy.
  const unsized: string[] = []
  for (const dialog of dialogs) {
    const named = /\bsize="([a-z]+)"/.exec(dialog.text)
    if (!named) {
      unsized.push(dialog.name)
      continue
    }
    assert.ok(
      sizeSteps.includes(named[1]),
      `${dialog.name} asks for size="${named[1]}", which is not one of ${sizeSteps.join(' | ')}`,
    )
  }

  // CommandPaletteModal, GlobalSearchModal and WorkspaceWindowPicker still take
  // their width from a style.css rule of their own. The first two belong to
  // another owner this wave; the picker's 580px is load-bearing for its option
  // rows and needs its own step or its own justification. Migrating one means
  // editing this list, which is the point.
  assert.deepEqual(
    unsized.sort(),
    [
      'lib/modals/search/CommandPaletteModal.svelte',
      'lib/modals/search/GlobalSearchModal.svelte',
      'lib/workbench/WorkspaceWindowPicker.svelte',
    ],
    'the set of dialogs still sizing themselves from style.css changed',
  )
})

test('the size scale is defined once, in the shell', () => {
  const shell = readFileSync(join(modalRoot, 'Modal.svelte'), 'utf8')
  for (const [step, width] of [
    ['small', '420px'],
    ['medium', '460px'],
    ['large', '720px'],
    ['xlarge', '1060px'],
  ] as const) {
    assert.ok(
      new RegExp(`\\.modal-${step}\\s*\\{\\s*width: min\\(${width}, 100%\\);`).test(shell),
      `Modal.svelte no longer defines .modal-${step} as ${width}`,
    )
  }
  // Scoped CSS is what makes these outrank a leftover .code-generator-dialog in
  // style.css, and Svelte only scopes a selector it can statically match — an
  // interpolated `class="… modal-{size}"` would compile the whole block away
  // and every dialog would silently fall back to 460px.
  for (const step of sizeSteps) {
    assert.ok(
      shell.includes(`class:modal-${step}={size === '${step}'}`),
      `Modal.svelte applies .modal-${step} some way other than a class: directive`,
    )
  }
})

test('a dialog that tracks a busy state tells the shell about it', () => {
  // aria-busy was wired for three of 33 dialogs while sixteen more tracked a
  // busy flag, disabled their buttons on it, and said nothing. A screen reader
  // heard the buttons go quiet and was told no reason for it.
  for (const dialog of dialogs) {
    if (!/\bbusy\??:\s*(string|boolean)/.test(dialog.text)) continue
    assert.ok(
      /\bbusy[=}]/.test(modalTag(dialog.text)),
      `${dialog.name} declares a busy prop but never forwards it to <Modal busy=…>`,
    )
  }
})

test('a dialog title is Title Case, or a question in sentence case', () => {
  // 26 titles were Title Case and six were not, including between direct
  // siblings: "New Folder" from one sidebar menu and "New request" from the
  // next. The convention is the majority one — Title Case, no terminal
  // punctuation — with one carve-out that is a real pattern rather than a
  // lapse: a dialog phrased as a yes/no question keeps sentence case and its
  // question mark, because "Replace Existing Collections?" reads as a headline
  // and not as something being asked of you.
  const smallWords = new Set(['a', 'an', 'and', 'as', 'at', 'by', 'for', 'in', 'of', 'on', 'or', 'the', 'to', 'with'])
  // Words that are lowercase because that is their name.
  const properLowercase = new Set(['grpcurl'])
  // Another owner's this wave: "Command palette" and "Global Search" are the
  // two titles left in the old split. Named here rather than silently skipped
  // so the debt is visible; see handoff-modals-2.md.
  const notMine = new Set([
    'lib/modals/search/CommandPaletteModal.svelte',
    'lib/modals/search/GlobalSearchModal.svelte',
  ])

  for (const dialog of dialogs) {
    if (notMine.has(dialog.name)) continue
    for (const title of headingTexts(dialog.text)) {
      if (title.endsWith('?')) {
        assert.ok(
          /^[A-Z]/.test(title),
          `${dialog.name}'s question title "${title}" does not start with a capital`,
        )
        continue
      }
      assert.ok(
        !/[.!?]$/.test(title),
        `${dialog.name}'s title "${title}" ends in punctuation; only a yes/no question may`,
      )
      const words = title.split(/\s+/).filter((word) => /[A-Za-z]/.test(word))
      words.forEach((word, index) => {
        const bare = word.replace(/[^A-Za-z0-9]/g, '')
        if (bare === '') return
        if (properLowercase.has(bare)) return
        if (index > 0 && smallWords.has(bare.toLowerCase())) return
        assert.ok(
          /^[A-Z0-9]/.test(bare),
          `${dialog.name}'s title "${title}" is not Title Case: "${word}"`,
        )
      })
    }
  }
})

test('a confirm button says the verb its own title says', () => {
  // Clone Collection's button said "Create" while Clone Folder's and Clone
  // Request's said "Clone" — same shaped form, same busy-label pattern, a
  // different verb for the same act. The rule is that the footer's decided
  // action repeats the title's verb, so nothing has to be remembered per file.
  const verbForTitle: Record<string, string> = {
    Clone: 'Clone',
    Rename: 'Rename',
    Delete: 'Delete',
    Remove: 'Remove',
    Create: 'Create',
    // "New Folder" and "New Request" are the two dialogs whose title verb is
    // not the button's; a thing that does not exist yet cannot be "New"ed.
    New: 'Create',
  }

  for (const dialog of dialogs) {
    for (const title of headingTexts(dialog.text)) {
      const verb = verbForTitle[title.split(/\s+/)[0]]
      if (!verb) continue
      const decided = buttonRows(dialog.text)
        .flatMap(buttonTags)
        .filter((tag) => /class="[^"]*(danger-button|\bprimary\b)/.test(tag))
      assert.ok(decided.length > 0, `${dialog.name} ("${title}") has no decided action in its footer`)
      const labels = decided.map((tag) => {
        const end = dialog.text.indexOf('</button>', dialog.text.indexOf(tag))
        return dialog.text.slice(dialog.text.indexOf(tag) + tag.length, end)
      })
      assert.ok(
        labels.some((label) => label.includes(`'${verb}'`) || label.trim() === verb),
        `${dialog.name} is titled "${title}" but its confirm button never says "${verb}": ${labels.join(' / ')}`,
      )
    }
  }
})

test('a dialog you can type into submits on Return', () => {
  // Sixteen dialogs got Enter-to-submit for free by wrapping their fields in a
  // <form>; OAuth2AuthorizationModal wrapped its callback field in a <div>, so
  // it was the one text-entry dialog in the app where the key everyone presses
  // after pasting did nothing at all.
  //
  // The two search dialogs are exempt and are not an oversight: their input
  // drives a live-filtered list and Return means "open the highlighted result",
  // which is a keydown on the list, not a form submission.
  const listDriven = new Set([
    'lib/modals/search/CommandPaletteModal.svelte',
    'lib/modals/search/GlobalSearchModal.svelte',
  ])

  for (const dialog of dialogs) {
    if (listDriven.has(dialog.name)) continue
    const typed = inputTags(dialog.text).some(
      (tag) => !/type="(checkbox|radio|color|file|range)"/.test(tag),
    )
    if (!typed) continue
    assert.ok(
      /<form[^>]*(on:submit|onsubmit)/.test(dialog.text),
      `${dialog.name} has a text field but no <form> to submit it; Return does nothing there`,
    )
  }
})

test('the code-generator family disables Copy when there is nothing to copy', () => {
  for (const name of [
    'lib/modals/codegen/GrpcurlCommandModal.svelte',
    'lib/modals/codegen/RequestCodeModal.svelte',
    'lib/modals/codegen/ResponseExampleCodeModal.svelte',
  ]) {
    const dialog = dialogs.find((candidate) => candidate.name === name)
    assert.ok(dialog, `${name} is no longer where this test looks for it`)
    const copy = buttonTags(dialog.text).find((tag) => {
      const end = dialog.text.indexOf('</button>', dialog.text.indexOf(tag))
      return dialog.text.slice(dialog.text.indexOf(tag) + tag.length, end).trim() === 'Copy'
    })
    assert.ok(copy, `${name} has no Copy button`)
    assert.ok(
      /disabled=/.test(copy),
      `${name}'s Copy button is live with nothing to copy; it reports success and copies ''`,
    )
  }
})

test('every dialog writes an ellipsis as one character', () => {
  // 'Exporting...' beside 'Running…' and 'Saving…' one pane over. Three periods
  // and U+2026 are a different width in every font the app ships with, so the
  // busy label visibly jumps depending on which dialog you opened.
  for (const dialog of dialogs) {
    const source = withoutComments(dialog.text)
    // A word character before the dots is what separates a truncated label from
    // JavaScript spread syntax, which is always preceded by a bracket or space.
    const found = /\w\.\.\./.exec(source)
    assert.ok(
      !found,
      `${dialog.name} writes "${found?.[0]}"; use the single character … instead`,
    )
  }
})

test('a dialog with local styles uses the type scale and no token fallbacks', () => {
  // DiscoveryModal set 0.9rem, 0.8rem and 0.75rem — 14.4px, 12.8px and 12px
  // against a closed px scale — so its headings and captions sat a fraction off
  // every other heading and caption in the app. It also wrote
  // `var(--border, rgba(0, 0, 0, 0.12))`, the fallback habit that hid ten
  // undefined tokens until designTokens.test.mts went looking for them.
  for (const dialog of dialogs) {
    const styles = styleBlock(dialog.text)
    if (styles === '') continue
    for (const [, value] of styles.matchAll(/font-size:\s*([^;]+);/g)) {
      assert.ok(
        /var\(--(font-size-\d+|code-font-size)\)/.test(value.trim()),
        `${dialog.name} sets font-size: ${value.trim()}; use a --font-size-* token`,
      )
    }
    const fallback = /var\(\s*--[a-z0-9-]+\s*,/.exec(styles)
    assert.ok(
      !fallback,
      `${dialog.name} gives a token a fallback (${fallback?.[0]}); define the token instead`,
    )
  }
})
