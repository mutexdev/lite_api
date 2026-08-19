// Gathering script logs from across the workspace into one console.
//
// A pm.console.log() lands on the response of whichever request produced it, so
// the logs are scattered. This console is the only place they are visible
// together, which is what makes "which request printed that?" answerable — and
// a log that arrives without its origin is a log nobody can act on.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { responseScriptLogs, devToolsConsoleLogs } from '../src/lib/devToolsConsole.ts'

const log = (message: string, level = 'log') => ({ level, message })
const item = (name: string, folderPath: string, logs: unknown[]) =>
  ({ name, folderPath, response: { scriptLogs: logs } })

const workspace = (collections: unknown[]) => ({ collections }) as never

test('logs are read off a response and tolerate their absence', () => {
  assert.deepEqual(responseScriptLogs({ scriptLogs: [log('hi')] } as never), [log('hi')])
  assert.deepEqual(responseScriptLogs({} as never), [], 'a response with no script logs is not an error')
  assert.deepEqual(responseScriptLogs(undefined), [], 'nor is a request that has never run')
})

test('every log in the workspace is collected', () => {
  const rows = devToolsConsoleLogs(
    workspace([
      { name: 'Billing', items: [item('Create', 'invoices', [log('a'), log('b')])] },
      { name: 'Reporting', items: [item('Run', '', [log('c')])] }
    ])
  )
  assert.deepEqual(rows.map((r) => r.message), ['a', 'b', 'c'])
})

// A log whose origin reads with a gap in it looks like the origin was lost.
test('the source breadcrumb omits an empty folder', () => {
  const rows = devToolsConsoleLogs(
    workspace([
      {
        name: 'Billing',
        items: [item('Create', 'invoices', [log('nested')]), item('Health', '', [log('root')])]
      }
    ])
  )
  assert.equal(rows[0].source, 'Billing / invoices / Create')
  assert.equal(rows[1].source, 'Billing / Health', 'no double separator for a root-level request')
})

test('each row carries the collection and request names separately', () => {
  const rows = devToolsConsoleLogs(workspace([{ name: 'Billing', items: [item('Create', 'inv', [log('x')])] }]))
  assert.equal(rows[0].collectionName, 'Billing')
  assert.equal(rows[0].requestName, 'Create')
})

test('the original log fields survive', () => {
  const rows = devToolsConsoleLogs(
    workspace([{ name: 'C', items: [item('R', '', [{ level: 'error', message: 'boom', args: ['a', 'b'] }])] }])
  )
  assert.equal(rows[0].level, 'error')
  assert.equal(rows[0].message, 'boom')
  assert.deepEqual(rows[0].args, ['a', 'b'], 'the args a script logged must not be dropped')
})

// Logs from one run belong together. Interleaving two requests by timestamp
// makes a single script's output impossible to follow.
test('order is by collection then item then emission, not chronological', () => {
  const rows = devToolsConsoleLogs(
    workspace([
      { name: 'A', items: [item('first', '', [log('a1'), log('a2')]), item('second', '', [log('b1')])] },
      { name: 'B', items: [item('third', '', [log('c1')])] }
    ])
  )
  assert.deepEqual(rows.map((r) => r.message), ['a1', 'a2', 'b1', 'c1'])
})

test('an empty or absent workspace yields nothing', () => {
  assert.deepEqual(devToolsConsoleLogs(undefined), [])
  assert.deepEqual(devToolsConsoleLogs(workspace([])), [])
  assert.deepEqual(devToolsConsoleLogs(workspace([{ name: 'C' }])), [], 'a collection with no items')
  assert.deepEqual(
    devToolsConsoleLogs(workspace([{ name: 'C', items: [{ name: 'R', folderPath: '' }] }])),
    [],
    'a request that has never run'
  )
})
