// Reaching a single request inside the state tree.
//
// Both functions here walk the same shape — workspaces, then collections, then
// items — one to patch a request in place and one to answer whether a request
// is still there at all. The traversal is identical and the failure it guards
// against is the same: matching a collection and an item SEPARATELY rather than
// as a pair, so a request that lives in a different collection reads as present
// under this one.
//
// Applying an in-flight edit to the state tree before the backend confirms it.
//
// This runs on every keystroke in the request editor. The user's typing has to
// appear immediately, so the edit is written into the state tree first and
// persisted asynchronously behind a coalescer.
//
// The `draft: true` it sets is not cosmetic. It is what the tab strip reads to
// show the unsaved dot, and what the close guard reads before letting a tab go.
// An edit applied without it looks saved, and closing the tab discards it with
// no prompt.

import type { types } from '../../wailsjs/go/models'

/**
 * Returns the state with one request patched and marked as a draft.
 *
 * Collections that do not hold the target are returned BY IDENTITY rather than
 * copied. That is deliberate: this runs per keystroke, and every `$derived` and
 * `{#each}` keyed on a collection would re-run for all of them otherwise —
 * turning a character into a re-render of the whole sidebar.
 *
 * Every workspace is searched rather than just the active one, because a
 * collection id is unique across the tree and a request can be patched from a
 * window whose active workspace is not the one that owns it.
 */
export function withOptimisticPatch(
  state: types.AppState,
  collectionId: string,
  itemId: string,
  patch: types.RequestPatch
): types.AppState {
  return {
    ...state,
    workspaces: (state.workspaces ?? []).map((workspace) => ({
      ...workspace,
      collections: (workspace.collections ?? []).map((collection) =>
        collection.id !== collectionId
          ? collection
          : {
              ...collection,
              items: (collection.items ?? []).map((item) =>
                item.id !== itemId ? item : ({ ...item, ...patch, draft: true } as types.RequestItem)
              )
            }
      )
    }))
  } as types.AppState
}

/**
 * Whether a history entry still points at a request that exists.
 *
 * The item is looked for INSIDE the matching collection, not anywhere in the
 * tree. Two separate checks — "some collection has this id" and "some item has
 * this id" — would both pass for an entry whose request has since been moved to
 * a different collection, and the history row would offer to open something
 * that is not there.
 *
 * An entry missing either id is not openable. Those entries exist: history
 * records a send, and a scratch request sent before it was ever saved has no
 * collection to point back at.
 */
export function historyEntryExists(
  // `| null` as well as `| undefined`: App.svelte's appState is null before the
  // first load, and a parameter that only accepts undefined pushes a cast onto
  // the call site for a case the function already handles.
  state: types.AppState | null | undefined,
  collectionId: string | undefined,
  itemId: string | undefined
): boolean {
  if (!collectionId || !itemId) return false
  return (state?.workspaces ?? []).some((workspace) =>
    (workspace.collections ?? []).some(
      (collection) =>
        collection.id === collectionId &&
        (collection.items ?? []).some((item) => item.id === itemId)
    )
  )
}
