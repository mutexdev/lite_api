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
  unresolvedAuthVariables,
  unresolvedBodyVariables,
  unresolvedHeaderVariables,
  unresolvedParamVariables,
  unresolvedRequestVariables,
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

// ── A5-09: the same silent failure everywhere else ──────────────────────────
//
// Headers were fixed; the URL, params, body and every auth field were not, and
// auth is the worst of them. A client secret of literal `{{clientSecret}}`
// produces a token request the server rejects, and the Auth tab reads fine.

test('a missing variable in a query param is reported', () => {
  const found = unresolvedParamVariables([{ name: 'page', value: '{{page}}' }], 'Query param', knowsNothing)

  assert.deepEqual(found, [{ location: 'Query param', field: 'page', name: 'page' }])
})

test('a disabled param is not reported, and a param NAME is', () => {
  assert.deepEqual(
    unresolvedParamVariables([{ name: 'x', value: '{{gone}}', enabled: false }], 'Query param', knowsNothing),
    []
  )
  assert.deepEqual(unresolvedParamVariables([{ name: '{{key}}' }], 'Query param', knowsNothing), [
    { location: 'Query param name', field: '{{key}}', name: 'key' }
  ])
})

test('only the body mode actually being sent is scanned', () => {
  // A JSON body left behind when the user switched to XML still holds whatever
  // they last typed. Warning about it is the cry-wolf this feature cannot
  // afford.
  const body = { mode: 'xml', json: '{"a":"{{stale}}"}', xml: '<a>{{live}}</a>' }
  const found = unresolvedBodyVariables(body, knowsNothing)

  assert.deepEqual(found.map((entry) => entry.name), ['live'])
})

test('form and graphql bodies are scanned in their own fields', () => {
  assert.deepEqual(
    unresolvedBodyVariables({ mode: 'formUrlEncoded', formUrlEncoded: [{ name: 'q', value: '{{term}}' }] }, knowsNothing)
      .map((entry) => entry.name),
    ['term']
  )
  assert.deepEqual(
    unresolvedBodyVariables({ mode: 'graphql', graphqlQuery: '{ a(id: "{{id}}") }', graphqlVariables: '{}' }, knowsNothing)
      .map((entry) => entry.name),
    ['id']
  )
})

test('a binary or multipart body has nothing to scan and does not throw', () => {
  assert.deepEqual(unresolvedBodyVariables({ mode: 'multipartForm' }, knowsNothing), [])
  assert.deepEqual(unresolvedBodyVariables(undefined, knowsNothing), [])
})

test('every auth field of the selected mode is scanned', () => {
  const found = unresolvedAuthVariables(
    { mode: 'oauth2', oauth2: { grantType: 'client_credentials', clientId: 'app', clientSecret: '{{clientSecret}}' } },
    knowsNothing
  )

  assert.deepEqual(found, [
    { location: 'Auth · Client secret', field: 'Client secret', name: 'clientSecret' }
  ])
})

test('auth fields of OTHER modes are not scanned', () => {
  // A bearer token left over from before the user switched to basic is not
  // sent, and flagging it would train people to ignore the banner.
  const found = unresolvedAuthVariables(
    { mode: 'basic', username: 'u', password: 'p', token: '{{stale}}' },
    knowsNothing
  )

  assert.deepEqual(found, [])
})

test('OAuth2 static token and the token placement follow-up field are both scanned', () => {
  const header = unresolvedAuthVariables(
    { mode: 'oauth2', token: '{{fallbackToken}}', oauth2: { tokenPlacement: 'header', tokenHeaderPrefix: '{{prefix}}' } },
    knowsNothing
  )
  assert.deepEqual(header.map((entry) => entry.name).sort(), ['fallbackToken', 'prefix'])

  const query = unresolvedAuthVariables(
    { mode: 'oauth2', oauth2: { tokenPlacement: 'url', tokenQueryKey: '{{queryKey}}' } },
    knowsNothing
  )
  assert.deepEqual(query.map((entry) => entry.name), ['queryKey'])
})

test('inherit and none have no auth fields of their own to scan', () => {
  assert.deepEqual(unresolvedAuthVariables({ mode: 'inherit', token: '{{x}}' }, knowsNothing), [])
  assert.deepEqual(unresolvedAuthVariables({ mode: 'none', token: '{{x}}' }, knowsNothing), [])
  assert.deepEqual(unresolvedAuthVariables(undefined, knowsNothing), [])
})

test('a whole request is scanned in reading order', () => {
  const found = unresolvedRequestVariables(
    {
      url: 'https://{{host}}/v1',
      params: [{ name: 'page', value: '{{page}}' }],
      headers: [{ name: 'X-Trace', value: '{{trace}}' }],
      body: { mode: 'json', json: '{"a":"{{payload}}"}' },
      auth: { mode: 'bearer', token: '{{token}}' }
    },
    knowsNothing
  )

  assert.deepEqual(found.map((entry) => entry.name), ['host', 'page', 'trace', 'payload', 'token'])
})

test('the message names the places as well as the variables', () => {
  // With six surfaces scanned, "{{token}} is unresolved" leaves the user
  // hunting through all six.
  const message = unresolvedVariableMessage(
    unresolvedRequestVariables(
      { url: 'https://{{host}}/v1', auth: { mode: 'bearer', token: '{{token}}' } },
      knowsNothing
    )
  )

  assert.match(message, /the URL and auth/)
  assert.match(message, /\{\{host\}\}/)
  assert.match(message, /\{\{token\}\}/)
})

test('a request with nothing unresolved produces no message', () => {
  const found = unresolvedRequestVariables(
    { url: 'https://{{host}}/v1', auth: { mode: 'bearer', token: '{{token}}' } },
    knows('host', 'token')
  )

  assert.equal(unresolvedVariableMessage(found), '')
})
