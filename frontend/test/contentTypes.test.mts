// Guessing a content type from a filename, and a body type from a content type.
//
// Both run while someone is typing and both get WRITTEN into the request rather
// than merely shown, so a wrong guess is saved and sent.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { contentTypeForFilePath, responseExampleBodyTypeForContentType } from '../src/lib/contentTypes.ts'

test('common extensions map to their types', () => {
  for (const [path, want] of [
    ['/tmp/a.json', 'application/json'],
    ['/tmp/a.xml', 'application/xml'],
    ['/tmp/a.csv', 'text/csv; charset=utf-8'],
    ['/tmp/a.png', 'image/png'],
    ['/tmp/a.jpg', 'image/jpeg'],
    ['/tmp/a.jpeg', 'image/jpeg'],
    ['/tmp/a.svg', 'image/svg+xml'],
    ['/tmp/a.pdf', 'application/pdf']
  ] as [string, string][]) {
    assert.equal(contentTypeForFilePath(path), want, path)
  }
})

test('the extension is read case-insensitively and around whitespace', () => {
  assert.equal(contentTypeForFilePath('  /tmp/REPORT.PDF  '), 'application/pdf')
})

// Paths pasted from a browser carry a query or fragment, and the file is still
// what its extension says.
test('a query or fragment is stripped before reading the extension', () => {
  assert.equal(contentTypeForFilePath('/tmp/report.pdf?download=1'), 'application/pdf')
  assert.equal(contentTypeForFilePath('/tmp/report.pdf#page=2'), 'application/pdf')
  assert.equal(contentTypeForFilePath('https://x.test/a.json?v=2#top'), 'application/json')
})

// An empty content type lets the server sniff; a wrong one overrides it. So an
// unknown extension guesses nothing rather than octet-stream.
test('an unknown or absent extension yields no content type', () => {
  assert.equal(contentTypeForFilePath('/tmp/archive.zzz'), '')
  assert.equal(contentTypeForFilePath('/tmp/noextension'), '')
  assert.equal(contentTypeForFilePath(''), '')
  assert.equal(contentTypeForFilePath('/tmp/.hidden'), '', 'a dotfile has no extension')
})

test('text-ish types carry a charset and binary ones do not', () => {
  assert.ok(contentTypeForFilePath('/tmp/a.txt').includes('charset=utf-8'))
  assert.ok(contentTypeForFilePath('/tmp/a.html').includes('charset=utf-8'))
  assert.ok(!contentTypeForFilePath('/tmp/a.png').includes('charset'), 'a charset on an image is meaningless')
  assert.ok(!contentTypeForFilePath('/tmp/a.json').includes('charset'), 'JSON is defined as UTF-8')
})

test('the body type follows the content type', () => {
  assert.equal(responseExampleBodyTypeForContentType('application/json'), 'json')
  assert.equal(responseExampleBodyTypeForContentType('text/xml'), 'xml')
  assert.equal(responseExampleBodyTypeForContentType('application/xml'), 'xml')
  assert.equal(responseExampleBodyTypeForContentType('text/html'), 'html')
  assert.equal(responseExampleBodyTypeForContentType('text/plain'), 'text')
  assert.equal(responseExampleBodyTypeForContentType(''), 'text', 'no content type means show it as text')
})

// Real headers carry parameters and arbitrary casing.
test('the body type ignores parameters and case', () => {
  assert.equal(responseExampleBodyTypeForContentType('Application/JSON; charset=utf-8'), 'json')
  assert.equal(responseExampleBodyTypeForContentType('text/html; charset=iso-8859-1'), 'html')
})

// A vendor JSON type is JSON to a human and would render badly as plain text,
// but this matches on the base type only — recorded rather than claimed as
// correct, because it is a real limitation someone will hit.
test('a vendor +json subtype is NOT detected as json', () => {
  assert.equal(
    responseExampleBodyTypeForContentType('application/vnd.api+json'),
    'text',
    'matching is substring-on-application/json, so +json suffixes fall through to text'
  )
})
