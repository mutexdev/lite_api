// Reading a .env file into editable rows.
//
// These become the process.env values a request interpolates, so a line parsed
// wrongly means a request sends the wrong secret, or none at all.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseDotEnvRows } from '../src/lib/dotEnv.ts'

const names = (rows: ReturnType<typeof parseDotEnvRows>) => rows.map((r) => r.name)

test('a simple definition parses', () => {
  assert.deepEqual(parseDotEnvRows('TOKEN=abc123'), [{ lineIndex: 0, name: 'TOKEN', value: 'abc123' }])
})

// Shell-sourceable .env files carry it, and the variable is FOO, not "export FOO".
test('a leading export is stripped', () => {
  assert.deepEqual(parseDotEnvRows('export TOKEN=abc'), [{ lineIndex: 0, name: 'TOKEN', value: 'abc' }])
  assert.deepEqual(parseDotEnvRows('export    TOKEN=abc'), [{ lineIndex: 0, name: 'TOKEN', value: 'abc' }])
})

// A variable legitimately named "exported" must not lose its first characters.
test('export is only stripped when it is a whole word', () => {
  assert.deepEqual(names(parseDotEnvRows('exported=1')), ['exported'])
  assert.deepEqual(names(parseDotEnvRows('exportable=1')), ['exportable'])
})

// Connection strings, base64 and JWTs all contain "=". Splitting on every one
// would truncate the secret at its first padding character.
test('only the first equals splits the line', () => {
  assert.deepEqual(parseDotEnvRows('DSN=postgres://u:p@h/db?a=1&b=2')[0], {
    lineIndex: 0,
    name: 'DSN',
    value: 'postgres://u:p@h/db?a=1&b=2'
  })
  assert.equal(parseDotEnvRows('B64=YWJjZA==')[0].value, 'YWJjZA==')
})

test('comments and blank lines are skipped', () => {
  const rows = parseDotEnvRows(['# a comment', '', '   ', 'TOKEN=abc', '  # indented comment'].join('\n'))
  assert.deepEqual(names(rows), ['TOKEN'])
})

// The discriminating case. A comment with no "=" is already skipped by the
// malformed-line check, so testing only those proves nothing about the comment
// rule — a control removing it failed nothing until this existed. A COMMENTED-
// OUT VARIABLE is the shape that tells them apart, and it is the common one:
// people comment out a secret rather than delete it.
test('a commented-out variable does not become an editable row', () => {
  assert.deepEqual(names(parseDotEnvRows('# TOKEN=old')), [], 'a row named "# TOKEN" would let a comment be edited into existence')
  assert.deepEqual(names(parseDotEnvRows('  # TOKEN=old')), [])
  assert.deepEqual(names(parseDotEnvRows('#export TOKEN=old')), [])
})

// An empty variable name is not addressable, so both malformed shapes are
// skipped rather than guessed at.
test('a line with no equals, or a leading equals, is skipped', () => {
  assert.deepEqual(parseDotEnvRows('JUST_A_WORD'), [])
  assert.deepEqual(parseDotEnvRows('=orphan'), [])
  assert.deepEqual(parseDotEnvRows('export =orphan'), [])
})

// Writing an edit back has to land on the line it came from, so skipped lines
// still advance the index.
test('lineIndex counts skipped lines too', () => {
  const rows = parseDotEnvRows(['# comment', '', 'FIRST=1', 'not a definition', 'SECOND=2'].join('\n'))
  assert.deepEqual(rows.map((r) => [r.name, r.lineIndex]), [['FIRST', 2], ['SECOND', 4]])
})

test('surrounding whitespace is trimmed from both sides', () => {
  assert.deepEqual(parseDotEnvRows('  TOKEN  =  abc  ')[0], { lineIndex: 0, name: 'TOKEN', value: 'abc' })
})

test('an empty value is a definition, not a malformed line', () => {
  assert.deepEqual(parseDotEnvRows('EMPTY=')[0], { lineIndex: 0, name: 'EMPTY', value: '' })
})

test('empty content yields no rows', () => {
  assert.deepEqual(parseDotEnvRows(''), [])
  assert.deepEqual(parseDotEnvRows('\n\n\n'), [])
})

test('CRLF line endings do not leave a carriage return in the value', () => {
  const rows = parseDotEnvRows('A=1\r\nB=2\r\n')
  assert.deepEqual(rows.map((r) => [r.name, r.value]), [['A', '1'], ['B', '2']])
})

test('duplicate names are both returned, in file order', () => {
  const rows = parseDotEnvRows('TOKEN=first\nTOKEN=second')
  assert.deepEqual(
    rows.map((r) => [r.value, r.lineIndex]),
    [['first', 0], ['second', 1]],
    'the editor shows the file as written; deduping would hide a real conflict'
  )
})
