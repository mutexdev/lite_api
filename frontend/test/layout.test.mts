import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  DEFAULT_RESPONSE_SPLIT,
  DEFAULT_SIDEBAR_WIDTH,
  clampResponseSplit,
  clampSidebarWidth,
  readWorkbenchLayout,
  splitFractionAt,
  workbenchStorageKey,
  writeWorkbenchLayout,
  type LayoutStorage,
} from '../src/lib/workbench/layout.ts'

function memoryStorage(seed: Record<string, string> = {}) {
  const map = new Map(Object.entries(seed))
  return {
    map,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => {
      map.set(key, value)
    },
  } satisfies LayoutStorage & { map: Map<string, string> }
}

const current = { sidebarWidth: DEFAULT_SIDEBAR_WIDTH, responseSplit: DEFAULT_RESPONSE_SPLIT }

test('the sidebar width is held between its bounds and rounded to a pixel', () => {
  assert.equal(clampSidebarWidth(300.4), 300)
  assert.equal(clampSidebarWidth(10), 220)
  assert.equal(clampSidebarWidth(9000), 420)
})

// A NaN width reaches a CSS custom property, where it is dropped and the shell
// renders with no sidebar at all. Math.max returns NaN for a NaN argument, so
// the clamp does not stop it on its own.
test('a non-finite width clamps to the minimum rather than propagating', () => {
  assert.equal(clampSidebarWidth(Number.NaN), 220)
  assert.equal(clampSidebarWidth(Number.POSITIVE_INFINITY), 220)
})

test('the response split is held between its bounds', () => {
  assert.equal(clampResponseSplit(0.5), 0.5)
  assert.equal(clampResponseSplit(0), 0.3)
  assert.equal(clampResponseSplit(1), 0.7)
  assert.equal(clampResponseSplit(Number.NaN), 0.3)
})

// The split is a fraction. Rounding it the way the width is rounded would
// collapse every position to 0 or 1 and make the divider unusable.
test('the response split is not rounded to an integer', () => {
  assert.equal(clampResponseSplit(0.52), 0.52)
  assert.notEqual(clampResponseSplit(0.61), Math.round(0.61))
})

test('a storage key is namespaced by the window scope', () => {
  assert.equal(workbenchStorageKey('win-1', 'sidebar-width'), 'liteapi.workbench.v3.win-1.sidebar-width')
  assert.notEqual(
    workbenchStorageKey('win-1', 'sidebar-width'),
    workbenchStorageKey('win-2', 'sidebar-width'),
  )
})

// The scope arrives from an async GetWebStorageScope() call, so it is "" for the
// first frames of startup. An unscoped fallback key would be shared by every
// workspace window, and their layouts would overwrite each other on every drag.
test('an unknown scope produces no key, and neither read nor write touches storage', () => {
  assert.equal(workbenchStorageKey('', 'sidebar-width'), '')

  const storage = memoryStorage({ 'liteapi.workbench.v3..sidebar-width': '400' })
  assert.equal(writeWorkbenchLayout('', { sidebarWidth: 400, responseSplit: 0.4 }, storage), false)
  assert.equal(storage.map.size, 1, 'writing under an unknown scope added an entry')
  assert.deepEqual(readWorkbenchLayout('', current, storage), current)
})

test('a saved layout round-trips', () => {
  const storage = memoryStorage()
  assert.equal(writeWorkbenchLayout('win', { sidebarWidth: 380, responseSplit: 0.44 }, storage), true)
  assert.deepEqual(readWorkbenchLayout('win', current, storage), { sidebarWidth: 380, responseSplit: 0.44 })
})

// The stored values are plain text and can be hand-edited or corrupted. They
// reach a CSS custom property and a flex-basis without further validation.
test('an out-of-range stored value is clamped on the way back in', () => {
  const storage = memoryStorage({
    'liteapi.workbench.v3.win.sidebar-width': '99999',
    'liteapi.workbench.v3.win.response-split': '-4',
  })
  assert.deepEqual(readWorkbenchLayout('win', current, storage), { sidebarWidth: 420, responseSplit: 0.3 })
})

// Number(null) and Number('') are both 0, which is finite. Treating an absent
// entry as a number would clamp it to the MINIMUM — so a window that had never
// saved a layout would open with the narrowest possible sidebar instead of the
// default one.
test('an absent or empty entry keeps the current value instead of reading as zero', () => {
  for (const seed of [{}, { 'liteapi.workbench.v3.win.sidebar-width': '' }]) {
    const got = readWorkbenchLayout('win', current, memoryStorage(seed))
    assert.equal(got.sidebarWidth, DEFAULT_SIDEBAR_WIDTH, JSON.stringify(seed))
    assert.equal(got.responseSplit, DEFAULT_RESPONSE_SPLIT, JSON.stringify(seed))
  }
})

test('an unparseable entry keeps the current value', () => {
  const storage = memoryStorage({
    'liteapi.workbench.v3.win.sidebar-width': 'wide',
    'liteapi.workbench.v3.win.response-split': '0.41',
  })
  assert.deepEqual(readWorkbenchLayout('win', { sidebarWidth: 350, responseSplit: 0.6 }, storage), {
    sidebarWidth: 350,
    responseSplit: 0.41,
  })
})

// Half a saved layout must not reset the other half. Passing the defaults
// instead of the live values would snap a divider the user just dragged.
test('a half-saved layout leaves the other divider where it is', () => {
  const storage = memoryStorage({ 'liteapi.workbench.v3.win.response-split': '0.65' })
  const got = readWorkbenchLayout('win', { sidebarWidth: 400, responseSplit: 0.5 }, storage)
  assert.deepEqual(got, { sidebarWidth: 400, responseSplit: 0.65 })
})

// A WebView with storage disabled throws on access rather than returning null.
// Layout is an enhancement and must never take the app down with it.
test('storage that throws is survived by both directions', () => {
  const hostile: LayoutStorage = {
    getItem() {
      throw new Error('storage disabled')
    },
    setItem() {
      throw new Error('quota exceeded')
    },
  }
  assert.deepEqual(readWorkbenchLayout('win', current, hostile), current)
  assert.equal(writeWorkbenchLayout('win', current, hostile), false)
})

test('a pointer maps to a split fraction along the axis the panes stack on', () => {
  const bounds = { top: 100, left: 200, width: 1000, height: 500 }
  assert.equal(splitFractionAt(bounds, { clientX: 700, clientY: 0 }, false, 0.52), 0.5)
  assert.equal(splitFractionAt(bounds, { clientX: 0, clientY: 300 }, true, 0.52), 0.4)
})

// The compact layout stacks the panes whatever the persisted preference says,
// so the two axes must genuinely differ: reading clientX for a vertical stack
// would move the divider sideways-to-nowhere as the pointer travels down.
test('the two orientations read different axes', () => {
  const bounds = { top: 0, left: 0, width: 1000, height: 1000 }
  const point = { clientX: 400, clientY: 600 }
  assert.notEqual(
    splitFractionAt(bounds, point, false, 0.52),
    splitFractionAt(bounds, point, true, 0.52),
  )
})

test('a drag past either edge is clamped rather than escaping the pane', () => {
  const bounds = { top: 0, left: 0, width: 1000, height: 1000 }
  assert.equal(splitFractionAt(bounds, { clientX: -500, clientY: 0 }, false, 0.52), 0.3)
  assert.equal(splitFractionAt(bounds, { clientX: 5000, clientY: 0 }, false, 0.52), 0.7)
})

// A workbench measured before layout reports zero height. Dividing by it yields
// NaN, which sticks in the flex-basis until the next successful drag.
test('a zero-sized workbench leaves the split alone instead of producing NaN', () => {
  const collapsed = { top: 0, left: 0, width: 0, height: 0 }
  assert.equal(splitFractionAt(collapsed, { clientX: 10, clientY: 10 }, true, 0.52), 0.52)
  assert.equal(splitFractionAt(collapsed, { clientX: 10, clientY: 10 }, false, 0.52), 0.52)
})
