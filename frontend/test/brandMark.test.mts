// The sidebar brand mark is the application icon, not a text placeholder.
//
// build/appicon.svg has been the real mark all along, but build/ is Wails'
// packaging directory and is never part of the Vite source root — so the
// sidebar rendered the literal string "LA" while the actual icon shipped in the
// .app bundle and the installer. The two disagreed everywhere except the dock.
//
// These assert on the source rather than a rendered tree because the repo has
// no component-rendering harness (see nativeMenu.test.mts and
// proxyModeVocabularies.test.mts, which read .svelte files the same way). That
// makes them weak about layout and strong about the one thing that regressed:
// which of the two marks is actually wired up.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

test('the sidebar renders the brand mark component, not a text placeholder', () => {
  const markup = read('../src/lib/SidebarHeader.svelte')

  assert.ok(
    /<BrandMark\b/.test(markup),
    'SidebarHeader no longer renders <BrandMark />',
  )
  assert.ok(
    /import BrandMark from ['"]\.\/BrandMark\.svelte['"]/.test(markup),
    'SidebarHeader does not import BrandMark from ./BrandMark.svelte',
  )
  assert.ok(
    !/>\s*LA\s*</.test(markup),
    'the "LA" text placeholder is back in SidebarHeader',
  )
})

test('the brand mark is inline SVG rather than a raster import', () => {
  const markup = read('../src/lib/BrandMark.svelte')

  assert.ok(/<svg\b/.test(markup), 'BrandMark does not contain an inline <svg>')

  // Checked against the markup, not the whole file: an earlier version of this
  // test searched for ".png" anywhere and matched the source comment explaining
  // why the PNG is excluded, so the test failed on a file that was correct.
  const template = markup.slice(markup.indexOf('</script>'))
  assert.ok(
    !/<img\b/.test(template),
    'BrandMark renders an <img>; the mark must be inline SVG',
  )
  assert.ok(
    !/^\s*import\s.+\.(png|jpe?g|webp|gif)['"]/m.test(markup),
    'BrandMark imports a raster asset; appicon.png is 872 KB and must stay out of the bundle',
  )
})

// A mark with no accessible name is announced as "image" or skipped entirely,
// and this one sits at the top of the sidebar where it reads as the app's
// identity. aria-hidden would also be defensible — but only alongside the
// visible "LiteAPI" heading next to it, which is why the choice is pinned here
// rather than left to whoever edits the file next.
test('the brand mark carries an accessible name', () => {
  const markup = read('../src/lib/BrandMark.svelte')

  assert.ok(/role="img"/.test(markup), 'BrandMark is missing role="img"')

  // Accepts both a literal aria-label="..." and Svelte's aria-label={binding}.
  // The first version of this test allowed only the literal form and so failed
  // against a component that takes its label as a prop — which is the better
  // design, since the label has to change where adjacent text already says
  // "LiteAPI".
  assert.ok(
    /aria-label=("[^"]+"|\{[^}]+\})/.test(markup),
    'BrandMark is missing an aria-label',
  )
})

// THE MARK MUST NOT FOLLOW THE THEME ACCENT. It is a logo: fixed colours across
// all 12 theme variants are the point, so it stays recognisable in Nord and
// Catppuccin as well as the defaults. The old placeholder painted itself with
// var(--accent), and that rule has to be gone from .brand-mark or it will show
// through behind the icon's own rounded background.
test('the brand mark wrapper no longer paints the accent behind the icon', () => {
  const css = read('../src/style.css')
  const start = css.indexOf('.brand-mark {')
  assert.ok(start > 0, '.brand-mark rule is gone or was renamed')
  const rule = css.slice(start, css.indexOf('}', start))

  assert.ok(
    !/background:/.test(rule),
    '.brand-mark still paints a background behind the icon',
  )
  assert.ok(
    !/var\(--accent\)/.test(rule),
    '.brand-mark still references the theme accent',
  )
})
