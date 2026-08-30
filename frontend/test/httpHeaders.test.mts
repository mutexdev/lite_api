import assert from 'node:assert/strict'
import test from 'node:test'

import {
  COMMON_REQUEST_HEADER_NAMES,
  HEADER_SUGGESTION_LIMIT,
  REQUEST_HEADER_NAMES,
  moveSuggestionIndex,
  suggestHeaderNames,
  suggestHeaderValues
} from '../src/lib/httpHeaders.ts'

test('a prefix match outranks a match found later in the name', () => {
  const suggestions = suggestHeaderNames('cont')
  assert.equal(suggestions[0], 'Content-Disposition')
  assert.ok(suggestions.includes('Content-Type'))
  const byType = suggestHeaderNames('type')
  assert.deepEqual(byType, ['Content-Type'])
})

test('matching ignores case, because nobody types header names in canonical case', () => {
  assert.ok(suggestHeaderNames('AUTHOR').includes('Authorization'))
  assert.ok(suggestHeaderNames('authoriz').includes('Authorization'))
})

test('the canonical spelling is what gets inserted', () => {
  assert.deepEqual(suggestHeaderNames('if-none'), ['If-None-Match'])
  assert.deepEqual(suggestHeaderNames('x-api'), ['X-API-Key'])
  assert.deepEqual(suggestHeaderNames('idempot'), ['Idempotency-Key'])
})

test('an empty cell offers the headers a request usually needs', () => {
  assert.deepEqual(suggestHeaderNames(''), [...COMMON_REQUEST_HEADER_NAMES])
})

test('headers already in the table are not offered again', () => {
  const suggestions = suggestHeaderNames('', ['content-type', 'Accept'])
  assert.ok(!suggestions.includes('Content-Type'))
  assert.ok(!suggestions.includes('Accept'))
  assert.ok(suggestions.includes('Authorization'))
})

test('the caller passes the other rows, so the row being edited still matches', () => {
  assert.ok(suggestHeaderNames('Accept-L', ['Authorization']).includes('Accept-Language'))
  assert.ok(!suggestHeaderNames('Accept-L', ['Accept-Language']).includes('Accept-Language'))
})

test('a name typed out in full offers nothing further', () => {
  // Authorization is a prefix of nothing, but it is a substring of
  // Proxy-Authorization: without this rule, finishing the word would leave a
  // popup open whose Enter key replaced what the user meant.
  assert.deepEqual(suggestHeaderNames('Authorization'), [])
  assert.deepEqual(suggestHeaderNames('authorization'), [])
  assert.deepEqual(suggestHeaderValues('Cache-Control', 'no-cache'), [])
})

test('the list is capped so it stays quicker to read than to type', () => {
  assert.ok(suggestHeaderNames('a').length <= HEADER_SUGGESTION_LIMIT)
  assert.equal(suggestHeaderNames('a', [], 3).length, 3)
})

test('nothing is offered for a name that matches no known header', () => {
  assert.deepEqual(suggestHeaderNames('zzzzz'), [])
})

test('values are offered for the headers whose values are worth remembering', () => {
  assert.ok(suggestHeaderValues('Content-Type', '').includes('application/json'))
  assert.ok(suggestHeaderValues('content-type', 'json').includes('application/json'))
  assert.deepEqual(suggestHeaderValues('Authorization', 'Bea'), ['Bearer '])
  assert.ok(suggestHeaderValues('Cache-Control', 'no').includes('no-cache'))
})

test('no values are invented for headers that have none worth suggesting', () => {
  assert.deepEqual(suggestHeaderValues('Host', ''), [])
  assert.deepEqual(suggestHeaderValues('X-Trace-Id', 'a'), [])
})

test('only request headers are offered', () => {
  for (const responseOnly of ['Set-Cookie', 'Age', 'ETag', 'Location', 'Server']) {
    assert.ok(
      !REQUEST_HEADER_NAMES.includes(responseOnly),
      `${responseOnly} is a response header and should not be offered in a request editor`
    )
  }
})

test('the header list has no duplicates and no blank entries', () => {
  const lowered = REQUEST_HEADER_NAMES.map((name) => name.toLowerCase())
  assert.equal(new Set(lowered).size, lowered.length)
  assert.ok(REQUEST_HEADER_NAMES.every((name) => name.trim() !== ''))
})

test('every commonly offered header is a real entry in the full list', () => {
  for (const name of COMMON_REQUEST_HEADER_NAMES) {
    assert.ok(REQUEST_HEADER_NAMES.includes(name), `${name} is offered but not in the list`)
  }
})

test('moveSuggestionIndex wraps at both ends and copes with an empty list', () => {
  assert.equal(moveSuggestionIndex(-1, 1, 3), 0)
  assert.equal(moveSuggestionIndex(-1, -1, 3), 2)
  assert.equal(moveSuggestionIndex(2, 1, 3), 0)
  assert.equal(moveSuggestionIndex(0, -1, 3), 2)
  assert.equal(moveSuggestionIndex(0, 1, 0), -1)
})
