# Handoff — A3, sidebar and search

Implementer X3. Owned files: `lib/modals/search/**`, `lib/sidebar/**`,
`lib/SidebarSearch.svelte`, `lib/SidebarHeader.svelte`, `lib/globalSearch.ts`,
`lib/commandPalette.ts`, `frontend/test/**`.

Most of the sidebar's markup is in `App.svelte`, which this wave's owner alone
may edit. Everything below is paste-ready against the tree as of this writing;
each hunk quotes enough surrounding text to be located after the file moves.

---

## What landed, in files I own

| Finding | Landed | Needs a patch below |
| --- | --- | --- |
| A3-02 Global Search has no listbox ARIA | `GlobalSearchModal.svelte` now carries the palette's wiring verbatim; both modals converted to runes and to one `optionId` helper | — |
| A3-09 five phrasings for "no results" | `lib/sidebar/emptyState.ts` defines the rule; both search modals use it | H5, H6 |
| A3-06 timeline blank on zero matches | The `{:else}` branch already exists (added by the ResponseInspector owner) — only the wording is off-rule | H6 |
| A3-10 three type-indicator conventions | `lib/sidebar/RowBadge.svelte` | H3 |
| A3-11 click/double-click undiscoverable | `lib/sidebar/rowHints.ts` | H4 |
| A3-03 zero-collections shows a failed-search string | `lib/sidebar/SidebarEmpty.svelte` | H1 |
| A3-05 flow rows excluded from the tree | `sidebarRows.ts` walks flows; `sidebarActions.ts` has a `flow` kind; navigation needed no change (pinned by test) | H2 |
| A3-04 remainder | `SidebarSearch.svelte` now renders `FindBar` | H7, H8 |

New files: `lib/sidebar/emptyState.ts`, `lib/sidebar/rowHints.ts`,
`lib/sidebar/RowBadge.svelte`, `lib/sidebar/SidebarEmpty.svelte`,
`test/emptyState.test.mts`.

---

## The rules I settled, so nobody has to re-decide them

### Empty-result wording

One function, `emptyStateMessage({ query, noun })` in
`frontend/src/lib/sidebar/emptyState.ts`:

* **a query matched nothing** → `No results for “{query}”`
  The query is quoted back because that is the one fact the user cannot verify
  from an empty list — the filter may be scrolled out of view or pre-filled by ⌘K.
* **there was never anything** → `No {noun} yet`
  "Yet" says the surface is empty by history, not by failure.

No trailing period on either. The query is trimmed, so a box holding spaces is
treated as cleared.

That is the **body** slot. The **counter** slot is `FindBar`'s and stays as it
is (`No {noun}` / `{n} of {m}` / `{n} {noun}`) — different slot, different job,
both now written down in exactly one place each.

The module lives under `lib/sidebar/` only because `lib/ui/` has another owner
this week. It imports nothing and knows nothing about the sidebar; when `lib/ui/`
is free, move the file to `lib/ui/emptyState.ts` and update the three imports.

### Row type indicators

Every **type or state** mark is a `RowBadge` (`status` / `state` / `glyph`).
The **method chip is not one**, deliberately: it renders a value rather than a
type, through `--method-color`, a channel each of the thirteen themes sets per
verb. Folding it in would mean re-encoding that palette inside a component whose
job is a word and a colour role.

### Row tooltips

`sidebarRowHint({ kind, label, detail })` in `lib/sidebar/rowHints.ts`:

* two behaviours on one row → explain the **behaviour**, in one sentence shape
  (`{name} — click for {pane}, double-click to expand`). Collections and folders.
* one behaviour → explain the **content** (a request's URL, a flow's description).
* nothing to add → **say nothing**, rather than repeating the visible label.

Note the folder row's shipped string said "double-click to open" while its own
chevron says "Expand". The rule uses `expand` on both rows.

---

## H1 — A3-03: the onboarding empty state

**Why**: `sidebarCollections` returns the collections untouched when the query is
empty, so `visibleSidebarCollections.length === 0` means *either* "your filter
excluded everything" *or* "you have never made a collection", and both rendered
`No matching requests`. A brand-new user's first screen reports a failed search
they never ran.

**1a. Import** — beside the other `lib/sidebar/` component imports:

```svelte
  import SidebarActionMenu from './lib/sidebar/SidebarActionMenu.svelte'
  import TreeChevron from './lib/sidebar/TreeChevron.svelte'
  import SidebarEmpty from './lib/sidebar/SidebarEmpty.svelte'
```

**1b. The tree's empty branch.** Find, inside the `role="tree"` div:

```svelte
        {#if visibleSidebarCollections.length === 0}
          <div class="sidebar-empty">No matching requests</div>
        {/if}
```

Replace with:

```svelte
        {#if visibleSidebarCollections.length === 0}
          <!-- Two causes, two states. `searchQuery` is what tells them apart:
               empty means the workspace has never held a collection, non-empty
               means a filter excluded everything. The single string that served
               both told new users they had failed a search they never ran. -->
          <SidebarEmpty query={searchQuery} />
        {/if}
```

**1c. The per-collection empty branch** (the other half of A3-09 inside the
tree). Find:

```svelte
            {:else if groups.length === 0 && !collectionCollapsed}
              <div class="sidebar-empty">No requests</div>
            {/if}
```

Replace with:

```svelte
            {:else if groups.length === 0 && !collectionCollapsed}
              <SidebarEmpty query={searchQuery} noun="requests" />
            {/if}
```

`SidebarEmpty` renders its hint line only in the never-had-any case, so a
filtered collection still shows one muted sentence exactly as before.

---

## H2 — A3-05: flow rows join the tree

Four hunks in `App.svelte` and one in `style.css`. `sidebarRows.ts` and
`sidebarActions.ts` are already done and are inert until 2a runs.

### 2a. Feed the walk

Find, in the `sidebarWalk` derivation:

```svelte
    examplesFor: (item) => ((item as types.RequestItem).examples ?? []).map((example) => ({
      id: responseExampleIdentifier(example),
      name: example.name
    }))
  }))
```

Replace with:

```svelte
    examplesFor: (item) => ((item as types.RequestItem).examples ?? []).map((example) => ({
      id: responseExampleIdentifier(example),
      name: example.name
    })),
    // The walk must agree with the DOM row for row, so this mirrors the markup
    // exactly: unfiltered by the search query, because the flow list below is
    // unfiltered too. The label is flowTabLabel's, not the raw name, so
    // type-ahead matches what is actually drawn.
    flowsFor: (id) => {
      const collection = visibleSidebarCollections.find((candidate) => candidate.id === id)
      return collection && collectionHasFlows(collection)
        ? collectionFlows(collection).map((flow) => ({ id: flow.id, name: flowTabLabel(flow) }))
        : []
    }
  }))
```

### 2b. Activation and actions

Find, in `activateSidebarRow`:

```svelte
    if (row.kind === 'request') { void openRequestTab(row.collectionId, row.itemId); return }
```

Replace with:

```svelte
    if (row.kind === 'request') { void openRequestTab(row.collectionId, row.itemId); return }
    // A flow row carries its flow id in itemId — the field means "the leaf this
    // row names", which is what lets sidebarObjectForRow stay one mapping.
    if (row.kind === 'flow') { openFlowTab(row.collectionId, row.itemId); return }
```

Find, in `runSidebarAction`, the line that bails out of everything that is not a
request:

```svelte
    if (row.kind !== 'request') return
    const item = sidebarItemFor(collection, row.itemId)
```

Insert the flow branch immediately **above** it:

```svelte
    if (row.kind === 'flow') {
      const flow = collectionFlows(collection).find((candidate) => candidate.id === row.itemId)
      if (!flow) return
      // A flow has no file of its own — it is stored in the collection's root
      // config — so "reveal" opens the directory that actually contains it.
      if (action === 'reveal') { void revealFolderInFolder(collection, ''); return }
      if (action === 'delete') { openDeleteFlowModalFor(collection.id, flow); return }
      return
    }

    if (row.kind !== 'request') return
    const item = sidebarItemFor(collection, row.itemId)
```

The registry offers a flow exactly `reveal` and `delete`, so no other `action`
value can arrive here. Rename is deliberately absent: there is no
`RenameFlowModal`, and a menu entry that opens nothing is the dead-promise
failure `sidebarActions.ts` already documents for `copyItem`/`pasteItem`. Adding
`'rename'` to the `flow` set in `sidebarActions.ts` and a branch here should
happen in the same change as the dialog, not before.

### 2c. Delete a flow that has no open tab

`openDeleteFlowModal(flow)` reads `activeFlowTab` for both ids, so it cannot
serve a sidebar row. Find:

```svelte
  /** Asks first. Deleting a flow rewrites the collection file and there is no
   *  recovery copy for flows the way there is for requests. */
  function openDeleteFlowModal(flow: types.Flow) {
    const tab = activeFlowTab
    if (!tab) return
    deleteFlowTarget = { collectionId: tab.collectionId, tabId: tab.id, flow }
  }
```

Replace with:

```svelte
  /** Asks first. Deleting a flow rewrites the collection file and there is no
   *  recovery copy for flows the way there is for requests. */
  function openDeleteFlowModal(flow: types.Flow) {
    const tab = activeFlowTab
    if (!tab) return
    openDeleteFlowModalFor(tab.collectionId, flow)
  }

  /**
   * The same dialog, reached from a sidebar row rather than from an open tab.
   *
   * The tab id is DERIVED rather than looked up, and that is what makes this
   * safe when the flow has never been opened: confirmDeleteFlow finishes by
   * calling closeFlowTab, which filters a list that does not contain the id and
   * returns early because it is not the active tab. So one path serves both
   * "delete the flow I am editing" and "delete a flow I have never opened",
   * instead of a second confirm flow that would have to be kept in step.
   */
  function openDeleteFlowModalFor(collectionId: string, flow: types.Flow) {
    deleteFlowTarget = { collectionId, tabId: flowTabID(collectionId, flow.id), flow }
  }
```

`flowTabID` is already imported from `./lib/flowView`.

### 2d. The row markup

Find the whole flow row block and replace it. **Old**:

```svelte
                  {#each collectionFlows(collection) as flow (flow.id)}
                    <!--
                      The keyboard cursor is deliberately NOT moved onto a flow
                      row. sidebarRows.walkSidebar does not emit these rows —
                      they are outside the virtualised walk — so a cursor
                      pointing at one would resolve to index -1, and the next
                      arrow key would jump to the top of the tree instead of
                      moving by one. Leaving the cursor where it was is the
                      honest behaviour until the walk learns about flows.
                    -->
                    <button
                      class="sidebar-flow-row"
                      class:item-active={activeFlowTab?.collectionId === collection.id && activeFlowTab?.flowId === flow.id}
                      type="button"
                      id={sidebarRowDomId(flowRowKey(collection.id, flow.id))}
                      data-testid="sidebar-flow-row"
                      title={flow.description || flowTabLabel(flow)}
                      onclick={() => openFlowTab(collection.id, flow.id)}
                    >
                      <span class="flow-glyph">Flow</span>
                      <span>{flowTabLabel(flow)}</span>
                      <small>{(flow.steps ?? []).length}</small>
                    </button>
                  {/each}
```

**New** (`data-testid="sidebar-flow-row"` and the DOM id are preserved exactly):

```svelte
                  {#each collectionFlows(collection) as flow (flow.id)}
                    <!--
                      walkSidebar now emits these rows, so the cursor can name
                      one and every tree guarantee reaches it: arrow keys, the
                      Menu key, right-click, and the same ⋯ menu the other three
                      row kinds open. The comment that used to sit here explained
                      why the cursor was withheld — it was withheld because the
                      walk was blind, and the walk is not blind any more.

                      A flow is a LEAF and carries no aria-expanded: its steps
                      are not sidebar rows, they live in the flow editor.
                    -->
                    <div class="flow-row-shell">
                      <button
                        class="sidebar-flow-row"
                        class:item-active={activeFlowTab?.collectionId === collection.id && activeFlowTab?.flowId === flow.id}
                        class:row-cursor={focusedSidebarRowKey === flowRowKey(collection.id, flow.id)}
                        type="button"
                        id={sidebarRowDomId(flowRowKey(collection.id, flow.id))}
                        role="treeitem"
                        aria-level={2}
                        aria-selected={focusedSidebarRowKey === flowRowKey(collection.id, flow.id)}
                        tabindex="-1"
                        data-testid="sidebar-flow-row"
                        title={sidebarRowHint({ kind: 'flow', label: flowTabLabel(flow), detail: flow.description })}
                        oncontextmenu={(event) => sidebarRowContextMenu(event, flowRowKey(collection.id, flow.id))}
                        onclick={() => { markSidebarRowFocused(flowRowKey(collection.id, flow.id)); openFlowTab(collection.id, flow.id) }}
                      >
                        <RowBadge tone="glyph" text="Flow" />
                        <span>{flowTabLabel(flow)}</span>
                        <small>{(flow.steps ?? []).length}</small>
                      </button>
                      <button
                        class="row-menu-button"
                        type="button"
                        data-testid="flow-actions-menu-toggle"
                        aria-haspopup="menu"
                        aria-expanded={sidebarMenuRowKey === flowRowKey(collection.id, flow.id)}
                        aria-label={`More actions for ${flowTabLabel(flow)}`}
                        title={`More actions for ${flowTabLabel(flow)}`}
                        onclick={(event) => { event.stopPropagation(); toggleSidebarRowMenu(flowRowKey(collection.id, flow.id)) }}
                      >⋯</button>
                      {#if sidebarMenuRowKey === flowRowKey(collection.id, flow.id)}
                        <SidebarActionMenu
                          actions={focusedSidebarActions}
                          label={flowTabLabel(flow)}
                          onrun={runSidebarMenuAction}
                          onclose={() => closeSidebarRowMenu()}
                        />
                      {/if}
                    </div>
                  {/each}
```

Two imports for this hunk (see also H3/H4):

```svelte
  import RowBadge from './lib/sidebar/RowBadge.svelte'
  import { sidebarRowHint } from './lib/sidebar/rowHints'
```

`flow.description` is optional on `types.Flow`; `sidebarRowHint` takes
`detail?: string` and trims, so `undefined` yields `''` and Svelte drops the
attribute — the old `|| flowTabLabel(flow)` fallback is gone on purpose, per the
tooltip rule.

### 2e. `style.css` — the shell (owner: style.css)

`SidebarActionMenu` is `position: absolute`, so it needs a positioned ancestor
or it anchors to `.collections` and lands in the wrong place. This is the flow
row's equivalent of `.request-row-shell`. Add beside it:

```css
/* A flow row's ⋯ menu needs something to anchor to, exactly as a request row's
   does. Same two declarations, and the same 26px gutter on the row itself so
   the toggle sits inside the row rather than over the step count. */
.flow-row-shell {
  position: relative;
  width: 100%;
}
.flow-row-shell .row-menu-button {
  position: absolute;
  top: 3px;
  right: 3px;
  z-index: 4;
}
```

and add `padding-right: 26px;` plus `user-select: none;` to the existing
`.collections article .sidebar-flow-row` rule (the selection rule already lists
`.collection-title`, `.folder-row`, `.request-row` and `.sidebar-example-row` —
add `.collections .sidebar-flow-row` to that list instead, if you prefer it in
one place).

---

## H3 — A3-10: the five call sites become `RowBadge`

`import RowBadge from './lib/sidebar/RowBadge.svelte'`.

| Old | New |
| --- | --- |
| `{#if collectionIsScratch(collection)}<small>Scratch</small>{/if}` | `{#if collectionIsScratch(collection)}<RowBadge tone="status" text="Scratch" />{/if}` |
| `{#if collection.remote}<small>Git</small>{/if}` | `{#if collection.remote}<RowBadge tone="status" text="Git" />{/if}` |
| `{#if collection.notFoundLocally}<small>Not cloned</small>{/if}` | `{#if collection.notFoundLocally}<RowBadge tone="status" text="Not cloned" />{/if}` |
| `<small>{collection.format}</small>` | `<RowBadge tone="status" text={collection.format} />` |
| `{#if requestIsTransient(collection, item)}<em>temp</em>{/if}` | `{#if requestIsTransient(collection, item)}<RowBadge tone="state" text="temp" />{/if}` |
| `{#if item.draft}<em>draft</em>{/if}` | `{#if item.draft}<RowBadge tone="state" text="draft" />{/if}` |
| `<span class="example-glyph">Ex</span>` | `<RowBadge tone="glyph" text="Ex" />` |
| `<span class="flow-glyph">Flow</span>` | `<RowBadge tone="glyph" text="Flow" />` (already in H2's hunk) |

`.row-badges:empty { display: none }` keeps working — the wrapper is still empty
when neither badge renders.

### style.css, after the call sites move (owner: style.css)

Three selectors currently exclude the glyph spans by class, and the glyph is now
`.row-badge`. Without this the badge picks up the row's ellipsis rule; at 10px
bold in a 32px track nothing actually clips, so this is tidying rather than a
bug, and it is safe to land separately.

```css
/* was: .sidebar-example-row span:not(.example-glyph) */
.sidebar-example-row span:not(.row-badge) { … }

/* was: .sidebar-flow-row span:not(.flow-glyph) */
.sidebar-flow-row span:not(.row-badge) { … }

/* was: .collections article .request-row span:not(.method):not(.row-badges) */
.collections article .request-row span:not(.method):not(.row-badges):not(.row-badge) { … }
```

Then `.example-glyph` and `.flow-glyph` — byte-for-byte identical rules, which is
what made this a finding — can both be deleted. `em { color: var(--warning); … }`
stays; it is a global rule with other users.

---

## H4 — A3-11: the collection row explains its gesture

`import { sidebarRowHint } from './lib/sidebar/rowHints'`.

**Collection row** — currently has no `title` at all. Add one, directly under
`aria-selected`:

```svelte
                id={sidebarRowDomId(`c:${collection.id}`)}
                role="treeitem"
                aria-level="1"
                aria-expanded={!collectionCollapsed}
                aria-selected={focusedSidebarRowKey === `c:${collection.id}`}
                title={sidebarRowHint({ kind: 'collection', label: collection.name })}
```

**Folder row** — replace the hardcoded string:

```svelte
                      title={`${group.folder} — click for settings, double-click to open`}
```

with:

```svelte
                      title={sidebarRowHint({ kind: 'folder', label: sidebarFolderLabel(group.folder) })}
```

Note this also changes the label from the full path to the row's own name, which
matches what the row draws, and "open" to "expand", which matches what the
chevron beside it already says.

**Request row** — behaviour unchanged, routed through the rule so all four rows
answer one function:

```svelte
                      title={group.folder ? `${group.folder} · ${item.url}` : item.url}
```

becomes:

```svelte
                      title={sidebarRowHint({ kind: 'request', label: item.name, detail: group.folder ? `${group.folder} · ${item.url}` : item.url })}
```

**Example row** — same treatment, and it now falls silent rather than repeating
its own visible name:

```svelte
                          title={example.description || example.request?.url || example.name}
```

becomes:

```svelte
                          title={sidebarRowHint({ kind: 'example', label: example.name, detail: example.description || example.request?.url })}
```

---

## H5 — A3-09 remainder inside `App.svelte`

Beyond H1's two, `App.svelte` owns three more filter-empty strings. All three
already branch on the right thing — they just each invented their own sentence,
and one of them invented a **seventh** phrasing the audit had not yet counted.
Add `import { emptyStateMessage } from './lib/sidebar/emptyState'` and replace:

```svelte
{global environment variables}
  {globalEnvironmentVariableQuery ? 'No results found' : `No ${globalEnvironmentVariableTab}`}
→ {emptyStateMessage({ query: globalEnvironmentVariableQuery, noun: globalEnvironmentVariableTab })}

{per-environment variables}
  {environmentVariableQuery ? 'No results found' : `No ${environmentVariableTab}`}
→ {emptyStateMessage({ query: environmentVariableQuery, noun: environmentVariableTab })}

{cookies}
  {:else if visibleCookieGroups.length === 0}
    <div class="empty-state">No matching cookies</div>
→ <div class="empty-state">{emptyStateMessage({ query: cookieSearch, noun: 'cookies' })}</div>
```

The two variable tables collapse from a ternary to one call, because the rule
already encodes the branch — and `No {tab}` gains the "yet" that says the table
is empty by history rather than by failure.

Leave the cookie pane's outer `No stored cookies` where it is *or* route it
through `emptyStateMessage({ query: '', noun: 'stored cookies' })`; it is a true
resting state and either reading is defensible. What must not survive is the
same surface saying "No stored cookies" above and "No matching cookies" below
with no shared shape between them.

---

## H6 — A3-06 and A3-09 in `ResponseInspector.svelte` (owner: response inspector)

The timeline's missing `{:else}` has since been added — thank you — so A3-06 is
closed as a *blank pane*. What is left is wording. Three strings there are still
off-rule, and all three already have the query in hand:

```svelte
{ResponseInspector.svelte, headers}
  'No headers match this search.'            → emptyStateMessage({ query: headerSearch, noun: 'headers' })
  'This response carried no headers.'          keep — that is a resting state, not a filter result
{ResponseInspector.svelte, timeline}
  'No timeline entries match this filter.'   → emptyStateMessage({ query: timelineSearch, noun: 'timeline entries' })
```

```ts
import { emptyStateMessage } from '../sidebar/emptyState'
```

The distinction worth keeping: `This response carried no headers.` describes the
**response**, not the filter, and is correct as prose. Only the strings that
answer "your filter matched nothing" go through the rule.

(When `lib/ui/` is free, the module moves to `lib/ui/emptyState.ts` and this
import becomes `'../ui/emptyState'`. One line, three files.)

---

## H7 — retire the `SidebarSearch` input bridge

`FindBar` deliberately keeps its `<input>` private and exposes `focus()`.
`App.svelte`'s ⌘F handler holds an `HTMLInputElement` and calls `.focus()` and
`.select()` on it, so `SidebarSearch` currently reaches through a wrapper div
with `querySelector('input')` to hand that element up. It works and is typed,
but it is a lie about the component boundary.

When `App.svelte` can take it, change:

```svelte
  let requestSearchInput = $state<HTMLInputElement | undefined>(undefined)
  …
  <SidebarSearch bind:value={requestSearch} bind:input={requestSearchInput} matchCount={sidebarSearchCount} />
  …
      case 'sidebarSearch':
        requestSearchInput?.focus()
        requestSearchInput?.select()
        return
```

to:

```svelte
  let sidebarFindBar = $state<{ focus: () => void } | undefined>(undefined)
  …
  <SidebarSearch bind:value={requestSearch} bind:this={sidebarFindBar} matchCount={sidebarSearchCount} />
  …
      case 'sidebarSearch':
        // FindBar.focus() focuses AND selects, which is the two lines this
        // replaces — and it is the same call every other pane's find bar makes.
        sidebarFindBar?.focus()
        return
```

and in `SidebarSearch.svelte`, drop the `input` prop, the `host` binding and the
`$effect`, adding instead:

```svelte
  let bar = $state<{ focus: () => void } | undefined>(undefined)
  export function focus() { bar?.focus() }
```

with `bind:this={bar}` on the `<FindBar>`. Check the other `requestSearchInput`
reference (the tree's Escape handler, `sidebarTreeKeydown`'s `exit` branch) at
the same time.

---

## H8 — optional: `FindBar` in the rail's palette (owner: style.css)

`FindBar` paints itself with the main-surface tokens (`--surface`, `--border`,
`--text`), and the sidebar rail runs its own (`--rail-card`,
`--rail-input-border`, `--rail-text`). The bar is legible as-is in all thirteen
themes — the rail's own `.rail-section input` rule is out-specified by FindBar's
scoped styles, which is the correct outcome for a shared primitive — but it does
read as a slightly lighter card than the box it replaced.

If you want the exact rail palette back, this is the whole change; it targets
FindBar's real class names, which survive Svelte's scoping:

```css
/* The one find bar, wearing the rail's colours. Scoped to the sidebar so every
   other pane keeps the primitive's own surface. */
.search-section .find-bar {
  background: var(--rail-card);
  border-color: var(--rail-input-border);
}
.search-section .find-bar input {
  color: var(--rail-text);
}
```

---

## Not done, and why

* **`RenameFlowModal`.** `sidebarActions.ts` offers a flow `reveal` and `delete`
  only. Rename needs a dialog, which lives in `lib/modals/collection/` — not my
  files. One line in `AVAILABLE.flow` and one branch in `runSidebarAction`
  (H2b) when it exists.
* **Flows are not filtered by the sidebar search.** They were not before either;
  the walk mirrors the markup on purpose, because a walk that disagrees with the
  DOM is the exact silent-drift failure `sidebarRows.ts` exists to prevent.
  Filtering flows is one change to `groupedItems`' sibling and to `flowsFor`
  together, in that order.
* **A3-07** was already closed: both search modals use `IconButton`, which makes
  `label` both the accessible name and the tooltip. The ~27 other dialogs are the
  modals owner's sweep.
* **⌘K / ⌘⇧P stay two modals.** Locked product decision; the ARIA is now
  identical between them but they remain separate surfaces with separate lists.

---

## Verification

From `frontend/`, at the time of writing (the tree is being edited concurrently
by five other implementers, so this is a snapshot):

* `npm run check` — **1 error, 1 warning**, both in
  `lib/views/preferences/CacheSection.svelte:63` (a local `state` binding
  colliding with the `$state` rune). Preferences owner's file. **0 problems in
  any file I own**, and 0 attributable to this work at any point: earlier runs
  during the wave showed transient errors in `lib/workbench/ResponseInspector.svelte`,
  `lib/VariableTextOverlay.svelte` and `lib/views/preferences/PreferencesPanel.svelte`,
  all in other owners' files and all since fixed by them.
* `npm test` — **1311 tests, 1310 pass, 1 fail**. The failure is
  `preferencesRows.test.mts`, "every section routes its settings through the
  shared row primitive" — the same owner, the same file, not mine.
  Running only the suites touching my area —
  `sidebarRows sidebarActions sidebarNavigation sidebarTree sidebarFilter
  globalSearch commandPalette emptyState designTokens` — gives **150 tests, 150
  pass, 0 fail**.
* `npm run lint` — **0 problems.**

The tree moved under me throughout (five other implementers), so these are a
snapshot; the constant across every run is that nothing in `lib/sidebar/`,
`lib/modals/search/`, `lib/SidebarSearch.svelte` or `lib/SidebarHeader.svelte`
ever reported a problem.

Tests added (extending, not duplicating): flow-row coverage in
`sidebarRows.test.mts`, `sidebarActions.test.mts` and
`sidebarNavigation.test.mts`; listbox-parity source assertions in
`globalSearch.test.mts`; the tooltip rule in `sidebarTree.test.mts`; and a new
`emptyState.test.mts` whose second half scans the converted files for a
hand-written empty-result sentence. That scan is scoped to
`lib/sidebar/`, `lib/modals/search/` and `lib/SidebarSearch.svelte` on purpose —
a guard that fails in somebody else's tree gets deleted rather than satisfied.
Widen it to `src/` once H5 and H6 land.
