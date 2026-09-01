# LiteAPI's clutter-free shell

Written 2026-09-01, from a hands-on walk through the running app at 1440×900,
against Postman, Insomnia, Yaak and Bruno. Builds on
[`uniform-ux-system.md`](uniform-ux-system.md): that campaign gave the app its
shared primitives; this one uses them to take chrome away.

## The complaint

"Clutter." The first screen shows **four rows of chrome** above the request
tabs and **three** above the response body, and the sidebar spends 130px on
brand, a button, a helper sentence and a labelled search box before the first
collection. Every reference app spends 36–44px there.

The measured evidence, first screen, default request:

| Surface | LiteAPI today | Yaak / Bruno / Postman |
| --- | --- | --- |
| Sidebar chrome above the tree | 130px, 5 elements | 36–44px, 1 row: search + `+` |
| Top bar controls | 13, four with text labels | 4–6, icons |
| Places to start a "New" request | 2 (sidebar button, top-bar menu) | 1 |
| Places to run the collection | 2 (top bar, request strip) | 1 (collection menu / runner page) |
| Orientation toggles | 2 (top bar, request strip) | 1 |
| Request strip rows | 2 (URL row + a row of five uppercase chips) | 1 |
| Request sub-tabs | 11, one a placeholder ("App") | 6–8 |
| Response rows before the body | 3 (status + tabs + view toolbar), status shows `0 ms 0 B` at rest | 2, status inline with tabs |
| Page-header layouts across views | one per view | one |

None of the reference apps show the collection's *file format* on every tree
row, an "Env:" chip when the environment picker is 40px above, or a
`SAVED` chip when the tab can carry a dot.

## The rule

**Chrome earns its row by being needed every minute.** Everything else lives
one click away in a menu that is the same menu everywhere, and is reachable
from the command palette and a shortcut. Nothing is deleted — it is moved to
where the reference apps put it, so a Postman or Bruno user finds it by
reflex.

Standing constraints from [[sidebar-ux-constraints]] hold: ⌘K finds, ⌘⇧P
runs commands, they stay separate modals; every keyboard path survives; no
theme or keybinding customisation work.

## Decisions

**D1 — The sidebar header is one row.** Brand mark and name on the left, two
icon buttons on the right: *search* (⌘F) and *new* (⌘N). The tagline, the
full-width New button and its helper sentence, and the "Search" field label
go. The find bar is hidden until the search icon or ⌘F opens it, stays while
it has a query, and Escape on an empty query closes it and returns focus to
the tree. The *new* button opens the exact `newItems` menu the top bar's
`+ New` opened (HTTP / GraphQL / gRPC / WebSocket / Folder / Collection /
Import), so there is one New menu, in the place every reference app puts it.

**D2 — Tree rows show state, not properties.** The `YML` / `BRU` format badge
leaves the collection row (it is on the collection page and in *Info*).
`Scratch`, `Git` and `Not cloned` stay: they change what the user can do. The
`⋯` button keeps its hit target and position but rests at reduced opacity and
comes to full on row hover, keyboard cursor, or focus. The collection's `⋯`
menu gains *Run collection*, which is where the top bar's Run button goes.

**D3 — The top bar is icons, at most nine controls.** Leading: sidebar
toggle, workspace, environment. Centre: the breadcrumb, which finally has room.
Trailing: *search* (⌘K, magnifier), *commands* (⌘⇧P, prompt icon — the
magnifier was on the wrong button), *notifications*, *recovery* (only while
there is something to recover), `⋯`. Removed from the bar and re-homed:
`+ New` (sidebar, D1), *Cookies* (already in `⋯` and the palette), *Run*
(collection menu, runner page, palette — while a run is in flight the bar
shows a single *Running… ■* cancel control in the trailing slot, because the
M3 QA contract requires a named cancel in the global toolbar), *Local/Git*
(a dot on the breadcrumb's collection segment, with the same tooltip), and the
orientation toggle (the request strip already has it). No text labels remain
in the trailing cluster.

**D4 — The request strip is one row.** `[method] [url] [Send ⌘↵]` then a
compact cluster: *Save* (icon, ⌘S, with a dot while dirty) and the orientation
toggle. The chip row is removed: protocol is on the tab, environment is in the
top bar, TLS and proxy are in Settings, saved state is on the tab (D6). The
*Run collection* button leaves the strip (D2, D3). Transport cues are computed
so that defaults produce **no** cue — only *TLS off* and a non-system proxy
qualify — and the survivors render as muted text at the right end of the
request sub-tab row.

**D5 — The response status lives in the tab row.** The `response-summary`
row is folded into a `PaneToolbar` whose left slot is the existing
`role="tablist"`, middle slot is the status (code, text, duration, size, the
failed-tests link), right slot is *Save as example* as an icon button. At rest
the middle slot renders nothing — no `Idle`, no `0 ms`, no `0 B`. The first
tab is renamed **Body**: inside a pane called Response, a tab called Response
names nothing.

**D6 — Tabs carry a dirty dot.** A tab whose request is a draft or transient
(the rule `commandState.ts:107` already applies to the active tab) shows a
dot in the close button's position at rest and the `×` on hover or focus, the
VS Code convention. This is what lets the `SAVED` chip go.

**D7 — Every full-pane view uses `PageHeader`.** `lib/ui/PageHeader.svelte`:
title left, live facts in a truncating middle, actions right, subtitle only
when it says something. Environments' two "create" forms move into the cards
they create; Import's *Export active* is removed from the header (export
lives on the collection page's Share section); Dev Tools loses the subtitle
that repeats its own tab names and keeps its counts as meta.

**D8 — The `App` request tab is removed.** It renders the sentence "Request
app runtime surface" and nothing else. Ten tabs remain.

**D9 — One vocabulary for chrome.** Icon buttons are `IconButton` (label is
the tooltip). No uppercase chips in chrome. No `<small>` helper sentences
under controls; the tooltip carries the hint.

## What is enforced by test

`frontend/test/shellChrome.test.mts` reads the sources and fails if:

| Invariant | Decision |
| --- | --- |
| `SidebarHeader.svelte` has no `<p>` tagline, no `<small>` helper, and renders two `IconButton`s | D1 |
| `SidebarSearch.svelte` renders `FindBar` only under a condition (hidden at rest) | D1 |
| The collection row in `App.svelte` does not render `{collection.format}` | D2 |
| `sidebarActions.ts` includes `run-collection` for a collection | D2 |
| `WorkspaceCommandBar.svelte` has no `Cookies`, `Run`, `Local`, `Git` or `Commands` text labels, and the palette button uses the `command` icon | D3 |
| `WorkspaceCommandBar.svelte` does not import `OrientationToggleButton` | D3 |
| `RequestCommandStrip.svelte` has no `.request-command-meta`, `command-protocol`, `command-environment`, `command-saved` or `command-scope-collection` | D4 |
| `commandState.ts` yields no cue for TLS on + system proxy | D4 |
| `App.svelte` has no `response-summary` and its response tab list starts with `Body` | D5 |
| `App.svelte` renders `tab-dirty` inside `.tab` | D6 |
| No `.svelte` file outside `lib/ui/` contains `class="panel-header"` | D7 |
| `requestTabs` in `App.svelte` has no `app` entry | D8 |
| `layout.test.mts` still finds a shell-scale media query in both chrome files | (unchanged) |

## Not attempted, and why

- **Environments as a sidebar list + table (Postman's model).** Right, and a
  whole view redesign; D7 tidies its header only.
- **Merging Assert into Tests.** A feature change, not a chrome change.
- **Collapsing the 19 font sizes.** Still the mechanical sweep the last
  campaign deferred, still waiting for a quiet tree.
