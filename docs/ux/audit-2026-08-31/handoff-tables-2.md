# Handoff — tables and DevTools, second pass (W-C2)

Follows `handoff-tables.md`. Closes A9-02, A9-09 (verification), A9-10, A9-12,
A9-14 (component side), A1-07 (survey) and A1-08 (component side).

Files this change owns and edited:

- `frontend/src/lib/networkSort.ts`
- `frontend/src/lib/views/devtools/NetworkTable.svelte` (**new**)
- `frontend/src/lib/views/devtools/RequestDetailsPanel.svelte`
- `frontend/src/lib/KeyValueTable.svelte`
- `frontend/src/lib/MultipartTable.svelte`
- `frontend/src/lib/FileBodyTable.svelte`
- `frontend/test/networkSort.test.mts` (+9 tests, 34 → 43)
- `frontend/test/tableRowActions.test.mts` (+8 tests, 13 → 21)

`App.svelte`, `style.css`, `lib/ui/**` and `lib/workbench/**` were not touched.
Everything needing one of them is below, paste-ready.

---

## 0. A9-02 — the recommendation, and why

**There should be one network table, and this change makes there be one
component. The `activeView === 'network'` route should keep existing for now and
mount it; deleting the route is the right follow-up, not the right first move.**

The two surfaces read the same array. `rawDevToolsNetworkRows` is
`appState?.networkLog ?? []` (`App.svelte:1631`) and the legacy view iterates
`appState.networkLog ?? []` (`App.svelte:11258`). Not a superset, not a
different layer — the same field. So the audit's first branch ("if it has no
unique data, delete the legacy view") is the one that applies, and there is no
argument for keeping two.

What does not follow is deleting the *route* in this change. `activeView` is a
union threaded through the sidebar, the command palette, tab restore and stored
preferences; removing a member of it changes what happens to a session whose
last view was that one, which is a navigation change with a different blast
radius from a table change and belongs to whoever owns the shell. Extracting the
good table into a component that both call sites mount closes the entire
fidelity gap now — the legacy view gets virtualisation, sorting, column resize,
method filtering, the detail pane, the empty states and an accessible name — and
leaves the route deletion as a two-line change to a `{:else if}` afterwards,
with nothing about the table riding on it.

So: §1 is the extraction, §2.2 is the DevTools mount, §2.3 is the legacy mount,
and §2.8 is the optional route deletion once someone owns that call.

---

## 1. What changed

### `lib/views/devtools/NetworkTable.svelte` (new) — A9-02

The DevTools network table, whole: filter bar, virtualised sortable resizable
table, row selection, and the lazily-imported detail pane. It owns every piece
of state that is a fact about the table rather than about the app — method
filters, sort key and direction, column widths, selected row, detail subtab,
scroll window. That state was eleven `let`s and twelve functions spread across
four regions of `App.svelte`, which is the actual reason nobody ever mounted
this table twice: the markup was the small part.

It does **not** own persistence. Stored preferences arrive as a prop and changes
leave through `onPreferencesChange`; saving them is a call into Go and this
component has no business knowing that. The in/out pair is guarded on the
serialised payload, exactly as `App.svelte` guarded it — preferences are
re-delivered on every app-state refresh, and an identity check would re-apply on
every refresh and undo a resize the moment the next snapshot arrived.

Props: `rows`, `label` (default `'Network requests'`), `preferences`,
`onPreferencesChange`, `detailsPanelWidth`, `onStartDetailsResize`.

Two deliberate behaviour changes:

- **An empty filter is now a different empty state from an empty log.** The old
  table said "No network requests" to both. An empty log means make a request;
  an empty *filtered* log means the rows exist and the method filter above is
  hiding them, and saying the first to the second reads as the log having been
  cleared. The new state names the hidden count and points at Show All.
- **The `<table>` has an accessible name** (A9-14), defaulted rather than
  required so a call site cannot produce an unnamed grid by omission.

No `<style>` block: every selector it uses (`.devtools-network-table`,
`.network-layout`, `.network-filter-bar`, `.method-filter-list`,
`.column-resizer`, `.network-spacer`) is defined in `style.css` and unchanged,
so moving the markup moved no pixels.

### `lib/networkSort.ts` — the formatters and the column list

Six functions moved out of `App.svelte`: `networkLogTime`, `networkStatusDisplay`
(was `statusDisplay`), `networkSizeDisplay` (was `formatNetworkSize`),
`networkHeaderRows`, `networkLogBody`, `networkLogLines`. Plus
`filteredNetworkRows` (was `filteredDevToolsNetworkRows`).

Four of these used to be *props of `RequestDetailsPanel`* — a component taking
formatters as parameters because they lived in a file it could not import from.
That is also why the legacy table rendered `row.status` and `row.durationMs`
raw: nobody passed them to it.

`NETWORK_COLUMNS`, `NETWORK_SORT_KEYS` and `NETWORK_METHODS` also moved here, and
that closes a latent bug. `normalizedNetworkColumnWidths` rejects a stored width
array whose *length* differs from `DEFAULT_NETWORK_COLUMN_WIDTHS` — the guard
against a build that adds a column shifting every restored width onto the wrong
header. The guard only works while the width list and the column list are the
same length, and they were seven entries in `networkSort.ts` and seven entries
in `App.svelte` with nothing but luck holding them equal.
`networkSort.test.mts` now asserts it.

`networkSizeDisplay` delegates to `formatting.ts`'s `formatBytes` rather than
carrying its own copy of `formatRuntimeBytes`; the two were character-identical.

### `RequestDetailsPanel.svelte` — runes, and its own imports

Converted from `export let` to `$props()`, `on:click`/`on:mousedown` to
`onclick`/`onmousedown`. The four formatters are imported.

**The four formatter props are still accepted and must stay accepted until §2.2
lands.** `App.svelte` still mounts this component and cannot be edited from
here; a required-prop set it no longer satisfies is a type error in a file
nobody in this wave can fix. They are optional now and default to the imported
functions, so the argument `App.svelte` passes and the argument `NetworkTable`
omits are the same function either way. Delete all four (and
`startDevToolsDetailsPanelResize`'s optionality) the moment §2.2 removes that
mount. `tableRowActions.test.mts` asserts they are optional, so deleting them
means deleting that assertion too — deliberately, so it cannot happen by accident
while the old mount is still there.

The details-panel resizer is now conditional on `startDevToolsDetailsPanelResize`
being supplied: a resizer is only honest where the width it drags is held and
stored somewhere.

### The three table primitives — A9-12

`KeyValueTable`, `MultipartTable` and `FileBodyTable` gained an identical scoped
style block:

```css
tbody tr:hover td { background: color-mix(in srgb, var(--selected-bg) 55%, transparent); }
tbody tr:focus-within td { background: var(--selected-bg); }
```

The two states are the DevTools network table's, remapped rather than copied.
Hover is the same 55% tint and means the same thing. What that table calls
*selected* has no analogue in a table with no selection — so `:focus-within`
carries the full `--selected-bg`, and the row being typed into is marked as
plainly as the row being read. `:focus-within` is written second on purpose:
equal specificity, so source order decides, and a row that is both should read
as focused.

The audit's proposed rule ("hover only where clicking a row does something")
would have left every editable table with nothing, which is wrong for a reason
the rule does not consider: these grids are rows of near-identical inputs, and
the mistake they invite is editing the *wrong row*, not failing to click one.

The block is byte-identical in all three and `tableRowActions.test.mts` asserts
it stays that way. Its one right home is `style.css` — §3.1 has the paste.

### `KeyValueTable.svelte` — A9-10, A1-08, and the bulk-edit hole

**The mode toggle is now `SegmentedControl`.** It was two plain buttons with an
`.active` class, no role, no grouping, and two tab stops for a one-of-two
choice — the exact hand-rolled shape the primitive exists to absorb, and the
same control the body-mode and response-view pickers already are. `testId` is
`kv-mode` on the group; the per-button `data-testid="kv-mode-rows"` /
`kv-mode-bulk` are gone and the buttons are addressable by `data-value` instead,
which is the primitive's convention. Nothing in `test/` referenced the old ids.

**Bulk edit is now gated on what the table permits, not on one prop.**

```ts
const bulkEditAvailable = $derived(showBulkEdit && !readonly && !readonlyNames && showAddRow)
```

This is A9-10's other half and it is not a style issue. The bulk textarea is not
a view of the rows, it is a *rewrite* of them — `parseBulkText` returns a whole
new array, so it can rename a row, delete one and invent one. Path Params passes
`showBulkEdit={true}` next to `readonlyNames={true}`, `showAddRow={false}` and
`showActions={false}`: its names are derived from the URL and the row set with
them. It got a Bulk Edit tab that let a user rename `:id`, delete it, and invent
a path parameter the URL does not contain, in a grid otherwise locked against
exactly that. The audit filed it as "odd: bulk edit shown on a table with no
per-row actions". It was not odd, it was a hole. Deriving the condition in the
component rather than removing the prop at the one call site means no future
caller can pass the same pair and reopen it.

**`bulkLabel` is optional and derives from `label`.** Five call sites spell out
"Request headers bulk edit"; the rest fell back to the same literal
"Bulk edit rows", so a screen with two bulk-editable tables announced two
textareas with one name. Given `label`, the name is `"${label} bulk edit"` and
the call site has nothing to keep in step. Existing explicit `bulkLabel`s still
win.

**`showDescription` / `descriptionLabel` — A1-08.** `row.description` finally has
a rendering path: a trailing read-only `<code>` cell, em dash when empty. The
Vars tab has mapped `description: v.dataType` onto every row since the data-type
feature landed and this component had nowhere to put it — computed on every
keystroke, dropped on the floor. Read-only because it is *derived*: the data type
is a fact about the value the user typed, and an input there would invite edits
the next recomputation silently discards. Off by default, because switching it
on changes the column count and eleven call sites share this table.

### A9-09 — verified, not changed

The component side is complete in all three primitives: `showMove` prop,
`draggable={showMove && !readonly}` on the `<tr>`, dragstart/dragover/drop/dragend,
`onMove`, `onReorder`, and `RowActions` rendering the handle and the two Move
buttons with `count` disabling Move-down on the last row. Nothing was missing.
The call-site props are §2.5.

---

## 2. `frontend/src/App.svelte` — paste-ready

Line numbers are from the file as of this writing and **will have drifted** —
three other implementers are editing it. Every anchor below is also quoted as
code; match on the text.

### 2.1 Nothing to import

`NetworkTable` is mounted through `{#await import(...)}` like `ConsoleTab`,
`PerformanceTab` and `TerminalTab`, so the DevTools chunk stays off the initial
bundle. No new top-level import.

### 2.2 The DevTools Network tab — replace the branch

Replace **everything from** `{:else if devToolsTab === 'network'}` (9073) **up to
but not including** `{:else if devToolsTab === 'performance'}` (9202) — 129 lines
— with:

```svelte
            {:else if devToolsTab === 'network'}
              {#await import('./lib/views/devtools/NetworkTable.svelte') then NetworkTable}
                {@const NetworkTableComponent = NetworkTable.default}
                <NetworkTableComponent
                  rows={rawDevToolsNetworkRows}
                  label="Network requests"
                  preferences={appState?.preferences?.devTools?.network}
                  onPreferencesChange={(next) => void updateDevToolsNetworkPreferences(next)}
                  detailsPanelWidth={devToolsDetailsPanelWidth}
                  onStartDetailsResize={startDevToolsDetailsPanelResize}
                />
              {/await}
```

And one line above it, in the panel header (9057):

```svelte
              <span>{devToolsNetworkRows.length} requests</span>
<!-- becomes -->
              <span>{rawDevToolsNetworkRows.length} requests</span>
```

That is a deliberate change of meaning: the count was of the *filtered* rows and
is now of the log. A panel header stating a total while a filter bar directly
below it states per-method counts is the readable arrangement; the old pairing
made the header change as you ticked boxes with nothing saying why.

### 2.3 The legacy Network Log view — replace the table

`{:else if activeView === 'network'}` (11256). Replace the whole `<section>`:

```svelte
      {:else if activeView === 'network'}
        <section class="panel">
          <header class="panel-header"><h2>Network Log</h2></header>
          {#await import('./lib/views/devtools/NetworkTable.svelte') then NetworkTable}
            {@const NetworkTableComponent = NetworkTable.default}
            <NetworkTableComponent
              rows={appState.networkLog ?? []}
              label="Network log"
              preferences={appState?.preferences?.devTools?.network}
              onPreferencesChange={(next) => void updateDevToolsNetworkPreferences(next)}
              detailsPanelWidth={devToolsDetailsPanelWidth}
              onStartDetailsResize={startDevToolsDetailsPanelResize}
            />
          {/await}
        </section>
```

The two mounts share the stored sort and column widths (one preference about one
table) and keep separate transient state — filter ticks, selected row, scroll.
That is the right split: which columns you sort by is a preference, which row you
are looking at right now is not.

### 2.4 What to delete from the `<script>`

All of this becomes unreachable. Grouped by where it sits today.

**State (1055–1070)** — the whole run of declarations:

```
devToolsNetworkFilters, devToolsNetworkSortKey, devToolsNetworkSortDirection,
selectedDevToolsNetworkLogID, devToolsNetworkDetailTab,
devToolsNetworkColumnWidths, devToolsNetworkResizingColumn,
devToolsNetworkPreferencesKey
```

Keep `devToolsDetailsPanelWidth` (1054) — §2.2 and §2.3 pass it.

**Constants (1113–1128)**: `devToolsNetworkMethods`, `devToolsNetworkColumns`,
`devToolsNetworkSortKeys`, `devToolsNetworkDetailTabs`.

**Local types (540–542)**: `DevToolsNetworkSortKey`,
`DevToolsNetworkSortDirection`, `DevToolsNetworkDetailTab` — all three become
unused. (`networkSort.ts` exports `NetworkSortKey` / `NetworkSortDirection` if
anything still wants them.)

**Virtualisation (1445–1470)**: `devToolsNetworkRowFallbackHeight`,
`devToolsNetworkScrollTop`, `devToolsNetworkViewportHeight`,
`devToolsNetworkMeasuredRowHeight`, `devToolsNetworkRowHeight`,
`measureDevToolsNetworkViewport`.

**Derivations and effects (1632–1676)**: the `$effect` that calls
`applyDevToolsNetworkPreferences`, `devToolsNetworkMethodCounts`,
`devToolsNetworkActiveFilterCount`, `devToolsNetworkRows`,
`devToolsNetworkWindow`, `devToolsNetworkVisibleRows`,
`devToolsNetworkSortLabels`, `devToolsNetworkAriaSort`,
`devToolsNetworkTableWidth`, `selectedDevToolsNetworkRow`, and the two `$effect`s
that keep `selectedDevToolsNetworkLogID` valid.

Keep `rawDevToolsNetworkRows` (1631) — §2.2 passes it.

**Functions (7474–7570, 7620–7640)**: `normalizedDevToolsNetworkSortKey`,
`normalizedDevToolsNetworkSortDirection`, `defaultDevToolsNetworkColumnWidths`,
`normalizedDevToolsNetworkColumnWidths`, `devToolsNetworkPreferencePayload`,
`devToolsNetworkPreferencesKeyFor`, `applyDevToolsNetworkPreferences`,
`filteredDevToolsNetworkRows`, `cycleDevToolsNetworkSort`,
`setDevToolsNetworkFilter`, `setAllDevToolsNetworkFilters`,
`startDevToolsNetworkColumnResize`, `selectDevToolsNetworkRow`,
`selectDevToolsNetworkDetailTab`.

Keep `startDevToolsDetailsPanelResize` and `startDevToolsDrawerResize`.

**Cell formatters (7835–7870)**: `networkLogTime`, `statusDisplay`,
`formatNetworkSize`, `networkHeaderRows`, `networkLogBody`, `networkLogLines` —
all six now live in `networkSort.ts`. `statusDisplay` and `formatNetworkSize`
have no other caller; `formatRuntimeBytes` stays, it is used by the Performance
tab and the cache section.

**Imports (41–56)**: everything from `./lib/networkSort` except
`DEFAULT_NETWORK_COLUMN_WIDTHS` (still needed by §2.4's rewrite below) becomes
unused — `networkSortAriaValue`, `networkSortLabel`, `nextNetworkSort`,
`networkDomain`, `networkLogTimestamp`, `networkPath`, `normalizedNetworkMethod`,
`sortNetworkRows`, `normalizedNetworkColumnWidths`,
`normalizedNetworkSortDirection`, `normalizedNetworkSortKey`,
`networkSortPreference`, `resizeAdjacentColumns`. `computeWindow` (21) goes too:
`devToolsNetworkWindow` was its only use in this file — the sidebar's
virtualisation imports `sidebarGroupWindow` instead.

**Two functions to rewrite rather than delete:**

```ts
  // Was: normalised a partial update against eight pieces of local state.
  // The component owns that state now and hands over a complete, already
  // normalised payload, so this is only the save.
  async function updateDevToolsNetworkPreferences(payload: types.DevToolsNetworkPreferences) {
    if (!appState) return
    await savePreferences({
      ...appState.preferences,
      devTools: {
        ...(appState.preferences.devTools ?? {}),
        network: payload
      }
    } as types.Preferences)
  }
```

and inside `updateDevToolsShellPreferences` (7062), the `network:` line:

```ts
      network: appState?.preferences?.devTools?.network ?? devToolsNetworkPreferencePayload(devToolsNetworkSortKey, devToolsNetworkSortDirection, devToolsNetworkColumnWidths)
// becomes
      network: appState?.preferences?.devTools?.network ?? { sortKey: '', sortDirection: '', columnWidths: [...DEFAULT_NETWORK_COLUMN_WIDTHS] }
```

Net: roughly 260 lines out of `App.svelte`.

### 2.5 A9-09 — turn row reordering on where it is used

Handlers, beside `removeKeyValue` (~6015). `movedRows` / `reorderedRows` are
already imported (148).

```ts
  function moveKeyValue(kind: 'params' | 'headers', index: number, direction: -1 | 1) {
    if (!activeRequest) return
    patchRequest({ [kind]: movedRows(activeRequest[kind], index, direction) } as unknown as types.RequestPatch)
  }

  function reorderKeyValue(kind: 'params' | 'headers', from: number, to: number) {
    if (!activeRequest) return
    patchRequest({ [kind]: reorderedRows(activeRequest[kind], from, to) } as unknown as types.RequestPatch)
  }

  function moveFormUrlEncodedRow(index: number, direction: -1 | 1) {
    if (!activeRequest) return
    updateBody({ formUrlEncoded: movedRows(activeRequest.body.formUrlEncoded, index, direction) } as Partial<types.RequestBody>)
  }

  function reorderFormUrlEncodedRow(from: number, to: number) {
    if (!activeRequest) return
    updateBody({ formUrlEncoded: reorderedRows(activeRequest.body.formUrlEncoded, from, to) } as Partial<types.RequestBody>)
  }

  function moveMultipartRow(index: number, direction: -1 | 1) {
    if (!activeRequest) return
    updateBody({ multipart: movedRows(activeRequest.body.multipart, index, direction) } as Partial<types.RequestBody>)
  }

  function reorderMultipartRow(from: number, to: number) {
    if (!activeRequest) return
    updateBody({ multipart: reorderedRows(activeRequest.body.multipart, from, to) } as Partial<types.RequestBody>)
  }

  // fileBodyUpdate, not updateBody: the file table also maintains the derived
  // filePath/fileContentType of whichever row is selected, and reordering must
  // not silently repoint them at a different file.
  function moveFileBodyRow(index: number, direction: -1 | 1) {
    if (!activeRequest) return
    fileBodyUpdate(movedRows(fileBodyRows(activeRequest.body), index, direction))
  }

  function reorderFileBodyRow(from: number, to: number) {
    if (!activeRequest) return
    fileBodyUpdate(reorderedRows(fileBodyRows(activeRequest.body), from, to))
  }
```

Then, per call site (identified by its `rows=` line, which does not drift):

| call site | add |
|---|---|
| `rows={activeRequest.params}` | `showMove={true} onMove={(i, d) => moveKeyValue('params', i, d)} onReorder={(f, t) => reorderKeyValue('params', f, t)}` |
| `rows={activeRequest.headers}` | `showMove={true} onMove={(i, d) => moveKeyValue('headers', i, d)} onReorder={(f, t) => reorderKeyValue('headers', f, t)}` |
| `rows={activeRequest.body.formUrlEncoded ?? []}` | `showMove={true} onMove={moveFormUrlEncodedRow} onReorder={reorderFormUrlEncodedRow}` |
| `rows={activeRequest.body.multipart ?? []}` | `showMove={true} onMove={moveMultipartRow} onReorder={reorderMultipartRow}` |
| `rows={fileBodyRows(activeRequest.body)}` | `showMove={true} onMove={moveFileBodyRow} onReorder={reorderFileBodyRow}` |

`rows={activeRequest.pathParams}` must **not** get it — its order is derived from
the URL. Request Vars, Folder Headers and Collection Headers can have it, but
each needs its own pair written against `patchRequest` / `saveFolderSettings` /
`UpdateCollectionHeaders`, so they are a second pass rather than a paste.

### 2.6 A9-10 — `showBulkEdit`

Add `showBulkEdit={true}` to:

- `rows={activeRequest.body.formUrlEncoded ?? []}`
- `rows={activeRequest.vars?.req ?? []...}` (see §2.7, same site)
- `rows={editableFolder.headers ?? []}`
- `rows={activeCollection.headers ?? []}`
- `rows={activeCollection?.headers ?? []}`

Do **not** add it to `rows={activeRequest.pathParams}` — the component now
refuses it there anyway (§1), so the existing `showBulkEdit={true}` on that site
is dead and should be deleted rather than left to read as intent.

`MultipartTable` and `FileBodyTable` get no bulk edit and should not: their rows
carry `filePath` and `contentType`, which the `name: value` text format cannot
express, so a round trip through it would silently drop them.

`bulkLabel` can be dropped from every site that also passes `label` (§2.9) —
it derives.

### 2.7 A1-08 — the Vars table

The site is `rows={(activeRequest.vars?.req ?? []).map((v) => ({ name: v.name,
value: String(v.value ?? ''), enabled: v.enabled, secret: v.secret,
description: v.dataType }))}`. It currently passes only `rows`, `onAdd`,
`onChange`, `onRemove`. Add:

```svelte
                  label="Request variables"
                  showDescription={true}
                  descriptionLabel="Type"
                  showBulkEdit={true}
                  variableOverlay={true}
                  {busy}
                  valueVariableSegments={(value) => urlVariableSegments(value, requestVariableTooltips)}
                  {displayTooltipValue}
                  onEditorKey={handleVariableTooltipEditorKey}
                  onEditorBlur={handleVariableTooltipEditorBlur}
                  onSave={saveVariableTooltipEdit}
                  onCopy={copyVariableTooltipValue}
                  onBulkChange={(rows) => replaceRequestVariables(rows)}
```

`replaceRequestVariables` does not exist yet — model it on `replaceKeyValues`
(6004), mapping each row back to a `types.Variable` and **preserving
`dataType`**: the bulk text format cannot express it, and `parseBulkText` already
carries `secret` and `description` over from the previous rows for exactly this
reason, so the mapping should read `dataType` from the old row at that index.
If that is more than you want to write today, ship this site without
`showBulkEdit`/`onBulkChange` — the other three props are the whole of A1-08's
"silently drops the variable overlay" complaint and are safe on their own.

### 2.8 A9-02, optional follow-up — delete the route

Once §2.3 has landed and the two surfaces are the same component, the legacy
route is a duplicate *entry point*, not a duplicate table, and can go:

1. `case 'open-network':` (6856) → `void openDevTools('network')`.
2. Delete the `{:else if activeView === 'network'}` branch.
3. Remove `'network'` from the `activeView` union and from any stored-view
   normaliser — **and make that normaliser map a stored `'network'` to
   `'devtools'`**, or a user whose last session ended on that view restores into
   an unhandled branch.

Owner: whoever owns navigation. Not required for A9-02 to be closed.

### 2.9 A9-14 — table names

`label="…"` on each `KeyValueTable` / `MultipartTable` / `FileBodyTable`, keyed
by `rows=`:

| `rows=` | `label=` |
|---|---|
| `activeRequest.params` | `Query params` |
| `activeRequest.pathParams` | `Path params` |
| `activeRequest.headers` | `Request headers` |
| `activeRequest.body.formUrlEncoded ?? []` | `Form fields` |
| `activeRequest.body.multipart ?? []` | `Multipart form parts` |
| `fileBodyRows(activeRequest.body)` | `Request body files` |
| `activeRequest.vars?.req ?? []…` | `Request variables` |
| `draft.request?.formUrlEncoded ?? []` | `Example request form fields` |
| `draft.request?.multipartForm ?? []` | `Example request form parts` |
| `draft.request?.file ?? []` | `Example request files` |
| `draft.request?.params ?? []` | `Example request params` |
| `draft.request?.headers ?? []` | `Example request headers` |
| `draft.response.headers ?? []` | `Example response headers` |
| `editableFolder.headers ?? []` | `Folder headers` |
| `activeCollection.headers ?? []` | `Collection headers` |
| `activeCollection?.headers ?? []` | `Collection headers` |

The hand-rolled `<table>`s need `aria-label` written directly:
`"gRPC messages"`, `"WebSocket messages"`, `"Assertions"`,
`"Folder pre-request variables"`, `"Folder post-response variables"`,
`"Collection variables"`, `"Client certificates"`, `"Proto files"`,
`"Proto import paths"`, `"Global environment variables"`,
`"Environment variables"`, `"Dot-env files"`, `"Dot-env variables"`,
`"Cookies"`.

---

## 3. `frontend/src/style.css` — paste-ready

### 3.1 Row feedback, so it lives in one place (A9-12)

The rule is currently three byte-identical scoped copies, one per primitive,
held together by a test. Moving it here retires the test and the copies:

```css
/* Row feedback on the editable tables.
   Hover marks the row under the pointer; :focus-within marks the row with the
   caret, which is what "selected" means in a table you type into. The tints are
   the DevTools network table's, so the two families read the same. Order
   matters: equal specificity, so a row that is both reads as focused. */
.kv-table tbody tr:hover td {
  background: color-mix(in srgb, var(--selected-bg) 55%, transparent);
}

.kv-table tbody tr:focus-within td {
  background: var(--selected-bg);
}
```

Then delete the `<style>` block from `MultipartTable.svelte` and
`FileBodyTable.svelte`, delete those two rules from `KeyValueTable.svelte`
(keeping `.kv-description`), and delete the two tests named
`every editable table gives a row the same hover and focus feedback` and
`row feedback is painted from tokens, never a literal colour` from
`tableRowActions.test.mts`.

### 3.2 `.devtools-network-table code` (~3077)

```css
.devtools-network-table code {
  color: var(--text);
  white-space: normal;
  word-break: break-word;
}
```

Still sets no font family — but a bare `code, kbd, samp` rule now exists at
`style.css:858` and covers it, so §3.1 of the previous handoff is **closed by
someone else's change** and needs no edit. Left here so nobody re-adds the
override. The same is true of `RequestDetailsPanel.svelte`'s scoped block, whose
four selectors are now redundant with the global rule; it is kept because
`tableRowActions.test.mts` asserts them and because the scoped copy also pins
`--code-font-size` against a future `.network-body` size override. Worth one
person's decision, not two.

### 3.3 `.kv-bulk-toggle` (2336–2350) is now dead

The Key/Value ⁄ Bulk toggle is a `SegmentedControl` and renders `.segmented`.
`.kv-bulk-toggle` and `.kv-bulk-toggle button[.active]` match nothing. Delete
all three rules. (`.kv-bulk-textarea` at 2352 is still live.)

### 3.4 A column for the Type cell — optional

`.kv-description` is styled in `KeyValueTable.svelte` and needs nothing here.
If the Vars table's Type column should be width-capped, that belongs beside the
existing `.oauth2-param-group .kv-table th:nth-child(n)` rules (3551).

---

## 4. A1-07 — where the raw enum values still are

**None in the files this change owns.** `PerformanceTab.svelte`'s two `<option>`s
already carry written labels; nothing else in the primitives or in
`views/devtools/` renders an enum as option text. The Body mode select the audit
named is already fixed — `bodyModeOptions` / `storedBodyModeOptions` in
`lib/workbench/bodyMode.ts` carry `label`s and both selects use them.

What remains, all in `App.svelte`, all sourced from the const list at 1156–1162:

| vocabulary | raw values shown | should read |
|---|---|---|
| `authModes` (×4 selects: request, folder, collection, environment) | `apikey`, `awsv4`, `oauth2`, `oauth1`, `ntlm`, `wsse`, `inherit` | API Key, AWS Signature v4, OAuth 2.0, OAuth 1.0, NTLM, WSSE, Inherit |
| `oauth2GrantTypes` (×4) | `client_credentials`, `authorization_code` | Client Credentials, Authorization Code |
| `oauth2CredentialPlacements` (×4) | `basic_auth_header` | Basic Auth Header |
| `oauth2TokenSources` (×4) | `access_token`, `id_token` | Access Token, ID Token |
| `oauth2TokenPlacements` (×4) | `header`, `url` | Header, URL |
| `oauth1Placements` (×3) | `header`, `query`, `body` | Header, Query, Body |
| `responseExampleBodyTypes` | `json`, `text`, `xml`, `html`, `binary` | JSON, Text, XML, HTML, Binary |
| gRPC method type | `{methodType \|\| 'unspecified'}` | Unary / Server streaming / … |

`oauth1SignatureMethods` is already written correctly (`HMAC-SHA1`).

**Deliberately not fixed here, and not with a shared label module either.** Seven
of those eight rows are auth selects, and `test/authFields.test.mts` now asserts
that "every auth surface renders the shared form rather than its own markup" —
the A5 implementer is replacing that markup with a shared auth form as this is
written. A label lookup added from here would be a second, competing definition
landing in a file that is being rewritten. The labels belong in whatever
vocabulary that shared form defines. `responseExampleBodyTypes` and the gRPC
method type are not auth and are free for whoever gets there first.

---

## 5. `lib/workbench/ResponseInspector.svelte` — still open (A9-05, A9-11)

Not this change's file, and §4 of `handoff-tables.md` still applies verbatim:
four tables with no `<thead>`, values not wrapped in `<code>`. One correction to
that handoff — it asks for a scoped `code { … }` rule at the end, and that is no
longer needed: `style.css:858` now defines `code, kbd, samp` globally. Add the
header rows and the `<code>` wrappers and nothing else.

A9-12 also touches this file: its tables are read-only key/value lists, so under
the rule adopted here they get **no** hover and no `:focus-within` — nothing in
them is focusable and nothing happens when a row is clicked. That is the rule
being applied, not an omission, and is worth a comment there so the next audit
does not re-file it.

---

## 6. What the audit got wrong, or missed

1. **A9-10 is not a style inconsistency.** "Bulk edit is wired to 2 of ~11 call
   sites" is true and the interesting half is the eleventh: Path Params has it
   *and* has `readonlyNames`, `showAddRow={false}` and `showActions={false}`. The
   bulk textarea is a whole-array rewrite, so that combination let a user rename
   and delete path parameters in a table built to forbid both. The audit called
   it "odd". It was a hole, and it is now closed in the component.

2. **A9-02's "same data" is stronger than the audit hedged.** The report says
   "determine whether… if it has no unique data". It does not:
   `rawDevToolsNetworkRows` and the legacy view's `{#each}` both read
   `appState.networkLog`, ten lines apart in derivation. There was nothing to
   determine.

3. **The column list and the default column widths were two lists.**
   `normalizedNetworkColumnWidths` guards a restored width array by comparing its
   length to `DEFAULT_NETWORK_COLUMN_WIDTHS`, which is the guard against a build
   that adds a column mis-assigning every width. But the column list lived in
   `App.svelte` and the widths in `networkSort.ts`, so adding a column in one
   file would have passed the guard and shifted the table. Now one file, one
   test.

4. **`RequestDetailsPanel` took four formatters as props**, which is a component
   asking its parent for `String(status)`. That is also the mechanism behind
   A9-02's fidelity gap: the legacy table sat in the same file as those
   functions and used none of them, because "pass the formatter down" is a habit
   that only reaches components, not sibling markup.

5. **An unlisted HTTP method is invisible in the network log.** The filter reads
   `filters[method] === true`, and the bar only has checkboxes for seven methods
   — so a `TRACE` or `CONNECT` row is filtered out with no control that can
   reveal it. Preserved (changing it is a product call) and now asserted in
   `networkSort.test.mts` so it is a documented behaviour rather than an
   accident.

6. **"No network requests" was shown for two different situations** — an empty
   log and a log entirely hidden by the method filter. Fixed here.

7. **A9-12's proposed rule does not survive contact with an editable table.**
   "Hover only where clicking a row does something" leaves eleven grids of
   near-identical inputs with no row feedback at all, and the error those grids
   invite is editing the wrong row. The rule adopted instead: *hover marks the
   row under the pointer everywhere; the "selected" tint marks whatever the
   table's notion of current row is — the selected row where rows are
   selectable, the row holding the caret where they are editable, and nothing at
   all where neither is true (read-only, non-interactive lists).*

8. **The audit's `DataTable` contract makes `label` required.** Right in
   principle, unbuildable in this wave: `App.svelte` owns the call sites and no
   implementer can edit it, so a required prop would be a type error nobody
   could fix. `label` is optional-with-a-default everywhere it was added, and
   `NetworkTable` defaults it rather than leaving it undefined so the failure
   mode is a generic name, never no name.

---

## 7. Verification

From `frontend/`, on the tree as this was written:

- `npm run check` — **287 files, 0 errors, 0 warnings** at the point all of this
  change's edits were in and no other agent had a broken save on disk. A later
  run showed 1 error in `App.svelte` (`Argument of type 'RequestItem' is not
  assignable to parameter of type 'ScannableRequest'`, ~1717) which is another
  implementer's in-flight edit — it is in code this change neither wrote nor
  reads, and it appeared between two runs with no edit from here in between.
- `npm run lint` — clean for every file this change owns. The same later run
  reported `'unresolvedHeaderVariables' is not defined` at `App.svelte:1710`,
  from the same other change.
- `npm test` — **1294 tests, 1293 pass, 1 fail.** The failure is
  `every section routes its settings through the shared row primitive`
  (`preferencesRows.test.mts`), the preferences implementer's. Every test this
  change owns passes: `networkSort.test.mts` **43/43**,
  `tableRowActions.test.mts` **21/21**, and `rowEdits`, `bulkEdit`,
  `devToolsConsole`, `virtualList`, `httpHeaders`, `designTokens` all green.

Re-run once the other branches settle.
