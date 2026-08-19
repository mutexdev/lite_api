// The environment editor's two tabs and their search.
//
// Small, but one property here is load-bearing and invisible: the row index
// these functions carry is the position in the UNFILTERED list. Every edit
// handler in the editor writes back by index, so an index taken after filtering
// would edit a different variable than the one on screen — silently, and only
// while a search or the secrets tab is active.

import type { types } from '../../wailsjs/go/models'
import { searchHit } from './sidebarFilter.ts'

export type EnvironmentVariableTab = 'variables' | 'secrets'

export type IndexedVariable = {
  variable: types.Variable
  /** Position in the full variable list, NOT in the filtered result. */
  index: number
}

/**
 * Whether a variable matches the search box.
 *
 * `query` must already be lowercased — `searchHit` lowercases the candidate but
 * not the needle, so an uppercase query passed straight through matches
 * nothing. Callers route through `normalizedSearch`.
 */
export function environmentVariableMatches(variable: types.Variable, query: string): boolean {
  if (!query) return true
  return [variable.name, variable.value, variable.type, variable.dataType].some((value) =>
    searchHit(value, query)
  )
}

/**
 * The rows one tab shows for a query.
 *
 * The index is captured BEFORE either filter runs. That ordering is the whole
 * function: reversing it would renumber the rows to their filtered positions,
 * and every edit made while a search is active would land on the wrong
 * variable.
 *
 * The two tabs partition the list rather than overlapping — a variable is
 * secret or it is not, and showing a secret in the plain tab would put its
 * value on screen in a field with no masking.
 */
export function visibleEnvironmentVariables(
  vars: types.Variable[] | undefined,
  tab: EnvironmentVariableTab,
  query: string
): IndexedVariable[] {
  return (vars ?? [])
    .map((variable, index) => ({ variable, index }))
    .filter(({ variable }) => (tab === 'secrets' ? Boolean(variable.secret) : !variable.secret))
    .filter(({ variable }) => environmentVariableMatches(variable, query))
}

export function environmentVariableAddLabel(tab: EnvironmentVariableTab): string {
  return tab === 'secrets' ? 'Add secret' : 'Add variable'
}
