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
