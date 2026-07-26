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
