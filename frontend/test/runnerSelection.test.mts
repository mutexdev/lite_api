import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  runnerItemIsRunnable,
  runnerSelectableItems,
  runnerSelectedCount,
  setRunnerItemSelected,
  toggleRunnerSelectAll,
} from '../src/lib/runnerSelection.ts'
import type { types } from '../wailsjs/go/models'

function item(id: string, type?: string): types.RequestItem {
  return { id, name: id, type } as types.RequestItem
}

const items = [item('login', 'http'), item('fetch', 'graphql'), item('stream', 'grpc')]

// Requests written before the type field existed are HTTP. Excluding them would
// silently empty the runner for every older collection.
test('an item with no type is runnable', () => {
  assert.equal(runnerItemIsRunnable(item('legacy')), true)
})

// A socket is a session, not a request-response pair, so there is nothing for a
// sequential runner to wait for or assert on.
test('a websocket request is not runnable', () => {
  assert.equal(runnerItemIsRunnable(item('ws', 'websocket')), false)
})

test('http, graphql and grpc are runnable', () => {
  for (const type of ['http', 'graphql', 'grpc']) {
    assert.equal(runnerItemIsRunnable(item('x', type)), true, type)
  }
})

test('only runnable items are offered', () => {
  const collection = {
    items: [item('a', 'http'), item('b', 'websocket'), item('c')],
  } as types.Collection
  assert.deepEqual(runnerSelectableItems(collection).map((i) => i.id), ['a', 'c'])
  assert.deepEqual(runnerSelectableItems(undefined), [])
})

// THE PROPERTY THE MODULE EXISTS FOR. The runner executes the id list in order,
// so appending would order the run by CLICK SEQUENCE. A run that fires the
// login request third because it was ticked third fails in a way that looks
// like a broken API rather than a broken selection.
test('selecting keeps the list order, not the click order', () => {
  let selected: string[] = []
  for (const id of ['stream', 'login', 'fetch']) {
    selected = setRunnerItemSelected(selected, items, id, true)
  }
  assert.deepEqual(selected, ['login', 'fetch', 'stream'])
})

test('selecting one item at a time builds up in list order', () => {
  const afterStream = setRunnerItemSelected([], items, 'stream', true)
  assert.deepEqual(afterStream, ['stream'])
  assert.deepEqual(setRunnerItemSelected(afterStream, items, 'login', true), ['login', 'stream'])
})

// Removing an id cannot disturb the order of the ones that remain, so
// deselecting may filter.
test('deselecting removes just that id and keeps the rest in order', () => {
  assert.deepEqual(
    setRunnerItemSelected(['login', 'fetch', 'stream'], items, 'fetch', false),
    ['login', 'stream'],
  )
})

test('deselecting something that is not selected changes nothing', () => {
  assert.deepEqual(setRunnerItemSelected(['login'], items, 'fetch', false), ['login'])
})

test('selecting something already selected does not duplicate it', () => {
  assert.deepEqual(setRunnerItemSelected(['login'], items, 'login', true), ['login'])
})

// A request deleted while the runner screen was open cannot survive into the
// run — the rebuild only emits ids it finds in the item list.
test('a selected id that no longer exists is dropped on the next select', () => {
  assert.deepEqual(setRunnerItemSelected(['gone', 'login'], items, 'fetch', true), ['login', 'fetch'])
})

test('select-all selects everything when some are unselected', () => {
  assert.deepEqual(toggleRunnerSelectAll(1, items), ['login', 'fetch', 'stream'])
  assert.deepEqual(toggleRunnerSelectAll(0, items), ['login', 'fetch', 'stream'])
})

test('select-all clears when everything is already selected', () => {
  assert.deepEqual(toggleRunnerSelectAll(items.length, items), [])
})

// Selecting everything re-derives from the item list, so it also repairs a
// selection holding ids that are gone.
test('select-all repairs a selection holding stale ids', () => {
  assert.deepEqual(toggleRunnerSelectAll(0, items), items.map((i) => i.id))
})

// Counting the raw id list would let a deleted request keep the checkbox in its
// "all selected" state forever, since the count could never fall to match the
// shorter item list.
test('the count ignores ids that no longer exist', () => {
  assert.equal(runnerSelectedCount(['login', 'gone'], items), 1)
  assert.equal(runnerSelectedCount(['login', 'fetch', 'stream'], items), 3)
  assert.equal(runnerSelectedCount([], items), 0)
})

// The two must agree, or select-all toggles the wrong way: a stale id counted
// as selected makes the count equal the length and the click CLEARS instead of
// selecting.
test('the count and select-all agree after a request is deleted', () => {
  const stale = ['login', 'fetch', 'stream', 'gone']
  const shorter = items.slice(0, 3)
  assert.notEqual(stale.length, shorter.length)
  assert.equal(runnerSelectedCount(stale, shorter), shorter.length)
  assert.deepEqual(toggleRunnerSelectAll(runnerSelectedCount(stale, shorter), shorter), [])
})
