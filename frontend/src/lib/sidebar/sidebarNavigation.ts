// Resolving a keydown inside the sidebar tree to the thing it should do.
//
// Same division of labour as lib/shortcuts.ts, and for the same reason: this
// function DECIDES and the caller EXECUTES. Nothing here touches the DOM, calls
// preventDefault or reads app state, so every rule below is testable without a
// browser — and the rules are where tree navigation actually goes wrong.
//
// The behaviour follows the WAI-ARIA tree pattern, which is worth naming
// because two of its rules are counterintuitive and get "simplified" away:
//
//   * Left on an EXPANDED node collapses it; Left on an already-collapsed node
//     moves to the PARENT. One key, two jobs, and dropping the second one
//     strands the user at the bottom of a long folder with no way up but
//     Arrow-Up through every sibling.
//   * Right on a COLLAPSED node expands it without moving; only a second Right
//     descends. Combining them means a single keypress both reveals and enters,
//     and the user cannot look inside a folder without leaving where they were.
//
// TYPE-AHEAD IS NOT A NICETY HERE. The sidebar is a flat list of what are often
// hundreds of requests; arrowing to one is exactly the "too many keystrokes"
// version of the problem this work exists to fix. Typing "log" jumps to Login.

import type { SidebarRow } from './sidebarRows'
import type { SidebarActionID } from './sidebarActions'

/** The subset of KeyboardEvent this needs, so tests need not build real events. */
export type SidebarKeyEvent = {
  key: string
  shiftKey?: boolean
  metaKey?: boolean
  ctrlKey?: boolean
  altKey?: boolean
}

export type SidebarNavAction =
  /** Move the focused row. The caller scrolls it into view and focuses it. */
  | { kind: 'focus'; index: number }
  /** Open the row: a request tab, a collection, a folder's settings. */
  | { kind: 'activate'; index: number }
  | { kind: 'expand'; index: number }
  | { kind: 'collapse'; index: number }
  /** Open the contextual action menu for the row. */
  | { kind: 'menu'; index: number }
  /** Run one action directly, bypassing the menu. */
  | { kind: 'action'; index: number; action: SidebarActionID }
  /** Hand focus back to the filter box. */
  | { kind: 'exit' }

export type SidebarNavContext = {
  rows: readonly SidebarRow[]
  /** The currently focused row, or -1 when the tree has focus but no row. */
  index: number
  /** Whether an expandable row is currently open. */
  isExpanded: (row: SidebarRow) => boolean
  /**
   * Resolves a configured keybinding against the event, exactly as the global
   * dispatcher does. Passed in so the user's own bindings for renameItem,
   * cloneItem and deleteItem are honoured rather than hardcoded here.
   */
  matches: (bindingAction: string) => boolean
  /** Characters typed recently, for type-ahead. Empty starts a fresh search. */
  typeAhead: string
}

/** Collections and folders can be opened and closed; requests and examples cannot. */
export function isExpandable(row: SidebarRow | undefined): boolean {
  return row?.kind === 'collection' || row?.kind === 'folder'
}

/**
 * The index of a row's parent, or -1 when it has none.
 *
 * Walks BACKWARDS for the nearest row of smaller depth rather than reading a
 * parent id, because depth is the only thing the flat model guarantees and a
 * request's parent may be a folder or a collection depending on nesting.
 */
export function parentIndex(rows: readonly SidebarRow[], index: number): number {
  const row = rows[index]
  if (!row) return -1
  for (let candidate = index - 1; candidate >= 0; candidate -= 1) {
    if (rows[candidate].depth < row.depth) return candidate
  }
  return -1
}

/** Whether the row after `index` is a child of it. */
function hasVisibleChild(rows: readonly SidebarRow[], index: number): boolean {
  const row = rows[index]
  const next = rows[index + 1]
  return Boolean(row && next && next.depth > row.depth)
}

/**
 * The next row whose label starts with `prefix`, searching forward and wrapping.
 *
 * Starts AFTER the current row so repeatedly typing the same letter cycles
 * through the matches rather than sticking on the first one — the behaviour
 * every file manager has, and the reason the search wraps.
 */
export function typeAheadIndex(
  rows: readonly SidebarRow[],
  from: number,
  prefix: string
): number {
  if (!prefix) return -1
  const needle = prefix.toLowerCase()
  const total = rows.length
  for (let step = 1; step <= total; step += 1) {
    const candidate = (from + step + total) % total
    if (rows[candidate].label.toLowerCase().startsWith(needle)) return candidate
  }
  return -1
}

/**
 * How the buffer grows.
 *
 * Repeating the SAME single character restarts rather than extends, because
 * "ll" is not a prefix anybody means — it is the user asking for the next item
 * beginning with l. Exported so the component that owns the buffer and the
 * resolver that reads it cannot disagree about the rule.
 */
export function extendTypeAhead(buffer: string, key: string): string {
  return buffer === key ? key : buffer + key
}

/** A single printable character, which is what starts or extends a type-ahead. */
export function isTypeAheadKey(event: SidebarKeyEvent): boolean {
  return (
    event.key.length === 1 &&
    event.key !== ' ' &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.altKey
  )
}

/**
 * Resolves one keydown, or returns null to let the event through.
 *
 * ORDER MATTERS AND IS THE BEHAVIOUR. The configured bindings are checked
 * BEFORE the single-character type-ahead, because a binding may be a bare
 * letter and a type-ahead that ran first would swallow it. Structural keys come
 * before both, since none of them is a character.
 */
export function resolveSidebarKey(
  event: SidebarKeyEvent,
  context: SidebarNavContext
): SidebarNavAction | null {
  const { rows, index, isExpanded, matches } = context
  if (rows.length === 0) return null

  const row = rows[index]
  const last = rows.length - 1

  switch (event.key) {
    case 'ArrowDown':
      return { kind: 'focus', index: Math.min(index + 1, last) }
    case 'ArrowUp':
      // From "no row focused", Up lands on the last row rather than staying
      // nowhere: entering a list from below is what Up means.
      return { kind: 'focus', index: index < 0 ? last : Math.max(index - 1, 0) }
    case 'Home':
      return { kind: 'focus', index: 0 }
    case 'End':
      return { kind: 'focus', index: last }
    case 'Enter':
      return row ? { kind: 'activate', index } : null
    case ' ':
      // Space activates like Enter on a leaf, but toggles an expandable row —
      // matching how the chevron behaves under a pointer.
      if (!row) return null
      if (!isExpandable(row)) return { kind: 'activate', index }
      return isExpanded(row) ? { kind: 'collapse', index } : { kind: 'expand', index }
    case 'ArrowRight':
      if (!row) return null
      if (!isExpandable(row)) return null
      if (!isExpanded(row)) return { kind: 'expand', index }
      // Already open: descend, but only if there is something to descend into.
      return hasVisibleChild(rows, index) ? { kind: 'focus', index: index + 1 } : null
    case 'ArrowLeft': {
      if (!row) return null
      if (isExpandable(row) && isExpanded(row)) return { kind: 'collapse', index }
      const parent = parentIndex(rows, index)
      return parent >= 0 ? { kind: 'focus', index: parent } : null
    }
    case 'Escape':
      return { kind: 'exit' }
    case 'ContextMenu':
      return row ? { kind: 'menu', index } : null
    case 'F10':
      // Shift+F10 is the keyboard context menu everywhere the ContextMenu key
      // is absent, which includes every Apple keyboard.
      return event.shiftKey && row ? { kind: 'menu', index } : null
  }

  if (!row) return null

  // The configured bindings, honouring the user's own combos. These are the
  // three entries keybindings.ts has advertised in Preferences; renameItem and
  // cloneItem shipped bound to ⌘R and ⌘D and were never implemented.
  if (matches('renameItem')) return { kind: 'action', index, action: 'rename' }
  if (matches('cloneItem')) return { kind: 'action', index, action: 'clone' }
  if (matches('deleteItem')) return { kind: 'action', index, action: 'delete' }

  if (isTypeAheadKey(event)) {
    // A repeated single character cycles matches for that character; anything
    // else extends the buffer into a longer prefix.
    const repeated = context.typeAhead.length > 0 && context.typeAhead === event.key
    const prefix = extendTypeAhead(context.typeAhead, event.key)
    const found = typeAheadIndex(rows, repeated || !context.typeAhead ? index : index - 1, prefix)
    return found >= 0 ? { kind: 'focus', index: found } : null
  }

  return null
}
