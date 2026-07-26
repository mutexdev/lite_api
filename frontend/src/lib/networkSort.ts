// Sorting the DevTools network table.
//
// A column header is a tri-state control: click once to sort ascending, again
// for descending, again to turn sorting off entirely. The third state matters —
// without it there is no way back to the log's natural chronological order,
// which is the order that makes a request sequence readable.

import type { types } from '../../wailsjs/go/models'

export type NetworkSortKey = 'method' | 'status' | 'domain' | 'path' | 'time' | 'duration' | 'size'
export type NetworkSortDirection = 'asc' | 'desc' | ''

export type NetworkSort = {
  key: NetworkSortKey | ''
  direction: NetworkSortDirection
}

/**
 * nextNetworkSort returns the sort state after clicking a column header.
 *
 * Clicking a DIFFERENT column always starts ascending rather than inheriting
 * the previous column's direction: the direction was a statement about the old
 * column, and carrying it over means the first click on a new column lands on a
 * state the user did not choose.
 */
export function nextNetworkSort(
  currentKey: NetworkSortKey | '',
  currentDirection: NetworkSortDirection,
  clicked: NetworkSortKey
): NetworkSort {
  if (currentKey !== clicked) {
    return { key: clicked, direction: 'asc' }
  }
  if (currentDirection === 'asc') {
    return { key: clicked, direction: 'desc' }
  }
  if (currentDirection === 'desc') {
    // Off, not back to ascending. This is the only route to the natural order.
    return { key: '', direction: '' }
  }
  return { key: clicked, direction: 'asc' }
}

/**
 * The visible sort indicator for a column, or "" when it is not the sorted one.
 */
export function networkSortLabel(
  key: NetworkSortKey,
  activeKey: NetworkSortKey | '',
  direction: NetworkSortDirection
): string {
  if (activeKey !== key || !direction) return ''
  return direction === 'asc' ? 'ascending' : 'descending'
}

/**
 * The aria-sort attribute for a column.
 *
 * Nearly the same as networkSortLabel and deliberately not shared: aria-sort has
 * a fixed vocabulary in which the "not sorted" value is the STRING "none", while
 * the visible label wants an empty string so nothing renders. Returning "" to a
 * screen reader would be an invalid attribute value, and returning "none" to the
 * template would print the word "none" beside the column heading.
 */
export function networkSortAriaValue(
  key: NetworkSortKey,
  activeKey: NetworkSortKey | '',
  direction: NetworkSortDirection
): 'ascending' | 'descending' | 'none' {
  if (activeKey !== key || !direction) return 'none'
  return direction === 'asc' ? 'ascending' : 'descending'
}

/** The method as displayed and sorted. Absent means GET, the HTTP default. */
export function normalizedNetworkMethod(row: types.NetworkLog): string {
  return (row.method || 'GET').toUpperCase()
}

/**
 * The host column. Falls back to the raw URL, then to "-".
 *
 * A relative or malformed URL is not an error worth hiding the row for: the
 * request happened, and showing what was attempted is more useful than a blank
 * cell. "-" rather than "" so the column reads as empty-on-purpose.
 */
export function networkDomain(row: types.NetworkLog): string {
  try {
    const parsed = new URL(row.url)
    return parsed.host || row.url || '-'
  } catch {
    return row.url || '-'
  }
}

/**
 * The path column, including the query.
 *
 * The query is kept because two rows to the same path with different parameters
 * are different requests, and a table that renders them identically makes the
 * log useless for exactly the case people open it for.
 */
export function networkPath(row: types.NetworkLog): string {
  try {
    const parsed = new URL(row.url)
    return `${parsed.pathname || '/'}${parsed.search}`
  } catch {
    return row.url || '-'
  }
}

/** Sort key for the time column. An unusable timestamp sorts as 0. */
export function networkLogTimestamp(row: types.NetworkLog): number {
  if (!row.at) return 0
  const value = new Date(row.at)
  return Number.isNaN(value.getTime()) ? 0 : value.getTime()
}

/** The comparable value behind a column. */
export function networkSortValue(row: types.NetworkLog, key: NetworkSortKey): string | number {
  if (key === 'method') return normalizedNetworkMethod(row)
  if (key === 'status') return row.status ?? 0
  if (key === 'domain') return networkDomain(row)
  if (key === 'path') return networkPath(row)
  if (key === 'time') return networkLogTimestamp(row)
  if (key === 'duration') return row.durationMs ?? 0
  return row.size ?? 0
}

/**
 * Sorts the rows, or returns them untouched when sorting is off.
 *
 * Untouched means the SAME ARRAY, not a copy: the off state exists to show the
 * log in arrival order, and re-sorting it by anything would defeat that.
 *
 * Numbers compare numerically and everything else by locale. Comparing a status
 * as a string would put 404 before 5, which is the one ordering a status column
 * must never produce.
 */
export function sortNetworkRows(
  rows: types.NetworkLog[],
  key: NetworkSortKey | '',
  direction: NetworkSortDirection
): types.NetworkLog[] {
  if (!key || !direction) return rows
  const multiplier = direction === 'asc' ? 1 : -1
  return [...rows].sort((left, right) => {
    const leftValue = networkSortValue(left, key)
    const rightValue = networkSortValue(right, key)
    if (typeof leftValue === 'number' && typeof rightValue === 'number') {
      return (leftValue - rightValue) * multiplier
    }
    return String(leftValue).localeCompare(String(rightValue)) * multiplier
  })
}

/** The column widths a table that has never been resized starts with. */
export const DEFAULT_NETWORK_COLUMN_WIDTHS = [80, 70, 180, 300, 110, 100, 80]

/** The narrowest a column may be dragged or restored to. */
const MIN_NETWORK_COLUMN_WIDTH = 60

/**
 * Maps a stored sort key onto a real column, or "" for no sorting.
 *
 * A key naming a column that no longer exists would leave the comparator
 * reading an absent field on every row, which sorts by nothing while still
 * claiming to be sorted.
 */
export function normalizedNetworkSortKey(
  value: string | undefined,
  keys: readonly NetworkSortKey[]
): NetworkSortKey | '' {
  return keys.includes(value as NetworkSortKey) ? (value as NetworkSortKey) : ''
}

export function normalizedNetworkSortDirection(value: string | undefined): NetworkSortDirection {
  return value === 'asc' || value === 'desc' ? value : ''
}

/**
 * Restores stored column widths, falling back wholesale on any mismatch.
 *
 * The widths are positional, so a stored array of the wrong length is not
 * partially usable: applying it would shift every column onto the wrong
 * header. A build that adds or removes a column must therefore reset, not
 * merge — which is why the length check is an equality and not a minimum.
 */
export function normalizedNetworkColumnWidths(widths: number[] | undefined): number[] {
  if (!widths || widths.length !== DEFAULT_NETWORK_COLUMN_WIDTHS.length) {
    return [...DEFAULT_NETWORK_COLUMN_WIDTHS]
  }
  return widths.map((width) => Math.max(MIN_NETWORK_COLUMN_WIDTH, Math.round(Number(width) || 0)))
}

/**
 * The sort key and direction as they should be persisted.
 *
 * The two are stored together because they are only meaningful together: a
 * direction with no key, or a key with no direction, describes a state the
 * table cannot be in, and restoring either half would show a header marked as
 * sorted over rows in arrival order.
 */
export function networkSortPreference(
  sortKey: NetworkSortKey | '',
  sortDirection: NetworkSortDirection,
  keys: readonly NetworkSortKey[]
): NetworkSort {
  const key = normalizedNetworkSortKey(sortKey, keys)
  const direction = key ? normalizedNetworkSortDirection(sortDirection) : ''
  return { key: direction ? key : '', direction }
}

/**
 * Resizes one column against its right-hand neighbour.
 *
 * The two move together and by the same amount, so the table's TOTAL width is
 * invariant across a drag. That is what keeps the header aligned with the rows
 * under it: the columns are laid out from these widths, and a total that grows
 * pushes the last column off the panel with no way to drag it back.
 *
 * The delta is clamped against BOTH minimums, not one. Clamping only the column
 * being dragged lets the neighbour collapse to nothing; clamping only the
 * neighbour lets the dragged column go negative, which flips the layout. Each
 * bound is the distance that column has left before it hits the floor.
 *
 * A drag on the last column is a no-op, because there is no neighbour to take
 * the width from and the only alternative would be changing the total.
 */
export function resizeAdjacentColumns(
  startWidths: readonly number[],
  index: number,
  delta: number
): number[] {
  const next = [...startWidths]
  if (index < 0 || index + 1 >= next.length) return next
  const left = startWidths[index]
  const right = startWidths[index + 1]
  const clamped = Math.max(
    -(left - MIN_NETWORK_COLUMN_WIDTH),
    Math.min(right - MIN_NETWORK_COLUMN_WIDTH, delta)
  )
  next[index] = left + clamped
  next[index + 1] = right - clamped
  return next
}
