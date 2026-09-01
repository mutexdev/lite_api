// The response body's syntax colouring.
//
// The scanner is deliberately not a parser: it never rejects a document. A
// response body is whatever the server sent, including truncated JSON and
// malformed XML, and a highlighter that threw or bailed on those would remove
// the colour from precisely the payloads a user is squinting at.
import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import {
  highlightBudget,
  highlightLanguage,
  highlightSegments,
  syntaxTokenForKind,
  tokenizeJson,
  tokenizeXml
} from '../src/lib/workbench/bodyHighlight.ts'
import { syntaxTokenNames } from '../src/lib/workbench/syntaxHighlight.ts'
import { formatResponseBody, contentType as contentTypeOf } from '../src/lib/workbench/response.ts'

const read = (relative: string) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const kinds = (text: string, language: 'json' | 'xml' | 'plain' = 'json') =>
  highlightSegments(text, language).filter((segment) => segment.text.trim()).map((segment) => [segment.text, segment.kind])

test('a JSON key is coloured differently from a JSON string value', () => {
  const tokens = tokenizeJson('{"name": "Ada"}')
  const named = tokens.map((token) => token.kind)
  assert.ok(named.includes('key'), 'the left-hand side must be a key')
  assert.ok(named.includes('string'), 'the right-hand side must be a string')
  const key = tokens.find((token) => token.kind === 'key')
  const value = tokens.find((token) => token.kind === 'string')
  assert.equal('{"name": "Ada"}'.slice(key!.from, key!.to), '"name"')
  assert.equal('{"name": "Ada"}'.slice(value!.from, value!.to), '"Ada"')
})

test('a key is still a key when the colon is on the next line', () => {
  // Pretty-printed bodies from other tools do this, and a lookahead that only
  // checked the immediately next character would demote every such key.
  const tokens = tokenizeJson('{"name"\n  : "Ada"}')
  assert.equal(tokens[1].kind, 'key')
})

test('numbers, booleans and null each get their own kind', () => {
  assert.deepEqual(kinds('{"a":1,"b":true,"c":false,"d":null}'), [
    ['{', 'punctuation'],
    ['"a"', 'key'],
    [':', 'punctuation'],
    ['1', 'number'],
    [',', 'punctuation'],
    ['"b"', 'key'],
    [':', 'punctuation'],
    ['true', 'boolean'],
    [',', 'punctuation'],
    ['"c"', 'key'],
    [':', 'punctuation'],
    ['false', 'boolean'],
    [',', 'punctuation'],
    ['"d"', 'key'],
    [':', 'punctuation'],
    ['null', 'null'],
    ['}', 'punctuation']
  ])
})

test('an escaped quote does not end the string', () => {
  // Without escape awareness the closing quote of "C:\\" reads as the OPENING
  // quote of the next string and every colour after it is shifted by one.
  const body = '{"path": "C:\\\\", "ok": true}'
  const tokens = tokenizeJson(body)
  const strings = tokens.filter((token) => token.kind === 'string')
  assert.equal(strings.length, 1)
  assert.equal(body.slice(strings[0].from, strings[0].to), '"C:\\\\"')
  assert.equal(tokens.at(-2)?.kind, 'boolean')
})

test('an escaped backslash before the closing quote still terminates', () => {
  const tokens = tokenizeJson('{"a": "x\\\\"}')
  assert.equal(tokens.filter((token) => token.kind === 'string').length, 1)
})

test('a truncated JSON body is still coloured up to the cut', () => {
  // Truncation is the norm here: the pane renders a bounded prefix of large
  // responses, so the scanner is handed invalid JSON on purpose.
  const segments = highlightSegments('{"name": "Ada", "age": 3', 'json')
  assert.equal(segments.map((segment) => segment.text).join(''), '{"name": "Ada", "age": 3')
  assert.ok(segments.some((segment) => segment.kind === 'number'))
})

test('an unterminated string does not throw or lose the rest of the document', () => {
  const segments = highlightSegments('{"name": "Ada', 'json')
  assert.equal(segments.map((segment) => segment.text).join(''), '{"name": "Ada')
})

test('XML tag names, attribute names and attribute values are distinguished', () => {
  const tokens = tokenizeXml('<user id="7">Ada</user>')
  const source = '<user id="7">Ada</user>'
  const byKind = (kind: string) => tokens.filter((token) => token.kind === kind).map((token) => source.slice(token.from, token.to))
  assert.deepEqual(byKind('tag'), ['user', 'user'])
  assert.deepEqual(byKind('attr-name'), ['id'])
  assert.deepEqual(byKind('attr-value'), ['"7"'])
})

test('an XML declaration and a comment are meta and comment, not tags', () => {
  const tokens = tokenizeXml('<?xml version="1.0"?><!-- note --><a/>')
  assert.equal(tokens[0].kind, 'meta')
  assert.equal(tokens[1].kind, 'comment')
})

test('XML text content between tags is left plain', () => {
  assert.deepEqual(kinds('<a>hello</a>', 'xml').find((entry) => entry[0] === 'hello'), ['hello', 'plain'])
})

test('segments always reconstruct the input exactly', () => {
  // The template renders these and nothing else, so any gap or duplication here
  // is a silently corrupted response body rather than a wrong colour.
  for (const body of ['{"a":[1,2,{"b":null}]}', '<a b="c">d</a>', 'plain text', '', '   ', '{"unicode":"héllo → ✓"}']) {
    for (const language of ['json', 'xml', 'plain'] as const) {
      assert.equal(highlightSegments(body, language).map((segment) => segment.text).join(''), body)
    }
  }
})

test('a search hit inside a string keeps the string colour and gains the mark', () => {
  // This is the whole reason the two passes are merged rather than layered:
  // "ate" lands in the middle of "created_at", and neither feature may win.
  const body = '{"created_at": 1}'
  const segments = highlightSegments(body, 'json', [body.indexOf('ate')], 3)
  const marked = segments.filter((segment) => segment.match)
  assert.equal(marked.length, 1)
  assert.equal(marked[0].text, 'ate')
  assert.equal(marked[0].kind, 'key')
  assert.equal(segments.map((segment) => segment.text).join(''), body)
})

test('a search hit spanning a token boundary keeps one match ordinal', () => {
  // `": ` crosses the key's closing quote, the colon and the space. It must
  // still count as match number 0, or Next/Previous would skip past it.
  const body = '{"a": 1}'
  const segments = highlightSegments(body, 'json', [body.indexOf('": ')], 3)
  const marked = segments.filter((segment) => segment.match)
  assert.ok(marked.length > 1, 'the hit is expected to be split by token edges')
  assert.deepEqual([...new Set(marked.map((segment) => segment.matchIndex))], [0])
  assert.equal(marked.map((segment) => segment.text).join(''), '": ')
})

test('match ordinals count matches, not segments', () => {
  const body = '{"a":1,"a":2}'
  const segments = highlightSegments(body, 'json', [1, 7], 3)
  const ordinals = [...new Set(segments.filter((segment) => segment.match).map((segment) => segment.matchIndex))]
  assert.deepEqual(ordinals, [0, 1])
})

test('an empty query marks nothing', () => {
  const segments = highlightSegments('{"a":1}', 'json', [], 0)
  assert.equal(segments.some((segment) => segment.match), false)
  assert.deepEqual([...new Set(segments.map((segment) => segment.matchIndex))], [-1])
})

test('a body over the budget renders unpainted rather than slowly', () => {
  const body = `{"a":"${'x'.repeat(highlightBudget)}"}`
  const segments = highlightSegments(body, 'json')
  assert.equal(segments.length, 1)
  assert.equal(segments[0].kind, 'plain')
  assert.equal(segments[0].text, body)
})

test('a body exactly at the budget is still painted', () => {
  const filler = 'x'.repeat(highlightBudget - '{"a":""}'.length)
  const body = `{"a":"${filler}"}`
  assert.equal(body.length, highlightBudget)
  assert.ok(highlightSegments(body, 'json').some((segment) => segment.kind === 'key'))
})

test('the language follows the content type, then the body', () => {
  assert.equal(highlightLanguage('application/json; charset=utf-8', ''), 'json')
  assert.equal(highlightLanguage('application/vnd.api+json', ''), 'json')
  assert.equal(highlightLanguage('text/xml', ''), 'xml')
  assert.equal(highlightLanguage('text/html', ''), 'xml')
  // NOT 'plain'. formatResponseBody pretty-prints this as JSON regardless of
  // the content type, so painting it plain produced neatly-indented grey text.
  assert.equal(highlightLanguage('text/plain', '{"a":1}'), 'json')
  assert.equal(highlightLanguage('text/plain', 'hello'), 'plain')
  assert.equal(highlightLanguage('', '  {"a":1}'), 'json')
  assert.equal(highlightLanguage('', '  <a/>'), 'xml')
  assert.equal(highlightLanguage('', 'hello'), 'plain')
})

test('a plain-text body is one unpainted segment', () => {
  const segments = highlightSegments('hello world', 'plain')
  assert.deepEqual(segments, [{ text: 'hello world', kind: 'plain', match: false, matchIndex: -1 }])
})

// --- the anti-drift contract with the request editor ----------------------
//
// The response pane paints itself rather than mounting a read-only CodeMirror.
// That buys a lot (no editor chunk on the response path, one search UI, one
// answer to "how much is rendered") and costs exactly one thing: the two
// palettes could drift apart. These four tests are the price paid for it, and
// they are why the trade is safe.

test('every response token names a declared syntax token', () => {
  // An undefined custom property makes the whole `color:` declaration invalid
  // and the text silently falls back to the body colour — the exact failure
  // syntaxHighlight.test.mts was written to catch in the editor.
  for (const [kind, mapping] of Object.entries(syntaxTokenForKind)) {
    assert.ok(
      (syntaxTokenNames as readonly string[]).includes(mapping.token),
      `${kind} maps to ${mapping.token}, which syntaxTokenNames does not declare`
    )
  }
})

test('every response token is painted the same colour the editor paints its tag', () => {
  // Reads syntaxHighlight.ts as source for the same reason that file's own test
  // does: there is no component-rendering harness, and the pairing has to be
  // checked against what the editor actually declares rather than a copy of it.
  const source = read('../src/lib/workbench/syntaxHighlight.ts')
  const rules = [...source.matchAll(/\{\s*tag:\s*(\[[^\]]*\]|[^,]+),\s*color:\s*'var\((--syntax-[a-z]+)\)'/g)]
    // A MODIFIED tag is a different tag. `tags.function(tags.propertyName)`
    // paints a property whose value is a function with the function colour, and
    // it lives in a different rule from bare `tags.propertyName`. Counted
    // naively, every JSON key would look like it had two conflicting colours;
    // dropping the wrapped forms leaves only the rules that can actually claim
    // the bare tag a response body produces.
    .map((match) => ({ tags: match[1].replace(/tags\.\w+\(\s*tags\.\w+\s*\)/g, ''), token: match[2] }))
  assert.ok(rules.length > 5, 'the rule scrape found too little to be trusted')

  for (const [kind, mapping] of Object.entries(syntaxTokenForKind)) {
    // `tags.bool` must not be matched by a rule listing `tags.boolean`, hence
    // the word boundary rather than a substring test.
    const owning = rules.filter((rule) => new RegExp(`tags\\.${mapping.tag}\\b`).test(rule.tags))
    assert.equal(owning.length, 1, `${mapping.tag} should be painted by exactly one editor rule, found ${owning.length}`)
    assert.equal(
      owning[0].token,
      mapping.token,
      `the editor paints tags.${mapping.tag} with ${owning[0].token} but the response body paints ${kind} with ${mapping.token}`
    )
  }
})

test('the response body never paints anything with the invalid colour', () => {
  // The rule from syntaxHighlight.ts, restated for the second surface that now
  // has a palette: --syntax-invalid is the only red, and it belongs to the
  // linter. A response body is data, not a diagnostic — a payload full of
  // strings must not read as a payload full of errors.
  const tokens = Object.values(syntaxTokenForKind).map((mapping) => mapping.token)
  assert.equal(tokens.includes('--syntax-invalid'), false)
})

test('the stylesheet defines a rule for every response token kind', () => {
  // The scanner can emit a kind the stylesheet has no class for, and the only
  // symptom would be one token type quietly rendering unstyled.
  const css = read('../src/style.css')
  for (const kind of Object.keys(syntaxTokenForKind)) {
    assert.match(css, new RegExp(`\\.response-token-${kind}\\b`), `style.css has no .response-token-${kind} rule`)
  }
})

// --- defects found while attacking the scanner (T1) ------------------------
//
// These document real bugs rather than fix them. See the session report for
// severity and the full reasoning; each test below currently FAILS against
// the shipped implementation and is left red on purpose.

test('BUG: highlightLanguage agrees with formatResponseBody\'s own JSON sniff', () => {
  // formatResponseBody pretty-prints ANY body that looks like JSON (starts
  // with `{`/`[`) regardless of content type -- see response.ts, the first
  // branch of the JSON `if` is `contentType.includes('json') || bodySniff`.
  // highlightLanguage only body-sniffs when the content-type header is
  // COMPLETELY EMPTY (`if (!type) { ... }`), so a real, common response --
  // JSON served with a wrong-but-present content type such as
  // "text/plain" -- gets pretty-printed as JSON and then coloured as
  // 'plain': every key, string, number and boolean renders with NO colour at
  // all, i.e. exactly the "wall of grey" this whole feature exists to fix.
  const headers = { 'Content-Type': 'text/plain' }
  const rawBody = '{"user":"ada","active":true,"count":42}'
  const pretty = formatResponseBody(rawBody, headers)
  assert.notEqual(pretty, rawBody, 'formatResponseBody was expected to pretty-print this as JSON')
  const language = highlightLanguage(contentTypeOf(headers), pretty)
  assert.equal(language, 'json', 'formatResponseBody pretty-printed this as JSON, so it must be coloured as JSON')
})

test('BUG: an unquoted XML/HTML attribute value is coloured as attr-name, not attr-value', () => {
  // tokenizeXmlTag only recognises a value after `=` when it is quoted. An
  // unquoted value (legal HTML: <div class=unquoted>) falls into the same
  // "scan a name" branch used for attribute NAMES, so the value is painted
  // with the key colour instead of the string colour.
  const source = '<div class=unquoted>'
  const tokens = tokenizeXml(source)
  const value = tokens.find((token) => source.slice(token.from, token.to) === 'unquoted')
  assert.equal(value?.kind, 'attr-value', `"unquoted" was tokenised as ${value?.kind}, not attr-value`)
})

test('a CDATA section is literal text, not markup', () => {
  // `<![CDATA[...]]>` is opened via the same `<!` branch as a doctype/comment,
  // which stops at the first `>` -- not at the CDATA terminator `]]>`. Any
  // markup-shaped substring inside the CDATA payload (e.g. `<b>`/`</b>` used
  // as a literal, escaped example in a payload) is then scanned as if it were
  // real XML, so escaped/literal content gets painted with tag colours.
  const source = '<root><![CDATA[<b>bold</b>]]></root>'
  const tokens = tokenizeXml(source)
  const insideCdata = tokens.filter((token) => token.from >= source.indexOf('[CDATA[') && token.to <= source.indexOf(']]>') + 3)
  assert.equal(insideCdata.some((token) => token.kind === 'tag'), false, 'a literal "<b>" inside CDATA was painted as a real tag')
})

test('a digit-free run of number punctuation is not a number', () => {
  // scanJsonNumber's character class is deliberately permissive so a truncated
  // body ending mid-number still colours — but it accepted runs with no digit
  // at all, so `----` scanned as one 'number' token. Found in review.
  for (const body of ['----', '-.e+-', '-e-e-e']) {
    const tokens = tokenizeJson(body)
    for (const token of tokens) {
      if (token.kind !== 'number') continue
      assert.ok(/[0-9]/.test(body.slice(token.from, token.to)), `"${body}" produced a digit-free number token`)
    }
  }
  // And the scanner must still terminate rather than spinning on a character
  // it declined to consume.
  assert.equal(highlightSegments('----', 'json').map((segment) => segment.text).join(''), '----')
})
