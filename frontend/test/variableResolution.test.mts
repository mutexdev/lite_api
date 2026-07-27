// Resolving {{variables}}: precedence, secret propagation, and cycles.
//
// This logic lived in App.svelte untested. Every failure mode below is silent:
// the tooltip reports a value confidently, and the request sends a different
// one.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  isValidVariableName,
  folderChainForRequest,
  findTooltipVariable,
  resolveTooltipValue,
  resolveVariableTooltip,
  pathParamTooltipInfo,
  displayTooltipValue
} from '../src/lib/variableResolution.ts'
import { fallbackVariableTooltipInfo } from '../src/lib/urlSegments.ts'

type Any = Record<string, unknown>

const v = (name: string, value: string, extra: Any = {}) => ({ name, value, enabled: true, ...extra })

function fixture(overrides: Any = {}) {
  const collection = {
    variables: [v('host', 'collection.test')],
    environments: [{ id: 'env1', variables: [v('host', 'env.test')] }],
    folders: [
      { path: 'a', variables: [v('host', 'folder-a.test')] },
      { path: 'a/b', variables: [v('host', 'folder-ab.test')] }
    ],
    runtimeVariables: [],
    ...(overrides.collection as Any)
  }
  const workspace = {
    activeGlobalEnvironmentId: 'g1',
    globalEnvironments: [{ id: 'g1', variables: [v('host', 'global.test')] }],
    ...(overrides.workspace as Any)
  }
  const request = { folderPath: 'a/b', vars: { req: [] }, pathParams: [], ...(overrides.request as Any) }
  return { workspace, collection, request } as Any
}

const resolve = (name: string, f: Any, env = 'env1', processEnv: Record<string, string> = {}) =>
  resolveVariableTooltip(name, f.workspace as never, f.collection as never, f.request as never, env, processEnv)

test('a name must be word characters, dots or dashes', () => {
  for (const name of ['host', 'base_url', 'api.host', 'a-b', 'process.env.TOKEN']) {
    assert.equal(isValidVariableName(name), true, name)
  }
  for (const name of ['', 'has space', 'a{b', 'a}b', 'a/b', '?prompt']) {
    assert.equal(isValidVariableName(name), false, name)
  }
})

// The precedence chain decides what the request actually sends. If the tooltip
// disagrees with it, the feature whose whole job is explaining the value is
// telling the user something false.
test('precedence runs global < collection < environment < folder < request < runtime', () => {
  const f = fixture()

  // Nothing but global.
  const onlyGlobal = fixture({ collection: { variables: [], environments: [], folders: [], runtimeVariables: [] } })
  assert.equal(resolve('host', onlyGlobal).scope, 'Global')

  // Collection beats global.
  const noEnv = fixture({ collection: { ...f.collection as Any, environments: [], folders: [] } })
  assert.equal(resolve('host', noEnv).scope, 'Collection')

  // Environment beats collection.
  const noFolders = fixture({ collection: { ...(f.collection as Any), folders: [] } })
  assert.equal(resolve('host', noFolders).scope, 'Environment')

  // Folder beats environment.
  assert.equal(resolve('host', f).scope, 'Folder')

  // Request beats folder.
  const withRequest = fixture({ request: { folderPath: 'a/b', vars: { req: [v('host', 'request.test')] } } })
  assert.equal(resolve('host', withRequest).scope, 'Request')

  // Runtime beats everything — a script that just set it must win.
  const withRuntime = fixture({
    collection: { ...(f.collection as Any), runtimeVariables: [v('host', 'runtime.test')] },
    request: { folderPath: 'a/b', vars: { req: [v('host', 'request.test')] } }
  })
  assert.equal(resolve('host', withRuntime).scope, 'Runtime')
  assert.equal(resolve('host', withRuntime).resolvedValue, 'runtime.test')
})

// A nested folder is closer to the request than its parent, so it wins.
test('a nested folder beats its parent', () => {
  const f = fixture()
  assert.equal(resolve('host', f).resolvedValue, 'folder-ab.test')
  assert.deepEqual(
    folderChainForRequest(f.collection as never, f.request as never).map((folder) => folder.path),
    ['a', 'a/b'],
    'the chain must be outermost-first for last-write-wins to mean nearest-wins'
  )
})

test('a disabled variable is not found', () => {
  const f = fixture({
    collection: { variables: [v('host', 'x', { enabled: false })], environments: [], folders: [], runtimeVariables: [] },
    workspace: { globalEnvironments: [] }
  })
  const info = resolve('host', f)
  assert.equal(info.found, false)
  assert.equal(info.source, 'missing')
})

test('only the selected environment contributes', () => {
  const f = fixture({
    collection: {
      variables: [],
      folders: [],
      runtimeVariables: [],
      environments: [
        { id: 'env1', variables: [v('host', 'one.test')] },
        { id: 'env2', variables: [v('host', 'two.test')] }
      ]
    },
    workspace: { globalEnvironments: [] }
  })
  assert.equal(resolve('host', f, 'env2').resolvedValue, 'two.test')
  assert.equal(resolve('host', f, 'nope').found, false, 'an unknown environment id must not silently fall through')
})

// Secret-ness has to travel through references. Miss this and a masked value
// leaks in cleartext through any variable that merely mentions it.
test('a variable that interpolates a secret is itself secret', () => {
  const f = fixture({
    collection: {
      variables: [v('token', 'sk-live-123', { secret: true }), v('authHeader', 'Bearer {{token}}')],
      environments: [],
      folders: [],
      runtimeVariables: []
    },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })

  const direct = resolve('token', f)
  assert.equal(direct.secret, true)

  const indirect = resolve('authHeader', f)
  assert.equal(indirect.resolvedValue, 'Bearer sk-live-123')
  assert.equal(indirect.secret, true, 'a value built from a secret must itself be treated as secret')
  assert.equal(displayTooltipValue(indirect, false), '********')
  assert.equal(displayTooltipValue(indirect, true), 'Bearer sk-live-123')
})

// One level of indirection is caught before recursing, by checking the matched
// variable's own secret flag. TWO levels are only caught by propagating the
// recursive result back up — and a test with a single hop passes either way.
// Removing that propagation failed nothing until this test existed.
test('a secret two levels deep still marks the outer variable secret', () => {
  const f = fixture({
    collection: {
      variables: [
        v('deep', 'sk-live-999', { secret: true }),
        v('middle', '{{deep}}'),
        v('outer', 'Bearer {{middle}}')
      ],
      environments: [],
      folders: [],
      runtimeVariables: []
    },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })

  const info = resolve('outer', f)
  assert.equal(info.resolvedValue, 'Bearer sk-live-999')
  assert.equal(info.secret, true, 'a secret reached through an intermediate variable must still mask')
  assert.equal(displayTooltipValue(info, false), '********')
})

// {{a}} -> {{b}} -> {{a}} must terminate rather than hang the renderer.
test('a reference cycle terminates', () => {
  const f = fixture({
    collection: {
      variables: [v('a', 'A{{b}}'), v('b', 'B{{a}}')],
      environments: [],
      folders: [],
      runtimeVariables: []
    },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  assert.equal(resolve('a', f).resolvedValue, 'AB')
})

// The seen-set is removed after recursing on purpose: a value using the same
// variable twice in sequence is not a cycle and must resolve both times.
test('the same variable used twice in one value resolves twice', () => {
  const f = fixture({
    collection: {
      variables: [v('x', 'X'), v('pair', '{{x}}-{{x}}')],
      environments: [],
      folders: [],
      runtimeVariables: []
    },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  assert.equal(resolve('pair', f).resolvedValue, 'X-X')
})

test('an unresolvable reference becomes empty rather than literal braces', () => {
  const f = fixture({
    collection: { variables: [v('greeting', 'hello {{absent}}')], environments: [], folders: [], runtimeVariables: [] },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  const info = resolve('greeting', f)
  assert.equal(info.resolvedValue, 'hello ', 'showing {{absent}} would imply the request sends the braces')
})

// A prompt variable has no value until the user is asked at send time.
test('a prompt reference resolves to empty', () => {
  const f = fixture({
    collection: { variables: [v('url', 'https://{{?host}}/x')], environments: [], folders: [], runtimeVariables: [] },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  assert.equal(resolve('url', f).resolvedValue, 'https:///x')
})

test('process.env values are read-only and report loading separately from empty', () => {
  const f = fixture()
  const loaded = resolve('process.env.TOKEN', f, 'env1', { 'process.env.TOKEN': 'abc' })
  assert.equal(loaded.resolvedValue, 'abc')
  assert.equal(loaded.editable, false)
  assert.equal(loaded.scope, 'Process Env')

  const pending = resolve('process.env.TOKEN', f, 'env1', {})
  assert.equal(pending.resolvedValue, 'Loading...', 'an empty string would read as "defined but blank"')

  const blank = resolve('process.env.TOKEN', f, 'env1', { 'process.env.TOKEN': '' })
  assert.equal(blank.resolvedValue, '', 'a genuinely empty value must not say Loading')
})

// Editing a folder or runtime variable here would write somewhere the user is
// not looking.
test('folder and runtime variables are read-only, others are editable', () => {
  const f = fixture()
  assert.equal(resolve('host', f).readOnly, true, 'folder')

  const runtime = fixture({
    collection: { variables: [], environments: [], folders: [], runtimeVariables: [v('host', 'r')] },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  assert.equal(resolve('host', runtime).readOnly, true, 'runtime')

  const collectionScope = fixture({
    collection: { variables: [v('host', 'c')], environments: [], folders: [], runtimeVariables: [] },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  assert.equal(resolve('host', collectionScope).editable, true)
})

test('an invalid name is reported rather than looked up', () => {
  const info = resolve('has space', fixture())
  assert.equal(info.validName, false)
  assert.equal(info.source, 'invalid')
  assert.equal(info.editable, false)
})

// A blank path param is "not filled in yet", not "set to empty" — sending an
// empty segment produces a different URL.
test('path params treat blank as unfilled', () => {
  const rows = [{ name: 'id', value: '42', enabled: true }, { name: 'blank', value: '   ', enabled: true }]
  assert.equal(pathParamTooltipInfo('id', rows as never).found, true)
  assert.equal(pathParamTooltipInfo('blank', rows as never).found, false)
  assert.equal(pathParamTooltipInfo('absent', rows as never).found, false)
  assert.equal(pathParamTooltipInfo('absent', rows as never).editable, false)
})

// Path params are the one source that still displays when not found, because
// the row exists in the table and the user is about to type into it.
test('display falls back to "Not defined" except for path params', () => {
  const missing = resolve('nope', fixture())
  assert.equal(displayTooltipValue(missing, false), 'Not defined')

  const blankPath = pathParamTooltipInfo('blank', [{ name: 'blank', value: '', enabled: true }] as never)
  assert.equal(displayTooltipValue(blankPath, false), '')
})

test('findTooltipVariable reports the index within its scope', () => {
  const f = fixture({
    collection: {
      variables: [v('a', '1'), v('host', '2'), v('c', '3')],
      environments: [],
      folders: [],
      runtimeVariables: []
    },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  const match = findTooltipVariable('host', f.workspace as never, f.collection as never, f.request as never, '')
  assert.equal(match?.index, 1, 'the index is what an edit writes back to')
})

test('resolveTooltipValue reports secrets it passed through', () => {
  const f = fixture({
    collection: { variables: [v('s', 'shh', { secret: true })], environments: [], folders: [], runtimeVariables: [] },
    workspace: { globalEnvironments: [] },
    request: { folderPath: '', vars: { req: [] } }
  })
  const out = resolveTooltipValue(
    'x={{s}}',
    f.workspace as never,
    f.collection as never,
    f.request as never,
    '',
    {},
    new Set()
  )
  assert.equal(out.value, 'x=shh')
  assert.equal(out.containsSecret, true)
})

// The one-directional invariant the overlay depends on.
//
// I first wrote this as "readOnly and editable are always opposite", having
// read `{#if info.readOnly}` and `{:else if info.editable}` as a complementary
// pair. THEY ARE NOT. The markup is:
//
//     {#if editing}       a textarea
//     {:else if editable} an edit button
//     {:else}             the value, displayed plainly     <- a real third arm
//     {/if}
//     {#if readOnly}      a "read-only" note               <- a SEPARATE if
//
// so `readOnly: false, editable: false` is a legitimate third state — shown
// plainly, not labelled read-only — and pathParamTooltipInfo produces exactly
// that for a :name the params table has no row for yet. Nothing is wrong with
// it: the parameter is not read-only in principle, there is simply nothing
// there to edit.
//
// What must never happen is the other direction. readOnly AND editable together
// renders an edit button beside a note saying the value cannot be edited, and
// clicking it opens an editor whose save has nowhere to go.
test('no producer marks a tooltip both editable and read-only', () => {
  const workspace = { id: 'w', collections: [] } as unknown as types.Workspace
  const collection = { id: 'c', items: [], folders: [], variables: [] } as unknown as types.Collection
  const request = { id: 'r', name: 'req' } as types.RequestItem

  const infos = [
    resolveVariableTooltip('has space', workspace, collection, request, '', {}),
    resolveVariableTooltip('process.env.HOME', workspace, collection, request, '', { HOME: '/root' }),
    resolveVariableTooltip('undefinedName', workspace, collection, request, '', {}),
    fallbackVariableTooltipInfo('valid'),
    fallbackVariableTooltipInfo('not valid'),
    pathParamTooltipInfo('id', [{ name: 'id', value: '1', enabled: true } as types.KeyValue]),
    pathParamTooltipInfo('missing', [])
  ]

  for (const info of infos) {
    assert.equal(
      info.readOnly && info.editable,
      false,
      `${info.source}/${info.name}: an edit button beside a read-only note`
    )
  }
})

// A path parameter with no row yet is the third state, and it is deliberate.
test('a path parameter with no row is shown plainly, not labelled read-only', () => {
  const info = pathParamTooltipInfo('missing', [])
  assert.equal(info.validName, true)
  assert.equal(info.editable, false, 'there is no row to edit')
  assert.equal(info.readOnly, false, 'but it is not read-only in principle either')
  assert.equal(info.found, false)
})

// An invalid path parameter name IS read-only, because no row could ever
// resolve it.
test('an invalid path parameter name is read-only', () => {
  const info = pathParamTooltipInfo('not valid', [])
  assert.equal(info.readOnly, true)
  assert.equal(info.editable, false)
  assert.equal(info.source, 'invalid')
})

// A resolved variable is editable, and the pair still holds for it. Kept
// separate because it needs a collection that actually defines something.
test('a resolved variable keeps the pair opposite too', () => {
  const collection = {
    id: 'c', items: [], folders: [],
    variables: [{ name: 'token', value: 'abc', enabled: true }]
  } as unknown as types.Collection
  const info = resolveVariableTooltip(
    'token',
    { id: 'w', collections: [collection] } as unknown as types.Workspace,
    collection,
    { id: 'r', name: 'req' } as types.RequestItem,
    '',
    {}
  )
  assert.equal(info.found, true)
  assert.equal(info.readOnly, !info.editable)
})
