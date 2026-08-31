# Handoff — tables, row actions and DevTools (W-C)

Covers `09-devtools-and-tables.md` and A1-05/A1-09 from `01-request-builder.md`.

Files this change owns and edited:

- `frontend/src/lib/RowActions.svelte` (new)
- `frontend/src/lib/KeyValueTable.svelte`
- `frontend/src/lib/MultipartTable.svelte`
- `frontend/src/lib/FileBodyTable.svelte`
- `frontend/src/lib/SuggestionListbox.svelte`
- `frontend/src/lib/views/devtools/RequestDetailsPanel.svelte`
- `frontend/src/lib/views/devtools/TerminalTab.svelte`
- `frontend/test/tableRowActions.test.mts` (new, 13 tests)

`App.svelte`, `style.css`, `ResponseInspector.svelte` and `ui/Icon.svelte` were not
touched. Everything that needs one of them is below, paste-ready.

---

## 1. What changed

### `RowActions.svelte` — the one row-action cell (A9-06, A9-07, A9-08, A1-05)

The fifteen tables that delete a row had two unrelated controls doing it: an
`.icon-button` holding the letter `x` in seven, a text `Remove` button in eight.
The move and drag affordances were the same story — `^`, `v` and `::` as literal
characters. Replacing the glyphs alone would have left three copies of the same
cell in three files, free to drift apart again, so there is now one component
and the three primitives render it:

```svelte
<RowActions {index} count={rows.length} {showMove} {onMove} {onRemove} />
```

- delete: `IconButton icon="trash" label="Remove row"`, `tone="danger"`
- move: `IconButton icon="chevron-up" / "chevron-down"`
- drag handle: a `<span aria-hidden="true">` with the three-bar `list` icon

`noun` (default `'row'`, `'file'` in `FileBodyTable`) changes the wording of the
labels and nothing about the control.

**Behaviour change to know about:** the drag handle was a `<button>` with an
`aria-label` and no click handler — a tab stop on every row that did nothing
when activated. The drag has always lived on the `<tr>` (`draggable` +
dragstart/dragover/drop), so dragging is byte-for-byte unchanged and one dead
tab stop per row is gone. Keyboard reordering is the Move buttons, which work.

### The three table primitives

- Row-action cell replaced with `<RowActions>` (above). No prop or callback
  signature changed; every `data-testid` is intact.
- New **optional** `label?: string` prop → `aria-label` on the `<table>` (A9-14).
  It is optional only because App.svelte owns the call sites; §2.6 has the values
  to pass. Rendering is unchanged until they are passed.
- `FileBodyTable`'s add button: `Add File` → `Add file` (A1-09). The noun still
  differs because the rows differ; the casing no longer does.
- All three add buttons gained `type="button"`.

### `SuggestionListbox.svelte` — tokens, and the removal of the fallback chains

`var(--surface-raised, var(--surface, #fff))` never fired (the token is
declared), but if it ever stopped being declared this popup would paint itself
white in all six dark themes; the shadow was a hardcoded black wash regardless
of theme, and `0.85rem` was the one font size in the component tree that scaled
off the root instead of the app's own scale. Now `--surface-raised`, `--border`,
`--radius-6`, `--shadow-soft`, `--font-size-12`, `--space-*`, no fallbacks.

### DevTools (A9-13, A9-11, A9-14)

The audit asked for the decision to be made and recorded. **Adopted: DevTools
looks like DevTools** — denser, more tabular, more monospaced than the request
editors, because it is a log inspector and its strings are compared character by
character. Recorded in the comment at the top of `RequestDetailsPanel.svelte`'s
`<style>` block.

What was *not* deliberate was that the panel disagreed with itself:

- **Three code typographies in one panel.** `.console-row code` sets
  `--code-font-family`; `.details-table code`, `.network-body`,
  `.progress-row code` and `.devtools-network-table code` set only colour and
  wrapping, so they fell through to the *browser's* default monospace at the
  app's *proportional* font size. Fixed for everything this panel renders via a
  scoped style. `.devtools-network-table` is in App.svelte/style.css — §3.1.
- **The request URL and method** were the only strings in the panel rendered in
  the UI font. Now `<code>`, like the header values three lines below (A9-11).
- **`aria-label`** added to the two `details-table`s (A9-14).
- **Three empty-state shapes across four files.** Rule adopted:
  `.empty-state.devtools-empty` (headline + detail) when a whole tab is empty,
  `.empty-state.compact` when a section inside one is. `TerminalTab` was the only
  file using the bare class — 24px of padding and a dashed border around a
  sentence in a 200px rail. Both of its empty states now follow the rule.
- **`class="icon-button subtle"`** in `TerminalTab`: `.subtle` matches no rule
  anywhere in `style.css` — a dead class name of the same species as
  `.empty-appState`. The `×` beside it was a text glyph next to SVG icons
  everywhere else. Now `IconButton icon="close" label="Close terminal session"`.

---

## 2. `frontend/src/App.svelte` — paste-ready

### 2.1 Import

After `import MultipartTable from './lib/MultipartTable.svelte'`:

```svelte
  import RowActions from './lib/RowActions.svelte'
  import IconButton from './lib/ui/IconButton.svelte'
```

### 2.2 The eight text `Remove` buttons (A9-06)

Each is a whole `<td>`. Replace the cell, not just the button — `RowActions`
renders its own flex row.

| line | current | replace with |
|---|---|---|
| 10512 | `<td><button onclick={() => removeFolderVariable('variables', index)}>Remove</button></td>` | `<td><RowActions {index} count={(editableFolder?.vars?.variables ?? []).length} noun="variable" onRemove={() => removeFolderVariable('variables', index)} /></td>` |
| 10544 | `<td><button onclick={() => removeFolderVariable('resVariables', index)}>Remove</button></td>` | `<td><RowActions {index} count={(editableFolder?.vars?.resVariables ?? []).length} noun="variable" onRemove={() => removeFolderVariable('resVariables', index)} /></td>` |
| 10996 | `<td><button onclick={() => removeCollectionClientCertificate(index)}>Remove</button></td>` | `<td><RowActions {index} count={(activeCollection?.clientCertificates ?? []).length} noun="certificate" onRemove={() => removeCollectionClientCertificate(index)} /></td>` |
| 11029 | `<td><button onclick={() => removeCollectionProtoFile(index)}>Remove</button></td>` | `<td><RowActions {index} count={(activeCollection?.protoFiles ?? []).length} noun="proto file" onRemove={() => removeCollectionProtoFile(index)} /></td>` |
| 11059 | `<td><button onclick={() => removeCollectionProtoImportPath(index)}>Remove</button></td>` | `<td><RowActions {index} count={(activeCollection?.protoImportPaths ?? []).length} noun="import path" onRemove={() => removeCollectionProtoImportPath(index)} /></td>` |
| 11331 | `<td><button onclick={() => removeGlobalEnvironmentVariable(row.index)}>Remove</button></td>` | `<td><RowActions index={row.index} count={globalEnvironmentVariableRows.length} noun="variable" onRemove={() => removeGlobalEnvironmentVariable(row.index)} /></td>` |
| 11392 | `<td><button onclick={() => removeEnvironmentVariable(row.index)}>Remove</button></td>` | `<td><RowActions index={row.index} count={environmentVariableRows.length} noun="variable" onRemove={() => removeEnvironmentVariable(row.index)} /></td>` |
| 11451 | `<td><button onclick={() => removeDotEnvRow(row)}>Remove</button></td>` | `<td><RowActions {index} count={dotEnvRows.length} noun="variable" onRemove={() => removeDotEnvRow(row)} /></td>` |

`count` is only used to disable Move down, and Move is off here (`showMove`
defaults false), so if a row-count expression is awkward at any of these sites,
`count={0}` is safe. Check the `{#each}` binding names at 11331/11392/11451
before pasting — those three iterate a filtered/derived row list, not the raw
array.

### 2.3 The `x` glyph sites (A9-06, A9-08)

```svelte
<!-- 9492, gRPC messages -->
<button class="icon-button" title="Remove message" onclick={() => removeGrpcMessage(index)}>x</button>
<!-- becomes -->
<IconButton icon="trash" label="Remove message" tone="danger" onclick={() => removeGrpcMessage(index)} />

<!-- 9539, WebSocket messages -->
<button class="icon-button" title="Remove message" onclick={() => removeWSMessage(index)}>x</button>
<!-- becomes -->
<IconButton icon="trash" label="Remove message" tone="danger" onclick={() => removeWSMessage(index)} />

<!-- 9856, assertions — note this one had NO title and NO aria-label at all -->
<td><button class="icon-button" onclick={() => removeAssertion(index)}>x</button></td>
<!-- becomes -->
<td><RowActions {index} count={(activeRequest.assertions ?? []).length} noun="assertion" onRemove={() => removeAssertion(index)} /></td>

<!-- 11833, cookies: Edit + delete in one cell -->
<button class="icon-button" title="Delete cookie" onclick={() => deleteCookie(cookie.id)}>x</button>
<!-- becomes -->
<IconButton icon="trash" label="Delete cookie" tone="danger" onclick={() => deleteCookie(cookie.id)} />
```

Two more `x` glyphs are search-clear buttons, not row deletes (11310, 11371):

```svelte
<button class="icon-button ghost" title="Clear global environment variable search" onclick={() => (globalEnvironmentVariableSearch = '')}>x</button>
<!-- becomes -->
<IconButton icon="close" label="Clear global environment variable search" onclick={() => (globalEnvironmentVariableSearch = '')} />
```

…and the same for `environmentVariableSearch` at 11371. `IconButton` has no
`ghost` variant, so if the rail-toned background at those two sites matters,
either add `variant?: 'ghost'` to `IconButton` or leave these two for the agent
who owns `ui/`. `SidebarSearch.svelte:30` is the third instance of this exact
button and is not in this change's ownership either. **`FindBar` already solves
all three** — it has a Clear button built in.

### 2.4 A9-07 — `icon-button` holding words

```svelte
<!-- 9489 -->  <button class="icon-button" title="Send message" ...>Send</button>
<!-- 9491 -->  <button class="icon-button" title="Generate sample" ...>Gen</button>
<!-- 9538 -->  <button class="icon-button" title="Send message" ...>Send</button>
```

Drop `class="icon-button"` from all three and let them size as ordinary text
buttons, matching "Start stream"/"End"/"Cancel" in the same panel. Spell "Gen"
out as "Generate" while there — it is the only abbreviated verb in the pane.
`.icon-button` is `width: 32px; text-align: center`, which is why these three
wrap inside their own box today.

### 2.5 A9-09 — turn on row reordering where it is actually used

The component side is complete and tested; each call site needs three props and
a pair of handlers. `movedRows`/`reorderedRows` are already imported at
`App.svelte:148`.

Handlers to add beside `removeKeyValue` (~line 5969):

```ts
  function moveKeyValue(kind: 'params' | 'headers', index: number, direction: -1 | 1) {
    if (!activeRequest) return
    patchRequest({ [kind]: movedRows(activeRequest[kind], index, direction) } as unknown as types.RequestPatch)
  }

  function reorderKeyValue(kind: 'params' | 'headers', from: number, to: number) {
    if (!activeRequest) return
    patchRequest({ [kind]: reorderedRows(activeRequest[kind], from, to) } as unknown as types.RequestPatch)
  }
```

Beside `removeFormUrlEncodedRow` / `removeMultipartRow` / `removeFileBodyRow`:

```ts
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

Then, at each call site:

| line | component | add |
|---|---|---|
| 9357 | Params | `showMove={true} onMove={(i, d) => moveKeyValue('params', i, d)} onReorder={(f, t) => reorderKeyValue('params', f, t)}` |
| 9397 | Headers | `showMove={true} onMove={(i, d) => moveKeyValue('headers', i, d)} onReorder={(f, t) => reorderKeyValue('headers', f, t)}` |
| 9583 | form-urlencoded | `showMove={true} onMove={moveFormUrlEncodedRow} onReorder={reorderFormUrlEncodedRow}` |
| 9599 | multipart | `showMove={true} onMove={moveMultipartRow} onReorder={reorderMultipartRow}` |
| 9613 | file body | `showMove={true} onMove={moveFileBodyRow} onReorder={reorderFileBodyRow}` |

Path Params (9376) must **not** get it — its order is derived from the URL.
Request Vars (9798), Folder Headers (10448) and Collection Headers (10605,
11446) can have it too, but each needs its own move/reorder pair written against
`patchRequest` / `saveFolderSettings` / `UpdateCollectionHeaders` respectively,
so they are worth a second pass rather than a paste.

### 2.6 A9-14 — table names

Add `label="…"` to each call site: `label="Query params"` (9357),
`label="Path params"` (9376), `label="Request headers"` (9397),
`label="Form fields"` (9583), `label="Multipart form parts"` (9599),
`label="Request body files"` (9613), `label="Request variables"` (9798),
`label="Folder headers"` (10448), `label="Collection headers"` (10605, 11446),
and the same for the response-example tables (9995–10081).

The hand-rolled `<table>`s need it written directly: `aria-label="gRPC messages"`
(9455), `"WebSocket messages"` (9497), `"Assertions"` (9845),
`"Client certificates"` (10970), `"Proto files"` (11010),
`"Proto import paths"` (11044), `"Cookies"` (11810), and each variables table.

### 2.7 A9-03 / A9-04 — the two Collection Variables tables

Not attempted here: both live wholly in App.svelte. A9-03 (Collection Settings ▸
Vars, ~10557 in the audit's numbering) has no action column at all; add a `<th></th>`
and `<td><RowActions {index} count={(activeCollection?.variables ?? []).length}
noun="variable" onRemove={() => removeCollectionVariable(index)} /></td>` with a
`removeCollectionVariable` mirroring `removeEnvironmentVariable`. A9-04 (the
duplicate in the Environments view) is better deleted than repaired — the
adjacent "Collection Headers" card already shows `KeyValueTable` works in that
exact panel.

### 2.8 A9-10 — `showBulkEdit`

`showBulkEdit={true}` on form-urlencoded (9583), Request Vars (9798), Folder
Headers (10448) and Collection Headers (10605, 11446). Nothing in
`rowsToBulkText`/`parseBulkText` cares where the rows came from.

---

## 3. `frontend/src/style.css` — paste-ready

### 3.1 The DevTools network table's code cells

`.devtools-network-table code` (~line 3010) sets colour and wrapping but no font
family, so it renders in the browser's default monospace at the body font size
while the console rows two tabs over use `--code-font-family`. Add to that rule:

```css
  font-family: var(--code-font-family);
  font-size: var(--code-font-size);
```

The equivalent for everything `RequestDetailsPanel` renders is already applied
as a scoped style in that component; if you would rather have it central, move
these four selectors into `style.css` and delete the component's `<style>` block:

```css
.details-table code,
.network-body,
.progress-row code,
.detail-list dd code {
  font-family: var(--code-font-family);
  font-size: var(--code-font-size);
}
```

### 3.2 `.icon-button` and `.subtle`

`.subtle` (used as `class="icon-button subtle"`) has no rule anywhere. Its only
use was in `TerminalTab` and is gone; nothing to delete, but it is worth a grep
before someone re-adds it believing it does something.

`.icon-button` itself (931-936: `width: 32px; min-width: 32px; padding: 0;
text-align: center`) should be retired once §2.3/§2.4 land — `IconButton` brings
its own 28px box and only reuses the class for the border/background it
inherits. Retiring it early will resize every remaining raw `.icon-button`.

### 3.3 Optional: hover on selectable rows only (A9-12)

The rule worth stating: row hover/selection styling exists only where clicking a
row does something. That is true of `.devtools-network-table` today and of
nothing else, which makes the current state correct-by-accident. If the legacy
Network Log view (A9-02) survives, it needs the same treatment.

---

## 4. `frontend/src/lib/workbench/ResponseInspector.svelte` — paste-ready (A9-05, A9-11)

Four tables, no `<thead>`, values not monospaced — the same data DevTools
renders with both. Line numbers are from the current file.

**Line 527 (Headers tab)** — replace the `<table>` element:

```svelte
<table aria-label="Response headers"><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>{#each filteredHeaders as [name, value] (name)}<tr><td>{name}</td><td><code>{value}</code></td></tr>{/each}</tbody></table>
```

**Line 531 (Metadata / Trailers tab)**:

```svelte
<table aria-label={selectedTab}><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>{#each rows as row, index (index)}<tr><td>{row.name}</td><td><code>{row.value}</code></td></tr>{/each}</tbody></table>
```

**Line 537 (nested gRPC metadata and trailers inside Timeline)** — both tables,
keeping their `data-testid`s exactly:

```svelte
<table data-testid="timeline-grpc-metadata" aria-label="gRPC metadata"><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>...<tr><td>{row.name}</td><td><code>{row.value}</code></td></tr>...</tbody></table>
<table data-testid="timeline-grpc-trailers" aria-label="gRPC trailers"><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>...<tr><td>{row.name}</td><td><code>{row.value}</code></td></tr>...</tbody></table>
```

**Line 516 (Compare ▸ header diff)** — wrap `{row.current}` and `{row.selected}`
in `<code>`; the header row is already there.

**And add a scoped style**, or none of those `<code>` elements will use the app's
code font — there is no global `code` rule in `style.css`, so `<code>` currently
falls through to the browser's default monospace at the app's body size:

```svelte
<style>
  /* … existing rules … */
  code { font-family: var(--code-font-family); font-size: var(--code-font-size); }
</style>
```

---

## 5. `frontend/src/lib/ui/Icon.svelte` — optional (owner: whoever owns `ui/`)

`RowActions` draws the drag handle with the three-bar `list` icon because the set
has no grip. The conventional drawing is a two-column dot grid; if you want it,
add `'grip'` to `IconName` and `iconNames`, then between `plus` and `trash`:

```svelte
  {:else if name === 'grip'}
    <circle cx="7.6" cy="5.4" r="1.15" /><circle cx="12.4" cy="5.4" r="1.15" />
    <circle cx="7.6" cy="10" r="1.15" /><circle cx="12.4" cy="10" r="1.15" />
    <circle cx="7.6" cy="14.6" r="1.15" /><circle cx="12.4" cy="14.6" r="1.15" />
```

It picks up the existing `.ui-icon:has(circle + circle)` fill rule the `more`
icon already relies on. Then change the one line in `RowActions.svelte` from
`name="list"` to `name="grip"`.

---

## 6. What the audit missed

1. **`<code>` in this app is not the app's code font.** There is no bare
   `code { … }` rule in `style.css`, so every `<code>` that is not covered by a
   specific selector renders in the *browser's* default monospace at the app's
   *proportional* font size. A9-11 treats "wrapped in `<code>`" as equivalent to
   "monospaced with `--code-font-family`", and it is not: `.console-row code` is
   the only DevTools rule that sets the family. `.details-table code`,
   `.network-body`, `.devtools-network-table code`, `.progress-row code`,
   `.timeline-detail code`, `.ws-event-row code` and `.ghost-row code` all set
   colour/wrapping only. So DevTools already showed two monospace faces at two
   sizes before any of this. §3.1 and §4 are the remaining half of the fix.
2. **`.subtle` is a second dead class name** (`TerminalTab.svelte:48`), the same
   species as `.empty-appState` and missed because it was hiding behind a class
   that *does* exist. Worth a sweep for others: the class-vs-stylesheet check is
   now in `tableRowActions.test.mts` for the DevTools directory only.
3. **The drag handle was a dead control**, not just a bad glyph. A focusable
   `<button>` with an `aria-label` and no activation behaviour, once per row, in
   front of the two Move buttons — so keyboard users tabbed through a control
   that announced "Drag row to reorder" and did nothing.
4. **The assertions table's delete button has no accessible name at all**
   (`App.svelte:9856` — no `title`, no `aria-label`, content `x`). A9-06 lists it
   as a glyph inconsistency; it is also the one delete control in the app that a
   screen reader announces as just "x".
5. **`.empty-state` with no modifier is itself a bug in a narrow container.**
   A9-01 fixed the *name*; the Terminal tab showed that the base class (24px
   padding, dashed border) is wrong inside a 200px rail, so the modifier is part
   of the convention, not decoration.
6. **A1-08's dead `dataType` plumbing is still dead.** `App.svelte:9798` computes
   `description: v.dataType` per row and `KeyValueTable` has no rendering path
   for `description` anywhere. Not fixed here: adding a Type column changes the
   column count for all eleven call sites, which needs the App.svelte side in the
   same commit. Either render it or stop computing it.
7. **There is no truly read-only `KeyValueTable` call site.** The conformance
   table lists Path Params as read-only, but that site passes `readonlyNames`,
   not `readonly` — the values are editable. So the `readonly` branch of
   `KeyValueTable` (and any monospace-when-read-only styling) is currently
   unreachable, which is worth knowing before building the `DataTable` contract's
   `readonly` mode on the assumption it is exercised today.

---

## 7. Verification

From `frontend/`, after these changes:

- `npm run check` — 271 files, **0 errors, 0 warnings**
- `npm run lint` — clean
- `npm test` — `tableRowActions.test.mts`: **13 pass, 0 fail**. The suite total
  moves while three implementers are editing concurrently; the failures seen
  during this work were all in other agents' in-flight files
  (`flowView.test.mts`, `mcpSection.test.mts`, and the deliberately-named `BUG:`
  assertions in `bodyMode.test.mts` / `bodyHighlight.test.mts`), never in a file
  or test this change owns. Re-run once the other branches settle.
