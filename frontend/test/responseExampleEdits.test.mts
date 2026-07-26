// Editing a saved response example.
//
// An example is what "copy as curl" regenerates from and what the mock server
// answers with, so an edit that quietly drops a field produces a mock that
// behaves differently from the request that was saved.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  applyResponseExampleRequestField,
  applyResponseExampleFileRow,
  applyResponseExampleHeader,
  applyResponseExampleResponseField,
  removeResponseExampleFileRow
} from '../src/lib/responseExampleEdits.ts'

const req = (o: Record<string, unknown> = {}) => o as never

test('a plain field is stored as typed', () => {
  const next = applyResponseExampleRequestField(req(), 'url', 'https://api.test/x')
  assert.equal(next.url, 'https://api.test/x')
})

// "get" and "GET" are the same verb, and a lower-case one renders wrong
// everywhere it is shown.
test('the method is upper-cased', () => {
  assert.equal(applyResponseExampleRequestField(req(), 'method', 'post').method, 'POST')
  assert.equal(applyResponseExampleRequestField(req(), 'method', 'GeT').method, 'GET')
})

// The params panel and the URL must not be able to disagree about what is sent.
test('editing the url re-derives the query parameters', () => {
  const next = applyResponseExampleRequestField(req({ params: [] }), 'url', 'https://api.test/x?a=1&b=2')
  assert.deepEqual(next.params?.map((p) => [p.name, p.value]), [['a', '1'], ['b', '2']])
})

test('re-deriving params keeps rows the user disabled', () => {
  const existing = [{ name: 'off', value: 'x', enabled: false }] as never
  const next = applyResponseExampleRequestField(req({ params: existing }), 'url', 'https://api.test/x?a=1')
  assert.deepEqual(next.params?.map((p) => [p.name, p.enabled]), [['a', true], ['off', false]])
})

// Without this the editor binds a table to undefined and the first row typed
// goes nowhere.
test('switching body mode initialises the collection that mode needs', () => {
  assert.deepEqual(applyResponseExampleRequestField(req(), 'bodyMode', 'formUrlEncoded').formUrlEncoded, [])
  assert.deepEqual(applyResponseExampleRequestField(req(), 'bodyMode', 'multipartForm').multipartForm, [])
  assert.deepEqual(applyResponseExampleRequestField(req(), 'bodyMode', 'file').file, [])
})

test('switching body mode does not discard rows already there', () => {
  const rows = [{ name: 'k', value: 'v', enabled: true }] as never
  const next = applyResponseExampleRequestField(req({ formUrlEncoded: rows }), 'bodyMode', 'formUrlEncoded')
  assert.equal(next.formUrlEncoded?.length, 1, 'switching back to a mode must not clear its rows')
})

test('editing does not mutate the original request', () => {
  const original = { url: 'https://api.test/a', params: [] }
  applyResponseExampleRequestField(original as never, 'url', 'https://api.test/b')
  assert.equal(original.url, 'https://api.test/a')
})

// Exactly one attachment is sent, so selecting one must clear the rest —
// otherwise which file goes out depends on iteration order.
test('selecting a file row deselects the others', () => {
  const request = req({ file: [{ filePath: '/a', selected: true }, { filePath: '/b' }, { filePath: '/c' }] })
  const next = applyResponseExampleFileRow(request, 1, 'selected', true)
  assert.deepEqual(next.file?.map((r) => Boolean(r.selected)), [false, true, false])
})

test('deselecting does not promote another row', () => {
  const request = req({ file: [{ filePath: '/a', selected: true }, { filePath: '/b' }] })
  const next = applyResponseExampleFileRow(request, 0, 'selected', false)
  assert.deepEqual(next.file?.map((r) => Boolean(r.selected)), [false, false])
})

// The old content type described the old file.
test('editing a file path re-derives its content type', () => {
  const request = req({ file: [{ filePath: '/a.json', contentType: 'application/json' }] })
  const next = applyResponseExampleFileRow(request, 0, 'filePath', '/report.pdf')
  assert.equal(next.file?.[0].contentType, 'application/pdf')

  const unknown = applyResponseExampleFileRow(request, 0, 'filePath', '/thing.zzz')
  assert.equal(unknown.file?.[0].contentType, '', 'an unknown extension clears rather than keeping the stale type')
})

test('editing a row that does not exist yet creates it', () => {
  const next = applyResponseExampleFileRow(req({ file: [] }), 0, 'filePath', '/a.txt')
  assert.equal(next.file?.length, 1)
  assert.equal(next.file?.[0].selected, true, 'the first row added is selected, or nothing would be sent')
})

test('a header field edit is stored', () => {
  const result = applyResponseExampleHeader([{ name: 'X', value: 'a', enabled: true }] as never, 'text', 0, 'value', 'b')
  assert.equal(result.headers[0].value, 'b')
  assert.equal(result.bodyType, undefined, 'an unrelated header says nothing about the body')
})

test('changing the content-type value re-derives the body type', () => {
  const headers = [{ name: 'Content-Type', value: 'text/plain', enabled: true }] as never
  const result = applyResponseExampleHeader(headers, 'text', 0, 'value', 'application/json')
  assert.equal(result.bodyType, 'json')
})

// Retyping the same value must not clobber a body type the user picked by hand.
test('an unchanged content-type leaves the body type alone', () => {
  const headers = [{ name: 'Content-Type', value: 'application/json', enabled: true }] as never
  const result = applyResponseExampleHeader(headers, 'xml', 0, 'value', 'application/json')
  assert.equal(result.bodyType, undefined, 'no change means no override of a hand-picked body type')
})

test('a content-type change that implies the same body type is not an override', () => {
  const headers = [{ name: 'Content-Type', value: 'application/json', enabled: true }] as never
  const result = applyResponseExampleHeader(headers, 'json', 0, 'value', 'application/json; charset=utf-8')
  assert.equal(result.bodyType, undefined, 'json to json is not a body-type change')
})

// Renaming a header is not a statement about the body.
test('renaming a header does not re-derive the body type', () => {
  const headers = [{ name: 'X-Thing', value: 'application/json', enabled: true }] as never
  const result = applyResponseExampleHeader(headers, 'text', 0, 'name', 'Content-Type')
  assert.equal(result.bodyType, undefined)
  assert.equal(result.headers[0].name, 'Content-Type')
})

test('editing headers does not mutate the original array', () => {
  const headers = [{ name: 'X', value: 'a', enabled: true }]
  applyResponseExampleHeader(headers as never, 'text', 0, 'value', 'b')
  assert.equal(headers[0].value, 'a')
})

test('adding a header row beyond the end creates it with defaults', () => {
  const result = applyResponseExampleHeader([] as never, 'text', 0, 'name', 'X-New')
  assert.equal(result.headers[0].name, 'X-New')
  assert.equal(result.headers[0].enabled, true, 'a new header must be enabled or it is invisible on the wire')
})

// A status stored as the string "404" sorts beside "40" and fails every
// `< 300` check downstream.
test('status and size are parsed as numbers', () => {
  assert.equal(applyResponseExampleResponseField(undefined, 'status', '404').status, 404)
  assert.equal(applyResponseExampleResponseField(undefined, 'size', '2048').size, 2048)
  assert.equal(typeof applyResponseExampleResponseField(undefined, 'status', '200').status, 'number')
})

// NaN in a saved example renders as "NaN" and breaks the comparisons the number
// existed for. Zero is a visible, honest placeholder.
test('a cleared or half-typed number becomes zero rather than NaN', () => {
  assert.equal(applyResponseExampleResponseField(undefined, 'status', '').status, 0)
  assert.equal(applyResponseExampleResponseField(undefined, 'status', 'abc').status, 0)
  assert.equal(applyResponseExampleResponseField(undefined, 'size', '').size, 0)
  assert.ok(!Number.isNaN(applyResponseExampleResponseField(undefined, 'status', 'x').status))
})

test('other response fields are stored as given', () => {
  const next = applyResponseExampleResponseField(undefined, 'statusText', 'Not Found')
  assert.equal(next.statusText, 'Not Found')
})

test('editing a response field does not mutate the original', () => {
  const original = { status: 200 }
  applyResponseExampleResponseField(original as never, 'status', '404')
  assert.equal(original.status, 200)
})

// Deleting the selected attachment must promote another, or the example is left
// with files and nothing to send.
test('removing the selected file promotes the first remaining one', () => {
  const request = req({ file: [{ filePath: '/a' }, { filePath: '/b', selected: true }, { filePath: '/c' }] })
  const next = removeResponseExampleFileRow(request, 1)
  assert.deepEqual(next.file?.map((r) => r.filePath), ['/a', '/c'])
  assert.equal(next.file?.[0].selected, true)
})

test('removing an unselected file leaves the selection alone', () => {
  const request = req({ file: [{ filePath: '/a', selected: true }, { filePath: '/b' }, { filePath: '/c' }] })
  const next = removeResponseExampleFileRow(request, 2)
  assert.deepEqual(next.file?.map((r) => [r.filePath, Boolean(r.selected)]), [['/a', true], ['/b', false]])
})

// A list with no selection is a state worth repairing whenever it is noticed.
test('removing from an unselected list repairs the selection', () => {
  const request = req({ file: [{ filePath: '/a' }, { filePath: '/b' }, { filePath: '/c' }] })
  const next = removeResponseExampleFileRow(request, 2)
  assert.equal(next.file?.[0].selected, true)
})

test('removing the last file leaves an empty list rather than a phantom selection', () => {
  const next = removeResponseExampleFileRow(req({ file: [{ filePath: '/a', selected: true }] }), 0)
  assert.deepEqual(next.file, [])
})

test('removing does not mutate the original rows', () => {
  const original = { file: [{ filePath: '/a', selected: true }, { filePath: '/b' }] }
  removeResponseExampleFileRow(original as never, 0)
  assert.equal(original.file.length, 2)
})
