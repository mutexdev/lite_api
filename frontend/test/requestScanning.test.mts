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
  fileBodyRows,
  scanBodyVariables,
  scanBodyPrompts,
  variableNamesForRequest,
  collectPromptNames,
  pathParamNamesFromURL,
  queryParamsForURL
} from '../src/lib/requestScanning.ts'

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
