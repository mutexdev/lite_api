// The bounded, recursive JSON tree behind the response viewer's tree toggle.
//
// Extracted from ResponseInspector.svelte because it could not be tested where
// it was, and it had a bug that only a test would have caught: it returned no
// entries at all for an array-rooted body, while the button offering the view
// was shown for any parsed JSON. A list endpoint -- the most ordinary JSON
// response there is -- therefore offered a button that replaced the body with a
// silently empty panel.
//
// It was still not a tree after that fix. It listed the ROOT keys and rendered
// each value as one `JSON.stringify(child, null, 2)` dump inside a `<pre>`: a
// control labelled "JSON tree" offering exactly one level of accordion over the
// same flat, uncoloured text the body view already showed. Opening a field
// traded "no structure and no colour" for "no structure and no colour,
// indented" -- and the label promised the collapsible, colourised structural
// view every other API client ships.
//
// So this walks the whole value. The bounds are why walking it is safe, and
// they are the reason this module exists at all: a response is sized by a
// remote server, and "expand everything" over a 100 MB body is a frozen window
// -- a strictly worse bug than the flat list it replaces. There are four of
// them because they fail in four different ways, and no one of them catches the
// others:
//
//   JSON_TREE_MAX_ENTRIES  children built per container -- one object with
//                          200,000 keys.
//   JSON_TREE_BUDGET       characters of LEAF text across the whole tree -- one
//                          key whose value is a 10 MB base64 string.
//   JSON_TREE_MAX_DEPTH    nesting levels built -- a deeply self-similar
//                          document, and the backstop behind the cycle check.
//   JSON_TREE_MAX_NODES    nodes in the whole tree -- 300 keys of 300 keys,
//                          where not one bound above is exceeded but their
//                          product is 90,000 DOM rows.
//
// Every one of them sets `truncated`, and the view says so, because a bounded
// render that does not admit it is indistinguishable from a response that
// really did end there.

/** The largest number of children built for any one container. */
export const JSON_TREE_MAX_ENTRIES = 100

/**
 * The serialised-size budget, in characters, across every LEAF in the tree.
 *
 * Charged on leaves only, and that is deliberate. Charging each container the
 * length of its own full serialisation -- which is what the flat version did,
 * because every root entry WAS a full serialisation -- double-counts every
 * nested value once per level of depth, so a perfectly ordinary 40 KB response
 * would exhaust a 96 KB budget at depth two and the tree would stop expanding
 * for no reason the reader could see. A container's own rendered text is its
 * one-line summary, which is a rounding error; what actually costs is the leaf
 * text, and that is what this counts.
 */
export const JSON_TREE_BUDGET = 96 * 1024

/**
 * The deepest nesting level built.
 *
 * Not a display preference -- a bottom. `buildNode` also refuses to descend
 * into a value that is already one of its own ancestors, which is the real
 * cycle guard; this is the backstop for anything that guard cannot see, and
 * twelve levels is past the depth at which a human is reading structure anyway.
 */
export const JSON_TREE_MAX_DEPTH = 12

/**
 * The ceiling on nodes in the whole tree.
 *
 * The same reasoning `maxHighlightSegments` carries in bodyHighlight.ts: every
 * node becomes DOM, and the count that freezes a window is the total, not the
 * count at any one level. Two hundred keys each holding a twenty-element array
 * breaks no other bound here and is four thousand rows.
 */
export const JSON_TREE_MAX_NODES = 4_000

/**
 * What a node IS, which is not the same question as how it renders.
 *
 * `unserializable` is its own kind rather than a flavour of string because the
 * view must not colour it as data -- it is the tree talking about itself.
 */
export type JsonTreeKind =
  | 'object'
  | 'array'
  | 'string'
  | 'number'
  | 'boolean'
  | 'null'
  | 'unserializable'

/** The text shown for a value the tree cannot render. */
export const unserializableText = '[Unserializable value]'

export type JsonTreeEntry = {
  /** An object's key, or an array element's index rendered as a string. */
  name: string
  value: unknown
  /**
   * A stable identity for `{#each}` keys.
   *
   * Names are only unique among siblings, and the view renders the whole tree
   * in one recursive pass, so keying on the name alone would collide between
   * `a.id` and `b.id` and let Svelte reuse the wrong row's `<details>` -- which
   * shows up as a field that opens itself when an unrelated one is expanded.
   */
  path: string
  kind: JsonTreeKind
  /**
   * The rendered text of a LEAF, as JSON, or '' for a container.
   *
   * JSON rather than the raw value on purpose: it is handed straight to
   * `highlightSegments(text, 'json')`, so a string arrives quoted and takes the
   * string colour, matching the body view character for character.
   */
  text: string
  /** A container's one-line summary: `Array (3)`, `Object (2)`. '' for a leaf. */
  summary: string
  children: JsonTreeEntry[]
  /** How many children the value HAS, which is not how many were built. */
  childCount: number
  /** True when a bound stopped this container's children from being built. */
  collapsed: boolean
}

export type JsonTree = {
  entries: JsonTreeEntry[]
  /** True when a bound stopped the render, so the view can say so. */
  truncated: boolean
  /** Nodes actually built, so a test can assert the ceiling really binds. */
  nodes: number
}

type Walk = {
  /** Leaf characters spent so far, against JSON_TREE_BUDGET. */
  used: number
  nodes: number
  truncated: boolean
  /**
   * The containers currently open on the stack.
   *
   * Identity, not depth, is what catches a cycle: `a.self = a` has no bottom,
   * so leaving it to JSON_TREE_MAX_DEPTH would render twelve identical levels
   * of a field that does not exist twelve times. Removed again on the way out,
   * so a value legitimately reachable by two different paths -- the same object
   * appearing in two array slots, which `JSON.parse` never produces but a
   * caller could hand us -- still renders in both places.
   */
  ancestors: Set<object>
}

/**
 * Builds the bounded tree of a parsed JSON value.
 *
 * A value with no children -- `null`, a primitive, an empty array or object --
 * yields no entries and is not truncated, which lets the caller tell "nothing
 * to show" apart from "stopped early" and say the right thing about each.
 */
export function boundedJsonTree(value: unknown): JsonTree {
  if (value === null || typeof value !== 'object') return { entries: [], truncated: false, nodes: 0 }
  const walk: Walk = { used: 0, nodes: 0, truncated: false, ancestors: new Set<object>([value as object]) }
  const entries = buildChildren(value as object, 1, '', walk)
  return { entries, truncated: walk.truncated, nodes: walk.nodes }
}

function buildChildren(value: object, depth: number, path: string, walk: Walk): JsonTreeEntry[] {
  const entries: JsonTreeEntry[] = []
  // Object.entries covers arrays too, yielding '0', '1', ... which is exactly
  // the index-as-name the view wants. The array-rooted bug this module was
  // extracted for was a separate `Array.isArray` branch that returned nothing.
  for (const [name, child] of Object.entries(value as Record<string, unknown>)) {
    if (entries.length >= JSON_TREE_MAX_ENTRIES || walk.nodes >= JSON_TREE_MAX_NODES) {
      walk.truncated = true
      return entries
    }
    // The slot is taken BEFORE descending, so a container counts against the
    // same ceiling its own children are measured against. Counted afterwards
    // instead, every container on the stack is invisible to the check while its
    // subtree is being built, and the tree overruns the bound by its depth.
    walk.nodes += 1
    const node = buildNode(name, child, depth, `${path}/${name}`, walk)
    // null means a bound refused the node, not that the value was null -- a
    // JSON null is a perfectly good leaf and gets one.
    if (!node) {
      walk.nodes -= 1
      walk.truncated = true
      return entries
    }
    entries.push(node)
  }
  return entries
}

function buildNode(name: string, value: unknown, depth: number, path: string, walk: Walk): JsonTreeEntry | null {
  const container = value !== null && typeof value === 'object'
  if (!container) {
    const text = leafText(value)
    // Checked BEFORE the push, so a single field larger than the whole budget
    // yields no entries AND truncation, rather than one entry that blows it.
    if (walk.used + text.length > JSON_TREE_BUDGET) return null
    walk.used += text.length
    return { name, value, path, kind: leafKind(value), text, summary: '', children: [], childCount: 0, collapsed: false }
  }

  if (walk.ancestors.has(value as object)) {
    return {
      name,
      value,
      path,
      kind: 'unserializable',
      text: unserializableText,
      summary: 'Circular reference',
      children: [],
      childCount: 0,
      collapsed: false
    }
  }

  const childCount = Object.keys(value as object).length
  const kind: JsonTreeKind = Array.isArray(value) ? 'array' : 'object'
  const summary = kind === 'array' ? `Array (${childCount})` : `Object (${childCount})`
  if (depth >= JSON_TREE_MAX_DEPTH || walk.nodes >= JSON_TREE_MAX_NODES) {
    // Rendered as a row that names what is inside it and says it was not
    // opened. NOT serialised as a fallback: the reason we stopped is that this
    // subtree may be enormous, and `JSON.stringify` on it would spend exactly
    // the time the bound exists to refuse.
    if (childCount > 0) walk.truncated = true
    return { name, value, path, kind, text: '', summary, children: [], childCount, collapsed: childCount > 0 }
  }

  walk.ancestors.add(value as object)
  const children = buildChildren(value as object, depth + 1, path, walk)
  walk.ancestors.delete(value as object)
  return { name, value, path, kind, text: '', summary, children, childCount, collapsed: children.length < childCount }
}

function leafKind(value: unknown): JsonTreeKind {
  if (value === null) return 'null'
  if (typeof value === 'string') return 'string'
  if (typeof value === 'number') return 'number'
  if (typeof value === 'boolean') return 'boolean'
  // A parsed JSON body holds none of the remaining types, but this function is
  // reachable from any caller and `undefined`/`bigint`/a function must not be
  // painted as though they were data.
  return 'unserializable'
}

function leafText(value: unknown): string {
  // A parsed body can still hold something JSON.stringify refuses -- a bigint,
  // or a getter that throws. One such value must not take the whole panel down.
  try {
    return JSON.stringify(value) ?? String(value)
  } catch {
    return unserializableText
  }
}

/**
 * How many nodes of the tree contain `query`.
 *
 * The find bar stays reachable while the tree is showing, and the body's own
 * match list is computed over the flat document -- which is NOT what is on
 * screen in tree mode. Reporting that count beside a tree would be a counter
 * that names a number nothing on screen corresponds to, so the tree counts its
 * own hits, and the view paints exactly the nodes counted here.
 */
export function countJsonTreeMatches(entries: JsonTreeEntry[], query: string): number {
  const needle = query.trim().toLowerCase()
  if (!needle) return 0
  let found = 0
  const visit = (nodes: JsonTreeEntry[]) => {
    for (const node of nodes) {
      if (jsonTreeNodeMatches(node, needle)) found += 1
      visit(node.children)
    }
  }
  visit(entries)
  return found
}

/**
 * Whether one node contains `needle`, which must already be trimmed and lower
 * case -- the caller does that once rather than per node.
 */
export function jsonTreeNodeMatches(node: JsonTreeEntry, needle: string): boolean {
  if (!needle) return false
  return node.name.toLowerCase().includes(needle) || node.text.toLowerCase().includes(needle)
}
