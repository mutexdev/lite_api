import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  DEFAULT_AUTO_SAVE_INTERVAL_MS,
  DEFAULT_CODE_FONT,
  DEFAULT_CODE_FONT_FAMILY,
  DEFAULT_CODE_FONT_SIZE,
  MAX_RUNNER_DELAY_MS,
  MAX_RUNNER_ITERATIONS,
  ZOOM_DEFAULT_PERCENTAGE,
  ZOOM_MAX_PERCENTAGE,
  ZOOM_MIN_PERCENTAGE,
  normalizePresetRequestType,
  codeFontFamilyFor,
  normalizedAutoSaveInterval,
  normalizedCodeFont,
  normalizedCodeFontSize,
  normalizedDevToolsDetailsPanelWidth,
  normalizedDevToolsDrawerHeight,
  normalizedRequestTimeout,
  normalizedResponsePaneOrientation,
  normalizedRunnerDelayMs,
  normalizedRunnerIterations,
  normalizedTabID,
  normalizedThemeMode,
  normalizedThemeVariant,
  normalizedZoomPercentage,
  sizeFromTrailingEdgeDrag,
} from '../src/lib/preferences.ts'

// "ws" is the name an earlier build persisted for WebSocket presets. Dropping
// it turns every saved preset from that build into a no-op — the caller reads
// "" and opens nothing.
test('the legacy websocket preset name is still understood', () => {
  assert.equal(normalizePresetRequestType('ws'), 'websocket')
})

test('a known preset type passes through and an unknown one becomes empty', () => {
  for (const value of ['http', 'graphql', 'grpc', 'websocket']) {
    assert.equal(normalizePresetRequestType(value), value)
  }
  assert.equal(normalizePresetRequestType('soap'), '')
  assert.equal(normalizePresetRequestType(undefined), '')
})

test('the response pane falls back to horizontal', () => {
  assert.equal(normalizedResponsePaneOrientation('vertical'), 'vertical')
  assert.equal(normalizedResponsePaneOrientation('sideways'), 'horizontal')
  assert.equal(normalizedResponsePaneOrientation(undefined), 'horizontal')
})

test('the theme mode falls back to system', () => {
  for (const mode of ['light', 'dark', 'system']) {
    assert.equal(normalizedThemeMode(mode), mode)
  }
  assert.equal(normalizedThemeMode('solarized'), 'system')
})

// A variant id removed between versions reaches dataset.themeVariant, matches
// no stylesheet, and leaves the app rendered with no theme colours at all.
test('a theme variant that is no longer installed falls back to the first', () => {
  const variants = [{ id: 'slate' }, { id: 'amber' }]
  assert.equal(normalizedThemeVariant('amber', variants), 'amber')
  assert.equal(normalizedThemeVariant('retired', variants), 'slate')
  assert.equal(normalizedThemeVariant(undefined, variants), 'slate')
  assert.equal(normalizedThemeVariant('slate', []), '')
})

test('the zoom is clamped to its range', () => {
  assert.equal(normalizedZoomPercentage(120), 120)
  assert.equal(normalizedZoomPercentage(10), ZOOM_MIN_PERCENTAGE)
  assert.equal(normalizedZoomPercentage(9000), ZOOM_MAX_PERCENTAGE)
})

// A zoom of 0 scales the document to nothing. It is treated as absent rather
// than clamped up to the minimum: an unset preference is not a request for the
// smallest possible interface.
test('a zero or unusable zoom reads as unset, not as the minimum', () => {
  for (const value of [0, Number.NaN, undefined]) {
    assert.equal(normalizedZoomPercentage(value as number), ZOOM_DEFAULT_PERCENTAGE, String(value))
  }
  assert.notEqual(normalizedZoomPercentage(0), ZOOM_MIN_PERCENTAGE)
})

test('an empty font name falls back rather than being applied', () => {
  assert.equal(normalizedCodeFont('  '), DEFAULT_CODE_FONT)
  assert.equal(normalizedCodeFont(undefined), DEFAULT_CODE_FONT)
  assert.equal(normalizedCodeFont('  Fira Code  '), 'Fira Code')
})

// 1px is technically in range and completely unreadable. A stored 0 far more
// likely means "never configured" than "as small as possible".
test('a zero font size reads as unset, not as the minimum', () => {
  assert.equal(normalizedCodeFontSize(0), DEFAULT_CODE_FONT_SIZE)
  assert.notEqual(normalizedCodeFontSize(0), 1)
  assert.equal(normalizedCodeFontSize(undefined), DEFAULT_CODE_FONT_SIZE)
  assert.equal(normalizedCodeFontSize(Number.NaN), DEFAULT_CODE_FONT_SIZE)
})

test('the font size is clamped to a readable range', () => {
  assert.equal(normalizedCodeFontSize(0.2), 1)
  assert.equal(normalizedCodeFontSize(200), 32)
  assert.equal(normalizedCodeFontSize(16), 16)
})

// A stored 1 would be honoured as one write per millisecond — a continuous
// rewrite of the collection on disk for as long as the app is open.
test('a very short autosave period is floored, not honoured', () => {
  assert.equal(normalizedAutoSaveInterval(1), 500)
  assert.equal(normalizedAutoSaveInterval(499), 500)
  assert.equal(normalizedAutoSaveInterval(2000), 2000)
})

test('a non-positive autosave period reads as unset', () => {
  for (const value of [0, -1, Number.NaN, undefined]) {
    assert.equal(
      normalizedAutoSaveInterval(value as number),
      DEFAULT_AUTO_SAVE_INTERVAL_MS,
      String(value),
    )
  }
})

// The point of keeping these in one module: zero is a legitimate request
// timeout (meaning "no timeout") and an unset autosave interval. Collapsing the
// two into a shared guard would either cap requests the user left uncapped or
// let the autosave fire continuously.
test('zero means no timeout for a request but unset for an autosave', () => {
  assert.equal(normalizedRequestTimeout(0), 0)
  assert.equal(normalizedAutoSaveInterval(0), DEFAULT_AUTO_SAVE_INTERVAL_MS)
  assert.notEqual(normalizedRequestTimeout(0), normalizedAutoSaveInterval(0))
})

// A negative timeout reaches Go's http.Client.Timeout, where it makes every
// request fail instantly rather than being ignored.
test('a negative request timeout becomes zero rather than passing through', () => {
  assert.equal(normalizedRequestTimeout(-1), 0)
  assert.equal(normalizedRequestTimeout(-30000), 0)
  assert.equal(normalizedRequestTimeout(Number.NaN), 0)
  assert.equal(normalizedRequestTimeout(1500.6), 1501)
})

test('the runner delay is clamped to its ceiling', () => {
  assert.equal(normalizedRunnerDelayMs(250), 250)
  assert.equal(normalizedRunnerDelayMs(-5), 0)
  assert.equal(normalizedRunnerDelayMs(MAX_RUNNER_DELAY_MS + 1), MAX_RUNNER_DELAY_MS)
  assert.equal(normalizedRunnerDelayMs(Number.NaN), 0)
})

test('the runner iteration count is at least one and capped', () => {
  assert.equal(normalizedRunnerIterations(0), 1)
  assert.equal(normalizedRunnerIterations(-4), 1)
  assert.equal(normalizedRunnerIterations(500), MAX_RUNNER_ITERATIONS)
  assert.equal(normalizedRunnerIterations(12.9), 12)
})

test('the devtools panel sizes are clamped', () => {
  assert.equal(normalizedDevToolsDetailsPanelWidth(500), 500)
  assert.equal(normalizedDevToolsDetailsPanelWidth(10), 280)
  assert.equal(normalizedDevToolsDetailsPanelWidth(9000), 800)
  assert.equal(normalizedDevToolsDrawerHeight(400), 400)
  assert.equal(normalizedDevToolsDrawerHeight(10), 220)
  assert.equal(normalizedDevToolsDrawerHeight(9000), 720)
})

// A tab id removed between versions leaves the drawer open with no panel
// rendered inside it — an empty box with no way to tell it from a bug.
test('a tab id that no longer exists falls back', () => {
  const tabs = [{ id: 'console' }, { id: 'network' }] as const
  assert.equal(normalizedTabID('network', tabs, 'console'), 'network')
  assert.equal(normalizedTabID('performance', tabs, 'console'), 'console')
  assert.equal(normalizedTabID(undefined, tabs, 'console'), 'console')
})

// The result is written straight into a CSS custom property with
// style.setProperty, and the name is free text from a preferences field. A name
// containing a double quote closes the quoted string early and lets the rest be
// parsed as CSS.
test('a quote in a font name is escaped rather than closing the string', () => {
  const injected = codeFontFamilyFor('a", x: y; --z: "b')
  assert.equal(injected, `"a\\", x: y; --z: \\"b", ${DEFAULT_CODE_FONT_FAMILY}`)
  // No quote inside the name segment is left unescaped — an unescaped one ends
  // the string and everything after it is parsed as CSS.
  const name = injected.slice(0, injected.length - DEFAULT_CODE_FONT_FAMILY.length - 2)
  assert.equal(name.slice(1, -1).replace(/\\./g, '').includes('"'), false, name)
})

// Backslashes must be escaped first, which the single character class
// guarantees: escaping quotes and then backslashes would double the backslash
// just inserted, and the font name would be wrong.
test('a backslash is escaped exactly once', () => {
  assert.ok(codeFontFamilyFor('a\\b').startsWith('"a\\\\b"'))
  assert.ok(codeFontFamilyFor('a\\"b').startsWith('"a\\\\\\"b"'))
})

// The fallback stack is several families. Quoting it whole would name one font
// that does not exist, and every code surface would render in the browser
// default.
test('the default font short-circuits instead of being quoted', () => {
  assert.equal(codeFontFamilyFor(DEFAULT_CODE_FONT), DEFAULT_CODE_FONT_FAMILY)
  assert.equal(codeFontFamilyFor(''), DEFAULT_CODE_FONT_FAMILY)
  assert.equal(codeFontFamilyFor('   '), DEFAULT_CODE_FONT_FAMILY)
})

test('a chosen font is quoted and keeps the fallback stack behind it', () => {
  const family = codeFontFamilyFor('Fira Code')
  assert.equal(family, `"Fira Code", ${DEFAULT_CODE_FONT_FAMILY}`)
})

// The DevTools details panel is pinned to the right edge and the drawer to the
// bottom, so dragging the handle LEFT or UP makes them BIGGER. Adding the delta
// instead compiles, runs, and moves the panel the wrong way — a sign error with
// no symptom other than a handle that feels backwards.
test('a trailing-edge panel grows when the handle is dragged towards the content', () => {
  const bigger = sizeFromTrailingEdgeDrag(400, -100, normalizedDevToolsDetailsPanelWidth)
  const smaller = sizeFromTrailingEdgeDrag(400, 100, normalizedDevToolsDetailsPanelWidth)
  assert.equal(bigger, 500)
  assert.equal(smaller, 300)
  assert.ok(bigger > smaller)
})

test('the drag result is clamped by whichever normalizer it is given', () => {
  assert.equal(sizeFromTrailingEdgeDrag(400, -9999, normalizedDevToolsDetailsPanelWidth), 800)
  assert.equal(sizeFromTrailingEdgeDrag(400, 9999, normalizedDevToolsDetailsPanelWidth), 280)
  assert.equal(sizeFromTrailingEdgeDrag(320, -9999, normalizedDevToolsDrawerHeight), 720)
  assert.equal(sizeFromTrailingEdgeDrag(320, 9999, normalizedDevToolsDrawerHeight), 220)
})
