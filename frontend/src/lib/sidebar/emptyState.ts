// One sentence for "there is nothing here", for every surface that can run out
// of rows.
//
// THE APP HAD SIX ANSWERS TO ONE QUESTION. The audit counted them: "No results
// found" (⌘K), "No commands match." (⌘⇧P), "No matching requests" (the sidebar
// tree), "No requests" (a collection with nothing in it), "No matches" (the
// response body) and "No matching headers" (the headers tab) — six near
// identical states, six sentences, one of them ending in a period and five not,
// and four different nouns for the same idea. Nobody wrote any of them wrong;
// nobody owned the rule, so each surface invented one at the moment it needed
// one.
//
// Worse than the drift, one of the six was a LIE. `sidebarCollections` returns
// the workspace's collections untouched when the query is empty, so an empty
// result meant either "your filter matched nothing" or "you have never made a
// collection" — and both rendered "No matching requests". The first thing a new
// user saw was the app reporting a failed search they had not performed.
//
// THE RULE, and the reason it is two sentences rather than one:
//
//   * A QUERY produced nothing  →  `No results for “…”`
//     Quoting the query back is the whole job. It confirms what was actually
//     searched for, which is the one thing the user cannot otherwise verify
//     when a stale filter is scrolled out of view or was pre-filled by ⌘K.
//
//   * There was never anything  →  `No {things} yet`
//     "Yet" is doing real work: it says the surface is empty by history rather
//     than by failure, so nothing suggests the user did something wrong.
//
// The noun is the caller's, because "No results yet" under a sidebar with no
// collections is exactly as unhelpful as the string it replaces.
//
// This lives under lib/sidebar/ because the sidebar is where the copy bug was,
// and because lib/ui/ belongs to another owner during this wave. It imports
// nothing and knows nothing about the sidebar, so any pane can call it — see
// the handoff, which routes the response timeline here too.

/** Wording for the counter slot is FindBar's; this is wording for the body. */
export type EmptyStateInput = {
  /** The live query. Trimmed here, so callers need not pre-trim. */
  query: string
  /**
   * Plural noun for what the surface holds: "collections", "requests",
   * "commands", "timeline entries". Used only in the never-had-any case.
   */
  noun: string
}

/**
 * The one sentence a surface shows where its rows would have been.
 *
 * Deliberately returns a sentence with no trailing period. Five of the six
 * strings it replaces had none, these are labels rather than prose, and a
 * period is the kind of detail that drifts back apart the moment two people
 * write two of them.
 */
export function emptyStateMessage({ query, noun }: EmptyStateInput): string {
  const trimmed = query.trim()
  return trimmed ? `No results for “${trimmed}”` : `No ${noun} yet`
}

/**
 * True when the surface is empty because a filter excluded everything.
 *
 * Exported because several callers need to branch on the CAUSE for more than
 * the sentence — the sidebar shows an onboarding hint in one case and not the
 * other — and re-deriving "is there a query" beside every message is how the
 * two fall out of step.
 */
export function isFilteredEmpty(query: string): boolean {
  return query.trim().length > 0
}
