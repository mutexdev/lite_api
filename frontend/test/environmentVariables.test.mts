import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  environmentVariableAddLabel,
  environmentVariableMatches,
  visibleEnvironmentVariables,
} from '../src/lib/environmentVariables.ts'
import { normalizedSearch } from '../src/lib/sidebarFilter.ts'
import type { types } from '../wailsjs/go/models'

function variable(over: Partial<types.Variable> = {}): types.Variable {
  return { name: 'token', value: 'abc', enabled: true, secret: false, ...over } as types.Variable
}

const list = [
  variable({ name: 'HOST', value: 'api.test' }),
  variable({ name: 'API_KEY', value: 'k-1', secret: true }),
  variable({ name: 'port', value: '8080' }),
  variable({ name: 'PASSWORD', value: 'p-1', secret: true }),
]

// THE PROPERTY THIS MODULE EXISTS FOR. Every edit handler in the editor writes
// back by index. An index taken after filtering would renumber the rows to
// their filtered positions, so editing a row while a search or the secrets tab
// is active would silently modify a different variable.
test('the index is the position in the full list, not in the filtered result', () => {
  const secrets = visibleEnvironmentVariables(list, 'secrets', '')
  assert.deepEqual(
    secrets.map((row) => row.index),
    [1, 3],
  )
  assert.deepEqual(
    secrets.map((row) => row.variable.name),
    ['API_KEY', 'PASSWORD'],
  )
})

test('a search also preserves the original indices', () => {
  const found = visibleEnvironmentVariables(list, 'variables', 'port')
  assert.deepEqual(found.map((row) => row.index), [2])
})

// A variable is secret or it is not. Showing a secret in the plain tab would
// put its value on screen in a field with no masking.
test('the two tabs partition the list rather than overlapping', () => {
  const plain = visibleEnvironmentVariables(list, 'variables', '').map((row) => row.index)
  const secrets = visibleEnvironmentVariables(list, 'secrets', '').map((row) => row.index)
  assert.deepEqual(plain, [0, 2])
  assert.deepEqual(secrets, [1, 3])
  assert.equal(plain.filter((index) => secrets.includes(index)).length, 0)
  assert.equal(plain.length + secrets.length, list.length)
})

test('an empty query shows everything in the tab', () => {
  assert.equal(visibleEnvironmentVariables(list, 'variables', '').length, 2)
  assert.equal(visibleEnvironmentVariables(undefined, 'variables', '').length, 0)
})

test('a variable matches on its value as well as its name', () => {
  assert.equal(environmentVariableMatches(variable({ name: 'HOST', value: 'api.test' }), 'api'), true)
  assert.equal(environmentVariableMatches(variable({ name: 'HOST', value: 'api.test' }), 'host'), true)
  assert.equal(environmentVariableMatches(variable(), 'nothing'), false)
  assert.equal(environmentVariableMatches(variable(), ''), true)
})

// searchHit lowercases the candidate but not the needle. An uppercase query
// passed straight in matches nothing, which reads as an empty result set rather
// than a bug — so the contract is that every query comes through
// normalizedSearch, and that is asserted here rather than assumed.
test('a query routed through normalizedSearch matches regardless of case', () => {
  for (const raw of ['HOST', '  Host  ', 'hOsT']) {
    assert.equal(
      visibleEnvironmentVariables(list, 'variables', normalizedSearch(raw)).length,
      1,
      raw,
    )
  }
})

test('normalizedSearch trims and lowercases', () => {
  assert.equal(normalizedSearch('  MiXeD  '), 'mixed')
  assert.equal(normalizedSearch(''), '')
})

// The label names what the button adds, and the two tabs add different things.
// One label for both would offer "Add variable" on the secrets tab, where the
// row it creates is a secret.
test('the add label names what the tab adds', () => {
  assert.equal(environmentVariableAddLabel('secrets'), 'Add secret')
  assert.equal(environmentVariableAddLabel('variables'), 'Add variable')
  assert.notEqual(
    environmentVariableAddLabel('secrets'),
    environmentVariableAddLabel('variables'),
  )
})
