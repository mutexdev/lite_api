// The vocabulary the three "list of executed requests and what came back"
// surfaces share: the Runner, a Flow's run panel, and History.
//
// A8-03. There were four of these widgets — those three plus the response
// Timeline — built independently, and every capability any of them grew stayed
// where it was built. History had filtering; the Timeline had filtering AND row
// expansion AND export; Flow had per-row colour and inline detail; the Runner,
// the oldest and the one where "which of my forty requests failed and why"
// matters most, had none of the four. A `<table>`, an `<ol>` of always-open
// cards, a `<ul>` of cards and a list of expandable `<article>`s, all drawing
// the same event.
//
// The Timeline is not ours to change (it lives inside ResponseInspector, in the
// workbench), so it is the FIXED POINT: this module and RunResultRow.svelte
// reproduce the anatomy it already established — status, then what was called,
// then the metrics, all in one clickable summary that opens a detail region —
// and the other three are moved onto it. Agreeing with the surface that cannot
// move is the only way three of four end up agreeing at all.
//
// WHAT LIVES HERE IS ONLY THE PART THAT IS THE SAME. The three surfaces hold
// genuinely different rows — a runner result has an iteration number, a flow
// step has assertions and extracted variables, a history entry has a URL and a
// saved-request link — so this is a filter contract and a row model, not an
// attempt to make one component draw all three. The detail region is a snippet
// each surface fills for itself.

import type { StatusTone } from './statusTone.ts'

/**
 * The state of a results filter bar. One shape for all three surfaces, so the
 * FindBar wiring is written once per surface and reads identically.
 */
export type RunResultFilter = {
  query: string
  onlyFailures: boolean
}

/**
 * The searchable text of one row, as the fields the surface wants matched.
 *
 * Takes an array rather than a pre-joined string so a caller cannot accidentally
 * make two fields match as one — searching "GET /users" should not hit a row
 * whose method is GET and whose next field starts with "/users" only because
 * the join put them next to each other. They are joined with a newline here for
 * exactly that reason.
 */
export function runResultSearchText(fields: readonly (string | number | undefined)[]): string {
  return fields
    .filter((field) => field !== undefined && field !== '')
    .join('\n')
    .toLowerCase()
}

/**
 * Does this row survive the filter?
 *
 * "Failures only" means tone === 'danger', which is precisely the backend's own
 * definition for History (internal/history/history.go:311 keeps an entry when
 * `Error != "" || Status >= 400`). Reusing it means the checkbox does the same
 * thing on all three surfaces AND the same thing the server already did, so a
 * user who learned it in History does not have to re-learn it in the Runner.
 *
 * Note what it therefore EXCLUDES: a cancelled or skipped row is amber, not
 * red, so it disappears under "failures only". That is deliberate — a run the
 * user stopped is not a failure, and a list that showed it as one would make
 * every cancelled run look like a broken collection.
 */
export function runResultMatches(
  row: { tone: StatusTone; searchText: string },
  filter: RunResultFilter,
): boolean {
  if (filter.onlyFailures && row.tone !== 'danger') return false
  const query = filter.query.trim().toLowerCase()
  if (!query) return true
  return row.searchText.includes(query)
}
