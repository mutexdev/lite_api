// The editor's syntax colours.
//
// The reported fault: JSON string values were painted `#a11` by CodeMirror's
// defaultHighlightStyle — a dark red within a few points of this app's own
// --danger (#8e1a10). In an editor whose linter gutter, response pane and
// variable underlines all use red for "wrong", an ordinary request body read as
// a page of errors.
//
// Two rules come out of that, and neither is enforceable by looking at a
// screenshot once:
//
//   1. Every syntax token is legible against the surface it is drawn on.
//   2. --syntax-invalid is the only red.
//
// Both are checked here by parsing style.css, because a palette regresses the
// same way it arrived: someone adds a theme, copies a block, and changes the
// colours that were obviously wrong while leaving the ones that were only
// nearly wrong.
//
// Asserted against source rather than a rendered editor because the repo has no
// component-rendering harness — see brandMark.test.mts and sidebarTree.test.mts,
// which read their files the same way.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { syntaxTokenNames } from '../src/lib/workbench/syntaxHighlight.ts'

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const css = () => read('../src/style.css')
const style = () => read('../src/lib/workbench/syntaxHighlight.ts')
const editor = () => read('../src/lib/workbench/CodeEditor.svelte')

// --- colour maths ---------------------------------------------------------

function channel(value: number) {
  const ratio = value / 255
  return ratio <= 0.03928 ? ratio / 12.92 : ((ratio + 0.055) / 1.055) ** 2.4
}

function luminance(hex: string) {
  const parsed = hex.replace('#', '')
  const full = parsed.length === 3 ? [...parsed].map((c) => c + c).join('') : parsed
  const [r, g, b] = [0, 2, 4].map((offset) => parseInt(full.slice(offset, offset + 2), 16))
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

function contrast(foreground: string, background: string) {
  const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
  return (lighter + 0.05) / (darker + 0.05)
}

// Hue in degrees, and how saturated it is. Used to answer "is this red?" —
// which cannot be done by comparing against a list of reds, because the next
// wrong colour will be a red nobody listed.
function hueAndSaturation(hex: string) {
  const parsed = hex.replace('#', '')
  const full = parsed.length === 3 ? [...parsed].map((c) => c + c).join('') : parsed
  const [r, g, b] = [0, 2, 4].map((offset) => parseInt(full.slice(offset, offset + 2), 16) / 255)
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const delta = max - min
  if (delta === 0) return { hue: 0, saturation: 0 }
  let hue = 0
  if (max === r) hue = ((g - b) / delta) % 6
  else if (max === g) hue = (b - r) / delta + 2
  else hue = (r - g) / delta + 4
  hue = (hue * 60 + 360) % 360
  return { hue, saturation: delta / max }
}

// --- reading style.css ----------------------------------------------------

function blockFor(selector: string) {
  const source = css()
  const start = source.indexOf(selector + ' {')
  assert.ok(start >= 0, `no ${selector} block in style.css`)
  const end = source.indexOf('\n}', start)
  return source.slice(start, end)
}

function tokensIn(selector: string) {
  const found = new Map<string, string>()
  for (const match of blockFor(selector).matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g)) {
    found.set(match[1], match[2].trim())
  }
  return found
}

// The surface an editor is drawn on, resolved through inheritance: a variant
// that does not restate --surface inherits the base theme's.
function surfaceFor(selector: string, mode: 'light' | 'dark') {
  const own = tokensIn(selector).get('--surface')
  if (own) return own
  const base = mode === 'dark' ? tokensIn('html[data-theme="dark"]').get('--surface') : tokensIn(':root').get('--surface')
  assert.ok(base, 'no base --surface')
  return base as string
}

const BASE_THEMES: Array<[string, 'light' | 'dark']> = [
  [':root', 'light'],
  ['html[data-theme="dark"]', 'dark'],
]

// Variants that carry an upstream palette. Listed with the mode they are drawn
// in so the contrast check knows which base surface to fall back to.
const VARIANT_THEMES: Array<[string, 'light' | 'dark']> = [
  ['html[data-theme-variant="catppuccin-latte"]', 'light'],
  ['html[data-theme-variant="catppuccin-frappe"]', 'dark'],
  ['html[data-theme-variant="catppuccin-macchiato"]', 'dark'],
  ['html[data-theme-variant="catppuccin-mocha"]', 'dark'],
  ['html[data-theme-variant="nord"]', 'dark'],
  ['html[data-theme-variant="vscode-dark"]', 'dark'],
  ['html[data-theme-variant="vscode-light"]', 'light'],
]

// --- the style is theme-driven -------------------------------------------

// THE LOAD-BEARING ONE. A literal colour here would be right in one theme and
// wrong in the eleven others — the reason the old palette could not simply be
// swapped for a nicer fixed one.
test('the highlight style names no colour of its own', () => {
  const source = style()
  const declarations = [...source.matchAll(/color:\s*'([^']+)'/g)].map((match) => match[1])

  assert.ok(declarations.length > 0, 'the highlight style sets no colours at all')
  for (const declaration of declarations) {
    assert.match(
      declaration,
      /^var\(--syntax-[a-z-]+\)$/,
      `the highlight style hardcodes ${declaration} instead of using a theme token`,
    )
  }
})

test('every token the style uses is defined in both base themes', () => {
  const used = [...style().matchAll(/var\((--syntax-[a-z-]+)\)/g)].map((match) => match[1])
  assert.ok(used.length > 0)

  for (const [selector] of BASE_THEMES) {
    const defined = tokensIn(selector)
    for (const token of new Set(used)) {
      // An undefined custom property invalidates the whole `color:` declaration
      // and the text silently falls back to the body colour — which on screen
      // is indistinguishable from "this language has no grammar yet".
      assert.ok(defined.has(token), `${selector} does not define ${token}`)
    }
  }
})

test('the exported token list matches what the style actually uses', () => {
  const used = new Set([...style().matchAll(/var\((--syntax-[a-z-]+)\)/g)].map((match) => match[1]))
  const listed = new Set<string>(syntaxTokenNames)

  for (const token of used) assert.ok(listed.has(token), `${token} is used but not listed in syntaxTokenNames`)
  for (const token of listed) assert.ok(used.has(token), `${token} is listed but no rule uses it`)
})

// --- rule 1: legibility ---------------------------------------------------

test('every base-theme syntax colour is legible on its surface', () => {
  for (const [selector, mode] of BASE_THEMES) {
    const surface = surfaceFor(selector, mode)
    for (const [token, value] of tokensIn(selector)) {
      if (!token.startsWith('--syntax-')) continue
      const ratio = contrast(value, surface)
      // WCAG AA for body text. These are the themes the app ships on and the
      // ones a reader stares at all day.
      assert.ok(
        ratio >= 4.5,
        `${selector} ${token} (${value}) is ${ratio.toFixed(2)}:1 on ${surface}, want >= 4.5`,
      )
    }
  }
})

test('every upstream-palette syntax colour clears the large-text floor', () => {
  for (const [selector, mode] of VARIANT_THEMES) {
    const surface = surfaceFor(selector, mode)
    for (const [token, value] of tokensIn(selector)) {
      if (!token.startsWith('--syntax-')) continue
      const ratio = contrast(value, surface)
      // A LOWER bar than the base themes, deliberately. These reproduce
      // Catppuccin, Nord and VS Code as published; holding a third-party
      // palette to a standard its authors did not would mean shipping a theme
      // that is not the theme its name promises. Where an upstream value falls
      // below even this, it is adjusted and the CSS says so — see Nord's
      // comment colour.
      assert.ok(
        ratio >= 3,
        `${selector} ${token} (${value}) is ${ratio.toFixed(2)}:1 on ${surface}, want >= 3`,
      )
    }
  }
})

// --- rule 2: red means an error -------------------------------------------

// Detected by hue rather than by matching a list of known reds, because the
// next colour to get this wrong will be a red nobody thought to list. ~345°-20°
// with real saturation is the band that reads as an alarm.
function looksRed(value: string) {
  const { hue, saturation } = hueAndSaturation(value)
  return saturation > 0.35 && (hue >= 345 || hue <= 20)
}

test('in the base themes, only --syntax-invalid is red', () => {
  for (const [selector] of BASE_THEMES) {
    for (const [token, value] of tokensIn(selector)) {
      if (!token.startsWith('--syntax-') || token === '--syntax-invalid') continue
      assert.ok(
        !looksRed(value),
        `${selector} ${token} is ${value}, which reads as an error — red is reserved for --syntax-invalid`,
      )
    }
  }
})

test('--syntax-invalid actually is red, in both base themes', () => {
  for (const [selector] of BASE_THEMES) {
    const value = tokensIn(selector).get('--syntax-invalid')
    assert.ok(value, `${selector} defines no --syntax-invalid`)
    // Reserving red for errors only helps if the error colour claims it.
    assert.ok(looksRed(value as string), `${selector} --syntax-invalid is ${value}, which does not read as an error`)
  }
})

// The string colour is the one that was reported, so it gets its own check
// against the exact value it must not drift back toward.
test('string literals are not painted in the danger colour', () => {
  for (const [selector] of BASE_THEMES) {
    const tokens = tokensIn(selector)
    const string = tokens.get('--syntax-string')
    assert.ok(string)
    for (const dangerToken of ['--danger', '--danger-strong']) {
      const danger = tokens.get(dangerToken)
      if (!danger) continue
      assert.notEqual(string, danger, `${selector} paints strings in ${dangerToken}`)
    }
  }
})

// --- the wiring -----------------------------------------------------------

// Prec.highest is not decoration. CodeMirror resolves a tag against the
// highest-precedence highlighter that has a rule for it, and `basicSetup`
// carries its own defaultHighlightStyle; appended at normal precedence this
// style would sit below that one and paint nothing. The change would look
// applied, in the diff and in the extension list, and do nothing on screen.
test('the editor installs this style above basicSetup', () => {
  const source = editor()

  assert.match(
    source,
    /Prec\.highest\(syntaxHighlighting\(liteApiHighlightStyle/,
    'the highlight style is not installed at highest precedence',
  )
  // Checked on the IMPORT rather than on the word, because the file's own
  // comment explains what it replaced and names it — a bare word search would
  // fail for the code explaining itself.
  assert.ok(
    !/^\s*import[^\n]*defaultHighlightStyle/m.test(source),
    "CodeMirror's defaultHighlightStyle is imported again in CodeEditor.svelte",
  )
})

// The precedence claim, proven rather than asserted.
//
// Everything above reads source. If Prec.highest were wrong, every one of those
// tests would still pass and the editor would look exactly as it did before —
// the failure mode this whole file exists to prevent, reproduced one level up.
//
// So this builds real EditorStates and asks CodeMirror which class a string
// token actually resolves to. It needs no DOM: highlighting is resolved from
// state, and the view is only what paints it.
//
// The measured answer, and the reason the call is not decoration:
//
//   with Prec.highest : ͼp   <- a different class; this style won
//   without Prec      : ͼe   <- identical to basicSetup alone
//   basicSetup only   : ͼe
test('the style only wins at highest precedence', async () => {
  const { EditorState, Prec } = await import('@codemirror/state')
  const { syntaxHighlighting, highlightingFor } = await import('@codemirror/language')
  const { json } = await import('@codemirror/lang-json')
  const { basicSetup } = await import('codemirror')
  const { tags } = await import('@lezer/highlight')
  const { liteApiHighlightStyle } = await import('../src/lib/workbench/syntaxHighlight.ts')

  const stringClassWith = (extensions: unknown[]) =>
    highlightingFor(
      EditorState.create({ doc: '{"key":"value"}', extensions: extensions as never }),
      [tags.string],
    )

  const baseline = stringClassWith([basicSetup, json()])
  const appended = stringClassWith([basicSetup, json(), syntaxHighlighting(liteApiHighlightStyle, { fallback: true })])
  const raised = stringClassWith([
    basicSetup,
    json(),
    Prec.highest(syntaxHighlighting(liteApiHighlightStyle, { fallback: true })),
  ])

  assert.ok(baseline, 'basicSetup assigns no class to a string — the premise is gone')
  assert.equal(
    appended,
    baseline,
    'appended at normal precedence this style now wins — Prec.highest may no longer be needed, but check before removing it',
  )
  assert.notEqual(
    raised,
    baseline,
    "Prec.highest no longer overrides basicSetup's highlighter, so the editor is painting CodeMirror's default palette",
  )
})
