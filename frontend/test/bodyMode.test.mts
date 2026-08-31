// The mode/format split behind the body picker.
//
// The risk this file exists to hold down: the picker now writes a DIFFERENT
// value than the one the segment names, so a mapping bug does not look like a
// UI bug — it looks like a request being sent with the wrong Content-Type.
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  bodyFormatOptions,
  bodyModeOptions,
  contentTypeHint,
  editorLanguage,
  formatOf,
  modeOf,
  recallFormat,
  rememberFormat,
  storedForFormat,
  storedForMode,
  usesFormat,
  type BodyMode,
  type FormatMemory
} from '../src/lib/workbench/bodyMode.ts'

// Every value the old picker offered, plus the one it could display but never
// select. If a stored value is missing here it is a value the new picker cannot
// round-trip.
const storedModes = ['none', 'json', 'text', 'xml', 'formUrlEncoded', 'multipartForm', 'file', 'graphql', 'sparql']

test('every stored mode maps to a segment the picker actually offers', () => {
  const offered = new Set(bodyModeOptions.map((option) => option.value))
  for (const stored of storedModes) {
    assert.ok(offered.has(modeOf(stored)), `${stored} maps to a segment that does not exist`)
  }
})

test('an unknown stored mode falls back to none rather than rendering nothing', () => {
  // Importers write this field; a collection from a future version must not
  // produce a picker with no segment selected and no body editor.
  assert.equal(modeOf('someFutureMode'), 'none')
  assert.equal(modeOf(''), 'none')
})

test('the three raw formats collapse into one Raw segment', () => {
  assert.equal(modeOf('json'), 'raw')
  assert.equal(modeOf('xml'), 'raw')
  assert.equal(modeOf('text'), 'raw')
  assert.equal(modeOf('sparql'), 'raw')
})

test('the non-raw modes each keep their own segment', () => {
  assert.equal(modeOf('multipartForm'), 'form')
  assert.equal(modeOf('formUrlEncoded'), 'form-encoded')
  assert.equal(modeOf('file'), 'binary')
  assert.equal(modeOf('graphql'), 'graphql')
  assert.equal(modeOf('none'), 'none')
})

test('choosing a segment round-trips back to the same segment', () => {
  // The property that makes the picker honest: what you click is what shows as
  // selected afterwards.
  for (const option of bodyModeOptions) {
    assert.equal(modeOf(storedForMode(option.value, 'none')), option.value)
  }
})

test('leaving Raw and coming back keeps the format you were using', () => {
  // The bug this prevents: an XML body silently becoming a JSON body — same
  // text, wrong Content-Type, and the editor's linter now reporting errors on
  // valid XML.
  assert.equal(storedForMode('raw', 'xml'), 'xml')
  assert.equal(storedForMode('raw', 'sparql'), 'sparql')
  assert.equal(storedForMode('raw', 'text'), 'text')
})

test('Raw defaults to JSON when there is no previous raw format', () => {
  assert.equal(storedForMode('raw', 'multipartForm'), 'json')
  assert.equal(storedForMode('raw', 'none'), 'json')
  assert.equal(storedForMode('raw', 'graphql'), 'json')
})

test('the format dropdown applies only to Raw', () => {
  assert.equal(usesFormat('json'), true)
  assert.equal(usesFormat('sparql'), true)
  assert.equal(usesFormat('multipartForm'), false)
  assert.equal(usesFormat('graphql'), false)
  assert.equal(usesFormat('none'), false)
})

test('every offered format round-trips', () => {
  for (const option of bodyFormatOptions) {
    const stored = storedForFormat(option.value)
    assert.equal(modeOf(stored), 'raw')
    assert.equal(formatOf(stored), option.value)
  }
})

test('a non-raw mode reports JSON as its format rather than throwing', () => {
  // formatOf is read whenever the dropdown renders, including for one frame
  // during a mode change, so it has to answer for every stored value.
  for (const stored of storedModes) assert.ok(bodyFormatOptions.some((option) => option.value === formatOf(stored)))
})

test('the editor language follows the format, and GraphQL keeps its own', () => {
  assert.equal(editorLanguage('json'), 'json')
  assert.equal(editorLanguage('xml'), 'xml')
  assert.equal(editorLanguage('text'), 'text')
  assert.equal(editorLanguage('graphql'), 'graphql')
  // SPARQL has no bundled grammar; plain text is honest rather than pretending.
  assert.equal(editorLanguage('sparql'), 'text')
})

test('every mode that sends a body states what it will send', () => {
  for (const stored of storedModes) {
    if (stored === 'none') continue
    assert.notEqual(contentTypeHint(stored), '', `${stored} has no content-type hint`)
  }
  assert.equal(contentTypeHint('none'), '')
})

test('the raw formats do not all claim the same content type', () => {
  const hints = ['json', 'xml', 'text', 'sparql'].map(contentTypeHint)
  assert.equal(new Set(hints).size, 4)
})

// --- the raw format survives a detour ------------------------------------
//
// Found in review. The first version of storedForMode took only the mode being
// LEFT, which remembers exactly one hop — and the caller re-derives that from
// whatever the last click wrote, so by the second hop the memory is
// `multipartForm`, which is not a raw format, and Raw resolves to JSON.
//
// The consequence is not cosmetic: an XML body clicked to None and back came
// back as JSON. The XML text is still in `body.xml`, but the editor now shows
// the empty `json` field and the backend picks what to serialise from `mode`
// alone — so the request goes out as application/json with a body the user
// never wrote and cannot see. No error, no edit, no warning.

/** Replays a click path through the picker, carrying the memory the app carries. */
function clickPath(requestId: string, start: string, ...stops: BodyMode[]) {
  const memory: FormatMemory = new Map()
  let stored = start
  for (const stop of stops) {
    rememberFormat(memory, requestId, stored)
    stored = storedForMode(stop, stored, recallFormat(memory, requestId, stored))
  }
  return stored
}

test('a raw format survives more than one hop away from Raw', () => {
  assert.equal(clickPath('r1', 'xml', 'none', 'raw'), 'xml')
  assert.equal(clickPath('r1', 'xml', 'form', 'raw'), 'xml')
  assert.equal(clickPath('r1', 'xml', 'binary', 'raw'), 'xml')
  assert.equal(clickPath('r1', 'sparql', 'graphql', 'raw'), 'sparql')
  assert.equal(clickPath('r1', 'text', 'form-encoded', 'raw'), 'text')
})

test('a raw format survives a long detour, not just two hops', () => {
  assert.equal(clickPath('r1', 'xml', 'none', 'form', 'graphql', 'binary', 'form-encoded', 'raw'), 'xml')
})

test('each request remembers its own format', () => {
  // Keyed by request because the format belongs to that body. Without the key,
  // switching a second request's mode would rewrite the first one's memory.
  const memory: FormatMemory = new Map()
  rememberFormat(memory, 'xml-request', 'xml')
  rememberFormat(memory, 'text-request', 'text')
  assert.equal(recallFormat(memory, 'xml-request', 'none'), 'xml')
  assert.equal(recallFormat(memory, 'text-request', 'none'), 'text')
  assert.equal(recallFormat(memory, 'never-seen', 'none'), 'json')
})

test('a stored raw mode always beats the memory', () => {
  // What is stored is what will actually be sent, so it wins. Memory only
  // answers when the request is not currently in a raw mode.
  const memory: FormatMemory = new Map([['r1', 'xml' as const]])
  assert.equal(recallFormat(memory, 'r1', 'text'), 'text')
  assert.equal(recallFormat(memory, 'r1', 'json'), 'json')
})

test('remembering ignores modes that carry no format', () => {
  const memory: FormatMemory = new Map()
  for (const stored of ['none', 'multipartForm', 'formUrlEncoded', 'file', 'graphql']) {
    rememberFormat(memory, 'r1', stored)
  }
  assert.equal(memory.size, 0, 'a non-raw mode must not be recorded as a format')
})

test('storedForMode without a memory keeps its one-hop behaviour', () => {
  // The default has to stay correct for callers that genuinely have nothing to
  // remember, so the fix cannot silently reintroduce the bug for them.
  assert.equal(storedForMode('raw', 'xml'), 'xml')
  assert.equal(storedForMode('raw', 'none'), 'json')
})
