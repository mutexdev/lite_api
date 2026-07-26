// US-038 — characterisation suite for src/lib/workbench/response.ts.
//
// response.ts is a protected asset (improvement_v2.md §2.2). US-010 will rewire it onto a
// `ReadResponseBody` binding and US-009 deletes `BodyBase64`; these tests exist so that rewrite
// is provably behaviour-preserving. They are therefore written against the *contract* — "the
// slice decodes cleanly", "the flag means content was dropped" — not against the arithmetic in
// the implementation, so a rewrite that reaches the same answers a different way still passes
// and a rewrite that quietly changes the answers fails.
//
// Several assertions below record behaviour that looks wrong. Those are marked BUG and are
// asserted as-is on purpose: this story characterises the module, it does not change it.
//
// DOMParser: response.ts uses it only to detect an XML parse error before pretty-printing.
// Node has no DOM, so a ~5-line stub is installed on globalThis rather than pulling in jsdom.
// The stub answers the one question response.ts asks a parsed document ("is there a
// <parsererror> in it?"), which is the standard browser XML-failure signal, and leaves the
// actual indentation logic — the part under test — running for real.

import assert from 'node:assert/strict'
import test from 'node:test'

let xmlIsMalformed = false

class StubDOMParser {
  parseFromString(_source: string, _mimeType: string) {
    return {
      querySelector: (selector: string) =>
        selector === 'parsererror' && xmlIsMalformed ? { tagName: 'parsererror' } : null
    }
  }
}

const globalWithDOM = globalThis as unknown as { DOMParser?: unknown }
globalWithDOM.DOMParser = StubDOMParser

const {
  automaticPreviewLimit, embeddedPreviewLimit, fullRenderLimit, responseViews,
  utf8ByteLength, base64ByteLength, sliceBase64Bytes, sliceUtf8,
  normalizeResponseView, formatResponseBody, responseTextForView,
  contentType, previewKind, contentDispositionFilename,
  boundedLines, lineDiff, findMatches, compareHeaders, compareJsonStructure
} = await import('../src/lib/workbench/response.ts')

const encoder = new TextEncoder()
const decoder = new TextDecoder()
const byteLength = (value: string) => Buffer.byteLength(value, 'utf8')
const b64 = (bytes: number[] | string) =>
  (typeof bytes === 'string' ? Buffer.from(bytes, 'utf8') : Buffer.from(bytes)).toString('base64')
const decodeB64 = (value: string) => Buffer.from(value, 'base64')

// ─────────────────────────────────────────────────────────────────────────────
// 0. Tier constants and view enum — the guarding contract US-010 must preserve
// ─────────────────────────────────────────────────────────────────────────────

test('preview tiers keep their documented byte budgets in ascending order', () => {
  assert.equal(automaticPreviewLimit, 128 * 1024)
  assert.equal(embeddedPreviewLimit, 512 * 1024)
  assert.equal(fullRenderLimit, 1024 * 1024)
  assert.ok(automaticPreviewLimit < embeddedPreviewLimit && embeddedPreviewLimit < fullRenderLimit)
})

test('normalizeResponseView admits exactly the four views and falls back to pretty', () => {
  assert.deepEqual([...responseViews], ['pretty', 'raw', 'base64', 'hex'])
  for (const view of responseViews) assert.equal(normalizeResponseView(view), view)
  // Unknown, empty and wrong-case input all degrade to the safe view rather than throwing.
  for (const bad of ['', 'HEX', 'Pretty', 'binary', 'hex ']) {
    assert.equal(normalizeResponseView(bad), 'pretty', `expected fallback for ${JSON.stringify(bad)}`)
  }
})

// ─────────────────────────────────────────────────────────────────────────────
// 1. sliceUtf8 — must never split a multi-byte sequence
// ─────────────────────────────────────────────────────────────────────────────

// Contract, checked at every byte limit rather than at hand-picked ones:
//   a. the result is a prefix of the input (no substitution, no re-encoding);
//   b. the result survives an encode/decode round trip, i.e. it contains no lone surrogate;
//   c. the result never introduces U+FFFD;
//   d. the result fits the budget;
//   e. the result is *maximal* — whatever character comes next would have overflowed.
function assertSliceUtf8Contract(value: string) {
  const total = byteLength(value)
  assert.ok(!value.includes('�'), 'fixture must not already contain U+FFFD')
  for (let limit = 0; limit <= total + 2; limit += 1) {
    const label = `${JSON.stringify(value)} @ ${limit}`
    const result = sliceUtf8(value, limit)
    assert.ok(value.startsWith(result), `${label}: not a prefix (${JSON.stringify(result)})`)
    assert.equal(decoder.decode(encoder.encode(result)), result, `${label}: lone surrogate or invalid UTF-8`)
    assert.ok(!result.includes('�'), `${label}: introduced a replacement character`)
    assert.ok(byteLength(result) <= limit, `${label}: ${byteLength(result)} bytes exceeds budget`)
    if (result !== value) {
      const next = [...value.slice(result.length)][0] ?? ''
      assert.ok(byteLength(result + next) > limit, `${label}: could have fitted ${JSON.stringify(next)}`)
    }
  }
}

test('sliceUtf8 never splits a 2-byte sequence', () => {
  assertSliceUtf8Contract('héllo')                      // é precomposed, U+00E9 -> C3 A9
  // Boundary landing inside é yields the ASCII prefix, not a half character.
  assert.equal(sliceUtf8('héllo', 1), 'h')
  assert.equal(sliceUtf8('héllo', 2), 'h')              // one byte short of é
  assert.equal(sliceUtf8('héllo', 3), 'hé')        // exactly the boundary
  assert.equal(sliceUtf8('héllo', 4), 'hél')       // one byte past
})

test('sliceUtf8 never splits a 3-byte sequence', () => {
  assertSliceUtf8Contract('中文abc')                 // 中文 -> 3 bytes each
  assert.equal(sliceUtf8('中文abc', 1), '')
  assert.equal(sliceUtf8('中文abc', 2), '')
  assert.equal(sliceUtf8('中文abc', 3), '中')
  assert.equal(sliceUtf8('中文abc', 4), '中')
  assert.equal(sliceUtf8('中文abc', 6), '中文')
})

test('sliceUtf8 never splits a 4-byte sequence (JS surrogate pair)', () => {
  const emoji = '\u{1F600}ok'                                // 😀 = one code point, two UTF-16 units
  assert.equal(emoji.length, 4, 'fixture must be a surrogate pair, so a naive .slice() would break it')
  assertSliceUtf8Contract(emoji)
  for (let limit = 0; limit <= 3; limit += 1) {
    assert.equal(sliceUtf8(emoji, limit), '', `limit ${limit} must drop the whole astral character`)
  }
  assert.equal(sliceUtf8(emoji, 4), '\u{1F600}')
  assert.equal(sliceUtf8(emoji, 5), '\u{1F600}o')
})

test('sliceUtf8 keeps combining marks valid but may separate them from their base character', () => {
  const decomposed = 'éx'                              // e + COMBINING ACUTE (2 bytes) + x
  assert.equal(byteLength(decomposed), 4)
  assertSliceUtf8Contract(decomposed)
  // Documented limitation: the slicer is UTF-8-correct, not grapheme-cluster-correct. A budget
  // of 1 or 2 bytes yields a bare "e" whose accent has been dropped. The output is still valid
  // UTF-8 (that is the invariant that matters for a byte-bounded preview), but it is not the
  // same grapheme. Any US-010 rewrite is free to keep this; it must not produce invalid UTF-8.
  assert.equal(sliceUtf8(decomposed, 1), 'e')
  assert.equal(sliceUtf8(decomposed, 2), 'e')
  assert.equal(sliceUtf8(decomposed, 3), 'é')
})

test('sliceUtf8 handles zero, negative and over-budget limits without allocating a copy', () => {
  assert.equal(sliceUtf8('abc', 0), '')
  assert.equal(sliceUtf8('abc', -1), '')
  assert.equal(sliceUtf8('abc', -1000), '')
  assert.equal(sliceUtf8('', 0), '')
  assert.equal(sliceUtf8('', 128), '')
  // At or above the full byte length the original string is returned untouched.
  assert.equal(sliceUtf8('abc', 3), 'abc')
  assert.equal(sliceUtf8('abc', 4), 'abc')
})

test('sliceUtf8 is byte-bounded, not UTF-16-bounded, across a mixed payload', () => {
  const mixed = 'aé中\u{1F600}́z'
  assertSliceUtf8Contract(mixed)
  assert.notEqual(byteLength(mixed), mixed.length, 'fixture must distinguish bytes from code units')
  assert.equal(byteLength(sliceUtf8(mixed, automaticPreviewLimit)), byteLength(mixed))
})

// ─────────────────────────────────────────────────────────────────────────────
// 2 & 3. Byte accounting — measured against Buffer, not against the same arithmetic
// ─────────────────────────────────────────────────────────────────────────────

test('utf8ByteLength agrees with Buffer.byteLength on every fixture', () => {
  const fixtures = ['', 'a', 'ascii only', 'héllo', '中文', '\u{1F600}\u{1F1EF}\u{1F1F5}',
    'é', '\u0000\u001f\u007f', 'tab\tnewline\n', 'x'.repeat(1000)]
  for (const fixture of fixtures) {
    assert.equal(utf8ByteLength(fixture), Buffer.byteLength(fixture, 'utf8'), JSON.stringify(fixture.slice(0, 20)))
  }
})

test('utf8ByteLength counts a lone surrogate as the 3-byte replacement character', () => {
  // Not a wart: an unpaired surrogate is unencodable, and both TextEncoder and Buffer
  // substitute U+FFFD. Pinned so a rewrite cannot silently start throwing here.
  assert.equal(utf8ByteLength('\uD83D'), Buffer.byteLength('\uD83D', 'utf8'))
  assert.equal(utf8ByteLength('\uD83D'), 3)
})

test('base64ByteLength agrees with the decoded length for canonical padded Base64', () => {
  for (const source of ['', 'A', 'AB', 'ABC', 'ABCD', 'ABCDE', 'hello world', '中文']) {
    const encoded = b64(source)
    assert.equal(base64ByteLength(encoded), decodeB64(encoded).length, JSON.stringify(encoded))
  }
  assert.equal(base64ByteLength('QQ=='), 1)   // one pad char pair -> 1 byte
  assert.equal(base64ByteLength('QUI='), 2)
  assert.equal(base64ByteLength('QUJD'), 3)
})

test('base64ByteLength ignores whitespace, including line-wrapped Base64', () => {
  assert.equal(base64ByteLength('  QUJD  '), 3)
  assert.equal(base64ByteLength('QUJD\nRA=='), 4)
  assert.equal(base64ByteLength('Q U J D'), 3)
  assert.equal(base64ByteLength('   '), 0)
  assert.equal(base64ByteLength(''), 0)
})

test('base64ByteLength measures unpadded Base64, which is legal and common', () => {
  // FIXED under US-038. Unpadded Base64 is legal and common (JWTs, many APIs),
  // and flooring to whole quartets measured the final partial group as nothing —
  // so previews of small unpadded payloads reported as empty. The remainder is
  // 2 characters for one more byte and 3 for two.
  assert.equal(base64ByteLength('QQ'), 1)
  assert.equal(decodeB64('QQ').length, 1)
  assert.equal(base64ByteLength('QUI'), 2)
  assert.equal(decodeB64('QUI').length, 2)
})

test('BUG: base64ByteLength reports a length for input that is not Base64 at all', () => {
  // Still purely positional arithmetic with no alphabet validation, and
  // deliberately left that way: every caller feeds it output from Go's encoder,
  // and validating the alphabet on every preview keystroke would cost a full
  // scan of the payload to defend against input that cannot occur. Documented
  // rather than fixed. The number moved with the US-038 partial-quartet fix.
  assert.equal(base64ByteLength('not!base64'), 7)
})

// ─────────────────────────────────────────────────────────────────────────────
// 2. sliceBase64Bytes — quartet alignment
// ─────────────────────────────────────────────────────────────────────────────

const TEN_BYTES = b64('ABCDEFGHIJ')   // "QUJDREVGR0hJSg==", 16 chars, decodes to 10 bytes

test('sliceBase64Bytes always returns a slice that decodes cleanly and fits the budget', () => {
  assert.equal(decodeB64(TEN_BYTES).length, 10)
  for (let limit = -2; limit <= 14; limit += 1) {
    const slice = sliceBase64Bytes(TEN_BYTES, limit)
    // Quartet integrity is the point: a whole number of 4-char groups always decodes, and a
    // round trip through decode/encode reproduces the slice exactly when it is well formed.
    assert.equal(slice.length % 4, 0, `limit ${limit}: ${JSON.stringify(slice)} is not quartet-aligned`)
    const decoded = decodeB64(slice)
    assert.equal(decoded.toString('base64'), slice, `limit ${limit}: slice does not round-trip`)
    assert.ok(decoded.length <= Math.max(0, limit), `limit ${limit}: decoded ${decoded.length} bytes`)
    assert.ok('ABCDEFGHIJ'.startsWith(decoded.toString('latin1')), `limit ${limit}: decoded a corrupt tail`)
  }
})

test('sliceBase64Bytes rounds a mid-quartet budget down to the previous whole quartet', () => {
  // 3 decoded bytes per 4 chars, so budgets 3..5 all yield the same first quartet.
  assert.equal(sliceBase64Bytes(TEN_BYTES, 3), 'QUJD')
  assert.equal(sliceBase64Bytes(TEN_BYTES, 4), 'QUJD')
  assert.equal(sliceBase64Bytes(TEN_BYTES, 5), 'QUJD')
  assert.equal(sliceBase64Bytes(TEN_BYTES, 6), 'QUJDREVG')
  assert.equal(sliceBase64Bytes(TEN_BYTES, 9), 'QUJDREVGR0hJ')
})

test('sliceBase64Bytes returns the padded original once the budget covers the whole payload', () => {
  // The tail quartet carries "==" padding; it must be handed back intact, never trimmed.
  assert.equal(sliceBase64Bytes(TEN_BYTES, 10), TEN_BYTES)
  assert.equal(sliceBase64Bytes(TEN_BYTES, 11), TEN_BYTES)
  assert.ok(TEN_BYTES.endsWith('=='))
  const onePad = b64('ABCDE')
  assert.ok(onePad.endsWith('=') && !onePad.endsWith('=='))
  assert.equal(sliceBase64Bytes(onePad, 5), onePad)
  assert.equal(sliceBase64Bytes(onePad, 999), onePad)
})

test('sliceBase64Bytes strips whitespace before aligning quartets', () => {
  const wrapped = 'QUJD\nREVG\n'
  assert.equal(sliceBase64Bytes(wrapped, 3), 'QUJD')
  assert.equal(sliceBase64Bytes(wrapped, 6), 'QUJDREVG')
  assert.equal(sliceBase64Bytes(wrapped, 99), 'QUJDREVG', 'over-budget path also returns the compacted form')
})

test('sliceBase64Bytes yields nothing for a budget too small for one quartet', () => {
  // Documented conservatism: a 1- or 2-byte budget cannot be honoured without emitting a
  // partial quartet, so the slicer emits nothing rather than something atob would mangle.
  assert.equal(sliceBase64Bytes(TEN_BYTES, 1), '')
  assert.equal(sliceBase64Bytes(TEN_BYTES, 2), '')
  assert.equal(sliceBase64Bytes(TEN_BYTES, 0), '')
  assert.equal(sliceBase64Bytes(TEN_BYTES, -5), '')
  assert.equal(sliceBase64Bytes('', 0), '')
  assert.equal(sliceBase64Bytes('', 100), '')
})

// ─────────────────────────────────────────────────────────────────────────────
// 4. responseTextForView — dispatch and the hex dump
// ─────────────────────────────────────────────────────────────────────────────

test('responseTextForView dispatches raw/base64/pretty and treats unknown views as pretty', () => {
  assert.equal(responseTextForView('BODY', 'QkFTRTY0', 'raw', 'PRETTY'), 'BODY')
  assert.equal(responseTextForView('BODY', 'QkFTRTY0', 'base64', 'PRETTY'), 'QkFTRTY0')
  assert.equal(responseTextForView('BODY', 'QkFTRTY0', 'pretty', 'PRETTY'), 'PRETTY')
  assert.equal(responseTextForView('BODY', 'QkFTRTY0', 'nonsense', 'PRETTY'), 'PRETTY')
  assert.equal(responseTextForView('BODY', 'QkFTRTY0', '', 'PRETTY'), 'PRETTY')
})

// A hex row is `oooooooo  <hex, padded to 47>  <ascii>`; parsing it back is how we assert the
// dump without restating the formatting expression.
function parseHexRows(dump: string) {
  return dump.split('\n').map((row) => {
    assert.equal(row.slice(8, 10), '  ', `row lacks the offset separator: ${JSON.stringify(row)}`)
    assert.equal(row.slice(57, 59), '  ', `row lacks the ascii separator: ${JSON.stringify(row)}`)
    return { offset: row.slice(0, 8), hex: row.slice(10, 57), ascii: row.slice(59), raw: row }
  })
}

test('hex view lays out 16 bytes per row with 8-digit offsets and an ascii gutter', () => {
  const bytes = Array.from({ length: 20 }, (_, index) => index)
  const rows = parseHexRows(responseTextForView('', b64(bytes), 'hex', ''))
  assert.equal(rows.length, 2)
  assert.equal(rows[0].offset, '00000000')
  assert.equal(rows[1].offset, '00000010', 'second row offset is hexadecimal 16, not decimal')
  assert.equal(rows[0].hex.trim().split(' ').length, 16)
  assert.equal(rows[0].hex.trim().split(' ').map((byte) => parseInt(byte, 16)).join(), bytes.slice(0, 16).join())
  assert.equal(rows[1].hex.trim().split(' ').map((byte) => parseInt(byte, 16)).join(), bytes.slice(16).join())
  for (const row of rows) assert.equal(row.hex.length, 47, 'hex column is fixed width in every row')
})

test('hex view pads a final short row so the ascii gutter stays aligned', () => {
  const rows = parseHexRows(responseTextForView('', b64([0x01, 0x02, 0x03]), 'hex', ''))
  assert.equal(rows.length, 1)
  assert.equal(rows[0].hex, '01 02 03'.padEnd(47, ' '))
  assert.equal(rows[0].raw.length, 8 + 2 + 47 + 2 + 3)
  const full = parseHexRows(responseTextForView('', b64('A'.repeat(16)), 'hex', ''))
  assert.equal(full[0].raw.length, 8 + 2 + 47 + 2 + 16)
  assert.equal(full[0].hex.length, rows[0].hex.length, 'short and full rows share the hex column width')
})

test('hex view renders only printable ASCII in the gutter and dots everywhere else', () => {
  const bytes = [0x00, 0x1f, 0x20, 0x41, 0x7e, 0x7f, 0x80, 0xff]
  const rows = parseHexRows(responseTextForView('', b64(bytes), 'hex', ''))
  // 0x20 (space) and 0x7e (~) are the inclusive printable bounds; 0x7f DEL and everything
  // above it, plus every control byte, render as '.'.
  assert.equal(rows[0].ascii, '.. A~...')
  assert.equal(rows[0].hex.trim(), '00 1f 20 41 7e 7f 80 ff')
  assert.equal(rows[0].hex.trim().split(' ').every((byte) => byte.length === 2), true, 'bytes are zero-padded')
})

test('hex view is empty for empty or undecodable Base64 rather than throwing', () => {
  assert.equal(responseTextForView('', '', 'hex', ''), '')
  assert.equal(responseTextForView('', '=', 'hex', ''), '', 'shorter than one quartet')
  assert.equal(responseTextForView('', '!!!!', 'hex', ''), '', 'outside the Base64 alphabet')
})

test('hex view drops a trailing partial quartet instead of decoding a corrupt byte', () => {
  const full = b64('ABCDEF')                        // 8 chars, no padding
  assert.equal(full.length % 4, 0)
  const withStray = `${full}XY`                     // simulate a truncated transfer
  assert.equal(parseHexRows(responseTextForView('', withStray, 'hex', ''))[0].ascii, 'ABCDEF')
})

test('hex view strips whitespace before aligning quartets', () => {
  // FIXED under US-038. The hex path now strips whitespace before computing
  // quartet alignment, as sliceBase64Bytes always did. Line-wrapped Base64 —
  // which every MIME encoder emits — previously made the arithmetic count
  // newlines as data, so the slice landed mid-group and the last byte was lost.
  const dump = responseTextForView('', 'QUJD REVG', 'hex', '')
  assert.equal(parseHexRows(dump)[0].ascii, 'ABCDEF', 'wrapped Base64 must decode to the same bytes as unwrapped')
  assert.equal(parseHexRows(responseTextForView('', 'QUJDREVG', 'hex', ''))[0].ascii, 'ABCDEF', 'and match the unwrapped form exactly')
})

// ─────────────────────────────────────────────────────────────────────────────
// 5. boundedLines / lineDiff
// ─────────────────────────────────────────────────────────────────────────────

test('boundedLines splits on newlines and keeps a trailing newline from producing a phantom line', () => {
  assert.deepEqual(boundedLines('a\nb\nc', 10).lines, ['a', 'b', 'c'])
  assert.deepEqual(boundedLines('a\n', 10).lines, ['a'])
  assert.deepEqual(boundedLines('a\n\nb', 10).lines, ['a', '', 'b'], 'blank interior lines are preserved')
  assert.deepEqual(boundedLines('', 10).lines, [])
  assert.deepEqual(boundedLines('no newline at all', 10).lines, ['no newline at all'])
  assert.equal(boundedLines('\r\nwindows', 10).lines[0], '\r', 'CR is not stripped; only LF splits')
})

test('boundedLines caps the line count at the limit', () => {
  const ten = Array.from({ length: 10 }, (_, index) => `line ${index}`).join('\n')
  assert.deepEqual(boundedLines(ten, 3).lines, ['line 0', 'line 1', 'line 2'])
  assert.equal(boundedLines(ten, 1).lines.length, 1)
  assert.equal(boundedLines(ten, 0).lines.length, 0)
  assert.equal(boundedLines(ten, 10).lines.length, 10)
  assert.equal(boundedLines(ten, 11).lines.length, 10)
})

test('boundedLines defaults to 2400 lines and the 1 MB full-render character budget', () => {
  const lines = (count: number) => Array.from({ length: count }, (_, index) => String(index)).join('\n')
  assert.equal(boundedLines(lines(2399)).lines.length, 2399)
  assert.equal(boundedLines(lines(2399)).truncated, false)
  assert.equal(boundedLines(lines(2400)).lines.length, 2400)
  assert.equal(boundedLines(lines(2500)).lines.length, 2400)
  assert.equal(boundedLines(lines(2500)).truncated, true)
  // Character budget defaults to fullRenderLimit, so a single 1 MB+ line is cut.
  assert.equal(boundedLines('x'.repeat(fullRenderLimit)).truncated, false)
  assert.equal(boundedLines('x'.repeat(fullRenderLimit + 1)).truncated, true)
  assert.equal(boundedLines('x'.repeat(fullRenderLimit + 1)).lines[0].length, fullRenderLimit)
})

test('boundedLines honours the character limit independently of the line limit', () => {
  assert.deepEqual(boundedLines('abcdef', 10, 3), { lines: ['abc'], truncated: true })
  assert.deepEqual(boundedLines('abc', 10, 3), { lines: ['abc'], truncated: false })
  assert.deepEqual(boundedLines('ab\ncd', 10, 4), { lines: ['ab', 'c'], truncated: true })
  assert.deepEqual(boundedLines('ab\ncd', 10, 5), { lines: ['ab', 'cd'], truncated: false })
  assert.deepEqual(boundedLines('anything', 10, 0), { lines: [], truncated: true })
})

test('boundedLines reports truncated only when content was actually left behind', () => {
  // FIXED under US-038. The flag was `lines.length >= limit`, so content that
  // fitted exactly was still advertised as cut and the "Showing first N lines"
  // affordance lied on every response landing exactly on the limit. The
  // contract is now "set iff content was left behind".
  const exactly = boundedLines('a\nb', 2)
  assert.deepEqual(exactly.lines, ['a', 'b'], 'nothing was actually dropped')
  assert.equal(exactly.truncated, false, 'and it no longer claims otherwise')
  assert.equal(boundedLines('a\nb\nc', 3).truncated, false, 'exactly the limit is not truncation')
  assert.equal(boundedLines('a\nb\nc', 2).truncated, true, 'a line the loop never reached is')
  assert.equal(boundedLines('a\nb\nc', 4).truncated, false)
})

test('lineDiff pairs lines positionally and marks only differing rows as changed', () => {
  assert.deepEqual(lineDiff('a\nb', 'a\nc').rows, [
    { left: 'a', right: 'a', changed: false, change: 'unchanged' },
    { left: 'b', right: 'c', changed: true, change: 'changed' }
  ])
  assert.deepEqual(lineDiff('same', 'same').rows, [{ left: 'same', right: 'same', changed: false, change: 'unchanged' }])
  assert.deepEqual(lineDiff('', '').rows, [])
})

test('lineDiff pads the shorter side to the longer line count', () => {
  const rows = lineDiff('a\nb\nc', 'a').rows
  assert.equal(rows.length, 3)
  // US-038: a row the other side simply has no line for is `removed`/`added`,
  // not `changed`. `changed` stays true so existing callers keep highlighting
  // it, but the label no longer claims the line was edited.
  assert.deepEqual(rows[1], { left: 'b', right: '', changed: true, change: 'removed' })
  assert.deepEqual(rows[2], { left: 'c', right: '', changed: true, change: 'removed' })
  assert.equal(lineDiff('a', 'a\nb\nc').rows.length, 3)
  assert.deepEqual(lineDiff('a', 'a\nb').rows[1], { left: '', right: 'b', changed: true, change: 'added' })
})

test('lineDiff truncation is the union of both sides at limit-1, limit and limit+1', () => {
  const three = 'a\nb\nc'
  assert.equal(lineDiff(three, three, 4).truncated, false)
  assert.equal(lineDiff(three, three, 2).rows.length, 2)
  assert.equal(lineDiff(three, three, 2).truncated, true)
  assert.equal(lineDiff(three, 'a', 4).truncated, false, 'short side alone must not trip the flag')
  assert.equal(lineDiff('a', three, 2).truncated, true, 'either side truncating truncates the diff')
  // Inherits the boundedLines fix: exactly-`limit` lines are no longer reported
  // as truncated, because nothing was left behind.
  assert.equal(lineDiff(three, three, 3).truncated, false)
})

test('lineDiff distinguishes an absent line from an edited one', () => {
  // FIXED under US-038. A present-but-empty line and an absent line both render
  // as '', so the UI showed a row with identical blank content highlighted as a
  // change with no way to tell why. `change` names which of the three cases it
  // is; `changed` stays true so existing callers keep highlighting the row.
  const rows = lineDiff('a\n\n', 'a').rows
  assert.deepEqual(rows[1], { left: '', right: '', changed: true, change: 'removed' })
  // An genuinely edited line is still reported as changed rather than removed.
  assert.deepEqual(lineDiff('a\nb', 'a\nc').rows[1], { left: 'b', right: 'c', changed: true, change: 'changed' })
})

// ─────────────────────────────────────────────────────────────────────────────
// 6. findMatches — the match cap
// ─────────────────────────────────────────────────────────────────────────────

test('findMatches returns case-insensitive byte offsets of every occurrence', () => {
  assert.deepEqual(findMatches('abcabc', 'B'), [1, 4])
  assert.deepEqual(findMatches('ABCabc', 'abc'), [0, 3])
  assert.deepEqual(findMatches('abc', 'z'), [])
  assert.deepEqual(findMatches('', 'a'), [])
  assert.deepEqual(findMatches('needle', 'needle'), [0])
})

test('findMatches returns nothing for an empty or whitespace-only query', () => {
  assert.deepEqual(findMatches('abc', ''), [])
  assert.deepEqual(findMatches('abc', '   '), [])
  assert.deepEqual(findMatches('abc', '\t\n'), [])
})

test('findMatches returns nothing when the query is longer than the value', () => {
  assert.deepEqual(findMatches('abc', 'abcd'), [])
  assert.deepEqual(findMatches('a', 'aa'), [])
})

test('findMatches advances past each hit, so overlapping occurrences are not all reported', () => {
  // "aaaa" contains "aa" at 0, 1 and 2; the scanner steps by the needle length and reports 0, 2.
  assert.deepEqual(findMatches('aaaa', 'aa'), [0, 2])
  assert.deepEqual(findMatches('aaaaa', 'aa'), [0, 2])
  assert.deepEqual(findMatches('abababa', 'aba'), [0, 4])
})

test('findMatches caps results at 500 and gives the caller no signal that it did', () => {
  assert.equal(findMatches('x'.repeat(499), 'x').length, 499)
  assert.equal(findMatches('x'.repeat(500), 'x').length, 500)
  const capped = findMatches('x'.repeat(501), 'x')
  assert.equal(capped.length, 500, 'cap holds at limit+1')
  assert.equal(capped.at(-1), 499)
  assert.equal(findMatches('x'.repeat(5000), 'x').length, 500)
  // BUG (design): the return type is a bare number[], so "500 matches" and "500 of many"
  // are indistinguishable and the match counter in the UI silently plateaus. Asserted as-is.
})

test('BUG: findMatches trims the query, so a padded search term silently searches the trimmed one', () => {
  // Searching for " a " in "a b a" matches the bare "a"s at 0 and 4 — positions where the
  // padded query does not occur. Highlight ranges computed from these offsets are then wrong
  // by the trimmed amount. Relevant because the response search box passes raw user input.
  assert.deepEqual(findMatches('a b a', ' a '), [0, 4])
  assert.deepEqual(findMatches('xax', ' a '), [1])
})

// ─────────────────────────────────────────────────────────────────────────────
// 7. Header parsing and body formatting
// ─────────────────────────────────────────────────────────────────────────────

test('contentType is case-insensitive on the header name and lowercases the value', () => {
  assert.equal(contentType({ 'Content-Type': 'Application/JSON' }), 'application/json')
  assert.equal(contentType({ 'content-type': 'text/plain' }), 'text/plain')
  assert.equal(contentType({ 'CONTENT-TYPE': 'text/html; charset=UTF-8' }), 'text/html; charset=utf-8')
  assert.equal(contentType({}), '', 'absent header yields the empty string, never undefined')
  assert.equal(contentType(), '')
  assert.equal(contentType({ Accept: 'text/plain' }), '')
  assert.equal(contentType({ 'Content-Type': '' }), '')
})

test('contentType takes the first matching header when casing variants collide', () => {
  assert.equal(contentType({ 'Content-Type': 'a/b', 'content-type': 'c/d' }), 'a/b')
})

test('previewKind classifies media types in a fixed precedence order', () => {
  assert.equal(previewKind({ 'Content-Type': 'IMAGE/PNG' }), 'image')
  assert.equal(previewKind({ 'Content-Type': 'application/pdf' }), 'pdf')
  assert.equal(previewKind({ 'Content-Type': 'text/html; charset=utf-8' }), 'html')
  assert.equal(previewKind({ 'Content-Type': 'application/xml' }), 'xml')
  assert.equal(previewKind({ 'Content-Type': 'application/vnd.api+json' }), 'json')
  assert.equal(previewKind({ 'Content-Type': 'application/octet-stream' }), 'binary')
  assert.equal(previewKind({ 'Content-Type': 'text/plain' }), 'text')
  assert.equal(previewKind({}), 'text', 'no header at all is treated as text, not binary')
  assert.equal(previewKind(), 'text')
})

test('previewKind precedence: image/ and *html* win over the +xml suffix', () => {
  assert.equal(previewKind({ 'Content-Type': 'image/svg+xml' }), 'image', 'image/ prefix is checked first')
  assert.equal(previewKind({ 'Content-Type': 'application/xhtml+xml' }), 'html', 'substring "html" beats "xml"')
  assert.equal(previewKind({ 'Content-Type': 'application/pdf+json' }), 'pdf')
})

test('previewKind keeps known text-shaped application/* types out of the binary bucket', () => {
  for (const type of ['application/javascript', 'application/graphql', 'application/x-yaml',
    'application/x-www-form-urlencoded', 'text/csv']) {
    assert.equal(previewKind({ 'Content-Type': type }), 'text', type)
  }
  for (const type of ['application/zip', 'audio/mpeg', 'video/mp4', 'font/woff2']) {
    assert.equal(previewKind({ 'Content-Type': type }), 'binary', type)
  }
})

test('contentDispositionFilename reads quoted, unquoted and space-padded forms', () => {
  const name = (disposition: string) => contentDispositionFilename({ 'Content-Disposition': disposition })
  assert.equal(name('attachment; filename="report.json"'), 'report.json')
  assert.equal(name('attachment; filename=report.json'), 'report.json')
  assert.equal(name('attachment; filename = "spaced.txt"'), 'spaced.txt')
  assert.equal(name('inline; filename=report.json; foo=1'), 'report.json', 'stops at the parameter separator')
  assert.equal(contentDispositionFilename({ 'content-disposition': 'attachment; filename="x.txt"' }), 'x.txt')
})

test('contentDispositionFilename prefers the RFC 5987 filename* form and percent-decodes it', () => {
  const name = (disposition: string) => contentDispositionFilename({ 'Content-Disposition': disposition })
  assert.equal(name("attachment; filename*=UTF-8''%E2%82%AC%20rates.csv"), '€ rates.csv')
  assert.equal(name("attachment; filename*=utf-8''plain.txt"), 'plain.txt', 'charset token is case-insensitive')
  assert.equal(name('attachment; filename="fallback.txt"; filename*=UTF-8\'\'real.txt'), 'real.txt')
})

test('contentDispositionFilename returns empty for absent, empty or parameterless headers', () => {
  assert.equal(contentDispositionFilename({}), '')
  assert.equal(contentDispositionFilename(), '')
  assert.equal(contentDispositionFilename({ 'Content-Disposition': '' }), '')
  assert.equal(contentDispositionFilename({ 'Content-Disposition': 'attachment' }), '')
  assert.equal(contentDispositionFilename({ 'Content-Disposition': 'inline' }), '')
})

test('contentDispositionFilename neutralises path traversal and filesystem-hostile characters', () => {
  const name = (disposition: string) => contentDispositionFilename({ 'Content-Disposition': disposition })
  const traversal = name('attachment; filename="../../etc/passwd"')
  assert.equal(traversal, '_.._etc_passwd')
  assert.ok(!traversal.includes('/') && !traversal.includes('\\'), 'no separator survives')
  assert.equal(name('attachment; filename="a:b*c?d<e>f|g.txt"'), 'a_b_c_d_e_f_g.txt')
  assert.equal(name('attachment; filename="bad\u0000name.txt"'), 'bad_name.txt', 'NUL and control bytes are stripped')
  assert.equal(name('attachment; filename="...hidden.txt"'), 'hidden.txt', 'leading dots removed')
  assert.equal(name('attachment; filename="..."'), '', 'a name made only of dots collapses to nothing')
})

test('contentDispositionFilename truncates to 180 characters', () => {
  const long = `${'y'.repeat(300)}.txt`
  const result = contentDispositionFilename({ 'Content-Disposition': `attachment; filename="${long}"` })
  assert.equal(result.length, 180)
  assert.equal(result, 'y'.repeat(180))
})

test('a malformed filename* falls back to the plain filename beside it', () => {
  // FIXED under US-038. "%E0%A4%A" is a truncated percent escape, so
  // decodeURIComponent throws; the old ternary then never consulted the plain
  // filename sitting right beside it and a download that had a usable name got
  // none. filename* is preferred by the RFC, not exclusive.
  const disposition = 'attachment; filename="fallback.txt"; filename*=UTF-8\'\'%E0%A4%A'
  assert.equal(contentDispositionFilename({ 'Content-Disposition': disposition }), 'fallback.txt')
  // With nothing to fall back to there is still no name to offer.
  assert.equal(contentDispositionFilename({ 'Content-Disposition': "attachment; filename*=UTF-8''%E0%A4%A" }), '')
})

test('an empty quoted filename offers no filename at all', () => {
  // FIXED under US-038. The quoted pattern needs one or more characters so it
  // failed on `filename=""`; the unquoted pattern then captured the two quote
  // characters, which the sanitiser rewrote to "__" — a name that looks real
  // and saves a file nobody can identify. A name that sanitises to nothing
  // usable is no name.
  assert.equal(contentDispositionFilename({ 'Content-Disposition': 'attachment; filename=""' }), '')
  // A name that merely CONTAINS illegal characters still keeps its usable part.
  assert.equal(contentDispositionFilename({ 'Content-Disposition': 'attachment; filename="a/b.txt"' }), 'a_b.txt')
})

test('formatResponseBody pretty-prints JSON by content type or by leading brace/bracket', () => {
  assert.equal(formatResponseBody('{"a":1}', { 'Content-Type': 'application/json' }), '{\n  "a": 1\n}')
  assert.equal(formatResponseBody('{"a":1}'), '{\n  "a": 1\n}', 'sniffed with no headers at all')
  assert.equal(formatResponseBody('  \n [1,2]'), '[\n  1,\n  2\n]', 'leading whitespace is tolerated')
  assert.equal(formatResponseBody('{"a":1}', { 'CONTENT-TYPE': 'application/json' }), '{\n  "a": 1\n}')
  assert.equal(formatResponseBody('{"a":1}', { 'Content-Type': 'application/vnd.api+json' }), '{\n  "a": 1\n}')
})

test('formatResponseBody returns the body verbatim when it is not valid JSON', () => {
  assert.equal(formatResponseBody('{bad', { 'Content-Type': 'application/json' }), '{bad')
  assert.equal(formatResponseBody('plain text', { 'Content-Type': 'application/json' }), 'plain text')
  assert.equal(formatResponseBody('', { 'Content-Type': 'application/json' }), '')
  assert.equal(formatResponseBody('hello'), 'hello')
  assert.equal(formatResponseBody('[unclosed'), '[unclosed')
})

test('formatResponseBody sniffs JSON ahead of the declared content type', () => {
  // A JSON-shaped body wins even when the server said XML. Worth pinning: it means the
  // content type alone does not determine the formatter.
  assert.equal(formatResponseBody('{"a":1}', { 'Content-Type': 'application/xml' }), '{\n  "a": 1\n}')
})

test('formatResponseBody indents XML only when the content type says so', () => {
  const xml = { 'Content-Type': 'application/xml' }
  assert.equal(formatResponseBody('<root><a>1</a><b/></root>', xml), '<root>\n  <a>\n    1\n  </a>\n  <b/>\n</root>')
  assert.equal(formatResponseBody('<a>1</a>', { 'Content-Type': 'text/xml' }), '<a>\n  1\n</a>')
  // Self-closing tags and prologue/comment tokens must not open an indent level.
  assert.equal(formatResponseBody('<?xml version="1.0"?><r><a/></r>', xml), '<?xml version="1.0"?>\n<r>\n  <a/>\n</r>')
  assert.equal(formatResponseBody('<!-- c --><r><a/></r>', xml), '<!-- c -->\n<r>\n  <a/>\n</r>')
  // No content type means no XML formatting, even for an obviously XML body.
  assert.equal(formatResponseBody('<a>1</a>'), '<a>1</a>')
})

test('formatResponseBody leaves XML untouched when the parser reports a parsererror', () => {
  xmlIsMalformed = true
  try {
    assert.equal(formatResponseBody('<a>', { 'Content-Type': 'application/xml' }), '<a>')
    assert.equal(formatResponseBody('not xml at all', { 'Content-Type': 'application/xml' }), 'not xml at all')
  } finally {
    xmlIsMalformed = false
  }
})

test('formatResponseBody degrades to the raw body when DOMParser is unavailable', () => {
  // Guards the Node/SSR path and, more usefully, guards against a US-010 rewrite that moves
  // this call somewhere DOMParser is not defined: it must return the body, never throw.
  const saved = globalWithDOM.DOMParser
  delete globalWithDOM.DOMParser
  try {
    assert.equal(formatResponseBody('<root><a>1</a></root>', { 'Content-Type': 'application/xml' }), '<root><a>1</a></root>')
  } finally {
    globalWithDOM.DOMParser = saved
  }
})

// ─────────────────────────────────────────────────────────────────────────────
// 8. Comparison helpers
// ─────────────────────────────────────────────────────────────────────────────

test('compareHeaders classifies added, removed, changed and unchanged by lowercased key', () => {
  assert.deepEqual(compareHeaders({ A: '1', B: '2', D: '4' }, { a: '1', C: '3', d: '9' }), [
    { key: 'a', name: 'A', current: '1', selected: '1', change: 'unchanged' },
    { key: 'b', name: 'B', current: '2', selected: '', change: 'removed' },
    { key: 'c', name: 'C', current: '', selected: '3', change: 'added' },
    { key: 'd', name: 'D', current: '4', selected: '9', change: 'changed' }
  ])
})

test('compareHeaders sorts by key and prefers the current side spelling of the name', () => {
  const rows = compareHeaders({ 'Z-Head': '1', 'a-head': '2' }, { 'Z-HEAD': '1' })
  assert.deepEqual(rows.map((row) => row.key), ['a-head', 'z-head'])
  assert.equal(rows[1].name, 'Z-Head', 'display name comes from current when both sides have it')
  assert.equal(compareHeaders({}, { 'X-Only': '1' })[0].name, 'X-Only', 'falls back to the selected spelling')
})

test('compareHeaders handles empty sides', () => {
  assert.deepEqual(compareHeaders({}, {}), [])
  assert.deepEqual(compareHeaders({ A: '1' }, {}), [
    { key: 'a', name: 'A', current: '1', selected: '', change: 'removed' }
  ])
  assert.equal(compareHeaders({}, { A: '1' })[0].change, 'added')
})

test('compareJsonStructure reports added, removed and type-changed top-level keys', () => {
  assert.deepEqual(compareJsonStructure('{"a":1,"b":2}', '{"a":"x","c":3}'), {
    available: true, root: 'object → object', added: ['c'], removed: ['b'], changed: ['a']
  })
  const many = compareJsonStructure('{"z":1,"a":1}', '{"y":1,"b":1}')
  assert.deepEqual(many.added, ['b', 'y'], 'key lists are sorted')
  assert.deepEqual(many.removed, ['a', 'z'])
})

test('compareJsonStructure compares types, not values, so a value-only edit reports no change', () => {
  // Deliberate — it is a *structure* diff, and lineDiff already covers value changes. Pinned
  // because it is the single most surprising thing about this function.
  assert.deepEqual(compareJsonStructure('{"a":1}', '{"a":2}').changed, [])
  assert.deepEqual(compareJsonStructure('{"a":null}', '{"a":1}').changed, ['a'], 'null is its own type')
  assert.deepEqual(compareJsonStructure('{"a":[]}', '{"a":{}}').changed, ['a'], 'array is distinguished from object')
})

test('compareJsonStructure marks itself unavailable when either side is not JSON', () => {
  const invalid = { available: false, reason: 'invalid JSON', added: [], removed: [], changed: [] }
  assert.deepEqual(compareJsonStructure('nope', '{}'), invalid)
  assert.deepEqual(compareJsonStructure('{}', 'nope'), invalid)
  assert.deepEqual(compareJsonStructure('', ''), invalid, 'empty bodies are invalid JSON')
  assert.deepEqual(compareJsonStructure('{unclosed', 'also bad'), invalid)
})

test('compareJsonStructure refuses bodies over the 1 MB full-render limit', () => {
  const huge = `"${'x'.repeat(fullRenderLimit)}"`
  assert.ok(huge.length > fullRenderLimit)
  assert.equal(compareJsonStructure(huge, '{}').reason, 'too large')
  assert.equal(compareJsonStructure('{}', huge).reason, 'too large')
  assert.equal(compareJsonStructure(huge, '{}').available, false)
  // Left-hand reason wins when both sides are unusable for different reasons.
  assert.equal(compareJsonStructure(huge, 'nope').reason, 'too large')
  assert.equal(compareJsonStructure('nope', huge).reason, 'invalid JSON')
})

test('compareJsonStructure reports the root type pair without key lists for non-objects', () => {
  for (const [left, right, root] of [
    ['[1]', '{}', 'array → object'],
    ['null', 'null', 'null → null'],
    ['1', '"a"', 'number → string'],
    ['{"a":1}', '[]', 'object → array'],
    ['true', '{}', 'boolean → object']
  ]) {
    const shape = compareJsonStructure(left, right)
    assert.equal(shape.root, root, `${left} vs ${right}`)
    assert.equal(shape.available, true)
    assert.deepEqual([shape.added, shape.removed, shape.changed], [[], [], []])
  }
})
