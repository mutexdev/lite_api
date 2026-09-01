# A9 — DevTools and tables

## Summary

- `.empty-appState` — the class used for 24 empty states across `App.svelte` — has **zero CSS rules**. Every one of those empty states (WebSocket messages, proto files, cookies, environment variables, folder variables, .env files, auth-mode placeholders…) renders as an unstyled `<div>`, while the correctly-spelled `.empty-state` (used by DevTools and one lone spot in `App.svelte`) gets a dashed border, padding, and muted background. This alone is enough to make DevTools look like a "designed" app and the rest of the app look broken.
- There are **two completely separate "Network" table surfaces**: the DevTools Network tab (`App.svelte:8981`, virtualized, sortable, resizable, hover/selection states) and a second, plain, unstyled `activeView === 'network'` table (`App.svelte:11618`) reachable via the `open-network` command. Same data, two different implementations, wildly different fidelity.
- Row deletion is implemented two incompatible ways depending which table you're in: an `icon-button` glyph (`x`) in 6 tables, a full-word `Remove` text button in 6 others — no shared convention.
- One "Collection Variables" table (`App.svelte:10557`) has **no way to delete a row at all** — the action column is simply missing, unlike every sibling variables table. A second, duplicate "Collection Variables" table elsewhere (`App.svelte:11375`) has no header row, no Type column, and also no remove button, sitting in the same panel as a `KeyValueTable`-based "Collection Headers" article that has all of the above.
- The row-reorder affordance (drag handle + move up/down, `showMove`) is wired up only for the rarely-visited Response Examples editor tables; the primary, most-used Params/Headers/Body tables never get it, even though they use the identical `KeyValueTable`/`MultipartTable`/`FileBodyTable` components.
- The same kind of data — an HTTP header value — is monospaced in one panel (DevTools Request Details, wrapped in `<code>`) and rendered in the body/proportional font in another (main Response ▸ Headers tab), with no table header row on top of that.
- DevTools is visually its own thing: dense, resizable, sortable, hover/selection states, `aria-sort`. Nothing else in the app looks like it, and nothing states that this is deliberate.
- No table anywhere in the app — DevTools included — has zebra striping; only the DevTools network table has row hover/selection. That's fine as a house style, but it isn't declared anywhere, and everything else (missing header rows, missing remove buttons, `x` vs `×`) suggests it's incidental rather than a choice.

## Table conformance audit

Legend: **Hdr** = header row present · **Sort** = sortable columns · **En** = enabled/checkbox column · **Actions** = row action style · **Add** = add-row control & placement · **Bulk** = bulk-edit toggle · **Zebra/Hover** · **Virt** = virtualized · **Empty**

| # | Surface | File:line | Hdr | Sort | En | Actions | Add | Bulk | Zebra/Hover | Virt | Empty state |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | DevTools Network log | `App.svelte:8981` | Y | Y (tri-state, `networkSort.ts`) | – | row click → detail panel | – | – | hover+selected (tokens) | Y (`virtualList.ts`) | `.empty-appState` (unstyled) `App.svelte:8969` |
| 2 | DevTools Request Details ▸ headers (req/resp) | `RequestDetailsPanel.svelte:54,76` | Y | N | – | none (read-only) | – | – | none | N | `.empty-state.compact` "No headers" (styled) |
| 3 | DevTools Request Details ▸ network log lines | `RequestDetailsPanel.svelte:98` | n/a (div list, not `<table>`) | N | – | none | – | – | none | N | styled |
| 4 | Legacy Network Log view (`activeView==='network'`) | `App.svelte:11618` | Y | N | – | none | – | – | none | N | none (empty array just renders 0 rows) |
| 5 | `KeyValueTable` primitive (shared) | `lib/KeyValueTable.svelte:302` | Y | N | Y (`showEnabled`) | icon-button `x` (467) | "Add row" button below table (477) | optional (`showBulkEdit`) | none | N | caller-supplied, inconsistent class (see A9-01) |
| 6 | — used for: Params | `App.svelte:9335` | Y | N | Y | icon `x`, no move | Y | Y | – | – | – |
| 7 | — used for: Path Params (readonly) | `App.svelte:9354` | Y | N | N (`showEnabled=false`) | none (`showActions=false`) | N (`showAddRow=false`) | Y (odd: bulk edit shown on a table with no per-row actions) | – | – | – |
| 8 | — used for: Headers | `App.svelte:9375` | Y | N | Y | icon `x`, no move | Y | Y | – | – | – |
| 9 | — used for: form-urlencoded body | `App.svelte:9528` | Y | N | Y | icon `x`, no move | Y | N | – | – | – |
| 10 | — used for: Request Vars | `App.svelte:9743` | Y | N | Y | icon `x`, no move | Y | N | – | – | – |
| 11 | — used for: Folder Headers | `App.svelte:10393` | Y | N | Y | icon `x`, no move | Y | N | – | – | – |
| 12 | — used for: Collection Headers (×2, same table twice) | `App.svelte:10550`, `11391` | Y | N | Y | icon `x`, no move | Y | N | – | – | – |
| 13 | — used for: Response Example Params/Headers/Form/Body-headers | `App.svelte:9940-10033` | Y | N | mixed | icon `x`, **move enabled** | Y | Y | – | – | – |
| 14 | `MultipartTable` primitive | `lib/MultipartTable.svelte:139` | Y | N | Y | icon `x` | "Add row" (235) | N (no bulk edit option exists) | none | N | none built in |
| 15 | — used for: multipart body (live) | `App.svelte:9544` | Y | – | – | no move | – | – | – | – | – |
| 16 | — used for: Response Example multipart | `App.svelte:9952` | Y | – | – | **move enabled** | – | – | – | – | – |
| 17 | `FileBodyTable` primitive | `lib/FileBodyTable.svelte:70` | Y | N | radio (single-select) not checkbox | icon `x` | "Add File" (131) | N | none | N | none built in |
| 18 | — used for: file body (live) | `App.svelte:9558` | Y | – | – | no move | – | – | – | – | – |
| 19 | — used for: Response Example file | `App.svelte:9963` | Y | – | – | **move enabled** | – | – | – | – | – |
| 20 | gRPC messages | `App.svelte:9437` | Y (`Name/Content/""`) | N | N | icon-buttons holding TEXT: "Send" (9449), "Gen" (9451), icon `x` (9452) | "Add message" below (9459) | N | none | N | `.empty-appState` if no active method (upstream) |
| 21 | WebSocket messages | `App.svelte:9479` | Y (`Send/Name/Type/Content/""`) | N | Y ("Send" col is actually a per-row checkbox) | icon-button holding TEXT "Send" (9498), icon `x` (9499) | "Add message" below (9507) | N | none | N | `.empty-appState` "No WebSocket messages" (9477, unstyled) |
| 22 | Assertions | `App.svelte:9755` | Y (`""/Expression/Operator/Value/""`) | N | Y | icon `x` (9766) | "Add assertion" below (9771) | N | none | N | none (empty array = 0 rows, no message) |
| 23 | Folder pre-request variables | `App.svelte:10405` | Y (`On/Name/Value/Type/Secret/""`) | N | Y | **text `Remove`** button (10422) | "Add variable" **above** table, in section header (10402) | N | none | N | `.empty-appState` (unstyled, 10429) |
| 24 | Folder post-response variables | `App.svelte:10437` | Y | N | Y | **text `Remove`** (10454) | "Add variable" above (10434) | N | none | N | `.empty-appState` (unstyled, 10461) |
| 25 | Collection variables (Collection Settings ▸ Vars) | `App.svelte:10557` | Y (`""/Name/Value/Type/Secret` — **no action column at all**) | N | Y | **none — no way to delete a row** | "Add variable" below (10580) | N | none | N | none |
| 26 | Client certificates | `App.svelte:10879` | Y | N | – | **text `Remove`** (10905) | "Add client certificate" below, outside `.table-scroll` (10911) | N | none | N | none (0 rows renders empty tbody) |
| 27 | Proto files | `App.svelte:10919` | Y | N | – | **text `Remove`** (10938) | "Add proto file" **above**, in section header (10916) | N | none | N | `.empty-appState` (unstyled, 10945) |
| 28 | Proto import paths | `App.svelte:10953` | Y | N | Y | **text `Remove`** (10968) | "Add import path" above (10950) | N | none | N | `.empty-appState` (unstyled, 10975) |
| 29 | Global environment variables | `App.svelte:11223` | Y | N | Y | **text `Remove`** (11240) | in a `.toolbar` of 6 buttons below (11249) | N | none | N | `.empty-appState` (unstyled, 11246) |
| 30 | Environment variables | `App.svelte:11284` | Y | N | Y | **text `Remove`** (11301) | single "Add" button below (11309) | N | none | N | `.empty-appState` (unstyled, 11307) |
| 31 | .env files list | `App.svelte:11336` | Y (`Scope/File/Runtime`) | N | – | none (select via row button) | separate toolbar above (11326-11329) | N | none | N | `.empty-appState` (unstyled, 11349) |
| 32 | .env variables | `App.svelte:11353` | Y (`Name/Value/""`) | N | N (no enabled concept) | **text `Remove`** (11360) | "Add variable" below (11368) | N (raw/table toggle instead, 11332) | none | N | `.empty-appState` (unstyled, 11366) |
| 33 | Collection Variables — **duplicate**, Environments view | `App.svelte:11375` | **N — no `<thead>` at all** | N | Y | **none — no remove button, no Type column** | "Add variable" below (11387) | N | none | N | none |
| 34 | Cookies (per-domain) | `App.svelte:11720` | Y (`Name/Value/Path/Expires/Flags/""`) | N | – | **text `Edit`** + icon `x` **in the same cell** (11732-11733) | no per-table add row; cookies are created via the separate "Add cookie" form (11660) | N | none | N | `.empty-appState` ×2 (unstyled, 11706/11708) |
| 35 | Response ▸ Headers tab | `ResponseInspector.svelte:429` | **N — no `<thead>`** | N | – | none (read-only), no `<code>` on value | n/a | n/a | none | N | `.empty-state` (styled, in this file) |
| 36 | Response ▸ Metadata/Trailers tab | `ResponseInspector.svelte:433` | **N — no `<thead>`** | N | – | none | n/a | n/a | none | N | `.empty-state` |
| 37 | Response ▸ Compare ▸ header diff | `ResponseInspector.svelte:418` | Y | N | – | none | n/a | n/a | none | N | filtered list, no explicit empty row |
| 38 | Response ▸ Timeline ▸ gRPC metadata/trailers (nested) | `ResponseInspector.svelte:439` | **N — no `<thead>`** (×2) | N | – | none | n/a | n/a | none | N | conditionally rendered only when non-empty |

(Items 35–38 live in `ResponseInspector.svelte`, which the A2 report covers for body/toolbar issues; the table-structure gaps above are logged here since they fall under "every table in the app.")

That is **24 distinct table/grid markup sites** rendering row/column data (3 shared primitives covering 12 call sites, plus 21 one-off hand-rolled `<table>` blocks), not counting the non-`<table>` list surfaces (Console log list, Terminal sessions, gRPC/WS event logs) which behave like tables but use `<div>` rows.

## Findings

### A9-01 — `.empty-appState` is a dead class name; 24 empty states render completely unstyled
- **Severity**: critical
- **Where**: `frontend/src/App.svelte` — 24 occurrences, e.g. `:8969, :9477, :9565, :9739, :9777, :9873, :10372, :10429, :10461, :10752, :10945, :10975, :11004, :11006, :11008, :11052, :11246, :11266, :11307, :11311, :11349, :11366, :11706, :11708`. Confirmed absent from `frontend/src/style.css` (only `.empty-state` at `style.css:2621` and `.devtools-empty` at `:2770` are defined).
- **What the user sees**: an empty WebSocket message list, empty proto-files table, empty cookie store, etc. render as plain left-aligned text with no border, no padding, no background — visually indistinguishable from a rendering bug — while the *same kind* of empty state in DevTools (`ConsoleTab.svelte:14`, `RequestDetailsPanel.svelte:52/67/74/89/96`, both using the correctly-spelled `.empty-state`) gets a dashed-border card with generous padding and muted color.
- **Why it's wrong**: this is a one-character-off class name typo (`empty-appState` vs `empty-state`) that has propagated through 24 copy-pasted call sites. It's the single largest reason non-DevTools screens look unfinished relative to DevTools.
- **Proposed fix**: rename `empty-appState` → `empty-state` everywhere in `App.svelte` (mechanical find-and-replace, 24 sites). Verify `.empty-state.wide` (`style.css:2629`) and `.empty-state.compact` (`style.css:3082`) still apply correctly to the modifiers already used (`wide` at `9739/10752/11004/11006/11008`, `compact` at `11052`).
- **Shared primitive it should use**: `.empty-state` (already exists, already used correctly in 8 other places in the codebase).

### A9-02 — Two unrelated "Network" table implementations exist side by side
- **Severity**: critical
- **Where**: DevTools Network tab, `frontend/src/App.svelte:8949-9377` (virtualized via `virtualList.ts`, sortable via `networkSort.ts`, resizable columns, hover/selected states, `frontend/src/App.svelte:8981-9058`) vs. the legacy `activeView === 'network'` panel, `frontend/src/App.svelte:11615-11626`. Reachable independently: sidebar/command-palette item and the `open-network` command handler at `frontend/src/App.svelte:6769-6770` set `activeView = 'network'`, which is a different code path from `devToolsTab === 'network'` (`App.svelte:8949`).
- **What the user sees**: "Network Log" (`App.svelte:11617`, `<h2>Network Log</h2>`) is a bare, unstyled `<table>` with a static column set (`Method/URL/Status/Time/Error`) and no interaction at all, while DevTools' "Network" tab shows a fully-featured, resizable, sortable, virtualized table with a request/response detail pane. Both draw from request/response network activity.
- **Why it's wrong**: this is the clearest single example of "the app looks like a different application in each section" — the exact same category of data has a state-of-the-art implementation in one place and what looks like an abandoned prototype in another, both reachable from the main nav/command palette.
- **Proposed fix**: determine whether `activeView === 'network'` is legacy/dead code that predates the DevTools panel. If it has no unique data (i.e. `appState.networkLog` vs `devToolsNetworkRows` are the same underlying data or one is a strict subset), delete the legacy view and repoint its nav entry/command at DevTools ▸ Network. If it serves a genuinely different purpose (e.g. only completed requests logged at the Go layer vs. live browser-visible requests), it still needs the same table primitive and at minimum a header, column sizing, and empty state.
- **Shared primitive it should use**: the proposed `DataTable` (read-only mode) below, or outright reuse of the DevTools network table component if the data is the same.

### A9-03 — Collection variables table has no delete action
- **Severity**: major
- **Where**: `frontend/src/App.svelte:10557-10579` (`collectionTab === 'vars'`)
- **What the user sees**: a table with columns `On / Name / Value / Type / Secret` and no sixth column — there is no button, icon, or gesture to remove a variable once added. Every sibling variables table (folder pre-request `10405-10426`, folder post-response `10437-10458`, global env `11223-11244`, environment `11284-11305`) has a `Remove`/`x` action in the last column; this one doesn't.
- **Why it's wrong**: this isn't a style inconsistency, it's a missing feature — a user who adds a collection variable by mistake has no in-table way to remove it (they'd have to know to look in the separate Environments-view "Collection Variables" duplicate at `11375`, which also lacks a remove action — see A9-04).
- **Proposed fix**: add the same `<td><button class="icon-button" ...>x</button></td>` (or, given A9-06's recommendation, whatever the reconciled action style becomes) backed by `removeCollectionVariable(index)` (mirroring `removeGlobalEnvironmentVariable`/`removeEnvironmentVariable`).
- **Shared primitive it should use**: this table's data shape (`On/Name/Value/Type/Secret`) is identical to Folder Vars and Environment Vars — all three should be the same `DataTable` config, not three hand-rolled copies with drifted feature sets.

### A9-04 — Duplicate "Collection Variables" table with no header row and no remove action, next to a fully-featured sibling
- **Severity**: major
- **Where**: `frontend/src/App.svelte:11373-11388` (Environments view ▸ "Collection Variables" `<article>`), immediately followed by `frontend/src/App.svelte:11389-11397` ("Collection Headers" `<article>`, which uses `<KeyValueTable>`).
- **What the user sees**: scrolling the Environments screen, the "Collection Variables" card has bare rows with no column headings and a table with only 4 `<td>`s per row (enabled checkbox, name, value, secret — no Type, no remove); the very next card, "Collection Headers," is the polished `KeyValueTable` with header row, name/value inputs, and a working remove button. Two adjacent cards in the same panel, one raw and broken, one production-quality.
- **Why it's wrong**: this is a second, independently-maintained copy of the same "Collection Variables" data already shown (more completely, but still without a remove button — A9-03) under Collection Settings ▸ Vars. Two divergent renderings of one dataset is exactly the "looks like a different app" complaint, visible within a single scroll of a single screen.
- **Proposed fix**: delete this duplicate table and either link out to Collection Settings ▸ Vars, or replace it with a `KeyValueTable` bound to `activeCollection.variables` (matching the adjacent "Collection Headers" card's pattern) with an added Type/Secret column set.
- **Shared primitive it should use**: `KeyValueTable`, as the neighboring "Collection Headers" card already demonstrates is possible in this exact panel.

### A9-05 — Response header/metadata/trailer tables have no header row, unlike the equivalent DevTools tables
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:429` (Response ▸ Headers), `:433` (Metadata/Trailers), `:439` (nested gRPC metadata/trailers inside Timeline) — all `<table><tbody>...` with no `<thead>`. Compare `frontend/src/lib/views/devtools/RequestDetailsPanel.svelte:54-61` and `:76-83`, which render the identical kind of data (`Name`/`Value` header pairs) with `<thead><tr><th>Name</th><th>Value</th></tr></thead>` and wrap the value in `<code>` for monospace.
- **What the user sees**: opening the main Response panel's Headers tab shows two unlabeled columns of text; opening DevTools ▸ Network ▸ (select a request) ▸ Request/Response tab shows the same category of data — header name/value pairs — properly labeled and monospaced.
- **Why it's wrong**: same data, two conformance levels, in a codebase that otherwise consistently gives read-only key/value tables a header row (see rows 2, 34 vs 35/36/38 in the conformance table above).
- **Proposed fix**: add `<thead><tr><th>Name</th><th>Value</th></tr></thead>` to all four table instances in `ResponseInspector.svelte`, and wrap the value cell in `<code>` to match `RequestDetailsPanel.svelte`'s monospace treatment (see A9-11).
- **Shared primitive it should use**: the read-only mode of the proposed `DataTable` (see below) — this is exactly the "read-only key/value list" shape it should standardize.

### A9-06 — Row deletion is `icon-button x` in half the tables, a text `Remove` button in the other half
- **Severity**: major
- **Where**: Icon-button `x`: `lib/KeyValueTable.svelte:466`, `lib/MultipartTable.svelte:226`, `lib/FileBodyTable.svelte:122`, `App.svelte:9452` (gRPC), `App.svelte:9499` (WS), `App.svelte:9766` (assertions), `App.svelte:11733` (cookies). Text `Remove` button: `App.svelte:10422` (folder pre-request vars), `App.svelte:10454` (folder post-response vars), `App.svelte:10905` (client certs), `App.svelte:10938` (proto files), `App.svelte:10968` (proto import paths), `App.svelte:11240` (global env vars), `App.svelte:11301` (env vars), `App.svelte:11360` (.env variables).
- **What the user sees**: deleting a header row is a small square `x` button; deleting a proto file two tabs over is a full-width text button that says "Remove." Both actions do exactly the same thing (remove this row from this array).
- **Why it's wrong**: no evident rule decides which style a given table gets — it correlates with which developer/PR touched that table, not with any property of the data (all of these are simple flat-array-of-rows tables). This is the single most repeated inconsistency in the whole DevTools/tables surface.
- **Proposed fix**: standardize on one row-delete control for the whole app (icon-button is more space-efficient and is already the majority pattern in the shared primitives) and convert the 8 `Remove`-button sites to it.
- **Shared primitive it should use**: the `DataTable` contract's `rowActions` slot (see below), which should render a single delete control consistently across every table.

### A9-07 — `icon-button` class holding multi-letter text labels
- **Severity**: major
- **Where**: `App.svelte:9449` (`<button class="icon-button" ...>Send</button>`), `App.svelte:9451` (`>Gen</button>`), `App.svelte:9498` (`>Send</button>`). `.icon-button` is defined at `style.css:931-936` as `width: 32px; min-width: 32px; padding: 0; text-align: center;` — a fixed-width square meant to hold a single glyph or icon.
- **What the user sees**: in the gRPC and WebSocket message tables, the "Send" and "Gen(erate)" buttons are visibly cramped/wrapping inside a 32px-wide box because they're 3-4 letter words forced into an icon-sized square, while an identically-classed button one row over correctly holds a single character (`x`).
- **Why it's wrong**: the class name is a contract ("this button is icon-sized/icon-shaped") that these three instances violate; either the class is wrong for them or the content is.
- **Proposed fix**: drop `class="icon-button"` from these three buttons and let them size naturally as regular text buttons (consistent with "Start stream"/"End"/"Cancel" in the same panel at `App.svelte:9430-9432`), or replace their text with a real icon glyph if the intent was a compact toolbar.
- **Shared primitive it should use**: n/a — this is a misapplication of an existing class, not a missing primitive.

### A9-08 — Dismiss/remove glyph is inconsistently `x` (letter) vs `×` (multiplication sign)
- **Severity**: minor
- **Where**: ASCII letter `x`: `lib/FileBodyTable.svelte:122`, `lib/KeyValueTable.svelte:466`, `lib/MultipartTable.svelte:226`, `lib/SidebarSearch.svelte:30`, `App.svelte:9452, 9499, 9766, 11219, 11280, 11733` (10 sites). Proper `×` (U+00D7): `lib/views/devtools/TerminalTab.svelte:48`, `App.svelte:8859, 8882, 8918` (4 sites).
- **What the user sees**: two visually different close/remove glyphs used for the same affordance depending on which corner of the app the button is in — the letter `x` looks like an abbreviation, the `×` looks like a close icon.
- **Why it's wrong**: purely cosmetic but very visible — it's the kind of detail that makes controls feel hand-assembled rather than from one component set.
- **Proposed fix**: standardize on `×` (or an actual icon) everywhere; the `KeyValueTable`/`MultipartTable`/`FileBodyTable` family's `x` is the highest-leverage fix since it's shared by ~9 call sites.
- **Shared primitive it should use**: a single `RowDeleteButton` (or the `DataTable` `rowActions` slot from A9-06) fixes both A9-06 and this in one change.

### A9-09 — Row reordering (`showMove`) is enabled only on the least-visited tables
- **Severity**: major
- **Where**: `showMove={true}` appears exclusively inside the Response Examples editor: `App.svelte:9943` (example request form body), `9954` (example request multipart), `9965` (example request file), `9981` (example request params), `9995` (example request headers), `10029` (example response headers). It is **never** set on the live-request Params (`9335`), Path Params (`9354`), Headers (`9375`), form-urlencoded body (`9528`), multipart body (`9544`), file body (`9558`), Request Vars (`9743`), Folder Headers (`10393`), or Collection Headers (`10550`, `11391`) — all of which use the exact same components and already ship the `onMove`/`onReorder` plumbing (`KeyValueTable.svelte:73-74`, `MultipartTable.svelte:61-62`, `FileBodyTable.svelte:16-17`).
- **What the user sees**: dragging a header or param row to reorder it works nowhere in the primary request editor — the surface used on every single request — but works in the saved-response-examples editor, a secondary feature most users touch rarely.
- **Why it's wrong**: the affordance exists and is fully built (drag handle, up/down buttons, keyboard-reachable) but is switched on backwards relative to usage frequency. Reordering headers/params is a routine editing task; reordering a saved example's snapshot is not.
- **Proposed fix**: add `showMove={true}` to the live Params/Headers/body/Vars/Folder/Collection `KeyValueTable`/`MultipartTable`/`FileBodyTable` instances listed above, unless there's a deliberate reason (not evident in the code or comments) that live requests shouldn't support reordering.
- **Shared primitive it should use**: none needed — this is a one-line prop change per call site, once the `DataTable` contract makes `move` a first-class, opt-out-by-default capability rather than opt-in.

### A9-10 — Bulk-edit toggle is wired to 2 of ~11 `KeyValueTable` call sites
- **Severity**: minor
- **Where**: `showBulkEdit={true}` at `App.svelte:9336` (Params), `9378` (Headers), plus `9982/9996/10030` (Response Example Params/Headers/response-headers). Not set for: form-urlencoded body (`9528`), Request Vars (`9743`), Folder Headers (`10393`), Collection Headers (`10550`, `11391`), Path Params (`9354`, arguably correctly excluded since it's read-only).
- **What the user sees**: the "Key/Value Edit / Bulk Edit" toggle (`KeyValueTable.svelte:283-284`) appears above Params and Headers but not above the structurally identical Folder Headers or Collection Headers tables, with no stated reason.
- **Why it's wrong**: bulk edit is a general `KeyValueTable` capability (`rowsToBulkText`/`parseBulkText` don't care about the row's origin), so its absence elsewhere reads as an oversight rather than a decision.
- **Proposed fix**: either enable `showBulkEdit` uniformly on every name/value table that isn't read-only (Headers, Folder Headers, Collection Headers, form-urlencoded body, Vars), or document why Params/Headers specifically get it and others deliberately don't.
- **Shared primitive it should use**: `KeyValueTable`'s existing `showBulkEdit` prop — no new component needed, just consistent application.

### A9-11 — Header/value text is monospaced in DevTools but proportional in the main Response panel
- **Severity**: minor
- **Where**: `RequestDetailsPanel.svelte:58` and `:80` wrap header values in `<code>{value}</code>`. `ResponseInspector.svelte:429` renders the identical kind of value as bare `{value}` text (no `<code>`, no `--code-font-family`). `ResponseInspector.svelte:418`'s header-diff table also renders `{row.current}`/`{row.selected}` unwrapped.
- **What the user sees**: a header value like `application/json; charset=utf-8` reads in the app's UI (proportional) font in the main Response tab, and in a monospace font in DevTools' request-details pane, for what is definitionally the same string.
- **Why it's wrong**: header/URL/token values are exactly the kind of content the design already has a convention for (`--code-font-family`, used 15+ times elsewhere for values, URLs, and code) — its absence here looks accidental, not intentional.
- **Proposed fix**: wrap header/metadata/trailer values in `<code>` in `ResponseInspector.svelte` (ties into the A9-05 fix, since both need the same markup change).
- **Shared primitive it should use**: `DataTable`'s value-cell rendering should default to `--code-font-family` for any column semantically typed as a header/URL/token value.

### A9-12 — DevTools' network table is the only table in the app with hover/selection feedback
- **Severity**: minor
- **Where**: `style.css:2886-2905` (`.devtools-network-table tr.selected td`, `tr[data-network-row]:hover td`, `:focus-visible`). No equivalent rule exists for `.kv-table`, the gRPC/WS message tables, the variables tables, or the cookies table (confirmed: no `nth-child(even)`/`tr:hover` rules anywhere else in `style.css` except one unrelated `.openapi-spec-diff-cell:nth-child(odd)` at `4703`, which is not a table striping rule).
- **What the user sees**: hovering a row in DevTools' network log visibly highlights it and shows a focus ring when tabbed to; hovering a row in the cookies table, the assertions table, or any variables table does nothing.
- **Why it's wrong**: this is defensible as "editable tables don't need row hover, only read-only/selectable ones do" — but that rule is never stated, and several read-only-ish rows (cookies, the legacy network log) get no hover either, so it reads as one table having gotten more attention rather than a considered rule.
- **Proposed fix**: state the rule explicitly (row hover/selection only for tables where clicking/selecting a row does something, e.g. opens a detail view) and apply it to the legacy Network Log table and the Cookies table if they're kept.
- **Shared primitive it should use**: `DataTable`'s `selectable`/`onRowClick` flag should own hover/selected/focus-visible styling centrally, so it's automatic rather than hand-added per table.

### A9-13 — DevTools is styled as a distinct sub-application, undeclared
- **Severity**: polish
- **Where**: `App.svelte:8924-9377` (`devToolsPanel` snippet) and its supporting CSS (`.devtools-network-table`, `.network-filter-bar`, `.method-filter-list`, `.column-resizer`, all clustered around `style.css:2760-2920`).
- **What the user sees**: DevTools has denser rows, resizable/sortable columns, a filter bar, and hover/selection feedback that visually reads closer to Chrome DevTools than to the rest of LiteAPI's softer, bordered-card tables (compare the Cookies or Environments screens).
- **Why it's wrong**: not wrong on its own — a Chrome-DevTools-like density is a defensible choice for a panel literally called DevTools — but it isn't declared anywhere (no comment, no design doc reference) and the rest of the app doesn't share any of its primitives (no other table gets resizable columns, sorting, or hover states), so it reads as accidental rather than intentional.
- **Proposed fix**: either (a) explicitly adopt "DevTools looks like DevTools" as policy and backport its resize/sort/hover primitives to the app's other high-density tables (network log, history) so the family is at least internally consistent, or (b) restyle DevTools to match the softer card language used everywhere else. Either is fine; only "neither" is the current, undocumented state.
- **Shared primitive it should use**: n/a — this is a decision to make, not a component to build.

### A9-14 — No table in the app (outside per-row `aria-label`s) has an accessible name
- **Severity**: minor
- **Where**: every `<table>` cited in the conformance audit above lacks `aria-label`/`aria-labelledby`/`<caption>` on the `<table>` element itself — e.g. `App.svelte:9437` (gRPC messages), `10919` (proto files), `11720` (cookies per domain). Compare the one place row-level accessibility *is* handled well: `App.svelte:9029-9044`, where each network-log `<tr>` gets `role="button"` and a computed `aria-label`.
- **What the user sees**: nothing visually; a screen-reader user tabbing/browsing by table has no name announced for "what is this table," only column headers (where present) and cell content.
- **Why it's wrong**: minor but cheap to fix, and the codebase already shows the discipline to do this well elsewhere (per-row `aria-label`s, `aria-sort`, `role="combobox"` on suggestion inputs in `KeyValueTable.svelte:342-346`) — the table-level name is the one piece missing from an otherwise accessibility-conscious codebase.
- **Proposed fix**: add `aria-label` to each `<table>` (e.g. `aria-label="gRPC messages"`, `aria-label="Proto files"`), ideally driven by the same label the surrounding `<h3>`/section title already provides.
- **Shared primitive it should use**: `DataTable`'s contract should require a `label` prop and apply it as `aria-label` unconditionally.

## Cross-cutting primitives this area needs

1. **One `DataTable` component** (see contract below) that both the editable key/value tables (`KeyValueTable`, `MultipartTable`, `FileBodyTable`, and the dozen hand-rolled variable/cert/proto tables) and the read-only ones (response headers, cookies-per-domain, network log, DevTools request details) are built on. Right now there are 3 shared components plus ~18 one-off `<table>` blocks reimplementing the same header/row/action/add/empty structure with drifted details every time.
2. **One row-delete control.** Icon-button `x`/`×` vs text `Remove` should not both exist; pick one (A9-06, A9-08).
3. **One empty-state class**, correctly spelled and applied everywhere (A9-01) — the highest-leverage single fix in this report.
4. **One rule for when tables get hover/selection/zebra**, applied consistently rather than only where a developer happened to add it (A9-12).
5. **One rule for when a value cell is monospaced.** Any column holding a header name/value, URL, path, token, or raw payload should default to `--code-font-family`; plain field labels and free text should not. Currently this is decided per-`<td>`, not per-column-semantics (A9-11).
6. **One convention for add-row placement.** Currently "Add X" appears above the table in a `.settings-section-header` (proto files, folder vars) *and* below the table as a lone `<button>` (most others) *and* inside a 6-button `.toolbar` mixed with Copy/Export/Import/Delete (global env vars, `11248-11255`). Pick one position.
7. **`showMove`/`showBulkEdit` should default to the same value across all uses of a given data shape** — right now identical components with identical row shapes (Params vs. Folder Headers; live Params vs. Response Example Params) get different capability sets for no stated reason (A9-09, A9-10).

## Proposed `DataTable` contract

A single Svelte 5 component covering both the editable and read-only cases described above, replacing `KeyValueTable`/`MultipartTable`/`FileBodyTable` plus the ~18 hand-rolled tables in `App.svelte`/`ResponseInspector.svelte`.

```ts
type DataTableColumn<Row> = {
  key: string
  label: string                    // used for <th> text AND as a11y fallback
  width?: number | 'auto'
  align?: 'start' | 'end'
  monospace?: boolean              // renders cell content wrapped in <code>, uses --code-font-family
  render?: (row: Row) => string | Snippet   // default: row[key]
  editable?: {
    kind: 'text' | 'password' | 'checkbox' | 'radio' | 'select' | 'textarea'
    options?: string[]             // for 'select'
    placeholder?: string
    suggestions?: (query: string, row: Row, otherRows: Row[]) => string[]
  }
  sortable?: boolean               // read-only tables only; drives aria-sort + tri-state click
}

type DataTableProps<Row> = {
  label: string                    // REQUIRED — becomes aria-label on <table>, and the
                                    // empty-state / a11y fallback name. (Closes A9-14.)
  columns: DataTableColumn<Row>[]
  rows: Row[]
  rowKey: (row: Row, index: number) => string | number

  // Mode
  readonly?: boolean               // true: no editable cells rendered regardless of column config,
                                    // no add row, no row actions unless `rowActions` explicitly passed

  // Row actions (replaces every hand-rolled "Remove"/icon-button x)
  rowActions?: {
    onRemove?: (row: Row, index: number) => void      // renders the ONE standard delete control
    custom?: Snippet<[Row, number]>                    // for cases like Cookies' Edit+Delete pair
  }

  // Reordering — defaults ON for any editable table unless explicitly disabled,
  // closing the "reorder only works in Response Examples" gap (A9-09).
  reorder?: {
    enabled: boolean                 // default: true when !readonly
    onMove: (index: number, direction: -1 | 1) => void
    onReorder: (from: number, to: number) => void
  }

  // Add row — one placement convention: always directly below the table,
  // never above it and never folded into an unrelated toolbar (closes the
  // add-row-placement drift in "Cross-cutting primitives" #6).
  onAdd?: () => void
  addLabel?: string                 // default: `Add ${label singular}` derived from `label`

  // Bulk edit — available whenever every editable column is 'text'/'password'
  // (i.e. the shape KeyValueTable already supports); opt OUT, not opt in,
  // closing A9-10.
  bulkEdit?: { enabled?: boolean; label?: string } // default enabled: true when eligible

  // Selection / row click, for read-only tables that open a detail view
  // (network log, history). Owns hover/selected/focus-visible styling
  // centrally instead of each table re-implementing it (closes A9-12).
  onRowSelect?: (row: Row) => void
  isSelected?: (row: Row) => boolean

  // Virtualization — opt in for any table expected to exceed ~200 rows
  // (network log, history, console). Below that, plain rendering is fine.
  virtualized?: { rowHeight: number }

  emptyState?: { title: string; detail?: string } | Snippet
  // default renders via the corrected `.empty-state` class (closes A9-01)
}
```

Key decisions this contract encodes, each tied to a finding above:

- **`label` is required, not optional** — every table gets an accessible name and a derived empty-state/add-row string for free (A9-14, and reduces the "Add variable" vs "Add message" vs "Add File" label drift to one derivation rule).
- **`readonly` is a mode switch, not a per-field flag** — the current codebase has tables that are "half read-only" by omitting individual props (`showActions=false`, `showEnabled=false`) rather than declaring intent, which is how the header-less, action-less `activeView==='network'` and Response Headers tables (A9-02, A9-05) came to exist as degraded copies instead of one `DataTable` in read-only mode.
- **`reorder` defaults on for editable tables** rather than requiring every call site to remember `showMove={true}`, directly reversing the A9-09 inversion (reorder currently works where it matters least).
- **`bulkEdit` defaults on when eligible** rather than requiring each call site to opt in, closing A9-10.
- **One `rowActions.onRemove`** renders the single standard delete control app-wide, closing A9-03 (missing), A9-04 (missing), and A9-06/A9-08 (inconsistent) in one stroke — a table simply cannot end up with no delete affordance if `onRemove` is supplied, and cannot end up with a different glyph than its neighbors since the control is centrally rendered.
- **`monospace` is a column property**, not a per-cell decision, so "is this a code/token value" is answered once per table definition instead of per `<td>` (closes A9-11).
- **Hover/selection is owned by `onRowSelect`/`isSelected`**, not hand-written CSS per table, so any future selectable table (e.g. if the legacy Network Log view is kept rather than deleted) gets the same interaction for free (closes A9-12, informs A9-13 by making it cheap to extend DevTools' treatment to other tables if that's the chosen direction).
