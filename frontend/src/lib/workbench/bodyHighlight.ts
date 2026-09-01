// Syntax colouring for the RESPONSE body.
//
// The request body has been painted by CodeMirror since M4; the response body
// was a bare `<pre>`. So the same JSON document was legible while you were
// writing it and a wall of grey the moment the server sent it back — the one
// place a user actually reads a payload rather than types it.
//
// The obvious fix — mount a second, read-only CodeMirror over the response —
// was rejected for three reasons, all of which this module preserves instead:
//
//   1. The response pane already owns a byte-budget story (`automaticPreviewLimit`,
//      `fullRenderLimit`, Load more, Render full). CodeMirror would bring its own
//      viewport virtualisation and the two would have to be reconciled.
//   2. The response pane already has a find bar that paints `<mark>` over the
//      rendered text. CodeMirror's search is a different mechanism with a
//      different look, and this app is trying to have FEWER search UIs, not more.
//   3. CodeMirror is a deferred ~11-package chunk (see vite.config.ts). Making
//      the response pane depend on it would drag that chunk onto the path of
//      every single request, defeating US-036.
//
// So this is a small scanner that produces flat, already-merged segments: the
// token colour and the search-match flag on the same span, so the template can
// stay a single `{#each}` and the two features cannot fight over the same
// character range.
//
// THE COLOURS ARE NOT IN THIS FILE, for exactly the reason given at the top of
// syntaxHighlight.ts: they are `var(--syntax-*)`, defined per theme in
// style.css, so the response body lands in Nord's palette under Nord and
// Catppuccin's under Catppuccin — and, more to the point, matches the request
// editor sitting directly above it in every one of the nine themes.

/**
 * The token kinds this scanner emits.
 *
 * Deliberately a subset of the CodeMirror tag set: a response body is JSON,
 * XML or plain text, so there is nothing here for control keywords or class
 * names. `plain` carries whitespace and anything unrecognised, and is the one
 * kind that gets no colour at all.
 */
export type TokenKind =
  | 'plain'
  | 'key'
  | 'string'
  | 'number'
  | 'boolean'
  | 'null'
  | 'punctuation'
  | 'tag'
  | 'bracket'
  | 'attr-name'
  | 'attr-value'
  | 'meta'
  | 'comment'

/**
 * Each kind's colour token, and the lezer tag `liteApiHighlightStyle` routes to
 * the same one.
 *
 * This table is the anti-drift device. The alternative proposed during review
 * was to mount a read-only CodeMirror over the response so the palettes could
 * not diverge — correct about the risk, expensive about the remedy: it would
 * put the deferred ~11-package editor chunk on the path of every response,
 * bring a second search UI into an app that is trying to have one, and leave
 * two independent answers to "how much of this body is rendered".
 *
 * So the risk is closed by a test instead. `bodyHighlight.test.mts` asserts
 * every entry below names a token that `syntaxTokenNames` declares and that
 * `syntaxHighlight.ts` really does paint the paired tag with. A future edit
 * that recolours JSON keys in the editor and not here fails the suite rather
 * than shipping a response pane in last month's palette.
 *
 * Two pairings are worth reading twice, because both look like mistakes:
 *
 *   `null` → `--syntax-boolean`. CodeMirror groups `tags.bool` and `tags.null`
 *   into one rule; they are the same column of a JSON document. The kinds stay
 *   separate here only so the scanner's output is legible in tests.
 *
 *   `bracket` → `--syntax-tag`. `tags.angleBracket` is grouped with
 *   `tags.tagName`, so `<` and `>` take the TAG colour, not the punctuation
 *   colour. JSON's braces and commas are `tags.bracket`/`tags.punctuation` and
 *   do take the punctuation colour — which is why these need separate kinds.
 */
export const syntaxTokenForKind: Record<Exclude<TokenKind, 'plain'>, { token: string; tag: string }> = {
  key: { token: '--syntax-key', tag: 'propertyName' },
  string: { token: '--syntax-string', tag: 'string' },
  number: { token: '--syntax-number', tag: 'number' },
  boolean: { token: '--syntax-boolean', tag: 'bool' },
  null: { token: '--syntax-boolean', tag: 'null' },
  punctuation: { token: '--syntax-punctuation', tag: 'punctuation' },
  tag: { token: '--syntax-tag', tag: 'tagName' },
  bracket: { token: '--syntax-tag', tag: 'angleBracket' },
  'attr-name': { token: '--syntax-key', tag: 'attributeName' },
  'attr-value': { token: '--syntax-string', tag: 'attributeValue' },
  meta: { token: '--syntax-meta', tag: 'meta' },
  comment: { token: '--syntax-comment', tag: 'comment' }
}

export type HighlightLanguage = 'json' | 'xml' | 'plain'

export type TokenRange = { from: number; to: number; kind: TokenKind }

/**
 * One rendered span.
 *
 * `match` and `matchIndex` are carried alongside the token kind rather than
 * layered over it, because a search hit routinely lands in the MIDDLE of a
 * string token — `"created_at"` searched for `ate` — and two independent
 * passes would each want to own that range.
 */
export type HighlightSegment = { text: string; kind: TokenKind; match: boolean; matchIndex: number }

/**
 * The scanning budget, in UTF-16 code units.
 *
 * Above this the body renders unpainted rather than slowly. The response pane's
 * default preview is `automaticPreviewLimit` (128 KB) and its hard ceiling is
 * `fullRenderLimit` (1 MB), so this sits between them on purpose: everything
 * shown by default is coloured, and a user who presses "Render full" on a
 * multi-megabyte payload gets it fast and grey instead of coloured and janky.
 *
 * The cost that matters is not the scan — it is that every segment becomes a
 * DOM node, so a 1 MB JSON body would be well over a hundred thousand spans.
 */
export const highlightBudget = 256 * 1024

/**
 * The ceiling on rendered segments.
 *
 * The byte budget above bounds the SCAN; it does not bound the output, and
 * those are not the same thing. Review measured the ratio: a realistic 128 KB
 * JSON response yields about 0.29 segments per byte, but a pathological body —
 * alternating single digits and commas — reaches 1.0, which at the byte budget
 * is roughly 262,000 DOM nodes in one `<pre>`. That is a frozen window, not a
 * slow one.
 *
 * So the segment count is bounded directly. Past this the body renders
 * unpainted, exactly as it does past the byte budget: the text, the find bar
 * and the byte counts are all still correct, only the colour is gone.
 */
export const maxHighlightSegments = 40_000

const jsonPunctuation = new Set(['{', '}', '[', ']', ',', ':'])

/**
 * Picks the scanner from the response's content type, falling back to sniffing.
 *
 * Mirrors `formatResponseBody`'s rule so the pane cannot pretty-print a body as
 * JSON and then colour it as plain text, which would look like the highlighter
 * had failed.
 */
export function highlightLanguage(contentType: string, body: string): HighlightLanguage {
  const type = contentType.toLowerCase()
  // The JSON test comes first and ignores the content type entirely, because
  // that is exactly what `formatResponseBody` does. The first version of this
  // only body-sniffed when the content type was EMPTY, which review caught: a
  // JSON body served as `text/plain` — which plenty of APIs do — got
  // pretty-printed as JSON by the formatter and then painted as plain text by
  // this, so it arrived neatly indented and completely grey. The one payload
  // shape the feature exists for, rendered as though the feature were off.
  //
  // The rule now: whatever `formatResponseBody` decided this document IS, this
  // colours it as. `bodyHighlight.test.mts` asserts the two agree rather than
  // trusting the duplication to stay in step.
  if (type.includes('json') || /^[\s\n]*[{[]/.test(body)) return 'json'
  if (type.includes('xml') || type.includes('html')) return 'xml'
  if (!type && /^[\s\n]*</.test(body)) return 'xml'
  return 'plain'
}

/** Scans a JSON document into coloured ranges. */
export function tokenizeJson(text: string): TokenRange[] {
  const ranges: TokenRange[] = []
  const length = text.length
  let index = 0
  while (index < length) {
    const character = text[index]
    if (character === '"') {
      const end = scanJsonString(text, index)
      // A string is a KEY when the next non-whitespace character is a colon.
      // Without this every JSON document is one colour on both sides of the
      // colon, which is the single distinction a reader scanning a payload
      // actually uses.
      let lookahead = end
      while (lookahead < length && isWhitespace(text[lookahead])) lookahead += 1
      ranges.push({ from: index, to: end, kind: text[lookahead] === ':' ? 'key' : 'string' })
      index = end
      continue
    }
    if (character === '-' || (character >= '0' && character <= '9')) {
      const end = scanJsonNumber(text, index)
      // `end === index` means the run held no digit at all, so it is not a
      // number; fall through and let it be plain rather than looping forever.
      if (end > index) {
        ranges.push({ from: index, to: end, kind: 'number' })
        index = end
        continue
      }
    }
    if (text.startsWith('true', index) || text.startsWith('false', index)) {
      const end = index + (text[index] === 't' ? 4 : 5)
      ranges.push({ from: index, to: end, kind: 'boolean' })
      index = end
      continue
    }
    if (text.startsWith('null', index)) {
      ranges.push({ from: index, to: index + 4, kind: 'null' })
      index += 4
      continue
    }
    if (jsonPunctuation.has(character)) {
      ranges.push({ from: index, to: index + 1, kind: 'punctuation' })
      index += 1
      continue
    }
    index += 1
  }
  return ranges
}

function isWhitespace(character: string) {
  return character === ' ' || character === '\n' || character === '\r' || character === '\t'
}

/**
 * Returns the offset just past a JSON string literal.
 *
 * Escape-aware, and that is not pedantry: a body containing `"path": "C:\\"`
 * would otherwise be read as an unterminated string and every remaining token
 * in the document would take the wrong colour.
 */
function scanJsonString(text: string, start: number) {
  let index = start + 1
  while (index < text.length) {
    const character = text[index]
    if (character === '\\') {
      index += 2
      continue
    }
    if (character === '"') return index + 1
    index += 1
  }
  return text.length
}

/**
 * Returns the offset just past a JSON number, or `start` if there is no number.
 *
 * The permissive character class is deliberate — a truncated body ends
 * mid-number and `1.2.3` should still read as a number rather than derailing
 * everything after it. But review found it accepted runs with NO digit at all:
 * `----` and `-e-e-e` each scanned as a single number token. Returning `start`
 * for those hands the character back to the main loop, which treats it as
 * plain, so a malformed body stops claiming to contain numbers it does not.
 */
function scanJsonNumber(text: string, start: number) {
  let index = start
  if (text[index] === '-') index += 1
  let digits = 0
  while (index < text.length && /[0-9.eE+-]/.test(text[index])) {
    if (text[index] >= '0' && text[index] <= '9') digits += 1
    index += 1
  }
  return digits > 0 ? index : start
}

/** Scans an XML or HTML document into coloured ranges. */
export function tokenizeXml(text: string): TokenRange[] {
  const ranges: TokenRange[] = []
  const length = text.length
  let index = 0
  while (index < length) {
    const open = text.indexOf('<', index)
    if (open < 0) break
    if (text.startsWith('<!--', open)) {
      const close = text.indexOf('-->', open)
      const end = close < 0 ? length : close + 3
      ranges.push({ from: open, to: end, kind: 'comment' })
      index = end
      continue
    }
    // CDATA is checked BEFORE the general `<!` case, because its content is
    // literal text that may contain `>` and whole tags. Terminating on the
    // first `>` — which is what the general branch does — ended the section
    // early, so `<![CDATA[<b>bold</b>]]>` had its literal `</b>` painted as a
    // real closing tag and its actual `]]>` terminator left as plain text.
    if (text.startsWith('<![CDATA[', open)) {
      const close = text.indexOf(']]>', open)
      const end = close < 0 ? length : close + 3
      ranges.push({ from: open, to: end, kind: 'meta' })
      index = end
      continue
    }
    if (text.startsWith('<?', open) || text.startsWith('<!', open)) {
      const close = text.indexOf('>', open)
      const end = close < 0 ? length : close + 1
      ranges.push({ from: open, to: end, kind: 'meta' })
      index = end
      continue
    }
    const close = text.indexOf('>', open)
    const end = close < 0 ? length : close + 1
    ranges.push(...tokenizeXmlTag(text, open, end))
    index = end
  }
  return ranges
}

function tokenizeXmlTag(text: string, from: number, to: number): TokenRange[] {
  const ranges: TokenRange[] = []
  const closing = text[from + 1] === '/'
  const openEnd = from + (closing ? 2 : 1)
  ranges.push({ from, to: openEnd, kind: 'bracket' })

  let index = openEnd
  while (index < to && /[^\s/>]/.test(text[index])) index += 1
  if (index > openEnd) ranges.push({ from: openEnd, to: index, kind: 'tag' })

  while (index < to) {
    const character = text[index]
    if (isWhitespace(character)) {
      index += 1
      continue
    }
    if (character === '>' || character === '/') {
      ranges.push({ from: index, to: to, kind: 'bracket' })
      return ranges
    }
    if (character === '"' || character === "'") {
      const quoteEnd = findQuoteEnd(text, index, to, character)
      ranges.push({ from: index, to: quoteEnd, kind: 'attr-value' })
      index = quoteEnd
      continue
    }
    if (character === '=') {
      ranges.push({ from: index, to: index + 1, kind: 'punctuation' })
      index += 1
      continue
    }
    // An unquoted value — `<div class=box>`, which HTML allows and real
    // responses contain — used to fall into this name branch and take the KEY
    // colour, so `class` and `box` were painted identically. What decides it is
    // what came immediately before: a value follows an `=`.
    const nameStart = index
    while (index < to && /[^\s=/>]/.test(text[index])) index += 1
    if (index > nameStart) {
      const previous = ranges[ranges.length - 1]
      const afterEquals = previous?.kind === 'punctuation' && text[previous.from] === '='
      ranges.push({ from: nameStart, to: index, kind: afterEquals ? 'attr-value' : 'attr-name' })
    } else index += 1
  }
  return ranges
}

function findQuoteEnd(text: string, start: number, limit: number, quote: string) {
  const close = text.indexOf(quote, start + 1)
  return close < 0 || close >= limit ? limit : close + 1
}

/**
 * Builds the rendered segments for a body: token colours and search hits, merged.
 *
 * `matches` is `findMatches`' output — the start offset of each hit — and
 * `queryLength` its length, kept as separate arguments so this stays a pure
 * function over the same data the existing find bar already computes rather
 * than re-running the search.
 *
 * Returns one flat, gap-free list covering exactly `text`, so the template can
 * render it without needing to know that either feature exists.
 */
export function highlightSegments(
  text: string,
  language: HighlightLanguage,
  matches: number[] = [],
  queryLength = 0
): HighlightSegment[] {
  if (!text) return []
  // Over budget the body still RENDERS, and the find bar still works — only the
  // colour is dropped. Returning [] here would blank the response.
  const tokens = text.length > highlightBudget || language === 'plain'
    ? []
    : language === 'json' ? tokenizeJson(text) : tokenizeXml(text)

  const boundaries = collectBoundaries(text.length, tokens, matches, queryLength)
  // Checked BEFORE building, not after: the point is to never allocate the
  // 262,000-element array in the first place. `boundaries` is one number per
  // edge and is a close upper bound on the segment count.
  if (boundaries.length - 1 > maxHighlightSegments) {
    return [{ text, kind: 'plain', match: false, matchIndex: -1 }]
  }
  const segments: HighlightSegment[] = []
  let tokenCursor = 0
  let matchCursor = 0
  for (let index = 0; index + 1 < boundaries.length; index += 1) {
    const from = boundaries[index]
    const to = boundaries[index + 1]
    if (to <= from) continue
    while (tokenCursor < tokens.length && tokens[tokenCursor].to <= from) tokenCursor += 1
    while (matchCursor < matches.length && matches[matchCursor] + queryLength <= from) matchCursor += 1
    const token = tokens[tokenCursor]
    const kind = token && token.from <= from && token.to >= to ? token.kind : 'plain'
    const matchStart = matches[matchCursor]
    const match = queryLength > 0 && matchStart !== undefined && matchStart <= from && matchStart + queryLength >= to
    const chunk = text.slice(from, to)
    // Coalesce runs of the same kind. `collectBoundaries` cuts at every token
    // edge, so pretty-printed JSON puts each indent between two tokens in its
    // own `plain` segment — and every segment becomes a DOM node. Merging is
    // purely a rendering economy: the joined text is identical, so the
    // reconstruction guarantee is untouched.
    const last = segments[segments.length - 1]
    if (last && last.kind === kind && last.match === match && last.matchIndex === (match ? matchCursor : -1)) {
      last.text += chunk
      continue
    }
    segments.push({ text: chunk, kind, match, matchIndex: match ? matchCursor : -1 })
  }
  return segments
}

/**
 * The union of every range edge, so no segment straddles a token or match edge.
 *
 * Merging by cutting at boundaries rather than by nesting one pass inside the
 * other is what lets a search hit sit inside a string literal and keep both the
 * string colour and the highlight.
 */
function collectBoundaries(length: number, tokens: TokenRange[], matches: number[], queryLength: number) {
  const edges = new Set<number>([0, length])
  for (const token of tokens) {
    if (token.from > 0 && token.from < length) edges.add(token.from)
    if (token.to > 0 && token.to < length) edges.add(token.to)
  }
  if (queryLength > 0) {
    for (const start of matches) {
      if (start > 0 && start < length) edges.add(start)
      const end = start + queryLength
      if (end > 0 && end < length) edges.add(end)
    }
  }
  return [...edges].sort((left, right) => left - right)
}
