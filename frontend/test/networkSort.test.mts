// The DevTools network table's tri-state column sort.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  nextNetworkSort,
  networkSortLabel,
  networkSortAriaValue,
  normalizedNetworkMethod,
  networkDomain,
  networkPath,
  networkLogTimestamp,
  sortNetworkRows,
  DEFAULT_NETWORK_COLUMN_WIDTHS,
  normalizedNetworkColumnWidths,
  normalizedNetworkSortDirection,
  normalizedNetworkSortKey,
  networkSortPreference,
} from '../src/lib/networkSort.ts'

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

const row = (o: Record<string, unknown>) => ({ url: 'https://api.test/x', ...o }) as never

test('an absent method reads as GET, the HTTP default', () => {
  assert.equal(normalizedNetworkMethod(row({})), 'GET')
  assert.equal(normalizedNetworkMethod(row({ method: 'post' })), 'POST')
})

// The request happened; showing what was attempted beats a blank cell.
test('a malformed url still yields a domain and path', () => {
  assert.equal(networkDomain(row({ url: 'not a url' })), 'not a url')
  assert.equal(networkPath(row({ url: 'not a url' })), 'not a url')
  assert.equal(networkDomain(row({ url: '' })), '-', 'a dash reads as empty-on-purpose')
  assert.equal(networkPath(row({ url: '' })), '-')
})

test('domain and path split a real url', () => {
  assert.equal(networkDomain(row({ url: 'https://api.test:8080/v1/users?a=1' })), 'api.test:8080')
  assert.equal(networkPath(row({ url: 'https://api.test/v1/users?a=1' })), '/v1/users?a=1')
  assert.equal(networkPath(row({ url: 'https://api.test' })), '/', 'a bare host still has a path')
})

// Two rows to one path with different parameters are different requests, and a
// table rendering them identically is useless for what the log is opened for.
test('the path keeps its query string', () => {
  assert.notEqual(
    networkPath(row({ url: 'https://api.test/search?q=a' })),
    networkPath(row({ url: 'https://api.test/search?q=b' }))
  )
})

test('an unusable timestamp sorts as zero', () => {
  assert.equal(networkLogTimestamp(row({ at: '' })), 0)
  assert.equal(networkLogTimestamp(row({ at: 'rubbish' })), 0)
  assert.equal(networkLogTimestamp(row({})), 0)
  assert.ok(networkLogTimestamp(row({ at: '2030-01-01T00:00:00Z' })) > 0)
})

// Comparing a status as a string puts 404 before 5 — the one ordering a status
// column must never produce.
test('numeric columns compare numerically, not lexically', () => {
  const rows = [row({ status: 404 }), row({ status: 5 }), row({ status: 200 })]
  assert.deepEqual(sortNetworkRows(rows, 'status', 'asc').map((r) => r.status), [5, 200, 404])
  assert.deepEqual(sortNetworkRows(rows, 'duration', 'asc').map((r) => r.durationMs ?? 0), [0, 0, 0])

  const durations = [row({ durationMs: 1000 }), row({ durationMs: 9 })]
  assert.deepEqual(sortNetworkRows(durations, 'duration', 'asc').map((r) => r.durationMs), [9, 1000])
})

test('text columns compare by locale', () => {
  const rows = [row({ url: 'https://z.test/a' }), row({ url: 'https://a.test/a' })]
  assert.deepEqual(sortNetworkRows(rows, 'domain', 'asc').map((r) => networkDomain(r)), ['a.test', 'z.test'])
})

test('descending reverses the order', () => {
  const rows = [row({ status: 200 }), row({ status: 500 })]
  assert.deepEqual(sortNetworkRows(rows, 'status', 'desc').map((r) => r.status), [500, 200])
})

// The off state exists to show the log in arrival order, so it must not
// re-sort — and returning the same array is how the caller can tell.
test('sorting off returns the rows untouched', () => {
  const rows = [row({ status: 500 }), row({ status: 200 })]
  assert.equal(sortNetworkRows(rows, '', ''), rows, 'the same array, not a sorted copy')
  assert.equal(sortNetworkRows(rows, 'status', ''), rows)
  assert.deepEqual(sortNetworkRows(rows, '', 'asc').map((r) => r.status), [500, 200])
})

// Sorting in place would reorder the live log under the table.
test('sorting does not mutate the input', () => {
  const rows = [row({ status: 500 }), row({ status: 200 })]
  sortNetworkRows(rows, 'status', 'asc')
  assert.deepEqual(rows.map((r) => r.status), [500, 200])
})

// Each numeric column needs its own case: a missing value falls out of the
// `typeof === 'number'` branch into string comparison, which still produces a
// deterministic — and wrong — order. Testing one column proves nothing about
// the others, which a control demonstrated by leaving `status` unguarded and
// failing nothing.
test('a missing numeric field sorts as zero rather than breaking the comparator', () => {
  for (const key of ['size', 'status', 'duration'] as const) {
    const field = key === 'duration' ? 'durationMs' : key
    const rows = [row({ [field]: 10 }), row({}), row({ [field]: 5 })]
    const sorted = sortNetworkRows(rows, key, 'asc').map((r) => (r as never as Record<string, number>)[field] ?? 0)
    assert.deepEqual(sorted, [0, 5, 10], key)
  }
})

// A key naming a column that no longer exists leaves the comparator reading an
// absent field on every row: it sorts by nothing while the header still claims
// to be sorted.
test('a sort key for a column that no longer exists is discarded', () => {
  const keys = ['method', 'status', 'domain'] as const
  assert.equal(normalizedNetworkSortKey('status', keys), 'status')
  assert.equal(normalizedNetworkSortKey('initiator', keys), '')
  assert.equal(normalizedNetworkSortKey(undefined, keys), '')
})

test('a sort direction is one of the two, or empty', () => {
  assert.equal(normalizedNetworkSortDirection('asc'), 'asc')
  assert.equal(normalizedNetworkSortDirection('desc'), 'desc')
  assert.equal(normalizedNetworkSortDirection('ASC'), '')
  assert.equal(normalizedNetworkSortDirection(undefined), '')
})

test('stored column widths round-trip when they match the table', () => {
  const widths = DEFAULT_NETWORK_COLUMN_WIDTHS.map((width) => width + 10)
  assert.deepEqual(normalizedNetworkColumnWidths(widths), widths)
})

// The widths are positional. A stored array of the wrong length is not
// partially usable — applying it shifts every column onto the wrong header — so
// a build that adds or removes a column must reset rather than merge.
test('column widths of the wrong length are replaced wholesale, not padded', () => {
  const short = DEFAULT_NETWORK_COLUMN_WIDTHS.slice(0, 3)
  assert.deepEqual(normalizedNetworkColumnWidths(short), DEFAULT_NETWORK_COLUMN_WIDTHS)
  assert.deepEqual(
    normalizedNetworkColumnWidths([...DEFAULT_NETWORK_COLUMN_WIDTHS, 90]),
    DEFAULT_NETWORK_COLUMN_WIDTHS
  )
  assert.deepEqual(normalizedNetworkColumnWidths(undefined), DEFAULT_NETWORK_COLUMN_WIDTHS)
})

// The defaults are handed to a caller that will mutate them as the user drags.
// Returning the module's own array would let one resize rewrite the fallback
// every later reset restores to.
test('the fallback widths are a fresh array each time', () => {
  const first = normalizedNetworkColumnWidths(undefined)
  first[0] = 999
  assert.deepEqual(normalizedNetworkColumnWidths(undefined), DEFAULT_NETWORK_COLUMN_WIDTHS)
  assert.notEqual(normalizedNetworkColumnWidths(undefined), DEFAULT_NETWORK_COLUMN_WIDTHS)
})

test('a column dragged to nothing is floored rather than vanishing', () => {
  const widths = DEFAULT_NETWORK_COLUMN_WIDTHS.map(() => 0)
  assert.deepEqual(normalizedNetworkColumnWidths(widths), DEFAULT_NETWORK_COLUMN_WIDTHS.map(() => 60))
})

// A direction with no key, or a key with no direction, describes a state the
// table cannot be in. Restoring either half shows a header marked as sorted
// over rows in arrival order.
test('the persisted sort keeps its two halves consistent', () => {
  const keys = ['method', 'status'] as const
  assert.deepEqual(networkSortPreference('status', 'desc', keys), { key: 'status', direction: 'desc' })
  assert.deepEqual(networkSortPreference('status', '', keys), { key: '', direction: '' })
  assert.deepEqual(networkSortPreference('', 'desc', keys), { key: '', direction: '' })
  assert.deepEqual(networkSortPreference('gone' as never, 'asc', keys), { key: '', direction: '' })
})
