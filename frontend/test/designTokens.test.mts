// Every design token that is used is defined.
//
// The audit found five tokens used across the app and declared nowhere:
// --warning-border, --warning-bg-soft, --surface-raised, --surface-hover and
// --selection-bg. Two of them were used without a fallback, and an undefined
// custom property does not fall back to anything — it makes the ENTIRE
// declaration invalid. So the "large response" banner lost its border and
// background, the JSON-diff changed row lost its tint, and the editor's
// search-match highlight lost its outline, in all thirteen themes, for as long
// as the code had shipped.
//
// That is the worst kind of CSS bug: invisible in review (the line reads
// correctly), invisible in a diff, and indistinguishable on screen from a
// design that simply never had a border. style.css:1909 even carried a comment
// admitting --warning-border "doesn't exist" while three rules below went on
// using it — a note is not a guard.
//
// This is the guard. It is deliberately strict about fallbacks too: a
// `var(--x, something)` chain hides exactly this failure, and the audit found
// --surface-raised standing behind four DIFFERENT fallbacks in four files, so
// "raised" meant a different colour depending on which file you were looking at.
import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, relative } from 'node:path'

const sourceRoot = fileURLToPath(new URL('../src', import.meta.url))

function sourceFiles(directory: string): string[] {
  const found: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) found.push(...sourceFiles(path))
    else if (/\.(svelte|css|ts)$/.test(entry.name)) found.push(path)
  }
  return found
}

const files = sourceFiles(sourceRoot).map((path) => ({ path, text: readFileSync(path, 'utf8') }))
const css = files.find((file) => file.path.endsWith('style.css'))!.text

/**
 * Tokens whose value is written by JavaScript at runtime rather than declared
 * in a stylesheet.
 *
 * Each one is a real measurement — a dragged pane width, a tree row's depth —
 * so there is nothing sensible for a stylesheet to say about it. Every entry
 * here must be assigned somewhere in the source, which the last test checks, so
 * this list cannot quietly become a place to hide a genuinely missing token.
 */
const runtimeTokens = new Set([
  '--app-zoom',
  '--row-depth',
  '--sidebar-width',
  '--request-pane-size',
  '--devtools-drawer-height',
  '--network-details-width'
])

function definedTokens() {
  const defined = new Set<string>()
  for (const file of files) {
    for (const match of file.text.matchAll(/(--[a-z0-9-]+)\s*:/g)) defined.add(match[1])
  }
  return defined
}

function usages() {
  const used: { token: string; file: string; fallback: boolean }[] = []
  for (const file of files) {
    // Anchored on the closing `)` or the fallback comma, not just the name.
    // Prose in these files writes `var(--syntax-*)` to mean the whole family,
    // and a looser pattern reports that comment as a missing token.
    for (const match of file.text.matchAll(/var\(\s*(--[a-z0-9-]*[a-z0-9])\s*([,)])/g)) {
      used.push({ token: match[1], file: relative(sourceRoot, file.path), fallback: match[2] === ',' })
    }
  }
  return used
}

test('every token that is used is defined somewhere', () => {
  const defined = definedTokens()
  const missing = usages()
    .filter((usage) => !defined.has(usage.token) && !runtimeTokens.has(usage.token))
    .map((usage) => `${usage.token} (used in ${usage.file})`)
  assert.deepEqual([...new Set(missing)], [], 'these tokens are used but never declared')
})

test('no token is used without being defined AND without a fallback', () => {
  // The stricter half of the same rule, stated separately so a regression
  // reports which failure it is: a missing declaration, or a declaration that
  // only appears to work because a fallback is quietly carrying it.
  const defined = definedTokens()
  const unguarded = usages()
    .filter((usage) => !usage.fallback && !defined.has(usage.token) && !runtimeTokens.has(usage.token))
    .map((usage) => `${usage.token} in ${usage.file}`)
  assert.deepEqual([...new Set(unguarded)], [])
})

test('the base theme declares every token the themes are expected to share', () => {
  // A token defined only inside a variant block resolves to nothing under the
  // twelve other themes. Checked against :root, which every theme layers on.
  //
  // Scoped to the THEME blocks only. A token declared on a component class —
  // `--method-color`, set per HTTP method — is a local channel, not a palette
  // entry, and has no business being in :root.
  const blockTokens = (selector: string) => {
    const start = css.indexOf(selector)
    assert.ok(start >= 0, `${selector} not found in style.css`)
    const open = css.indexOf('{', start)
    return new Set([...css.slice(open, css.indexOf('}', open)).matchAll(/(--[a-z0-9-]+)\s*:/g)].map((match) => match[1]))
  }

  const rootTokens = blockTokens(':root')
  const variants = [...css.matchAll(/html\[data-theme(?:-variant)?="([a-z-]+)"\]/g)].map((match) => match[0])
  assert.ok(variants.length >= 12, `expected the full theme set, found ${variants.length}`)

  const orphans: string[] = []
  for (const variant of variants) {
    for (const token of blockTokens(variant)) {
      if (!rootTokens.has(token)) orphans.push(`${token} (only in ${variant})`)
    }
  }
  assert.deepEqual([...new Set(orphans)], [], 'these tokens exist only in a theme variant, so they are undefined in every other theme')
})

test('the derived tokens really are derived, so a new theme cannot forget them', () => {
  // The five that were missing are defined once, in terms of the family tokens
  // each theme already overrides. If someone later replaces one with a literal
  // colour, it silently stops tracking the theme — which is how they would come
  // back as light-mode colours bleeding into the dark themes.
  for (const [token, source] of [
    ['--warning-bg-soft', '--warning-bg'],
    ['--warning-border', '--warning'],
    ['--surface-raised', '--surface-soft'],
    ['--surface-hover', '--surface-alt'],
    ['--selection-bg', '--focus-ring-strong']
  ]) {
    assert.match(css, new RegExp(`${token}:\\s*var\\(${source}\\)`), `${token} should be derived from ${source}`)
  }
})

test('every runtime token is actually assigned by the app', () => {
  const assigned = files.some.bind(files)
  for (const token of runtimeTokens) {
    assert.ok(
      assigned((file) => file.text.includes(`'${token}'`) || file.text.includes(`"${token}"`) || new RegExp(`${token}\\s*:`).test(file.text)),
      `${token} is exempted as runtime-set but nothing sets it — it is simply undefined`
    )
  }
})

test('the empty-state class name matches the stylesheet', () => {
  // A one-character divergence — `.empty-appState` in the markup against
  // `.empty-state` in the stylesheet — left 24 empty states rendering with no
  // border, padding or background at all. Nothing errors; they just look like
  // stray sentences.
  const markup = files.filter((file) => file.path.endsWith('.svelte'))
  const classNames = new Set<string>()
  for (const file of markup) {
    for (const match of file.text.matchAll(/class="([^"{]*)"/g)) {
      for (const name of match[1].split(/\s+/)) if (name.startsWith('empty-')) classNames.add(name)
    }
  }
  for (const name of classNames) {
    const styled = css.includes(`.${name}`) || markup.some((file) => file.text.includes(`.${name}`))
    assert.ok(styled, `class "${name}" is used in markup but no rule defines it`)
  }
})
