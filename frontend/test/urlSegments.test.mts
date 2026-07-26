// Splitting a URL into text, {{variables}}, {{?prompts}} and /:pathParams.
//
// The overlay renders these on top of the URL input, so a wrong split shows a
// highlight over the wrong characters or opens a tooltip pointing at an editor
// that does not own the value.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { urlVariableSegments, fallbackVariableTooltipInfo } from '../src/lib/urlSegments.ts'

const info = (name: string) => fallbackVariableTooltipInfo(name)
const texts = (segments: ReturnType<typeof urlVariableSegments>) => segments.map((s) => s.text)

test('plain text with no tokens is one segment', () => {
  const segments = urlVariableSegments('https://api.test/users', [])
  assert.equal(segments.length, 1)
  assert.deepEqual([segments[0].text, segments[0].variable, segments[0].prompt], ['https://api.test/users', false, false])
})

test('an empty URL produces no segments', () => {
  assert.deepEqual(urlVariableSegments('', []), [])
})

// The concatenated segments must reproduce the input exactly, or the overlay
// drifts out of alignment with the text underneath it.
test('segments reassemble into the original string', () => {
  for (const url of [
    'https://{{host}}/users/:id?q={{query}}',
    '{{host}}',
    'no tokens here',
    '{{a}}{{b}}',
    'https://api.test/{{?prompt}}/x'
  ]) {
    const segments = urlVariableSegments(url, [], [])
    assert.equal(texts(segments).join(''), url, url)
  }
})

test('a variable token is marked as a variable and carries its info', () => {
  const segments = urlVariableSegments('https://{{host}}/x', [info('host')])
  const variable = segments.find((s) => s.variable)
  assert.ok(variable)
  assert.equal(variable.text, '{{host}}')
  assert.equal(variable.name, 'host')
  assert.equal(variable.info.name, 'host')
})

// A {{?prompt}} is neither a variable nor plain text: it resolves at send time
// from a dialog, so it must not offer a variable tooltip.
test('a prompt token is marked prompt, not variable', () => {
  const segments = urlVariableSegments('https://api.test/{{?token}}', [])
  const prompt = segments.find((s) => s.prompt)
  assert.ok(prompt)
  assert.equal(prompt.variable, false)
  assert.equal(prompt.name, 'token', 'the leading ? is stripped from the reported name')
})

// Path parameters resolve from the request's own table rather than the scope
// chain, so mislabelling one points the tooltip at the wrong editor.
test('a /:name token is recognised only when path params are supplied', () => {
  const withParams = urlVariableSegments('https://api.test/users/:id', [], [
    { name: 'id', value: '42', enabled: true } as never
  ])
  const path = withParams.find((s) => s.variable && s.path)
  assert.ok(path, 'a path token should be found when the caller passes path params')
  assert.equal(path.name, 'id')
  assert.equal(path.info.scope, 'Path Param')
  assert.equal(path.info.resolvedValue, '42')

  // A header or body value can contain a colon that is not a parameter.
  const withoutParams = urlVariableSegments('https://api.test/users/:id', [])
  assert.equal(withoutParams.length, 1, 'without path params the colon is ordinary text')
  assert.equal(withoutParams[0].variable, false)
})

// Two occurrences of one variable must be distinct rows, or the keyed each
// reuses a node and the tooltip opens over the wrong one.
test('repeated tokens get distinct keys', () => {
  const segments = urlVariableSegments('{{host}}/a/{{host}}', [info('host')])
  const keys = segments.map((s) => s.key)
  assert.equal(new Set(keys).size, keys.length, `duplicate key in ${JSON.stringify(keys)}`)
})

test('adjacent tokens produce no empty text segment between them', () => {
  const segments = urlVariableSegments('{{a}}{{b}}', [info('a'), info('b')])
  assert.equal(segments.length, 2)
  assert.deepEqual(texts(segments), ['{{a}}', '{{b}}'])
})

test('text before and after tokens is preserved', () => {
  const segments = urlVariableSegments('pre{{a}}post', [info('a')])
  assert.deepEqual(texts(segments), ['pre', '{{a}}', 'post'])
})

// An unknown variable still renders as a variable, so the user can click it and
// define one — that is the point of the fallback.
test('an unresolved variable falls back rather than rendering as text', () => {
  const segments = urlVariableSegments('{{absent}}', [])
  const variable = segments.find((s) => s.variable)
  assert.ok(variable)
  assert.equal(variable.info.found, false)
  assert.equal(variable.info.editable, true, 'the tooltip offers to define it')
})

test('an invalid variable name is not editable', () => {
  const bad = fallbackVariableTooltipInfo('has space')
  assert.equal(bad.validName, false)
  assert.equal(bad.editable, false, 'nothing could be written that would resolve')
  assert.equal(bad.source, 'invalid')
})

test('whitespace inside a token is trimmed from the name but kept in the text', () => {
  const segments = urlVariableSegments('{{  host  }}', [info('host')])
  const variable = segments.find((s) => s.variable)
  assert.ok(variable)
  assert.equal(variable.name, 'host')
  assert.equal(variable.text, '{{  host  }}', 'the rendered text must match the input character for character')
})

test('a path token stops at the next separator', () => {
  const segments = urlVariableSegments('https://api.test/:id/posts', [], [])
  const path = segments.find((s) => s.variable && s.path)
  assert.ok(path)
  assert.equal(path.name, 'id')
  assert.equal(texts(segments).join(''), 'https://api.test/:id/posts')
})

test('a mixed URL splits every token kind', () => {
  const segments = urlVariableSegments('https://{{host}}/users/:id?t={{?tok}}', [info('host')], [])
  assert.equal(segments.filter((s) => s.variable && !s.path).length, 1)
  assert.equal(segments.filter((s) => s.variable && s.path).length, 1)
  assert.equal(segments.filter((s) => s.prompt).length, 1)
})
