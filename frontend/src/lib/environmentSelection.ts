// Which collection environment is active — one answer, for everybody.
//
// THE BUG THIS EXISTS TO KILL. The active environment used to be a single
// string on the store, set once during startup from whichever collection
// happened to be active at that moment, and never revisited when the active
// collection changed. Three parts of the UI then disagreed about what it meant:
//
//   - the request command strip read `selectedEnvironment`, whose getter fell
//     back to `environments[0]` when the id did not match, and so displayed
//     "Env: Development";
//   - the Active-environment <select> read the raw id, and so showed "";
//   - every backend call passed the raw id, so variables did not resolve —
//     `{{host}}` went out as `{{host}}`.
//
// All three were "correct" given what they read. The fix is not to make the
// fallbacks agree, it is to have one function decide, and to remember the
// decision PER COLLECTION rather than once per session: switching collections
// is precisely the event the old code missed.
//
// Pure and storage-injectable so `node --test` can exercise the resolution
// rules, which is where every one of the above went wrong.

export interface EnvironmentLike {
  id: string
  name?: string
}

/**
 * Collection id -> chosen environment id.
 *
 * A MISSING key and a key holding "" mean different things, and conflating them
 * is a bug worth naming: missing is "this collection has never been looked at",
 * which resolves to its first environment; "" is "the user picked No
 * environment", which must stick. If "" were treated as absent, the No
 * environment option would silently reselect the first one and be impossible to
 * choose.
 */
export type EnvironmentSelectionMap = Record<string, string>

/** The subset of Storage this module uses, mirroring workbench/layout.ts. */
export interface SelectionStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

/**
 * Storage key for a window's environment selections.
 *
 * Returns "" when the scope is unknown, for the reason workbenchStorageKey
 * documents: the scope arrives from an async binding call, and an unscoped
 * fallback would make every workspace window share — and fight over — one entry.
 */
export function environmentSelectionKey(scope: string): string {
  return scope ? `liteapi.environments.v1.${scope}` : ''
}

function defaultStorage(): SelectionStorage | null {
  try {
    return globalThis.localStorage ?? null
  } catch {
    // Reading the property itself throws in a WebView with storage disabled.
    return null
  }
}

/**
 * Decides the active environment id for one collection.
 *
 * The single place the question is answered. Every caller — the chip, the
 * select, the binding calls — goes through this, which is what makes them agree.
 */
export function resolveEnvironmentId(
  environments: readonly EnvironmentLike[] | undefined,
  stored: string | undefined
): string {
  const available = environments ?? []
  // Explicitly "No environment". Honoured even when environments exist.
  if (stored === '') return ''
  // Never chosen for this collection: default to the first, which is what the
  // old startup line did — only now it happens for every collection the user
  // switches to, not just whichever one was active during load().
  if (stored === undefined) return available[0]?.id ?? ''
  if (available.some((environment) => environment.id === stored)) return stored
  // A stale id: the environment was deleted or the collection was replaced on
  // disk. Falling back keeps the collection usable, and because the id and the
  // displayed name now come from this same function, the user can see which
  // environment they landed on rather than being told one thing and sent
  // against another.
  return available[0]?.id ?? ''
}

export function withEnvironmentSelection(
  selections: EnvironmentSelectionMap,
  collectionId: string,
  environmentId: string
): EnvironmentSelectionMap {
  if (!collectionId) return selections
  return { ...selections, [collectionId]: environmentId }
}

/**
 * Reads persisted selections, tolerating anything.
 *
 * The entry is user-visible browser storage that survives upgrades, so it is
 * validated rather than trusted: a corrupt or hand-edited value must degrade to
 * "no stored choices" instead of throwing during startup, where the failure
 * would take the whole workspace load down with it.
 */
export function readEnvironmentSelections(
  scope: string,
  storage: SelectionStorage | null = defaultStorage()
): EnvironmentSelectionMap {
  const key = environmentSelectionKey(scope)
  if (!key || !storage) return {}
  try {
    const raw = storage.getItem(key)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const out: EnvironmentSelectionMap = {}
    for (const [collectionId, environmentId] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof environmentId === 'string') out[collectionId] = environmentId
    }
    return out
  } catch {
    return {}
  }
}

export function writeEnvironmentSelections(
  scope: string,
  selections: EnvironmentSelectionMap,
  storage: SelectionStorage | null = defaultStorage()
): void {
  const key = environmentSelectionKey(scope)
  if (!key || !storage) return
  try {
    storage.setItem(key, JSON.stringify(selections))
  } catch {
    // Quota or a private-mode WebView. Losing the persisted choice across a
    // reload is a far smaller problem than failing the interaction that set it.
  }
}
