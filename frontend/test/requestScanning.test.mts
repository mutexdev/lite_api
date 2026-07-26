// Scanning a request for {{variables}} and {{?prompts}}.
//
// scanBodyPrompts is the frontend twin of scanBodyPromptVariables in
// internal/scripting, and it fails the same silent way: a body mode whose
// fields go unscanned means the user is never asked for the value, and the
// request goes out with a literal {{?token}} in it. The server rejects it, or
// worse accepts it, and nothing points at the prompt that never appeared.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  collectVariableNames,
  syncPathParamsForURL,
  fileBodyRows,
  scanBodyVariables,
  scanBodyPrompts,
  variableNamesForRequest,
  collectPromptNames,
  pathParamNamesFromURL,
  queryParamsForURL
} from '../src/lib/requestScanning.ts'
import type { types } from '../wailsjs/go/models'

const body = (o: Record<string, unknown>) => o as never
const kv = (name: string, value: string, extra: Record<string, unknown> = {}) =>
  ({ name, value, enabled: true, ...extra }) as never

function promptsIn(b: Record<string, unknown>): string[] {
  const seen: string[] = []
  scanBodyPrompts(body(b), (v) => seen.push(String(v ?? '')), (rows) => {
    for (const row of rows ?? []) seen.push(row.name, row.value)
  })
  return seen
}

test('collectVariableNames finds names and skips prompts', () => {
  const names = new Set<string>()
  collectVariableNames('{{host}}/{{ path }}/{{?prompt}}', names)
  assert.deepEqual([...names].sort(), ['host', 'path'], 'a {{?prompt}} is not a variable')
})

test('collectVariableNames tolerates non-strings and empties', () => {
  const names = new Set<string>()
  collectVariableNames(null, names)
  collectVariableNames(undefined, names)
  collectVariableNames(42, names)
  collectVariableNames('{{}}', names)
  collectVariableNames('{{   }}', names)
  assert.equal(names.size, 0)
})

// Prompts are per-mode: a field the active mode does not send must not raise a
// dialog. This is the case a test that only exercises JSON cannot see.
test('scanBodyPrompts covers every body mode', () => {
  for (const [mode, b, want] of [
    ['json', { mode: 'json', json: '{{?tok}}' }, '{{?tok}}'],
    ['xml', { mode: 'xml', xml: '{{?tok}}' }, '{{?tok}}'],
    ['text', { mode: 'text', text: '{{?tok}}' }, '{{?tok}}'],
    ['sparql', { mode: 'sparql', text: '{{?tok}}' }, '{{?tok}}'],
    ['graphql query', { mode: 'graphql', graphqlQuery: '{{?q}}' }, '{{?q}}'],
    ['graphql vars', { mode: 'graphql', graphqlVariables: '{{?v}}' }, '{{?v}}'],
    ['file path', { mode: 'file', filePath: '{{?p}}' }, '{{?p}}']
  ] as [string, Record<string, unknown>, string][]) {
    assert.ok(promptsIn(b).includes(want), `${mode}: ${want} was never scanned`)
  }
})

test('scanBodyPrompts covers form-urlencoded rows and every multipart field', () => {
  assert.ok(promptsIn({ mode: 'formUrlEncoded', formUrlEncoded: [kv('k', '{{?v}}')] }).includes('{{?v}}'))

  const seen = promptsIn({
    mode: 'multipartForm',
    multipart: [{ name: '{{?n}}', value: '{{?v}}', filePath: '{{?p}}', contentType: '{{?c}}', enabled: true }]
  })
  for (const want of ['{{?n}}', '{{?v}}', '{{?p}}', '{{?c}}']) {
    assert.ok(seen.includes(want), `multipart ${want} not scanned`)
  }
})

// A disabled part is not sent, so prompting for it asks the user to supply a
// value that goes nowhere.
test('scanBodyPrompts skips disabled multipart parts', () => {
  const seen = promptsIn({ mode: 'multipartForm', multipart: [{ name: 'off', value: '{{?unused}}', enabled: false }] })
  assert.ok(!seen.includes('{{?unused}}'))
})

// The whole reason prompts dispatch per mode: a value in the JSON field while
// the mode is XML is not being sent, so asking for it would block the request on
// an irrelevant question.
test('scanBodyPrompts ignores fields the active mode does not send', () => {
  const seen = promptsIn({ mode: 'xml', xml: '<a/>', json: '{{?notSent}}' })
  assert.ok(!seen.includes('{{?notSent}}'))
  assert.deepEqual(promptsIn({ mode: 'none', json: '{{?tok}}' }), [])
})

// Variables are the opposite: scanned from every field regardless of mode, so
// switching body tabs does not make a name vanish from the tooltip list.
test('scanBodyVariables scans every field regardless of mode', () => {
  const seen: string[] = []
  scanBodyVariables(body({ mode: 'xml', xml: '{{a}}', json: '{{b}}', text: '{{c}}' }), (v) => seen.push(String(v ?? '')), () => {})
  assert.ok(seen.includes('{{b}}'), 'a variable in an inactive body field is still listed')
  assert.ok(seen.includes('{{c}}'))
})

test('fileBodyRows selects the first row when nothing is selected', () => {
  const rows = fileBodyRows(body({ files: [{ filePath: '/a', selected: false }, { filePath: '/b', selected: false }] }))
  assert.equal(rows[0].selected, true, 'a file body with no selection would send nothing')
  assert.equal(rows[1].selected, false)
})

test('fileBodyRows keeps an existing selection and synthesises the legacy shape', () => {
  const explicit = fileBodyRows(body({ files: [{ filePath: '/a' }, { filePath: '/b', selected: true }] }))
  assert.equal(explicit[1].selected, true)
  assert.equal(explicit[0].selected, undefined)

  const legacy = fileBodyRows(body({ filePath: '/legacy', fileContentType: 'text/plain' }))
  assert.equal(legacy.length, 1)
  assert.equal(legacy[0].filePath, '/legacy')
  assert.equal(legacy[0].selected, true)

  assert.deepEqual(fileBodyRows(body({})), [])
  assert.deepEqual(fileBodyRows(undefined), [])
})

// fileBodyRows copies its rows; mutating the result must not reach the request.
test('fileBodyRows does not alias the request body', () => {
  const original = { files: [{ filePath: '/a', selected: true }] }
  const rows = fileBodyRows(body(original))
  rows[0].filePath = '/mutated'
  assert.equal(original.files[0].filePath, '/a')
})

test('variableNamesForRequest gathers url, params, headers and body', () => {
  const names = variableNamesForRequest({
    url: '{{host}}/x',
    params: [kv('p', '{{param}}')],
    pathParams: [kv('id', '{{pathVar}}')],
    headers: [kv('h', '{{header}}')],
    body: { mode: 'json', json: '{{bodyVar}}' }
  } as never)
  assert.deepEqual(names.sort(), ['bodyVar', 'header', 'param', 'pathVar', 'host'].sort())
})

test('variableNamesForRequest skips disabled rows', () => {
  const names = variableNamesForRequest({
    url: '',
    headers: [kv('h', '{{kept}}'), kv('h2', '{{dropped}}', { enabled: false })]
  } as never)
  assert.deepEqual(names, ['kept'])
})

// A path parameter can be written :name, or inside a Bruno-style matcher.
test('pathParamNamesFromURL reads colon segments', () => {
  assert.deepEqual(pathParamNamesFromURL('https://api.test/users/:id/posts/:postId'), ['id', 'postId'])
  assert.deepEqual(pathParamNamesFromURL('api.test/v1/:id'), ['id'], 'a schemeless URL still parses')
  assert.deepEqual(pathParamNamesFromURL(''), [])
})

// Query and fragment are not path, so a :name there is not a path parameter.
test('pathParamNamesFromURL ignores the query and fragment', () => {
  assert.deepEqual(pathParamNamesFromURL('https://api.test/users/:id?filter=:notAParam'), ['id'])
  assert.deepEqual(pathParamNamesFromURL('https://api.test/users/:id#:alsoNot'), ['id'])
})

test('pathParamNamesFromURL reports each name once', () => {
  assert.deepEqual(pathParamNamesFromURL('https://api.test/:id/x/:id'), ['id'])
})

// Editing the URL rewrites the query table, and a disabled row is the user
// having deliberately turned a parameter off — losing it on every keystroke
// would make the toggle useless.
test('queryParamsForURL keeps disabled rows', () => {
  const rows = queryParamsForURL('https://api.test/x?a=1', [kv('off', 'x', { enabled: false })])
  assert.deepEqual(rows.map((r) => [r.name, r.enabled]), [['a', true], ['off', false]])
})

test('queryParamsForURL preserves description and secret on an existing row', () => {
  const rows = queryParamsForURL('https://api.test/x?token=new', [
    kv('token', 'old', { secret: true, description: 'the token' })
  ])
  assert.equal(rows[0].value, 'new')
  assert.equal(rows[0].secret, true, 'retyping the value must not clear the secret flag')
  assert.equal(rows[0].description, 'the token')
})

test('queryParamsForURL decodes percent escapes and plus as space', () => {
  const rows = queryParamsForURL('https://api.test/x?q=hello+world&p=a%20b&e=%zz')
  assert.equal(rows[0].value, 'hello world')
  assert.equal(rows[1].value, 'a b')
  assert.equal(rows[2].value, '%zz', 'an undecodable value is kept verbatim rather than throwing')
})

test('queryParamsForURL keeps a value containing an equals sign intact', () => {
  const rows = queryParamsForURL('https://api.test/x?filter=a=b')
  assert.deepEqual([rows[0].name, rows[0].value], ['filter', 'a=b'])
})

test('queryParamsForURL ignores the fragment and handles no query', () => {
  assert.deepEqual(queryParamsForURL('https://api.test/x#a=1'), [])
  assert.deepEqual(queryParamsForURL('https://api.test/x'), [])
})

// Prompts are collected from the whole resolution chain, not just the request,
// because a collection- or folder-level header can carry one too.
test('collectPromptNames reaches the request, collection, folder and environment', () => {
  const collection = {
    headers: [kv('X-Coll', '{{?collPrompt}}')],
    variables: [{ name: 'v', value: '{{?varPrompt}}', enabled: true }],
    folders: [{ path: 'a', headers: [kv('X-Folder', '{{?folderPrompt}}')], variables: [] }],
    environments: [{ id: 'env1', variables: [{ name: 'e', value: '{{?envPrompt}}', enabled: true }] }]
  }
  const request = { folderPath: 'a', url: '{{?urlPrompt}}', headers: [], params: [], pathParams: [] }
  const globalEnv = { id: 'g', variables: [{ name: 'g', value: '{{?globalPrompt}}', enabled: true }] }

  const prompts = collectPromptNames(collection as never, request as never, 'env1', globalEnv as never)
  assert.deepEqual(
    prompts.sort(),
    ['collPrompt', 'envPrompt', 'folderPrompt', 'globalPrompt', 'urlPrompt', 'varPrompt'].sort()
  )
})

test('collectPromptNames finds prompts nested inside auth objects', () => {
  const prompts = collectPromptNames(
    { headers: [], variables: [], folders: [], environments: [] } as never,
    { folderPath: '', url: '', auth: { mode: 'bearer', oauth2: { clientSecret: '{{?secret}}' } } } as never,
    '',
    undefined
  )
  assert.deepEqual(prompts, ['secret'], 'auth is scanned recursively, so a nested grant field still prompts')
})

test('collectPromptNames skips unselected websocket messages', () => {
  const prompts = collectPromptNames(
    { headers: [], variables: [], folders: [], environments: [] } as never,
    {
      folderPath: '',
      url: '',
      wsMessages: [
        { name: 'on', content: '{{?sent}}', selected: true },
        { name: 'off', content: '{{?notSent}}', selected: false }
      ]
    } as never,
    '',
    undefined
  )
  assert.deepEqual(prompts, ['sent'])
})

// This runs on every keystroke in the URL bar, so preservation is the point:
// rebuilding from scratch would clear every path-parameter value the moment any
// other part of the URL was edited.
test('editing an unrelated part of the URL keeps the path param values', () => {
  const current = [
    { name: 'id', value: '42', enabled: true, secret: false, description: 'the user' }
  ] as types.KeyValue[]
  const next = syncPathParamsForURL('https://api.test/users/:id?page=2', current)
  assert.equal(next.length, 1)
  assert.equal(next[0].value, '42')
  assert.equal(next[0].description, 'the user')
})

test('a new path parameter arrives empty and enabled', () => {
  const next = syncPathParamsForURL('https://api.test/users/:id/posts/:postId', [])
  assert.deepEqual(next.map((row) => row.name), ['id', 'postId'])
  assert.equal(next[1].value, '')
  assert.equal(next[1].enabled, true)
})

// A stale parameter would sit in the table with nowhere to go and be sent as
// nothing, so the result is ordered by the URL rather than merged into the old
// list.
test('a parameter removed from the URL is dropped', () => {
  const current = [
    { name: 'id', value: '42', enabled: true } as types.KeyValue,
    { name: 'gone', value: 'x', enabled: true } as types.KeyValue
  ]
  const next = syncPathParamsForURL('https://api.test/users/:id', current)
  assert.deepEqual(next.map((row) => row.name), ['id'])
})

test('a URL with no path parameters yields no rows', () => {
  assert.deepEqual(syncPathParamsForURL('https://api.test/users', [{ name: 'id' } as types.KeyValue]), [])
  assert.deepEqual(syncPathParamsForURL('', []), [])
})

// The rows follow the URL's order, so reordering the path reorders the table
// rather than leaving the old positions in place.
test('the rows follow the order of the URL', () => {
  const current = [
    { name: 'b', value: '2' } as types.KeyValue,
    { name: 'a', value: '1' } as types.KeyValue
  ]
  const next = syncPathParamsForURL('https://api.test/:a/:b', current)
  assert.deepEqual(next.map((row) => [row.name, row.value]), [['a', '1'], ['b', '2']])
})

// The uncovered branches below are exactly the failure this module's header
// describes: a field that goes unscanned means the user is never prompted, and
// the request goes out with a literal {{variable}} in it. Each of these fields
// is one a real request can carry.

// A multipart part's FILENAME and CONTENT TYPE are as templatable as its value —
// {{env}}.json is an ordinary thing to type — and an unscanned one uploads a
// file literally named "{{env}}.json".
test('a variable in any multipart field is found', () => {
  const request = {
    body: {
      mode: 'multipartForm',
      multipart: [
        { name: '{{fieldName}}', value: '{{fieldValue}}', filePath: '{{dir}}/a.json', contentType: 'application/{{fmt}}', enabled: true }
      ]
    }
  } as types.RequestItem
  const found = variableNamesForRequest(request)
  for (const name of ['fieldName', 'fieldValue', 'dir', 'fmt']) {
    assert.ok(found.includes(name), `${name} was not scanned`)
  }
})

// A disabled part is not sent, so prompting for its variables would ask the user
// for a value that goes nowhere.
test('a disabled multipart part is not scanned', () => {
  const request = {
    body: { mode: 'multipartForm', multipart: [{ name: '{{skipped}}', enabled: false }] }
  } as types.RequestItem
  assert.equal(variableNamesForRequest(request).includes('skipped'), false)
})

// The file body has two shapes — a single filePath/fileContentType pair, and a
// files[] array. Both reach the wire, so both must be scanned.
test('a variable in either file-body shape is found', () => {
  const single = { body: { mode: 'file', filePath: '{{home}}/x.bin', fileContentType: 'application/{{type}}' } } as types.RequestItem
  const found = variableNamesForRequest(single)
  assert.ok(found.includes('home'))
  assert.ok(found.includes('type'))

  const many = {
    body: { mode: 'file', files: [{ filePath: '{{a}}.bin', contentType: 'text/{{b}}', selected: true }] }
  } as types.RequestItem
  const foundMany = variableNamesForRequest(many)
  assert.ok(foundMany.includes('a'))
  assert.ok(foundMany.includes('b'))
})

// The message and array branches belong to collectPromptNames, NOT to
// variableNamesForRequest. I first wrote these against the wrong function and
// read the empty results as a bug in the scanner; the scanner was right and the
// test was asking the wrong thing. variableNamesForRequest scans the request's
// own fields for {{name}}; collectPromptNames walks the whole chain —
// collection, folders, environment, messages — for {{?name}} only.

const emptyCollection = { id: 'c', items: [], folders: [] } as unknown as types.Collection

// A WebSocket message is a payload the user sends. Leaving it unscanned sends
// the literal token over the socket, where there is no status code to notice.
test('a prompt in a websocket message is found', () => {
  const request = {
    wsMessages: [{ name: '{{?msgName}}', content: '{"t":"{{?token}}"}', selected: true }]
  } as types.RequestItem
  const found = collectPromptNames(emptyCollection, request, '', undefined)
  assert.ok(found.includes('msgName'))
  assert.ok(found.includes('token'))
})

// An unselected message is not sent, so prompting for it would block the
// request on a question about a payload that never leaves.
test('an unselected websocket message is not scanned for prompts', () => {
  const request = { wsMessages: [{ name: '{{?skipped}}', selected: false }] } as types.RequestItem
  assert.equal(collectPromptNames(emptyCollection, request, '', undefined).includes('skipped'), false)
})

test('a prompt in a grpc message is found', () => {
  const request = { grpcMessages: [{ name: '{{?rpc}}', content: '{"id":"{{?id}}"}' }] } as types.RequestItem
  const found = collectPromptNames(emptyCollection, request, '', undefined)
  assert.ok(found.includes('rpc'))
  assert.ok(found.includes('id'))
})

// scanObject recurses into arrays, and auth configs hold them. Without the
// array branch a prompt inside an array element is invisible — the dialog never
// asks, and the literal token is sent.
test('a prompt nested inside an array is found', () => {
  const request = { auth: { oauth2: { params: [{ value: '{{?nested}}' }] } } } as unknown as types.RequestItem
  assert.ok(collectPromptNames(emptyCollection, request, '', undefined).includes('nested'))
})

// A URL too malformed for the URL constructor still has a path the user can see
// and type parameters into. Falling back to a manual split keeps those
// addressable instead of silently offering none.
// The URL must be one the constructor GENUINELY rejects. My first version used
// "{{base}}/users/:id?x=1", which parses fine once http:// is prepended — the
// catch never ran, and removing the fallback failed nothing.
test('path parameters are found in a URL the URL constructor rejects', () => {
  assert.deepEqual(pathParamNamesFromURL('http://[bad/:id'), ['id'])
  assert.deepEqual(pathParamNamesFromURL('://:id'), ['id'])
})

// scan(body.filePath) looks redundant with the fileBodyRows loop below it, and
// for the single-file shape it is — fileBodyRows synthesises a row from exactly
// those two fields. It is NOT redundant when files[] is ALSO populated: then
// fileBodyRows returns only the array, and a filePath left over from an earlier
// single-file body would go unscanned. A control removing it failed nothing
// until this case existed.
test('a leftover single filePath is scanned even when files[] is populated', () => {
  const request = {
    body: {
      mode: 'file',
      files: [{ filePath: 'a.bin', contentType: 'x', selected: true }],
      filePath: '{{orphan}}.bin',
      fileContentType: 'application/{{orphanType}}'
    }
  } as types.RequestItem
  const found = variableNamesForRequest(request)
  assert.ok(found.includes('orphan'))
  assert.ok(found.includes('orphanType'))
})
