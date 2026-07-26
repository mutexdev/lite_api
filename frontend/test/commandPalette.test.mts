// US-055 — tests for the command palette's matching and navigation.
//
// The assertions that matter are about RANKING and about the selection index
// agreeing with what is rendered. Both fail silently: a bad rank runs the wrong
// command on Enter, and a selection index that refers to the ranked list while
// the UI renders grouped rows highlights one row and runs another.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  filterCommands,
  moveSelection,
  groupBySection,
  flattenGroups,
  type PaletteCommand
} from '../src/lib/commandPalette.ts'

const commands: PaletteCommand[] = [
  { id: 'close-tab', title: 'Close Tab', section: 'Tabs' },
  { id: 'close-all-tabs', title: 'Close All Tabs', section: 'Tabs' },
  { id: 'reopen-tab', title: 'Reopen Last Closed Tab', section: 'Tabs' },
  { id: 'new-request', title: 'New Request', section: 'Collections', keywords: ['create', 'add'] },
  { id: 'import', title: 'Import Collection', section: 'Collections' },
  { id: 'preferences', title: 'Open Preferences', section: 'App', keywords: ['settings', 'config'] },
  { id: 'terminal', title: 'Open in Terminal', section: 'Developer' },
  { id: 'zoom-in', title: 'Zoom In', section: 'View' }
]

test('an empty query returns every command in declared order', () => {
  const matches = filterCommands(commands, '')
  assert.equal(matches.length, commands.length)
  assert.deepEqual(
    matches.map((match) => match.command.id),
    commands.map((command) => command.id)
  )
})

test('whitespace-only queries behave as empty', () => {
  assert.equal(filterCommands(commands, '   ').length, commands.length)
})

// The rank that matters most. Typing the exact title of a command must select
// that command — not the longer one that also contains those words. Getting
// this wrong closes every tab when the user asked to close one.
test('an exact title beats a longer title containing it', () => {
  const matches = filterCommands(commands, 'close tab')
  assert.equal(matches[0].command.id, 'close-tab')
  assert.ok(
    matches.findIndex((m) => m.command.id === 'close-all-tabs') > 0,
    'Close All Tabs must rank below the exact match'
  )
})

test('a prefix beats a mid-string substring', () => {
  const matches = filterCommands(commands, 'open')
  // "Open Preferences" and "Open in Terminal" start with it; "Reopen Last
  // Closed Tab" only contains it and must rank below both.
  const reopenIndex = matches.findIndex((m) => m.command.id === 'reopen-tab')
  const prefixIndexes = matches
    .map((m, i) => (m.command.title.toLowerCase().startsWith('open') ? i : -1))
    .filter((i) => i >= 0)
  assert.ok(prefixIndexes.length >= 2, 'expected both Open commands to match')
  for (const index of prefixIndexes) {
    assert.ok(index < reopenIndex, 'a prefix match must outrank a mid-word substring')
  }
})

test('a word-start match beats a mid-word one', () => {
  const matches = filterCommands(commands, 'tab')
  // Every Tabs command matches at a word start; nothing should outrank them
  // by matching inside another word.
  assert.ok(matches.length >= 3)
  assert.equal(matches[0].command.section, 'Tabs')
})

test('keywords match but always rank below any title match', () => {
  const matches = filterCommands(commands, 'settings')
  assert.equal(matches.length, 1)
  assert.equal(matches[0].command.id, 'preferences')
  // The match was not in the visible title, so highlighting title characters
  // would misrepresent why it matched.
  assert.deepEqual(matches[0].highlights, [])
})

test('a title match outranks a keyword match for the same query', () => {
  const withCollision: PaletteCommand[] = [
    { id: 'keyword-only', title: 'Something Else', section: 'X', keywords: ['zoom'] },
    { id: 'zoom-in', title: 'Zoom In', section: 'View' }
  ]
  const matches = filterCommands(withCollision, 'zoom')
  assert.equal(matches[0].command.id, 'zoom-in')
})

test('fuzzy subsequence matching finds commands from initials', () => {
  const matches = filterCommands(commands, 'ipc')
  assert.ok(
    matches.some((match) => match.command.id === 'import'),
    'Import Collection should match its initials'
  )
})

test('a query matching nothing returns nothing', () => {
  assert.deepEqual(filterCommands(commands, 'qqqqzzz'), [])
})

test('matching is case insensitive', () => {
  const lower = filterCommands(commands, 'zoom in')
  const upper = filterCommands(commands, 'ZOOM IN')
  assert.deepEqual(
    lower.map((m) => m.command.id),
    upper.map((m) => m.command.id)
  )
})

test('highlights point at the characters that matched', () => {
  const matches = filterCommands(commands, 'zoom')
  const zoom = matches.find((match) => match.command.id === 'zoom-in')
  assert.ok(zoom)
  assert.deepEqual(zoom.highlights, [0, 1, 2, 3])
})

// Ties must not be re-sorted alphabetically: the declared order puts the most
// reached-for commands near the top, and alphabetising ties discards that.
test('ties preserve the declared order', () => {
  const tied: PaletteCommand[] = [
    { id: 'zebra', title: 'Alpha Zebra', section: 'S' },
    { id: 'apple', title: 'Alpha Apple', section: 'S' }
  ]
  const matches = filterCommands(tied, 'alpha')
  assert.deepEqual(
    matches.map((m) => m.command.id),
    ['zebra', 'apple']
  )
})

test('moveSelection wraps at both ends', () => {
  assert.equal(moveSelection(0, -1, 5), 4, 'Up on the first item reaches the last')
  assert.equal(moveSelection(4, 1, 5), 0, 'Down on the last item reaches the first')
  assert.equal(moveSelection(2, 1, 5), 3)
  assert.equal(moveSelection(2, -1, 5), 1)
})

test('moveSelection never returns a negative index for an empty list', () => {
  assert.equal(moveSelection(0, -1, 0), 0)
  assert.equal(moveSelection(3, 1, 0), 0)
})

test('groupBySection keeps rank order within and between groups', () => {
  const matches = filterCommands(commands, 'open')
  const groups = groupBySection(matches)
  // The section containing the best match must come first.
  assert.equal(groups[0].matches[0].command.id, matches[0].command.id)
})

// The bug this guards is invisible until someone runs the wrong command: the
// selection index counts positions in the RANKED list while the UI renders
// grouped rows, so the highlighted row and the row Enter runs diverge as soon
// as sections interleave.
test('flattenGroups reproduces exactly what is rendered, in order', () => {
  const interleaved: PaletteCommand[] = [
    { id: 'a1', title: 'Alpha One', section: 'A' },
    { id: 'b1', title: 'Alpha Two', section: 'B' },
    { id: 'a2', title: 'Alpha Three', section: 'A' },
    { id: 'b2', title: 'Alpha Four', section: 'B' }
  ]
  const matches = filterCommands(interleaved, 'alpha')
  const rendered = flattenGroups(groupBySection(matches))

  assert.equal(rendered.length, matches.length, 'grouping dropped or duplicated a row')
  // Grouped rendering reorders relative to the ranked list, which is precisely
  // why the selection index must be taken from the flattened order.
  assert.deepEqual(
    rendered.map((m) => m.command.id),
    ['a1', 'a2', 'b1', 'b2']
  )
})

test('flattenGroups round-trips an empty result', () => {
  assert.deepEqual(flattenGroups(groupBySection([])), [])
})

test('disabled commands are still listed so their reason can be shown', () => {
  const withDisabled: PaletteCommand[] = [
    { id: 'send', title: 'Send Request', section: 'Request', enabled: false, disabledReason: 'no request is open' }
  ]
  const matches = filterCommands(withDisabled, 'send')
  assert.equal(matches.length, 1)
  assert.equal(matches[0].command.enabled, false)
  assert.equal(matches[0].command.disabledReason, 'no request is open')
})

// The two tests below exist because a negative control exposed a gap: removing
// the exact-title and prefix tiers entirely left every other test passing. The
// earlier cases were all distinguishable by the word-start tier alone, so they
// never proved the higher tiers did anything. These are the cases where the
// tiers are the only thing deciding the order — and the first row is what Enter
// runs.

test('an exact title beats an equally word-start-matching longer title', () => {
  const tied: PaletteCommand[] = [
    // Declared first, so if the exact tier did nothing this would win on the
    // tie-break and Enter would open the wrong thing.
    { id: 'new-tab', title: 'New Tab', section: 'Tabs' },
    { id: 'tab', title: 'Tab', section: 'Tabs' }
  ]
  const matches = filterCommands(tied, 'tab')
  assert.equal(matches[0].command.id, 'tab', 'an exact title must outrank a word-start match')
})

test('a title prefix beats a word-start match later in the title', () => {
  const tied: PaletteCommand[] = [
    { id: 'close-tab', title: 'Close Tab', section: 'Tabs' },
    { id: 'tab-overflow', title: 'Tab Overflow', section: 'Tabs' }
  ]
  const matches = filterCommands(tied, 'tab')
  assert.equal(matches[0].command.id, 'tab-overflow', 'a prefix must outrank a mid-title word start')
})
