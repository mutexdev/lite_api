// Opening a folder in the sidebar.
//
// The tree had exactly one way to expand or collapse a branch: a 20px button
// drawn as the character "▾" in the muted rail colour, whose hover rule set the
// same transparent background the button already had. Clicking a folder's NAME
// opens that folder's settings pane and does not expand it, so if you could not
// hit the speck, you could not reach the requests inside.
//
// Two changes, and these tests pin both:
//   - the chevron is a real control (drawn glyph, its own hit area, a hover
//     state that is actually visible),
//   - double-clicking a folder or collection row toggles it, which is the
//     gesture people already try when a row does not respond to a single click.
//
// Asserted against the source rather than a rendered tree because the repo has
// no component-rendering harness — see brandMark.test.mts and nativeMenu.test.mts,
// which read .svelte files the same way. That makes these weak about pixels and
// strong about the wiring, which is what regressed.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const app = () => read('../src/App.svelte')
const chevron = () => read('../src/lib/sidebar/TreeChevron.svelte')
const styles = () => read('../src/style.css')

test('both tree rows render the shared chevron component', () => {
  const markup = app()

  assert.ok(
    /import TreeChevron from ['"]\.\/lib\/sidebar\/TreeChevron\.svelte['"]/.test(markup),
    'App.svelte does not import TreeChevron',
  )
  // One for the collection header, one for the folder row. Two hand-rolled
  // buttons is how the old pair drifted apart in the first place.
  const uses = markup.match(/<TreeChevron\b/g) ?? []
  assert.equal(uses.length, 2, `expected 2 <TreeChevron /> uses, found ${uses.length}`)

  assert.ok(
    !/class="tree-chevron"/.test(markup),
    'a hand-rolled .tree-chevron button is back in App.svelte',
  )
})

// THE GLYPH IS DRAWN, NOT TYPED. "▾" depends on whichever font supplies the
// arrow, which is why it sat off-baseline and at a different weight from
// everything around it.
test('the chevron is an inline svg rather than a text arrow', () => {
  const source = chevron()

  assert.ok(/<svg\b/.test(source), 'TreeChevron does not contain an inline <svg>')
  assert.ok(/<path\b/.test(source), 'TreeChevron draws no path')

  // The glyph check runs against the TEMPLATE only. Run against the whole file
  // it matches the component's own doc comment, which names the character it
  // replaced — the test would then fail for explaining itself.
  const template = source.slice(source.indexOf('</script>'))
  assert.ok(
    !/[▾▸▶►▼]/.test(template),
    'TreeChevron still renders a text arrow glyph',
  )
})

test('the chevron reports and labels its own state', () => {
  const markup = chevron()

  assert.ok(/aria-expanded=\{expanded\}/.test(markup), 'chevron does not expose aria-expanded')
  // The label has to name the thing AND the direction, because "chevron" alone
  // tells a screen-reader user nothing about what it opens.
  assert.ok(/aria-label=/.test(markup), 'chevron has no aria-label')
  assert.ok(/Collapse/.test(markup) && /Expand/.test(markup), 'chevron label does not switch with state')
  assert.ok(
    /aria-hidden="true"/.test(markup),
    'the decorative svg is not hidden from assistive technology',
  )
})

// The chevron sits inside a row whose own click does something different
// (opening settings). Without stopPropagation, toggling a folder would also
// navigate, which is the sort of thing that reads as the app ignoring the click.
test('toggling does not also trigger the row underneath', () => {
  assert.ok(
    /event\.stopPropagation\(\)/.test(chevron()),
    'TreeChevron does not stop its click from reaching the row',
  )
})

test('double-clicking a folder or collection row toggles it', () => {
  const markup = app()

  assert.ok(
    /ondblclick=\{\(\) => toggleSidebarFolder\(collection\.id, group\.folder\)\}/.test(markup),
    'a folder row does not toggle on double-click',
  )
  assert.ok(
    /ondblclick=\{\(\) => toggleSidebarCollection\(collection\.id\)\}/.test(markup),
    'a collection row does not toggle on double-click',
  )
})

// Single click must KEEP opening settings. The double-click is additive; if it
// replaced the single click, every visit to a folder's settings would also
// collapse the folder out from under the user.
test('single click still opens settings rather than toggling', () => {
  const markup = app()

  assert.ok(
    /onclick=\{\(\) => \{ markSidebarRowFocused\(`f:\$\{collection\.id\}:\$\{group\.folder\}`\); selectFolderSettings\(collection, group\.folder\) \}\}/.test(markup),
    'the folder row no longer opens folder settings on a single click',
  )
  assert.ok(
    /onclick=\{\(\) => \{ markSidebarRowFocused\(`c:\$\{collection\.id\}`\); selectCollection\(collection\.id\) \}\}/.test(markup),
    'the collection row no longer selects the collection on a single click',
  )
})

test('the chevron has a hover state that changes something', () => {
  const css = styles()
  const rule = css.match(/\.tree-chevron:hover\s*\{[^}]*\}/)?.[0]

  assert.ok(rule, '.tree-chevron:hover rule is missing')
  // The shipped rule was `background: transparent; color: var(--rail-text)` on a
  // button that was ALREADY transparent and already that colour — a hover state
  // that repainted the resting state.
  assert.ok(
    !/background:\s*transparent/.test(rule),
    'the hover state paints the same transparent background it already had',
  )
  assert.ok(
    /background:\s*var\(--rail-hover\)/.test(rule),
    'the hover state does not raise a visible background',
  )
})

test('the chevron hit area is at least as wide as its grid track', () => {
  const css = styles()

  const width = css.match(/\.tree-chevron\s*\{[^}]*?\bwidth:\s*(\d+)px/)?.[1]
  assert.ok(width, '.tree-chevron declares no width')

  const track = css.match(/\.folder-row-shell\s*\{[^}]*?grid-template-columns:\s*(\d+)px/)?.[1]
  assert.ok(track, '.folder-row-shell declares no first grid track')

  // A track narrower than the button squashes the control back to the size that
  // made it unhittable in the first place.
  assert.equal(
    Number(track),
    Number(width),
    `folder row first track is ${track}px but the chevron is ${width}px`,
  )
})

test('the chevron still rotates to show state', () => {
  const css = styles()

  assert.ok(
    /\.tree-chevron\.collapsed\s*\{[^}]*transform:\s*rotate\(/.test(css),
    'the collapsed chevron no longer rotates, so state is carried by colour alone',
  )
})
