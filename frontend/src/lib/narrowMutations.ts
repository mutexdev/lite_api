// US-014 — applying narrow mutator results to the local AppState.
//
// The hot mutators now return only what changed, paired with the revision the
// mutation produced. This module is the merge, kept out of App.svelte so it can
// be tested directly: the interesting cases here are all about being WRONG, and
// none of them are reachable through a component test.
//
// THE CONTRACT, and why every part of it is load-bearing:
//
//   * Mutations increment AppState.Revision by exactly one. So a result whose
//     revision is not (last applied + 1) proves that something changed the
//     state without going through us — another window, the collection watcher,
//     a binding not yet migrated, the readiness normalisation on the Go side.
//
//   * A narrow result patched onto a stale copy is worse than a stale copy. The
//     patch lands, the view looks freshly updated, and the parts that went stale
//     stay stale with nothing to ever correct them. That is why a gap forces a
//     full refetch rather than being patched over and hoped about.
//
//   * An UNCHANGED revision is not a gap. A mutation that turns out to be a
//     no-op does not bump the counter — MoveOpenTab clamped against the end of
//     the tab bar is the real case — and its result is still the current truth.
//     Treating that as a gap would fire a full AppState refetch every time a
//     user dragged a tab against the edge, which is the cost this story exists
//     to remove, arriving through the mechanism meant to prevent it.
//
//   * A revision that goes BACKWARDS is a gap. It means a response overtook a
//     newer one, and applying it would walk the UI backwards. It should not
//     happen, but treating "impossible" as "need not be handled" is how a
//     desynchronised UI becomes permanent; recovery costs one GetState.

import type { core, types } from '../../wailsjs/go/models'

export type AppStateLike = types.AppState

/** What the caller must do after applying a narrow result. */
export type MergeOutcome =
  | { kind: 'applied'; state: AppStateLike; revision: number }
  | { kind: 'refetch'; reason: string }

/**
 * Decides whether a narrow result can be applied on top of what we hold.
 *
 * `expected` is the revision we last applied. A first-ever call passes the
 * revision that came with the boot GetState.
 */
export function canApplyNarrowResult(expected: number, incoming: number): MergeOutcome | null {
  // Unchanged (a no-op mutation) or the very next revision: safe to apply.
  if (incoming === expected || incoming === expected + 1) return null
  if (incoming < expected) {
    return {
      kind: 'refetch',
      reason: `revision went backwards (held ${expected}, received ${incoming})`,
    }
  }
  return {
    kind: 'refetch',
    reason: `missed ${incoming - expected - 1} update(s) (held ${expected}, received ${incoming})`,
  }
}

/**
 * Replaces one request item inside a workspace's collection.
 *
 * Rebuilds the containing arrays rather than mutating them: Svelte's legacy
 * reactivity tracks assignment, and an in-place splice would update the data
 * without updating the view — the exact failure mode US-004 found in
 * ResponseInspector.
 *
 * A result naming a collection or item we do not hold is NOT applied silently.
 * It means our copy is missing something, which is the gap case.
 */
export function applyRequestMutation(
  state: AppStateLike,
  expectedRevision: number,
  result: core.RequestMutation,
): MergeOutcome {
  const gap = canApplyNarrowResult(expectedRevision, result.revision)
  if (gap) return gap

  let found = false
  const workspaces = state.workspaces.map((workspace) => {
    if (!workspace.collections?.some((collection) => collection.id === result.collectionId)) {
      return workspace
    }
    return {
      ...workspace,
      collections: workspace.collections.map((collection) => {
        if (collection.id !== result.collectionId) return collection
        if (!collection.items?.some((item) => item.id === result.item.id)) return collection
        found = true
        return {
          ...collection,
          items: collection.items.map((item) => (item.id === result.item.id ? result.item : item)),
        }
      }),
    }
  }) as AppStateLike['workspaces']

  if (!found) {
    return {
      kind: 'refetch',
      reason: `request ${result.item.id} is not in collection ${result.collectionId} locally`,
    }
  }

  return {
    kind: 'applied',
    state: { ...state, workspaces, revision: result.revision } as AppStateLike,
    revision: result.revision,
  }
}

/**
 * Replaces the tab bar from a narrow tab result.
 *
 * Unlike the request case there is no "not found" check to make: the result IS
 * the complete tab state, so it is authoritative once the revision checks out.
 */
export function applyTabsMutation(
  state: AppStateLike,
  expectedRevision: number,
  result: core.TabsMutation,
): MergeOutcome {
  const gap = canApplyNarrowResult(expectedRevision, result.revision)
  if (gap) return gap

  return {
    kind: 'applied',
    state: {
      ...state,
      openTabs: result.openTabs,
      activeTabId: result.activeTabId,
      revision: result.revision,
    } as AppStateLike,
    revision: result.revision,
  }
}
