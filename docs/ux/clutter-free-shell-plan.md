# Clutter-free shell — implementation plan

**Spec:** [`clutter-free-shell.md`](clutter-free-shell.md). Read it first; every
task below cites a decision (D1–D9) from it.

**Goal:** remove every row of chrome the reference apps do without, without
removing any capability or keyboard path.

**Architecture:** four implementers work in parallel on disjoint files (W1–W4)
and return paste-ready handoffs for the two single-owner files; a fifth (W5)
owns `App.svelte` and `style.css`, applies its own tasks plus every handoff,
and takes the tree green. Validators then check the result against the spec
in code and in the running app.

**Already in the tree (done by the planner, do not redo):**
- `lib/ui/Icon.svelte` has `sidebar`, `command`, `save`, `bell`, `play`,
  `stop`, `restore`, `cookie`, `bookmark`.
- `lib/ui/PageHeader.svelte` exists: `{ title, subtitle?, meta?: Snippet,
  actions?: Snippet, testId? }`.

## Global constraints

- Svelte 5 runes only; callback props, not event dispatchers; snippets, not slots.
- Every CSS value is a token (`--space-*`, `--font-size-*`, `--radius-*`);
  `designTokens.test.mts` fails on an undefined token. Media queries only at
  1180 / 960 / 680 (`layout.test.mts`).
- Icon buttons are `lib/ui/IconButton.svelte`; icons are `lib/ui/Icon.svelte`.
  Add an icon there if one is missing; do not inline an SVG.
- Never remove a keyboard shortcut or a command-palette entry. Moving a button
  into a menu is allowed; deleting the action is not.
- ⌘K (search) and ⌘⇧P (commands) stay separate modals.
- Do not commit. The planner commits after review.
- **Ownership is strict.** Edit only the files your task lists. If you need a
  change in another file, write it into your handoff as a paste-ready diff
  with the exact anchor line to find, and say what breaks until it lands.
- Gates you can run from `frontend/`: `npm run check`, `npm run lint`,
  `npm test`, `npm run build`. Run the ones your files affect before returning.
  A red test you introduced on purpose (an invariant that another owner's
  file has to satisfy) must be named in the handoff.

---

## W1 — Sidebar header, search, actions, invariants (D1, D2, D9)

**Files (owned):** `lib/SidebarHeader.svelte`, `lib/SidebarSearch.svelte`,
`lib/sidebar/sidebarActions.ts`, `test/shellChrome.test.mts` (new),
`test/sidebarActions.test.mts`, `test/brandMark.test.mts`,
`test/emptyState.test.mts` (only if a path you changed is listed there).

**SidebarHeader.svelte** becomes one row:

```svelte
type Props = {
  onCommand: (id: WorkbenchCommandID, invoker: HTMLElement | null) => void | Promise<void>
  onToggleSearch: () => void
  searchOpen: boolean
}
```

- Left: `<BrandMark />` at 22px and `<h1>LiteAPI</h1>` at `--font-size-13`,
  weight 700. No `<p>` tagline.
- Right: `IconButton icon="search" label="Search requests (⌘F)"` with
  `pressed={searchOpen}`, and a `CommandOverflowMenu label="New" icon="add"
  align="left" items={newItems} onSelect={onCommand}`. `newItems` is the
  list currently built inline in `WorkspaceCommandBar.svelte` — W2 is moving it
  to `lib/workbench/workbenchCommands.ts` as `export function newItems(store):
  WorkbenchCommandItem[]`; import it from there. Until W2 lands, build the same
  list locally and note it in the handoff.
- Row height 36px; no `<small>` helper; no `<kbd>` (the tooltip says ⌘N/⌘F).
- The `rail-section rail-create` section and `.new-request-button` are gone.
  Handoff to W5: delete `.rail-create`, `.new-request-button` rules from
  `style.css`; pass the new props from `App.svelte` (`onCommand={runWorkbenchCommand}`,
  `bind:searchOpen={sidebarSearchOpen}`, `onToggleSearch`), and change the ⌘F
  handler so it sets `sidebarSearchOpen = true` before `await tick()` and
  focusing the input.

**SidebarSearch.svelte** gains `open: boolean` ($bindable) and renders
`FindBar` only when `open || value !== ''`. Remove the `<span class="field-label">Search</span>`.
Escape inside the bar with an empty value sets `open = false` and calls a new
`onClose?: () => void` prop (W5 wires it to focus the tree). FindBar already
handles Escape-clears-when-non-empty; add the second step here by handling
`keydown` on the host.

**sidebarActions.ts** adds `'run-collection'` to `SidebarActionID` and to the
collection's action list, label "Run collection", placed after `new-flow`.
Handoff to W5: `runSidebarMenuAction` handles `'run-collection'` by calling
the same function the top bar's `open-runner` command calls.

**test/shellChrome.test.mts** — write every invariant in the spec's table.
Read sources with `readFileSync` like `layout.test.mts` does. Strip HTML
comments before matching (`emptyState.test.mts` has `withoutComments`). Tests
for other owners' files will be red until W5 finishes — that is expected; list
each one in your handoff.

Update `brandMark.test.mts` only if it asserts markup you removed (it checks
`BrandMark` is rendered and `.brand-mark` CSS; keep both true).

**Return** (structured): `summary`, `handoffAppSvelte` (paste-ready, with anchor
lines), `handoffStyleCss`, `redTestsExpected` (names), `gatesRun`.

---

## W2 — Workspace command bar (D3, D9)

**Files (owned):** `lib/workbench/WorkspaceCommandBar.svelte`,
`lib/workbench/CommandOverflowMenu.svelte`, `lib/workbench/workbenchCommands.ts`,
`lib/workbench/EnvironmentContextMenu.svelte`, `test/layout.test.mts` (only the
owned-files list if a file name changes — it must not).

- Move `newItems` and `moreItems` builders out of the component into
  `workbenchCommands.ts` as exported functions taking the store-derived flags
  (`canCreateRequest`, `canCreateFolder`). `moreItems` gains `Cookie jar`
  (`open-cookies`) under Tools and `Collection runner` (`open-runner`) under
  Tools; keep every existing entry.
- Leading group: `IconButton icon="sidebar"` (toggle), workspace select/button,
  `EnvironmentContextMenu`. Remove the `+ New` menu and the Cookies button.
- Centre: the breadcrumb. The collection segment gets `<span class="git-dot">`
  when `gitConnected`, with the existing "Git connected" tooltip text on the
  button `title`. Remove the separate `git-status` button.
- Trailing group, in order: `IconButton icon="search" label="Search workspace (⌘K)"`
  → `onCommand('workspace-search')`; `IconButton icon="command"
  label="Command palette (⌘⇧P)"` → `command-palette`; notifications as
  `IconButton icon="bell"` with the existing `<strong>` badge kept (wrap the
  IconButton in a `span.notification-anchor` for the badge); the `recovery`
  snippet (unchanged, still only when entries exist); `CommandOverflowMenu
  label="Main menu" icon="more"`. Remove the orientation toggle and its import.
- Running state: when `runningCollectionName` is non-empty, render one
  `button.command-running` before the trailing icons: `Icon stop` + text
  `Running` / `Cancelling`, with the existing aria-labels, calling
  `cancel-run`. At rest nothing renders.
- All hover/focus styling stays token-based; `.command-icon` rules that only
  served removed buttons go.
- Breakpoints: keep at least one `@media (max-width: 1180px)`, `960px`,
  `680px` block (the layout test requires a query in this file). At 960 hide
  the breadcrumb; at 680 hide the workspace control.
- Tests: `commandState.test.mts` / `commandPalette.test.mts` do not read this
  file; run `npm test` anyway.

**Handoff to W5:** the `SidebarHeader` import of `newItems` (W1) and nothing
else if the prop surface is unchanged. If you change a prop name, list the
`App.svelte` mount change.

**Return:** `summary`, `handoffAppSvelte`, `handoffStyleCss`, `gatesRun`.

---

## W3 — Request command strip (D4, D9)

**Files (owned):** `lib/workbench/RequestCommandStrip.svelte`,
`lib/workbench/OrientationToggleButton.svelte`, `lib/workbench/commandState.ts`,
`lib/workbench/types.ts`, `test/commandState.test.mts`.

- The strip is a single row: the `requestLine` snippet (method + URL) fills,
  then `.request-command-actions`: `button.primary` **Send** (keep its `kbd`
  and cancel/`command-cancel` behaviour exactly), then `IconButton
  icon="save" label="Save (⌘S)"` with a `class:dirty` dot (`::after`, 6px,
  `--accent`) when `command.dirty`, then the `OrientationToggleButton`.
  Background-cancellation button stays as it is.
- Delete `.request-command-meta`, the context chips (`command-protocol`,
  `command-environment`, `command-dirty`, `command-saved`, `command-cue`) and
  the `command-scope-collection` button, its count, divider and their CSS.
  Remove the `runSelectionCount`, `runCollectionName` props and
  `actions.onRun` from `RequestCommandActions` in `types.ts` (W5 removes the
  mount arguments — list them).
- `commandState.ts`: `transportCues` yields `[]` for TLS on + system proxy;
  `'TLS off'` when verification is off; `Proxy: <mode>` only for a non-system
  proxy. Update `commandState.test.mts` accordingly (there is a test asserting
  the two-cue default; rewrite it to assert the empty default and the two
  non-default cases).
- Cues render nowhere in this file any more. Handoff to W5: render
  `requestCommand.transportCues` as `<span class="request-cues">` at the right
  end of the request `subtabs` row in `App.svelte` (line ~9335), muted,
  `--font-size-11`, only when non-empty.
- Keep one `@media (max-width: 680px)` block (layout test).

**Return:** `summary`, `handoffAppSvelte`, `handoffStyleCss`, `gatesRun`.

---

## W4 — Views adopt PageHeader (D7, D9)

**Files (owned):** `lib/views/RunnerPanel.svelte`, `lib/views/ImportPanel.svelte`,
`lib/views/preferences/PreferencesPanel.svelte`, `lib/views/flows/FlowTab.svelte`,
`lib/views/HistoryPanel.svelte`, and any test under `test/` that greps one of
those files (`runnerSelection`, `importPlanning`, `preferencesRows`,
`flowView` — check with `grep -l` first).

- Replace each `<header class="panel-header">…</header>` with
  `<PageHeader title=… >` using `{#snippet actions()}` for the buttons and
  `{#snippet meta()}` for live counts. Subtitles survive only if they carry
  information (Runner: none; Import: none — delete "Choose local files first…";
  Preferences: none; Flow: the collection name is fine).
- Import: remove the *Export active* button from the header (D7). Do not
  delete the export function if the file exports it elsewhere; only the header
  button goes.
- Runner: `Run N Requests` stays as the last action; `Deselect All` / `Reset`
  stay in the config panel where they are.
- Local `<style>` rules for the removed headers go.

**Return:** `summary`, `handoffAppSvelte` (empty unless a prop changed),
`handoffStyleCss` (rules under `.panel-header` that no owned file uses any
more — W5 decides), `gatesRun`.

---

## W5 — Shell integration: `App.svelte` and `style.css` (D2, D5, D6, D7, D8 + all handoffs)

**Files (owned):** `App.svelte`, `style.css`, `test/responseInspector.test.mts`,
`test/emptyState.test.mts`, `test/unresolvedVariables.test.mts` (it references
the request tab list), plus any test that reads `App.svelte` or `style.css`.

Runs after W1–W4 return. Apply every handoff first, then:

- **D2** Collection row: remove `<small>{collection.format}</small>`. In
  `style.css`, `.row-menu-button` rests at `opacity: .45` and is `1` on
  `article:hover`, `.row-cursor` siblings, `:focus-visible`, and while its
  menu is open (`aria-expanded="true"`). Hit target unchanged.
- **D5** Replace `.response-summary` with `<PaneToolbar ariaLabel="Response">`:
  `left` = the existing `div.subtabs[role=tablist]` unchanged; `middle` = the
  status cluster, rendered only when `activeRequest.response` (or the
  cancelled/failed states the summary shows today) exists — at rest render
  nothing; `right` = `IconButton icon="bookmark" label="Save response as example"`
  with the same `disabled` rule. Rename `{ id: 'response', label: 'Response' }`
  to `label: 'Body'` in both response tab lists (ids unchanged).
- **D6** Per-tab dirty: derive with the rule in `commandState.ts:107`
  (`transient || Boolean(request.draft)`) looked up per tab. Render
  `<span class="tab-dirty" aria-hidden="true"></span>` inside `.tab` when
  dirty; CSS shows the dot at rest and the `×` on `.tab:hover`, `.tab:focus-within`.
  Add "unsaved" to the close button's `aria-label` when dirty.
- **D7** Every `<header class="panel-header">` in `App.svelte` becomes
  `<PageHeader>`: Collection (title = name, subtitle = `${format} · N requests`,
  actions = Refresh active), Environments (title only — move the two
  create forms into the *Global Environment* and *Collection* cards below,
  each as one `div.split` at the top of its card), Git, Flow, Network Log,
  Cookies, Dev Tools (meta = the two counts; actions = Refresh, Close; no
  subtitle), Local Capabilities. Then delete `.panel-header` and
  `.panel-subtitle` from `style.css`.
- **D8** Remove `{ id: 'app', label: 'App' }`, the `'app'` union member and
  the `requestPaneTab === 'app'` branch.
- Take `npm run check && npm run lint && npm test && npm run build` green,
  including `shellChrome.test.mts`. If a W1–W4 invariant cannot be satisfied
  without editing their file, make the smallest edit and say so in your return.

**Return:** `summary`, `filesTouched`, `gatesRun` (exact commands and results),
`deviations` (anything done differently from the plan and why).

---

## Validation (Sonnet 5), after W5

**V1 — gates.** From repo root: `cd frontend && npm run check && npm run lint
&& npm test && npm run build`, then `go build ./... && go vet ./...`. Report
each command's exit and the failing lines verbatim.

**V2 — spec conformance in code.** For each of D1–D9, open the files the
decision names and answer *implemented / partial / missing* with the line
that proves it. Also list any capability that was deleted rather than moved
(grep for the old command ids: `open-cookies`, `open-runner`,
`change-orientation`, `cancel-run`, `workspace-search`, `command-palette` —
each must still be dispatched from somewhere).

**V3 — in the running app.** The Wails dev server is at
`http://localhost:34115` (load the browser tools with ToolSearch:
`select:mcp__Claude_Browser__navigate,mcp__Claude_Browser__computer,mcp__Claude_Browser__find,mcp__Claude_Browser__read_page,mcp__Claude_Browser__read_console_messages`).
Vite hot-reloads, so the page reflects the tree. Check, with a screenshot each:
sidebar header is one row and the find bar is hidden until the search icon
or ⌘F; top bar shows no text labels in the trailing cluster and a running
collection shows a cancel control; request strip is one row and Save shows a
dot after typing in the URL; response tab row carries the status after Send
and nothing at rest; every view (Runner, Environments, Import, Collection,
Preferences, Cookies, Network, Dev Tools) has the same header shape; the
console shows no new errors. Reset the sidebar search to empty when done.

Each validator returns `findings: [{ severity: critical|major|minor, decision,
file, line, what, evidence }]`.

## Fix loop

Findings of severity `critical` or `major` go to one Opus 5 fixer with full
ownership, then V1–V3 rerun. At most two rounds; leftovers are reported, not
hidden.
