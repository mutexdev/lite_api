// Tests for detecting {{variables}} that will be sent as literal text.
//
// The failure being caught is a request that SUCCEEDS: `Authorization: Bearer
// {{token}}` with no `token` in scope reaches the server as those exact
// characters, comes back 401, and no error path in the app is taken. So the
// interesting cases here are all about precision — a warning that fires on
// references which do resolve, or on prompt variables, would be ignored within
// a day, and then the real one would be ignored with it.

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  referencedVariableNames,
  unresolvedHeaderVariables,
  unresolvedVariableMessage
} from '../src/lib/unresolvedVariables.ts'

/** Resolves only the names given; everything else is missing. */
const knows = (...names: string[]) => (name: string) => names.includes(name)
const knowsNothing = () => false

test('a missing variable in a header value is reported', () => {
  const found = unresolvedHeaderVariables([{ name: 'Authorization', value: 'Bearer {{token}}' }], knowsNothing)

  assert.deepEqual(found, [{ location: 'Header', field: 'Authorization', name: 'token' }])
})

test('a variable that resolves is not reported', () => {
  const found = unresolvedHeaderVariables([{ name: 'Authorization', value: 'Bearer {{token}}' }], knows('token'))

  assert.deepEqual(found, [])
})

test('a missing variable in a header NAME is reported too', () => {
  // Just as silent, and stranger to debug: the request goes out with a header
  // literally called "{{headerName}}".
  const found = unresolvedHeaderVariables([{ name: '{{headerName}}', value: 'x' }], knowsNothing)

  assert.deepEqual(found, [{ location: 'Header name', field: '{{headerName}}', name: 'headerName' }])
})

test('a disabled header is not reported', () => {
  // It is not sent, so a stale variable in it is not a problem the user has.
  const found = unresolvedHeaderVariables(
    [{ name: 'X-Old', value: '{{gone}}', enabled: false }],
    knowsNothing
  )

  assert.deepEqual(found, [])
})

test('prompt variables are not reported', () => {
  // `{{?name}}` has no value until the user is asked at send time. Flagging it
  // would be flagging the feature working.
  const found = unresolvedHeaderVariables([{ name: 'X-Token', value: '{{?token}}' }], knowsNothing)

  assert.deepEqual(found, [])
})

test('several missing variables in one header are all reported', () => {
  const found = unresolvedHeaderVariables([{ name: 'X-Trace', value: '{{a}}/{{b}}' }], knows('b'))

  assert.deepEqual(found, [{ location: 'Header', field: 'X-Trace', name: 'a' }])
})

test('whitespace inside the braces is tolerated, as the resolver tolerates it', () => {
  // A warning that used a stricter pattern than the resolver would stay quiet
  // about text the resolver also fails on.
  assert.deepEqual(referencedVariableNames('{{  token  }}'), ['token'])
})

test('a name repeated in one value is listed once', () => {
  assert.deepEqual(referencedVariableNames('{{a}}-{{a}}'), ['a'])
})

test('text with no references yields none', () => {
  assert.deepEqual(referencedVariableNames('Bearer abc'), [])
  assert.deepEqual(referencedVariableNames(''), [])
  assert.deepEqual(referencedVariableNames(undefined), [])
})

test('empty braces are not a variable', () => {
  assert.deepEqual(referencedVariableNames('{{}}'), [])
  assert.deepEqual(referencedVariableNames('{{   }}'), [])
})

test('no headers at all is not a failure', () => {
  assert.deepEqual(unresolvedHeaderVariables(undefined, knowsNothing), [])
  assert.deepEqual(unresolvedHeaderVariables([], knowsNothing), [])
})

test('the message names the variables rather than counting them', () => {
  // Not knowing WHICH one is missing is the entire difficulty of this bug.
  const message = unresolvedVariableMessage([
    { location: 'Header', field: 'Authorization', name: 'token' },
    { location: 'Header', field: 'X-Trace', name: 'traceId' }
  ])

  assert.match(message, /\{\{token\}\}/)
  assert.match(message, /\{\{traceId\}\}/)
  assert.match(message, /variables/)
})

test('the message stays singular for one variable and lists it once when repeated', () => {
  const message = unresolvedVariableMessage([
    { location: 'Header', field: 'A', name: 'token' },
    { location: 'Header', field: 'B', name: 'token' }
  ])

  assert.match(message, /variable in headers/)
  assert.equal(message.match(/\{\{token\}\}/g)?.length, 1)
})

test('nothing unresolved produces no message', () => {
  assert.equal(unresolvedVariableMessage([]), '')
})
