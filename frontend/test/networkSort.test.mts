// The DevTools network table's tri-state column sort.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { nextNetworkSort, networkSortLabel, networkSortAriaValue } from '../src/lib/networkSort.ts'

// The third state is the point. Without it there is no way back to the log's
// natural chronological order, which is what makes a request sequence readable.
test('clicking one column cycles ascending, descending, off', () => {
  const first = nextNetworkSort('', '', 'status')
  assert.deepEqual(first, { key: 'status', direction: 'asc' })

  const second = nextNetworkSort(first.key as never, first.direction, 'status')
  assert.deepEqual(second, { key: 'status', direction: 'desc' })

  const third = nextNetworkSort(second.key as never, second.direction, 'status')
  assert.deepEqual(third, { key: '', direction: '' }, 'the third click turns sorting off, not back to ascending')

  const fourth = nextNetworkSort(third.key as never, third.direction, 'status')
  assert.deepEqual(fourth, { key: 'status', direction: 'asc' }, 'and the cycle restarts')
})

// The direction was a statement about the OLD column; carrying it over lands
// the first click on a new column in a state nobody chose.
test('clicking a different column always starts ascending', () => {
  assert.deepEqual(nextNetworkSort('status', 'desc', 'duration'), { key: 'duration', direction: 'asc' })
  assert.deepEqual(nextNetworkSort('status', 'asc', 'duration'), { key: 'duration', direction: 'asc' })
})

test('every column can be sorted', () => {
  for (const key of ['method', 'status', 'domain', 'path', 'time', 'duration', 'size'] as const) {
    assert.deepEqual(nextNetworkSort('', '', key), { key, direction: 'asc' }, key)
  }
})

test('the label shows only on the sorted column', () => {
  assert.equal(networkSortLabel('status', 'status', 'asc'), 'ascending')
  assert.equal(networkSortLabel('status', 'status', 'desc'), 'descending')
  assert.equal(networkSortLabel('status', 'duration', 'asc'), '', 'an unsorted column shows nothing')
  assert.equal(networkSortLabel('status', 'status', ''), '', 'sorting off shows nothing')
  assert.equal(networkSortLabel('status', '', ''), '')
})

// aria-sort has a fixed vocabulary where "not sorted" is the string "none".
// Returning "" would be an invalid attribute value.
test('aria-sort says none rather than empty', () => {
  assert.equal(networkSortAriaValue('status', 'status', 'asc'), 'ascending')
  assert.equal(networkSortAriaValue('status', 'status', 'desc'), 'descending')
  assert.equal(networkSortAriaValue('status', 'duration', 'asc'), 'none')
  assert.equal(networkSortAriaValue('status', 'status', ''), 'none')
})

// The two functions agree on WHICH column is sorted and disagree only on how
// they spell "not this one" — that difference is the reason both exist.
test('the label and the aria value agree except on the unsorted spelling', () => {
  const columns = ['method', 'status', 'domain', 'path', 'time', 'duration', 'size'] as const
  for (const key of columns) {
    for (const active of [...columns, '' as const]) {
      for (const direction of ['asc', 'desc', ''] as const) {
        const label = networkSortLabel(key, active, direction)
        const aria = networkSortAriaValue(key, active, direction)
        if (label === '') {
          assert.equal(aria, 'none', `${key}/${active}/${direction}: label empty but aria ${aria}`)
        } else {
          assert.equal(aria, label, `${key}/${active}/${direction}: they disagree on direction`)
        }
      }
    }
  }
})
