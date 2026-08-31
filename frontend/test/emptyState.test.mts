// One wording for "there is nothing here", and a guard that keeps it that way.
//
// The audit counted six sentences for one state: "No results found", "No
// commands match.", "No matching requests", "No requests", "No matches", "No
// matching headers". Four nouns, one stray period, and — the part that was an
// actual bug rather than drift — "No matching requests" shown to a user who had
// never searched, because the sidebar could not tell a filter that excluded
// everything from a workspace that had never held anything.
//
// The uniform-UX system's own rule applies here: a comment saying "keep these
// in step" has already been proved not to keep anything in step. So the second
// half of this file is a source scan. It is scoped to the files this wave
// converted, deliberately — App.svelte and ResponseInspector still carry their
// old strings and belong to other owners this week, and a test that fails in
// somebody else's tree is a test that gets deleted rather than satisfied.
// Widening the scope is the last step of the campaign, not the first.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import { emptyStateMessage, isFilteredEmpty } from '../src/lib/sidebar/emptyState.ts'

test('a query that matched nothing is quoted back to the user', () => {
  assert.equal(
    emptyStateMessage({ query: 'login', noun: 'requests' }),
    'No results for “login”'
  )
})

// The query is trimmed, because a filter box holding "  " is a filter box the
// user has effectively cleared — and `No results for “  ”` is a sentence that
// makes the app look broken.
test('whitespace is not a search', () => {
  assert.equal(emptyStateMessage({ query: '   ', noun: 'collections' }), 'No collections yet')
  assert.equal(emptyStateMessage({ query: '  login  ', noun: 'requests' }), 'No results for “login”')
  assert.equal(isFilteredEmpty('   '), false)
  assert.equal(isFilteredEmpty(' x '), true)
})

// "YET" IS THE WHOLE POINT of the second branch. It says the surface is empty
// by history rather than by failure, which is the difference between welcoming
// a new user and telling them a search they never ran came back empty.
test('a surface that never had anything says so with the caller’s own noun', () => {
  assert.equal(emptyStateMessage({ query: '', noun: 'collections' }), 'No collections yet')
  assert.equal(emptyStateMessage({ query: '', noun: 'commands' }), 'No commands yet')
})

test('neither sentence ends in a period', () => {
  for (const query of ['', 'login']) {
    assert.ok(!emptyStateMessage({ query, noun: 'requests' }).endsWith('.'))
  }
})

// ── The guard ───────────────────────────────────────────────────────────────

const sourceRoot = fileURLToPath(new URL('../src', import.meta.url))

/** The surfaces converted in this pass; every one of them must use the rule. */
const CONVERTED = [
  'lib/sidebar',
  'lib/modals/search',
  'lib/SidebarSearch.svelte'
]

function filesUnder(path: string): string[] {
  const full = join(sourceRoot, path)
  if (full.endsWith('.svelte') || full.endsWith('.ts')) return [full]
  return readdirSync(full, { withFileTypes: true })
    .flatMap((entry) => filesUnder(join(path, entry.name)))
}

/**
 * Comments stripped before scanning.
 *
 * Not a convenience — a correctness requirement. Every file in this set
 * explains WHY the old sentence went by quoting it, so a scan over raw source
 * would report the explanation as the violation and the only way to pass would
 * be to delete the reasoning. The check is about what ships to the screen.
 */
function withoutComments(text: string): string {
  return text
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1')
}

const converted = CONVERTED.flatMap(filesUnder).map((path) => ({
  path: path.slice(sourceRoot.length + 1),
  text: withoutComments(readFileSync(path, 'utf8'))
}))

test('the converted surfaces contain no hand-written empty-result sentence', () => {
  // The shapes that were actually in use: "No results found", "No commands
  // match.", "No matching headers", "No matches".
  const banned = /No (?:results found|matches|matching \w+|\w+ match)\b/i

  const offenders = converted
    .filter((file) => banned.test(file.text))
    .map((file) => file.path)

  assert.deepEqual(offenders, [], 'these files hand-roll an empty-result string instead of calling emptyStateMessage')
})

test('both search modals render their empty state through the shared rule', () => {
  for (const name of ['lib/modals/search/GlobalSearchModal.svelte', 'lib/modals/search/CommandPaletteModal.svelte']) {
    const file = converted.find((candidate) => candidate.path === name)
    assert.ok(file, `${name} was not scanned`)
    assert.match(file.text, /emptyStateMessage\(\{/, `${name} does not call emptyStateMessage`)
  }
})
