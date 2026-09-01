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
  resizeAdjacentColumns,
  normalizedNetworkColumnWidths,
  normalizedNetworkSortDirection,
  normalizedNetworkSortKey,
  networkSortPreference,
  NETWORK_COLUMNS,
  NETWORK_METHODS,
  NETWORK_SORT_KEYS,
  filteredNetworkRows,
  networkHeaderRows,
  networkLogBody,
  networkLogLines,
  networkSizeDisplay,
  networkStatusDisplay,
  networkLogTime,
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

// The two columns move together and by the same amount, so the table's total
// width is invariant across a drag. A total that grows pushes the last column
// off the panel with no way to drag it back.
test('resizing a column takes the width from its neighbour', () => {
  const widths = [100, 200, 300]
  const next = resizeAdjacentColumns(widths, 0, 40)
  assert.deepEqual(next, [140, 160, 300])
  assert.equal(
    next.reduce((sum, w) => sum + w, 0),
    widths.reduce((sum, w) => sum + w, 0)
  )
})

test('dragging left narrows the column and widens its neighbour', () => {
  assert.deepEqual(resizeAdjacentColumns([100, 200, 300], 0, -30), [70, 230, 300])
})

// Clamping only the dragged column lets the neighbour collapse to nothing;
// clamping only the neighbour lets the dragged column go negative, which flips
// the layout. Each bound is the distance that column has left.
test('a drag is clamped against both columns, not one', () => {
  const widths = [100, 80, 300]
  assert.deepEqual(resizeAdjacentColumns(widths, 0, 9999), [120, 60, 300])
  assert.deepEqual(resizeAdjacentColumns(widths, 0, -9999), [60, 120, 300])
})

test('a clamped drag still preserves the total', () => {
  const widths = [100, 80, 300]
  for (const delta of [9999, -9999, 5, -5]) {
    assert.equal(
      resizeAdjacentColumns(widths, 0, delta).reduce((sum, w) => sum + w, 0),
      480,
      String(delta)
    )
  }
})

test('neither column ever falls below the minimum', () => {
  const widths = [70, 65, 300]
  for (const delta of [-9999, -100, -1, 0, 1, 100, 9999]) {
    for (const width of resizeAdjacentColumns(widths, 0, delta)) {
      assert.ok(width >= 60, `${delta} produced ${width}`)
    }
  }
})

// There is no neighbour to take the width from, and the only alternative would
// be changing the total.
test('dragging the last column does nothing', () => {
  const widths = [100, 200, 300]
  assert.deepEqual(resizeAdjacentColumns(widths, 2, 50), widths)
  assert.deepEqual(resizeAdjacentColumns(widths, -1, 50), widths)
  assert.notEqual(resizeAdjacentColumns(widths, 2, 50), widths)
})

// The same column with NO direction is a state nextNetworkSort never produces —
// turning sorting off clears the key as well. It can only arrive from a stored
// preference written inconsistently, or from a build whose normalizer differed.
// Clicking recovers to ascending rather than doing nothing, which is what makes
// the header usable again instead of dead.
test('clicking a column that is current but has no direction restarts ascending', () => {
  assert.deepEqual(nextNetworkSort('status', '', 'status'), { key: 'status', direction: 'asc' })
})

// A URL with no host — file:, mailto:, data: — parses successfully, so the
// catch never runs and `parsed.host` is the empty string. Falling back to the
// raw URL keeps the row identifiable; showing "" would leave a blank domain
// cell beside a request that plainly went somewhere.
test('a hostless scheme falls back to the raw url for its domain', () => {
  for (const url of ['file:///tmp/x.json', 'mailto:a@b.test', 'data:text/plain,hi']) {
    assert.equal(networkDomain(row({ url })), url, url)
  }
  assert.equal(networkDomain(row({ url: 'grpc://host/svc' })), 'host', 'a real host still wins')
})

// The path of a hostless URL is still its meaningful part, so it is shown
// rather than falling back — file:///tmp/x.json is about /tmp/x.json.
test('a hostless scheme still yields its path', () => {
  assert.equal(networkPath(row({ url: 'file:///tmp/x.json' })), '/tmp/x.json')
  assert.equal(networkPath(row({ url: 'mailto:a@b.test' })), 'a@b.test')
})

// A stored width that is not a number reaches Math.round as NaN, and a NaN
// width collapses the column to nothing with no way to drag it back. The `|| 0`
// turns it into the minimum instead.
test('an unusable stored width becomes the minimum, not NaN', () => {
  const withNaN = [...DEFAULT_NETWORK_COLUMN_WIDTHS]
  withNaN[6] = Number.NaN
  assert.equal(normalizedNetworkColumnWidths(withNaN)[6], 60)

  const withText = [...DEFAULT_NETWORK_COLUMN_WIDTHS] as unknown as number[]
  withText[0] = 'wide' as unknown as number
  assert.equal(normalizedNetworkColumnWidths(withText)[0], 60)
})


// ---------------------------------------------------------------------------
// The table became a component (A9-02), and these are the pieces it needed to
// take with it: the column list, the method filter, and the six cell
// formatters that used to be private functions inside App.svelte.
// ---------------------------------------------------------------------------

// normalizedNetworkColumnWidths rejects a stored array whose length does not
// match the default — that is the guard that stops a build which adds a column
// from shifting every restored width onto the wrong header. The guard is only
// as good as the two lists agreeing, and until they lived in one file nothing
// checked that they did.
test('the column list and the default width list describe the same table', () => {
  assert.equal(NETWORK_COLUMNS.length, DEFAULT_NETWORK_COLUMN_WIDTHS.length)
  assert.deepEqual(NETWORK_SORT_KEYS, NETWORK_COLUMNS.map((column) => column.key))
  for (const key of NETWORK_SORT_KEYS) {
    assert.equal(normalizedNetworkSortKey(key, NETWORK_SORT_KEYS), key, `${key} is not a sortable column`)
  }
})

// Every column heading is a word a reader recognises, not the field name behind
// it — the failure mode A1-07 catalogued in the option lists two panes over.
test('every column carries a written label', () => {
  for (const column of NETWORK_COLUMNS) {
    assert.match(column.label, /^[A-Z][A-Za-z ]+$/, `${column.key} has no readable label`)
  }
})

test('the method filter keeps only the ticked methods', () => {
  const rows = [row({ id: '1', method: 'get' }), row({ id: '2', method: 'POST' }), row({ id: '3' })]
  const filters = Object.fromEntries(NETWORK_METHODS.map((method) => [method, true]))
  assert.equal(filteredNetworkRows(rows, filters).length, 3, 'an absent method counts as GET')
  assert.deepEqual(
    filteredNetworkRows(rows, { ...filters, GET: false }).map((r) => (r as { id: string }).id),
    ['2']
  )
})

// Documented rather than fixed: a method outside the filter bar's seven has no
// checkbox, so `filters[method] === true` is false and the row is invisible
// with no control that reveals it. Preserved from App.svelte deliberately —
// changing it is a product decision. This test exists so the next person to
// read `filteredNetworkRows` finds the behaviour asserted, not inferred.
test('a method the filter bar does not list is hidden with no way to show it', () => {
  const filters = Object.fromEntries(NETWORK_METHODS.map((method) => [method, true]))
  assert.deepEqual(filteredNetworkRows([row({ id: '1', method: 'TRACE' })], filters), [])
})

test('the status and size cells say "-" and "0 B" rather than nothing', () => {
  assert.equal(networkStatusDisplay(undefined), '-')
  assert.equal(networkStatusDisplay(0), '-', 'no response arrived is not status zero')
  assert.equal(networkStatusDisplay(404), '404')
  assert.equal(networkSizeDisplay(undefined), '0 B')
  assert.equal(networkSizeDisplay(1536), '1.5 KB')
})

// "-", not "" and not "—". This disagrees with formatting.ts, which writes an
// em dash for an absent status and an empty string for an absent time; the
// vocabularies should converge and the handoff says so. Asserted here at the
// value App.svelte shipped, so that convergence is a deliberate edit to a
// failing test rather than a silent change to what the table renders.
test('an unusable timestamp renders as "-"', () => {
  assert.equal(networkLogTime(row({ at: undefined })), '-')
  assert.equal(networkLogTime(row({ at: 'not a date' })), '-')
  assert.notEqual(networkLogTime(row({ at: '2026-08-31T10:00:00Z' })), '-')
})

// Sorted by name, because the pane exists to answer "is this header set" — a
// question answered by looking a name up, which needs a stable place to look.
test('header rows are sorted by name and survive an absent map', () => {
  assert.deepEqual(networkHeaderRows(undefined), [])
  assert.deepEqual(
    networkHeaderRows({ 'X-Trace': '1', Accept: 'application/json' }),
    [['Accept', 'application/json'], ['X-Trace', '1']]
  )
})

// A body of nothing but whitespace is not a body: rendering it puts an empty
// <pre> where the "No body" empty state belongs.
test('a whitespace-only body counts as no body', () => {
  assert.equal(networkLogBody('   \n\t '), '')
  assert.equal(networkLogBody(undefined), '')
  assert.equal(networkLogBody(' {"a":1} '), ' {"a":1} ', 'a real body keeps its own whitespace')
})

test('the network log lines drop the error line when there is no error', () => {
  assert.equal(networkLogLines(undefined).length, 0)
  const clean = networkLogLines(row({ durationMs: 12, size: 30, error: '' }))
  assert.equal(clean.length, 3)
  assert.ok(clean.every((line) => !line.startsWith('Error:')))
  const failed = networkLogLines(row({ durationMs: 12, size: 30, error: 'dial tcp: refused' }))
  assert.equal(failed.length, 4)
  assert.equal(failed[3], 'Error: dial tcp: refused')
})
