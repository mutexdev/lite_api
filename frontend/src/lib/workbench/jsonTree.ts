// The bounded root-level tree behind the response viewer's "JSON tree" toggle.
//
// Extracted from ResponseInspector.svelte because it could not be tested where
// it was, and it had a bug that only a test would have caught: it returned no
// entries at all for an array-rooted body, while the button offering the view
// was shown for any parsed JSON. A list endpoint -- the most ordinary JSON
// response there is -- therefore offered a button that replaced the body with a
// silently empty panel.
//
// The bounds are the point of the module. A response is sized by a remote
// server, and rendering every key of a large one is how the window locks up.

/** The largest number of root entries rendered before the view says it stopped. */
export const JSON_TREE_MAX_ENTRIES = 100

/** The serialised-size budget, in characters, across all rendered entries. */
export const JSON_TREE_BUDGET = 96 * 1024

export type JsonTreeEntry = {
  /** An object's key, or an array element's index rendered as a string. */
  name: string
  value: unknown
  text: string
}

export type JsonTree = {
  entries: JsonTreeEntry[]
  /** True when a bound stopped the render, so the view can say so. */
  truncated: boolean
}

/**
 * Lists the root-level children of a parsed JSON value, bounded by count and
 * serialised size.
 *
 * Arrays are listed by index; objects by key. A value with no children --
 * `null`, a primitive, an empty array or object -- yields no entries and is not
 * truncated, which lets the caller tell "nothing to show" apart from "stopped
 * early" and say the right thing about each.
 */
export function boundedJsonTree(value: unknown): JsonTree {
  const entries: JsonTreeEntry[] = []
  if (value === null || typeof value !== 'object') return { entries, truncated: false }

  let used = 0
  for (const [name, child] of Object.entries(value as Record<string, unknown>)) {
    if (entries.length >= JSON_TREE_MAX_ENTRIES) return { entries, truncated: true }
    let text = ''
    // A parsed body can still hold something JSON.stringify refuses. One such
    // value must not take the whole panel down with it.
    try {
      text = JSON.stringify(child, null, 2) ?? String(child)
    } catch {
      text = '[Unserializable value]'
    }
    if (used + text.length > JSON_TREE_BUDGET) return { entries, truncated: true }
    entries.push({ name, value: child, text })
    used += text.length
  }
  return { entries, truncated: false }
}
