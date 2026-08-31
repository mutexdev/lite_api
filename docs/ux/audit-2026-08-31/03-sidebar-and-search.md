# A3 — Sidebar, tree, and app-wide search

## Summary

- The sidebar tree itself (collection/folder/request/example rows, chevron, `⋯` menu) has already had a real unification pass — `sidebarActions.ts` and `sidebarRows.ts` deliberately collapsed two legacy button rows into one registry, and the chevron/menu-button hit targets are both a tokenized 22×22px. That work is solid; do not re-litigate it.
- **Flow rows are the one row type that pattern never reached.** They get no chevron-equivalent, no `⋯` action menu, no `role="treeitem"`/`aria-level`/`aria-selected`, no right-click handler, and are explicitly excluded from keyboard navigation (`App.svelte:8762-8768`). They look and behave like a leftover.
- **There are at least nine independent "type a thing, filter/highlight results" implementations** in this app, and no two share markup, wording, or interaction model: sidebar filter, ⌘K, ⌘⇧P, the CodeMirror editor panel, response body, response headers, timeline, environment variables, cookies, and history. See the table below.
- The single most jarring one: the CodeMirror **editor search panel** is completely unstyled (only `.cm-searchMatch` highlight colors are themed) and **collides with the global ⌘F binding** — pressing ⌘F while typing in any request/response/script editor likely fires both the app's "focus the sidebar filter" handler and CodeMirror's own default "open search panel" command, because the global keydown listener has no check for whether focus is inside an editable field.
- The two flagship modals disagree on basic accessibility: `CommandPaletteModal.svelte` implements `role="listbox"`/`role="option"`/`aria-activedescendant` correctly; `GlobalSearchModal.svelte` — the more heavily used ⌘K surface — implements none of it.
- The sidebar's own true-empty state is a copy bug, not just a style inconsistency: a brand-new workspace with **zero collections** renders the exact same string ("No matching requests") as a search that matched nothing. There is no onboarding/empty-workspace state at all.
- In-pane find bars use "Search"/"Filter" placeholders inconsistently, four different "no results" phrasings, hardcoded pixel values instead of the design tokens the sidebar itself uses, and none of them support Escape to clear or exit (every modal does, via `Modal.svelte`).
- Close/clear affordances are inconsistent across ~29 dialogs: 27 use a literal `x` character with only a `title` (no `aria-label`), 2 use `×`. `GlobalSearchModal.svelte:37` is one of the plain-`x`, no-`aria-label` instances; `CommandPaletteModal.svelte:36` got it right.
- Row "type" indicators (badges/glyphs) use three unrelated conventions with no shared component: `<small>` pills for collections, `<em>` tags for request badges, and hand-rolled abbreviation spans (`Ex`, `Flow`) for examples and flows.

## The five search UIs

(nine counted; the prompt's five are marked ★)

| Surface | File:line | Presentation | Shortcut | Placeholder | Match counter | Prev/Next | Verdict |
|---|---|---|---|---|---|---|---|
| ★ Sidebar filter | `lib/SidebarSearch.svelte:28-35` | Inline box in rail, custom ghost `x` clear button | ⌘F (global, unscoped) | "Find requests" | "N matching requests" (only when non-blank) | none (arrow keys navigate the filtered tree) | Baseline pattern — tokenized, has a live region-ish count, but "Find" vs every other surface's "Search" wording |
| ★ Global Search (⌘K) | `lib/modals/search/GlobalSearchModal.svelte:32-69` | Modal | ⌘K | "Search collections and requests" | none (relies on scanning the list) | ↑/↓ in `App.svelte:8308-8321`, no visible affordance | No listbox/option ARIA at all — plain buttons + CSS `.active` class |
| Command Palette (⌘⇧P) | `lib/modals/search/CommandPaletteModal.svelte:33-49` | Modal (shares `.global-search-modal` class) | ⌘⇧P | "Type a command" | none | ↑/↓, proper `role="listbox"`/`role="option"`/`aria-activedescendant` | The one surface with correct ARIA — should be the template |
| ★ CodeMirror editor panel | `lib/workbench/CodeEditor.svelte:355` (button), `:12` (import) | Native CodeMirror floating panel, **zero app theming** beyond match-highlight colors | Toolbar "Search" button, **and** default Mod-f from `basicSetup`'s `searchKeymap` (confirmed in `node_modules/codemirror/dist/index.js:6,70`) — collides with the global ⌘F sidebar binding | CodeMirror's own ("Find") | CodeMirror's own | CodeMirror's own prev/next/all/replace buttons | Looks like a different application; also a live shortcut collision |
| ★ Response body | `lib/workbench/ResponseInspector.svelte:375-379` | Inline bar, hardcoded px CSS (`:471-472`) | none (must click in) | "Search response" | "No matches" / "N of M" | Explicit "Previous"/"Next" text buttons + Enter/Shift+Enter (`:236-240`) | Closest to a "real" find bar; not reachable by keyboard shortcut |
| ★ Response headers | `lib/workbench/ResponseInspector.svelte:428` | Inline bar, same toolbar row as a "Copy headers" action button | none | "Search headers" | "No matching headers" / "N of M" | none — pure filter, no highlight/jump | Different counter wording and no nav despite sitting one tab away from the body search |
| ★ Timeline | `lib/workbench/ResponseInspector.svelte:435` | Inline bar combined with a phase `<select>` and Copy/Export buttons | none | "Search phase, kind, source, payload, metadata…" | "N of M" only (no "0 results" message — see A3-06) | none | Longest, most technical placeholder in the app; silently blank on zero matches |
| Environment variables (global + per-env) | `App.svelte:11216-11220`, `:11277-11281` | Inline `.search-box` (reuses sidebar's class + ghost `x` button) | none | "Search variables" | none (table just filters) | none | Closest visual cousin of the sidebar filter — a good pattern to standardize on |
| Cookies | `App.svelte:11653` | Plain input inside `.runner-summary`, no clear button | none | "Search cookies" | none | none | Third distinct container class for the same concept |
| History | `lib/views/HistoryPanel.svelte:47-54` | Native `type="search"` input (browser-native clear affordance) | none | **"Filter history"** (only surface using "Filter" instead of "Search") | none | none | Wording and control type both diverge |

## Findings

### A3-01 — The CodeMirror editor search panel is a foreign UI, and it fights the global ⌘F binding
- **Severity**: critical
- **Where**: `frontend/src/lib/workbench/CodeEditor.svelte:12,355`; `frontend/src/lib/shortcuts.ts:68-147`; `frontend/src/App.svelte:8452-8490,8520`; `frontend/src/lib/keybindings.ts:60`
- **What the user sees**: Every text/JSON/GraphQL editor in the app (request body, pre-request scripts, etc.) ships CodeMirror's stock `basicSetup`, which bundles `searchKeymap` and binds `Mod-f` to `openSearchPanel` (verified in `node_modules/codemirror/dist/index.js:6,70`). Meanwhile `keybindings.ts:60` binds the *same* combo, `command+f`/`ctrl+f`, to `sidebarSearch`, dispatched by a `window`-level `onkeydown` in `App.svelte:8520` with no check for whether focus is inside an editable element (`shortcuts.ts:115-147` has no such guard). Pressing ⌘F while typing in a body editor is very likely to both pop CodeMirror's own unthemed find bar *and* yank focus out to the sidebar filter box, because CodeMirror's keydown handler calls `preventDefault()` on the key but does not `stopPropagation()`, so the event still bubbles to the window listener.
- **Why it's wrong**: This is the literal embodiment of "looks like a different application in each section" — the CodeMirror panel uses none of the app's tokens, fonts, or button styling (only `.cm-searchMatch`/`.cm-searchMatch-selected` are themed, `CodeEditor.svelte:156-157`), and it directly contradicts the shortcut Preferences advertises as "Search Sidebar."
- **Proposed fix**: Either (a) disable `searchKeymap` in the editor's extensions and route the toolbar "Search" button (and Mod-f while the editor has focus) into the same uniform find-bar component every other pane uses, styled with app tokens and anchored inside `.code-editor-toolbar`; or (b) scope the global `sidebarSearch` shortcut so it does not fire while focus is inside a CodeMirror `contentDOM`/any text input, and accept CodeMirror's own panel only for that one surface — but then it still needs the token restyle, since it's shown to the user today with zero relationship to the app's design system.
- **Shared primitive it should use**: the uniform find-bar spec below, or at minimum a themed override of `.cm-panel.cm-search`.

### A3-02 — Global Search (⌘K) has no listbox semantics; Command Palette (⌘⇧P) does
- **Severity**: major
- **Where**: `frontend/src/lib/modals/search/GlobalSearchModal.svelte:39-68` vs `frontend/src/lib/modals/search/CommandPaletteModal.svelte:38-48`
- **What the user sees**: In the Command Palette, the input carries `aria-controls="command-palette-commands"` and `aria-activedescendant`, the results container is `role="listbox"`, and each row is `role="option"` with `aria-selected` (`CommandPaletteModal.svelte:38-41`). In Global Search, the input (`:39-47`) has none of that, the results wrapper (`:51`) has no role, and each result button (`:52-65`) is only marked "active" via a CSS class. A screen reader gets a proper single-select listbox experience in one ⌘-modal and a bag of unrelated buttons in the other.
- **Why it's wrong**: These two modals are explicitly designed and documented (`commandPalette.ts:1-8`) as a matched pair — "Cmd+K search... add Cmd+Shift+P for commands" — so a user (or a screen reader) bouncing between them should feel one interaction model, not two.
- **Proposed fix**: Port `CommandPaletteModal`'s `role="listbox"`/`role="option"`/`aria-controls`/`aria-activedescendant` wiring onto `GlobalSearchModal` verbatim; the two already share the `.global-search-modal` CSS class family, so no visual change is needed, only the ARIA plumbing.
- **Shared primitive it should use**: a small shared `<SearchResultsList>`/listbox pattern the two modals both import instead of hand-rolling twice.

### A3-03 — A workspace with zero collections shows "No matching requests," the same copy as a failed search
- **Severity**: major
- **Where**: `frontend/src/App.svelte:8227-8231` (`sidebarCollections`), `:8548-8549`
- **What the user sees**: `sidebarCollections(workspace, query)` returns `workspace?.collections ?? []` untouched whenever `query` is empty (`:8229`). So `visibleSidebarCollections.length === 0` is true both when the user has typed a filter that matched nothing *and* when they have never created a collection at all — and in both cases the sidebar renders the identical `<div class="sidebar-empty">No matching requests</div>` (`:8549`). There is no dedicated "create your first collection" / onboarding state anywhere in the file (confirmed by search — no "Get started"/"onboarding" string exists).
- **Why it's wrong**: "No matching requests" presupposes a search happened. A brand-new user who has done nothing sees language implying they searched and failed, which is actively confusing at the exact moment the app should be welcoming them.
- **Proposed fix**: Branch the empty state on `searchQuery` truthiness: when there's no query and no collections, show a distinct first-run message ("No collections yet — import one or create a request to get started") with the existing "+ New" affordance; keep "No matching requests" only for the filtered-to-zero case.
- **Shared primitive it should use**: the same `.sidebar-empty` treatment, parameterized by cause, and reused as the shared "empty vs filtered-empty" pattern the uniform find-bar spec below asks every surface to adopt.

### A3-04 — In-pane find bars share no markup, tokens, or keyboard model
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:375-379,428,435,471-472`; `frontend/src/lib/views/HistoryPanel.svelte:47-54`; `frontend/src/App.svelte:11216-11220,11277-11281,11653`
- **What the user sees**: Six different find/filter inputs (response body, response headers, timeline, history, cookies, environment variables) each reimplement "type to filter" with different wording ("Search response" / "Search headers" / "Search phase, kind, source, payload, metadata…" / "Filter history" / "Search cookies" / "Search variables"), different result-count phrasing ("No matches" vs "No matching headers" vs bare "N of M" vs nothing), different clear affordances (custom ghost `x` button on env vars only; native browser clear on history's `type="search"`; no clear button at all on body/headers/timeline/cookies), and CSS that mixes tokens (`.env-search` uses `var(--space-8)`) with hardcoded pixels (`.response-search`/`.timeline-tools` at `ResponseInspector.svelte:471-472` use `gap:7px; padding:8px 10px; font-size:11px`, ignoring the `--space-*`/`--font-size-11` tokens the sidebar itself uses).
- **Why it's wrong**: This is the concrete, enumerable version of "the app looks like a different application in each section" — a user who learns how search works in one pane (Previous/Next buttons, a match counter) gets no transfer to the next pane over (a plain filter with different words for the same "nothing found" state).
- **Proposed fix**: Extract one `FindBar` component (icon, input, live counter, Previous/Next when applicable, clear button) per the spec at the end of this document, and have all six call sites render it instead of hand-rolled markup.
- **Shared primitive it should use**: the uniform find-bar spec below.

### A3-05 — Flow rows are excluded from every sidebar consistency guarantee the tree just built
- **Severity**: major
- **Where**: `frontend/src/App.svelte:8757-8785`
- **What the user sees**: Collection, folder, and request rows all get a `TreeChevron` (where applicable), a `role="treeitem"`, `aria-level`, `aria-selected`, a right-click handler wired to `sidebarRowContextMenu`, and a `⋯` button opening the shared `SidebarActionMenu`. The flow row (`:8770-8782`) is a bare `<button class="sidebar-flow-row">` with none of that: no `role`, no `aria-level`, no `aria-selected`, no `oncontextmenu`, no `⋯` menu, and the code comment at `:8762-8768` explains it is *deliberately* excluded from `sidebarRows.walkSidebar` and therefore from all keyboard navigation ("the keyboard cursor is deliberately NOT moved onto a flow row").
- **Why it's wrong**: A flow is a first-class object one click away from every other row type, but it cannot be renamed, deleted, cloned, or right-clicked from the sidebar, is invisible to a screen reader's tree navigation, and cannot be reached by arrow keys at all — it is the one row type that still looks like a bolted-on afterthought next to the otherwise-unified tree.
- **Proposed fix**: This is flagged as a real gap, not a request to build a new customization system (out of scope per the sidebar constraint) — it is closing an existing row type up to the standard its siblings already meet: give the flow row `role="treeitem"`/`aria-level`/`aria-selected`, wire `oncontextmenu`, and add flow rows to `sidebarRows.ts`'s walk (extending `SidebarRowKind` with `'flow'`) so keyboard navigation and the `⋯` menu both reach it. `sidebarActionsFor` already has a `'flow'`-shaped gap ready for this (a flow's own rename/delete/reveal), matching the registry's existing collection/folder/request pattern.
- **Shared primitive it should use**: `sidebarActionsFor`/`SidebarActionMenu`/`walkSidebar`, extended rather than duplicated.

### A3-06 — Timeline search renders a silently blank pane on zero matches; body/headers show text
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:435,439`
- **What the user sees**: Filtering the response body to zero matches shows "No matches" (`:377`); filtering headers to zero shows "No matching headers" (`:428`). Filtering the timeline (`:435`) only updates the counter to "0 of N" — the `{#each filteredTimeline as entry}` loop at `:439` has no `{:else}` branch, so the `.timeline` div renders completely empty with no message at all.
- **Why it's wrong**: Same pane, same interaction pattern, and one of the three tabs silently goes blank instead of confirming "your filter matched nothing" — which reads as a bug rather than an empty state.
- **Proposed fix**: Add an `{:else}` branch to the timeline `{#each}` reusing the same "No results for '{query}'" wording proposed in the uniform spec.
- **Shared primitive it should use**: the uniform find-bar's empty-result wording.

### A3-07 — Close/clear glyph and accessible-name inconsistency across ~29 dialogs, including both search modals
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/search/GlobalSearchModal.svelte:37` vs `frontend/src/lib/modals/search/CommandPaletteModal.svelte:36`; pattern repeats at `frontend/src/lib/SidebarSearch.svelte:30` and 27+ other modal files (e.g. `lib/modals/NotificationsModal.svelte:37`, `lib/modals/collection/RenameCollectionModal.svelte:18`)
- **What the user sees**: 27 of ~29 modal close buttons render a literal lowercase `x` character with only a `title` attribute (no `aria-label`), so their accessible name to a screen reader is "x," not "Close." Two outliers — `CommandPaletteModal.svelte:36` and `NewRequestModal.svelte:34` — use the proper `×` (multiplication sign) glyph *and* an explicit `aria-label`. `GlobalSearchModal.svelte:37` is one of the 27 plain ones, despite being the sibling of the one modal that got this right.
- **Why it's wrong**: Within my area specifically, the two ⌘-modals that are supposed to feel like one designed pair disagree on this detail, and the more-used one (⌘K) has the worse accessible name.
- **Proposed fix**: Standardize on `×` + `aria-label="Close <dialog name>"` and apply it to `GlobalSearchModal.svelte:37` at minimum; the other ~27 instances are outside this area's file list but are the same bug and worth a follow-up sweep.
- **Shared primitive it should use**: `Modal.svelte` could own a default close button (with correct glyph + aria-label) that individual dialogs opt out of, instead of every one of the ~29 call sites hand-rolling it.

### A3-08 — No in-pane find bar supports Escape to clear or exit
- **Severity**: minor
- **Where**: `frontend/src/lib/SidebarSearch.svelte:28` (no `onkeydown`); `frontend/src/lib/workbench/ResponseInspector.svelte:236-240,376,428,435` (`searchKeydown` only handles `Enter`); `frontend/src/App.svelte:11216-11220,11277-11281,11653`; `frontend/src/lib/shortcuts.ts:115-147` (Escape is only wired to the command palette and cancelling an active request)
- **What the user sees**: Every modal in the app closes on Escape via `Modal.svelte:70-76`. None of the seven in-pane find inputs do anything on Escape — it neither clears the field nor returns focus to the tree/pane, unlike the modal pattern the user already learns from ⌘K/⌘⇧P.
- **Why it's wrong**: Escape is the one key every search-like control in the OS and browser (and this app's own modals) agrees on. Its absence here is exactly the kind of per-surface behavioral drift the audit is hunting for.
- **Proposed fix**: Give the shared find-bar component (see spec) a standard `Escape` handler: clear the query if non-empty, else blur/return focus to the underlying content — same two-stage behavior VS Code's find bar uses.
- **Shared primitive it should use**: the uniform find-bar spec's Escape behavior.

### A3-09 — "No results" wording drifts across five phrasings
- **Severity**: polish
- **Where**: `GlobalSearchModal.svelte:49` ("No results found"), `CommandPaletteModal.svelte:46` ("No commands match."), `App.svelte:8549` ("No matching requests"), `App.svelte:8609` ("No requests"), `ResponseInspector.svelte:377` ("No matches"), `ResponseInspector.svelte:428` ("No matching headers")
- **What the user sees**: Six near-identical empty-result states, six different sentences, inconsistent punctuation (only one ends in a period), inconsistent noun choice ("results" vs "commands" vs "requests" vs "matches" vs "headers").
- **Why it's wrong**: Small on its own, but it's the same signal repeated everywhere else in this audit: nobody owns one wording rule for "your filter/search matched nothing."
- **Proposed fix**: Adopt one template — "No results for '{query}'" when there is a query, "No {things} yet" when there is not — per the uniform spec.
- **Shared primitive it should use**: the uniform find-bar's empty-result wording rule.

### A3-10 — Three unrelated conventions for a row's "type" indicator
- **Severity**: minor
- **Where**: `App.svelte:8592-8597` (collection badges, `<small>` pills: Scratch/Git/Not cloned/format), `App.svelte:8686-8689` (request badges, `<em>` tags: temp/draft), `App.svelte:8677` (method label, `<span class="method" data-method=...>`), `App.svelte:8731` (example glyph, `<span class="example-glyph">Ex</span>`), `App.svelte:8779` (flow glyph, `<span class="flow-glyph">Flow</span>`)
- **What the user sees**: A collection's status is a row of `<small>` pill badges; a request's status is a pair of italic `<em>` words; a request's method is a colored `data-method` badge; a saved example's type is a two-letter abbreviation "Ex"; a flow's type is the word "Flow." Five different mechanisms for what is conceptually the same "what kind of row is this / what state is it in" signal, with no shared component or even a shared HTML element choice.
- **Why it's wrong**: This is a direct contributor to the "different app per section" feeling within the sidebar itself, not just across panes.
- **Proposed fix**: Not a request to redesign the tree — just consolidate the *markup*: one `<Badge>`/`<TypeGlyph>` component parameterized by tone (status pill vs method vs abbreviation glyph), used by all five call sites instead of four bespoke inline spans.
- **Shared primitive it should use**: a new small `RowBadge.svelte` in `lib/sidebar/`.

### A3-11 — Single-click vs double-click behavior is explained on one row type and undiscoverable on the others
- **Severity**: minor
- **Where**: `App.svelte:8561-8573` (collection row, no `title`), `App.svelte:8630` (folder row, `title="{group.folder} — click for settings, double-click to open"`), `App.svelte:8673` (request row, `title` shows only the URL)
- **What the user sees**: A folder row's tooltip spells out that a single click opens settings and a double click expands it. A collection row has the identical click/double-click split (select vs toggle, `:8571-8572`) but no tooltip explaining it at all. A request row's tooltip shows the URL, not any click behavior (which is fine, since a request row's click just opens it — there's no second behavior to explain) — but the inconsistency is that the *one* row type with a non-obvious dual behavior (collection) is the one left unexplained, while the other dual-behavior row type (folder) is documented.
- **Why it's wrong**: Someone who learns "double-click to expand" from a folder row has no equivalent signal on the collection row above it, despite both behaving the same way.
- **Proposed fix**: Add the same `title` pattern to the collection row: `` `${collection.name} — click for details, double-click to expand` ``.
- **Shared primitive it should use**: none needed — just copy the existing folder-row string pattern.

## Cross-cutting primitives this area needs

1. **One `FindBar.svelte`** (icon + input + live counter + optional Previous/Next + clear button + Escape handling) used by every in-pane search: sidebar filter, response body, response headers, timeline, cookies, history, environment variables, and the CodeMirror editor toolbar. ⌘K and ⌘⇧P stay as modals per the locked constraint — they are a different surface, not a candidate for this primitive.
2. **One `SearchResultsList` / listbox pattern** shared by `GlobalSearchModal` and `CommandPaletteModal`, so ARIA semantics can't drift between the pair again.
3. **One empty-state wording function** (`"No results for '{query}'"` / `"No {things} yet"`) imported everywhere a filter can produce zero rows, replacing five ad hoc strings.
4. **A `walkSidebar` extension for flows** (new `SidebarRowKind: 'flow'`) so flows inherit tree semantics, keyboard navigation, and the `⋯`/`SidebarActionMenu` registry the other three row kinds already share, instead of staying a permanent exception.
5. **A `RowBadge`/`TypeGlyph` component** replacing the four bespoke inline-span conventions (`<small>` pills, `<em>` tags, `Ex`, `Flow`) used for "what kind of row / what state is this."
6. **A default `Modal` close button** (correct `×` glyph + `aria-label="Close {title}"`) that the ~29 call sites opt into instead of re-typing a bare `x` with only a `title`.

## Proposed uniform find-bar spec

**Layout**: a single-row flex bar — `[search icon] [text input, flex:1] [live match counter, muted, right-aligned] [Previous ‹] [Next ›] [clear ×]` — built from the app's existing tokens (`var(--space-6)` gaps, `var(--font-size-11)` for the counter, `var(--radius-4)` control corners), matching `SidebarSearch`'s `.search-box` grid rather than `ResponseInspector`'s hardcoded `gap:7px; padding:8px 10px`. Previous/Next render only when the surface supports match-navigation (body, editor); pure filters (headers, timeline, cookies, history, env vars, sidebar) omit them.

**Icon set**: one search glyph (magnifying glass, matching the existing `.icon-button` sizing) leading every instance; the clear button is always the `×` glyph with `aria-label="Clear search"`, never a bare letter `x`.

**Shortcut**: every surface's own input is reachable by clicking or, where the surface already owns a shortcut (sidebar = ⌘F), that binding must be scoped so it does not fire while focus is inside a *different* editable field (the CodeMirror body editors in particular) — no shortcut should ever fight another surface's default keymap the way ⌘F currently does.

**Escape behavior**: first Escape clears a non-empty query; a second Escape (or Escape on an already-empty query) blurs the input and returns focus to the underlying content (tree, response body, timeline list) — mirroring how `Modal.svelte` already returns focus on close, so the pattern is familiar rather than novel.

**Match counter wording**: `"{index+1} of {total}"` while a match is selected (body/editor style), or bare `"{total} of {grandTotal}"` for pure filters (headers/timeline/env style) — pick one and use it everywhere; never blank space where a counter belongs.

**Empty-result wording**: exactly `"No results for '{query}'"` when the query is non-empty, and a surface-specific `"No {things} yet"` when the underlying collection is itself empty (this is the fix for A3-03's copy bug generalized) — never a bare counter with no message, and never omit the `{:else}` branch that renders it.
