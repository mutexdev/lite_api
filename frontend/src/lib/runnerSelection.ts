// Choosing which requests a collection run executes.
//
// The selection is stored as a list of ids, and the runner executes them in
// that list's order. That single fact is why selection is not a Set and why
// adding an id is not a push — the order the user happens to CLICK checkboxes
// is not the order the requests are meant to run in, and a run that fires the
// login request third because it was ticked third fails in a way that looks
// like a broken API rather than a broken selection.

import type { types } from '../../wailsjs/go/models'

/**
 * Whether a request can appear in a collection run.
 *
 * An item with NO type is runnable: requests written before the field existed
 * are HTTP, and excluding them would silently empty the runner for every older
 * collection.
 *
 * WebSocket is the exclusion that matters. A socket is a session, not a
 * request-response pair, so there is nothing for a sequential runner to wait
 * for or assert on.
 */
export function runnerItemIsRunnable(item: types.RequestItem): boolean {
  return !item.type || item.type === 'http' || item.type === 'graphql' || item.type === 'grpc'
}

/** The requests in a collection the runner can offer. */
export function runnerSelectableItems(collection: types.Collection | undefined): types.RequestItem[] {
  return (collection?.items ?? []).filter(runnerItemIsRunnable)
}

/**
 * Adds or removes one id, keeping the selection in the LIST's order.
 *
 * Selecting rebuilds the id list by walking `items` rather than appending,
 * which is the whole point: appending would order the run by click sequence.
 * Deselecting can filter, because removing an id cannot disturb the order of
 * the ones that remain.
 *
 * Ids not present in `items` are dropped on any select, since the rebuild only
 * emits ids it finds — a request deleted while the runner screen was open
 * cannot survive into the run.
 */
export function setRunnerItemSelected(
  selectedIds: readonly string[],
  items: readonly types.RequestItem[],
  itemId: string,
  selected: boolean
): string[] {
  if (!selected) return selectedIds.filter((id) => id !== itemId)
  const ids = new Set(selectedIds)
  ids.add(itemId)
  return items.filter((item) => ids.has(item.id)).map((item) => item.id)
}

/**
 * The result of the select-all checkbox.
 *
 * Selecting everything re-derives from `items`, so it also repairs a selection
 * holding ids that are gone.
 */
export function toggleRunnerSelectAll(
  selectedCount: number,
  items: readonly types.RequestItem[]
): string[] {
  if (selectedCount === items.length) return []
  return items.map((item) => item.id)
}

/**
 * How many of the selected ids are still real.
 *
 * Counting the raw id list instead would let a deleted request keep the
 * select-all checkbox in its "all selected" state forever, since the count
 * could never fall to match the shorter item list.
 */
export function runnerSelectedCount(
  selectedIds: readonly string[],
  items: readonly types.RequestItem[]
): number {
  return selectedIds.filter((id) => items.some((item) => item.id === id)).length
}
