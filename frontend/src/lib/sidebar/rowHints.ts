// What a sidebar row's tooltip says, and — more importantly — WHEN it says
// anything at all.
//
// TWO ROWS BEHAVE IDENTICALLY AND ONLY ONE OF THEM ADMITS IT. A folder row and
// a collection row both split single click from double click: one click opens
// that thing's settings pane, a double click expands or collapses it. The
// folder row spells this out in its title — "auth — click for settings,
// double-click to open" — and the collection row, which shipped the same split,
// carried no title whatsoever. So the gesture is discoverable on the row where
// it is arguably less surprising, and invisible on the row above it.
//
// That is not a missing string; it is a missing rule. The rule:
//
//   * A row whose click does ONE obvious thing explains its CONTENT.
//     A request row's tooltip is its URL, and that is right — clicking it opens
//     the request, there is no second behaviour to disclose, and the URL is the
//     one fact the row cannot fit on screen.
//
//   * A row whose click does TWO things explains its BEHAVIOUR, always, in the
//     same sentence shape. Collections and folders both qualify. A saved
//     example and a flow do not: they open, and that is all they do.
//
// The sentence shape is the folder row's existing one, kept verbatim rather
// than improved, because it is already shipped and already learned:
//   `{name} — click for {what a single click opens}, double-click to {toggle}`
//
// Only the two verbs differ between the two rows, and they differ because the
// panes differ: a collection opens its own detail view, a folder opens its
// settings. Both then say "expand" for the toggle — the old folder string said
// "open", which collides with the word the same sentence just used for the
// single click.

export type SidebarHintKind = 'collection' | 'folder' | 'request' | 'example' | 'flow'

export type SidebarHintInput = {
  kind: SidebarHintKind
  /** The row's own name, as drawn. */
  label: string
  /**
   * The content fact a single-behaviour row discloses instead: a request's URL,
   * an example's description, a flow's description. Ignored for the two
   * dual-behaviour rows, whose tooltip is about the gesture.
   */
  detail?: string
}

/**
 * The `title` for one row, or '' when the row has nothing worth saying.
 *
 * Returns the empty string rather than the label for a leaf with no detail: a
 * tooltip that repeats the visible text is a tooltip that trains people to
 * ignore tooltips, and the label is already on screen.
 */
export function sidebarRowHint({ kind, label, detail }: SidebarHintInput): string {
  if (kind === 'collection') return `${label} — click for details, double-click to expand`
  if (kind === 'folder') return `${label} — click for settings, double-click to expand`
  return detail?.trim() ?? ''
}
