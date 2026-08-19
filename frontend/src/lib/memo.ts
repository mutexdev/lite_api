// US-034 — keyed memoisation for derivations that are recomputed far more often
// than their inputs change.
//
// Svelte's legacy `$:` re-runs a statement whenever ANY of its dependencies is
// invalidated, and a template-called function re-runs on every render. Both
// patterns recompute results that could not possibly have changed — a
// collection's folder grouping is rebuilt on every render of every collection,
// including renders caused by something else entirely.
//
// The key is what makes this safe. A memo whose key misses an input returns a
// stale answer that looks completely normal: the sidebar keeps showing the
// grouping from before the rename, and nothing anywhere reports a problem. So
// the callers below key on the revision counter the backend bumps on every
// mutation, which is the one value guaranteed to change when anything the
// derivation reads has changed.

/**
 * A single-entry memo.
 *
 * Deliberately single-entry rather than a Map. These derivations are asked
 * about the thing the user is currently looking at, so a cache holds one useful
 * answer and an unbounded number of dead ones — each retaining whatever objects
 * it closed over. `memoizeBy` below keeps a small bounded map for the cases
 * where several are genuinely live at once.
 */
export type Memo<K, V> = { key: K; value: V } | null

export function memoized<K, V>(memo: Memo<K, V>, key: K, compute: () => V): { value: V; memo: Memo<K, V> } {
  if (memo && memo.key === key) return { value: memo.value, memo }
  const value = compute()
  return { value, memo: { key, value } }
}

/**
 * A bounded multi-key memo, for derivations asked about several subjects in one
 * render — the collection sidebar asks per collection.
 *
 * Bounded because an unbounded one is a leak: collections come and go as
 * workspaces are switched, and their entries would never be reclaimed.
 * Insertion order eviction is enough here; the working set is the collections
 * on screen, which is what stays hot.
 */
export class KeyedMemo<V> {
  private readonly entries = new Map<string, V>()
  private readonly limit: number

  // Declared and assigned explicitly rather than as a constructor parameter
  // property: node --test runs these files in strip-only TypeScript mode, which
  // rejects parameter properties outright. The tests would not run at all.
  constructor(limit = 32) {
    this.limit = limit
  }

  get(key: string, compute: () => V): V {
    const existing = this.entries.get(key)
    if (existing !== undefined) return existing

    const value = compute()
    this.entries.set(key, value)
    if (this.entries.size > this.limit) {
      // Map preserves insertion order, so the first key is the oldest.
      const oldest = this.entries.keys().next().value
      if (oldest !== undefined) this.entries.delete(oldest)
    }
    return value
  }

  /** Exposed for tests and for callers that need to drop everything. */
  clear() {
    this.entries.clear()
  }

  get size() {
    return this.entries.size
  }
}
