// Sorting the DevTools network table.
//
// A column header is a tri-state control: click once to sort ascending, again
// for descending, again to turn sorting off entirely. The third state matters —
// without it there is no way back to the log's natural chronological order,
// which is the order that makes a request sequence readable.

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
