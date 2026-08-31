# A1 — Request builder

## Summary
- The Body-mode picker is a one-off: it's the *only* place in the whole app that uses the `.field-row` CSS class (defined but otherwise unused), rendering a full label+`<select>` row instead of anything resembling Postman's compact segmented control.
- The per-request "Run" button sits next to Save/Send with identical styling but actually triggers the whole-collection runner and navigates away from the request — and silently no-ops if no runner items are selected, with no disabled state or message.
- The Settings tab wraps its content in a bordered/radius/background `<fieldset>` card; every sibling tab in the same pane (Params, Body, Headers, Vars, Script, Assert, Tests, Docs) renders bare into `.editor-surface`. Settings looks like it was imported from a different screen.
- The Script tab's "Pre-request"/"Post-response" labels use `class="field-label"`, but that class only has styling when nested under `.field-grid`/`.field-row`/`.import-grid`/etc. — here it isn't, so the labels render as unstyled plain text next to every other, properly-muted field-label in the pane.
- Row actions (remove/move/drag) across Params, Headers, Body's form-data/multipart/file tables render as literal ASCII glyphs (`x`, `^`, `v`, `::`) instead of icons, while the app's other toolbars (`CommandOverflowMenu`) use real SVG icons.
- Selects for Body mode, Auth mode, and gRPC method type have no accessible name — the adjacent "field-label" is a `<span>`, not a `<label>`, and isn't wired via `aria-labelledby`; compare to `ProtocolRequestLine`'s Method `<select aria-label="Method">`, which does it correctly.
- Raw enum values leak into the UI verbatim: the Body-mode dropdown shows `formUrlEncoded`, `multipartForm`, etc. as option text instead of human labels.
- The Body tab's three protocol variants (HTTP, gRPC, WebSocket) each use a structurally different toolbar (`field-row`, `.grpc-method-controls` flex row, `.ws-live-controls`) for the same conceptual job of "configure what gets sent."
- An always-visible "App" tab does nothing but show placeholder copy ("Request app runtime surface"), adding an 11th/12th tab of clutter to an already-crowded, non-overflowing tab bar.
- Workbench-specific components (`RequestCommandStrip`, `RequestSettingsPanel`, `CodeEditor`) hardcode raw pixel values in their scoped styles instead of using the app's `--space-*`/`--font-size-*`/`--radius-*` design tokens, even though the values mostly duplicate the token scale.

## Findings

### A1-01 — Body mode picker is a bespoke, unstyled-relative-to-app control
- **Severity**: critical
- **Where**: `frontend/src/App.svelte:9510-9517`; class defined at `frontend/src/style.css:2592-2602`
- **What the user sees**: A row labeled "Body mode" with a plain native `<select>` listing `none | json | text | xml | formUrlEncoded | multipartForm | file | graphql` as raw option text, taking a full label+control row above the editor.
- **Why it's wrong**: `grep` confirms `.field-row` (`style.css:2592`) is applied to exactly one element in the entire codebase — this one (`App.svelte:9510`). Everywhere else that needs a label+single-control row (gRPC method type at `App.svelte:9410`, Auth mode at `App.svelte:9569`, and 8 other places) uses `.field-grid`, which has a *different* label-column width (140px vs. 120px, `style.css:2599` vs `2604`). This is the single clearest piece of evidence for "looks like a different app in each section" — a copy-pasted one-off next to the app's actual repeated pattern. It's also the wrong control entirely: this is exactly the case the brief calls out — Postman/Bruno/Insomnia use a segmented row of mode pills with a small format dropdown, not a labeled full-width select.
- **Proposed fix**: Replace with a `SegmentedControl` (`none | form-data | x-www-form-urlencoded | raw | binary | GraphQL`), with a small format dropdown (JSON/XML/Text) appearing only when "raw" is selected. Drop the "Body mode" label row entirely — the segmented control is self-labeling. Retire `.field-row` since nothing else uses it.
- **Shared primitive it should use**: `SegmentedControl`

### A1-02 — "Run" button in the single-request command strip actually runs the whole collection, and can silently no-op
- **Severity**: critical
- **Where**: `frontend/src/lib/workbench/RequestCommandStrip.svelte:43`; wired to `runCollection` at `frontend/src/App.svelte:9133`; implementation at `frontend/src/App.svelte:3307-3337`
- **What the user sees**: A "Run" button styled identically to "Save" and "Send" inside the single-request toolbar. Clicking it does not run the open request — it invokes `runCollection()`, which runs the **collection runner** against `runnerSelectedItemIds` and navigates the view to `'runner'` on success.
- **Why it's wrong**: There is nothing in the button's label, styling, or position that signals "this runs something bigger than the request you're editing." Worse, `runCollection()` returns early with no feedback if `runnerSelectedItemIds` is empty (`App.svelte:3311`, `if (selectedItemIds.length === 0) return`) or if `activeCollectionRun`/`busy` is set — and the `disabled` prop passed from `App.svelte:9138` (`busy !== '' || hasActiveHTTPTransport`) does not account for an empty runner selection, so the button often looks enabled and clickable while doing absolutely nothing when clicked. A user reading "Run" next to "Send" will reasonably expect it to run the current request.
- **Proposed fix**: Remove "Run" from the per-request command strip, or relabel/restyle it clearly as "Run collection" with a distinct (secondary/ghost) visual treatment and route to the Runner view directly rather than requiring a pre-existing selection. At minimum, disable it and show a tooltip when there is no runner selection instead of silently no-op'ing.
- **Shared primitive it should use**: `CommandButton` (with an explicit `scope: 'request' | 'collection'` visual variant so cross-scope actions are never visually identical to same-scope ones)

### A1-03 — Settings tab is the only tab in the pane wrapped in a card
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/RequestSettingsPanel.svelte:98` (`.request-settings { border:1px solid var(--border); border-radius:8px; background:var(--surface-soft); padding:12px }`)
- **What the user sees**: Switching from Params/Body/Headers/Vars/Script/Assert/Tests/Docs (all of which render bare content directly into `.editor-surface`, no border/background) to Settings suddenly shows a bordered, rounded, tinted card with a `<legend>` and helper paragraph.
- **Why it's wrong**: Nothing else in `requestTabs` (`App.svelte:1055-1067`) uses a card container at the top level of the tab content. This single tab looking structurally different from its 10 siblings is a direct instance of the "looks like a different application" complaint.
- **Proposed fix**: Either (a) drop the card chrome from Settings so it matches the bare content style of every other tab, or (b) if the card treatment is worth keeping for grouped settings, apply the same wrapper convention to any other tab that groups fields (e.g. Auth's `field-grid` block) so it reads as an intentional, reused pattern rather than a one-off.
- **Shared primitive it should use**: `PaneSection` (a single decision, applied consistently, for whether tab content gets a card or not)

### A1-04 — Script tab's field labels are silently unstyled
- **Severity**: major
- **Where**: `frontend/src/App.svelte:9750` and `:9752`; CSS scoping requirement at `frontend/src/style.css:2610-2619` (also `:889` and `:5605`)
- **What the user sees**: "Pre-request" and "Post-response" render as plain, full-size, default-color text immediately above each `CodeEditor`, instead of the small muted bold caption style used for every other field label in the app (e.g. "Body mode," "Method type," "Mode" in Auth).
- **Why it's wrong**: `.field-label` in `style.css` is only ever styled as a descendant selector — `.field-grid .field-label`, `.field-row .field-label`, `.import-grid .field-label` (`2613-2615`), `.rail-section .field-label` (`889`), `.import-stack .field-label` (`5605`). There is no bare `.field-label` rule. The Script tab's two `<span class="field-label">` elements (`App.svelte:9750`, `9752`) are direct children of the tab content with no `.field-grid`/`.field-row`/`.import-grid`/`.import-stack` ancestor, so the class does nothing. This is a genuine CSS bug, not just a design inconsistency — verify by inspecting either span in devtools; it will show zero matched rules for `.field-label`.
- **Proposed fix**: Wrap each label+editor pair in a `.field-grid`-equivalent container (or introduce a lightweight `.field-caption` class with its own unscoped rule) so the labels actually pick up the intended muted/bold style.
- **Shared primitive it should use**: `FieldLabel` (a real, standalone component/class that doesn't depend on being inside a specific parent)

### A1-05 — Row-action buttons render literal characters instead of icons
- **Severity**: major
- **Where**: `frontend/src/lib/KeyValueTable.svelte:462-466`; `frontend/src/lib/MultipartTable.svelte:222-226`; `frontend/src/lib/FileBodyTable.svelte:118-122` — contrast with `frontend/src/lib/workbench/CommandOverflowMenu.svelte:111-117`
- **What the user sees**: In Params, Headers, and every Body sub-table (form-data, multipart, file), the drag/move/remove buttons literally contain the text `::`, `^`, `v`, and `x`. Meanwhile the "New"/overflow menu triggers elsewhere in the same workbench render real `<svg>` glyphs (a plus icon, a three-dot icon).
- **Why it's wrong**: This is exactly the "text where icons are expected, and vice versa" pattern the brief calls out. `x`/`^`/`v`/`::` look like unfinished placeholder markup, not a deliberate design choice, and they sit directly beside properly-iconified controls elsewhere in the same pane hierarchy (e.g. the command strip's orientation toggle uses a real `⇄`/`⇅` character consistently, but these use raw Latin letters that read as typos at a glance).
- **Proposed fix**: Replace with real SVG icons (drag-handle grip, chevron-up/down or arrow icons, an x/trash icon), reusing whatever icon set backs `CommandOverflowMenu`.
- **Shared primitive it should use**: `IconButton` (a single component wrapping the existing `.icon-button` class, taking an icon name so every consumer gets real SVGs by construction instead of ad hoc glyph strings)

### A1-06 — Several request-pane selects have no accessible name
- **Severity**: major
- **Where**: `frontend/src/App.svelte:9412` (gRPC method type), `:9512` (Body mode), `:9571` (Auth mode) — contrast with the correctly-labeled `frontend/src/lib/workbench/ProtocolRequestLine.svelte:42` (`<select aria-label="Method" ...>`)
- **What the user sees**: Nothing visually wrong, but a screen reader announces these as an unlabeled "combobox" — the adjacent "Body mode"/"Method type"/"Mode" caption is a `<span>`, not a `<label for>` and not wired via `aria-labelledby`.
- **Why it's wrong**: The pane already has a working pattern for this exact problem one component over — `ProtocolRequestLine.svelte:42` labels its Method select correctly. The inconsistency means keyboard/screen-reader users get a materially worse experience depending on which control they land on, with no visual cue predicting which.
- **Proposed fix**: Either convert the `.field-label` spans to real `<label>` elements with `for`, or add `aria-label`/`aria-labelledby` to every select/input that currently relies on visual proximity alone. Since `field-grid`/`field-row` is used ~10 times across the app, this is worth fixing at the pattern level.
- **Shared primitive it should use**: `FieldRow` (a component that always pairs a proper `<label>` with its control, so this class of bug becomes structurally impossible)

### A1-07 — Raw camelCase enum values shown as option text
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:9512-9516` (Body mode select); duplicated at `:9927-9932` (response-example draft body mode select)
- **What the user sees**: The Body mode dropdown's options read `none`, `json`, `text`, `xml`, `formUrlEncoded`, `multipartForm`, `file`, `graphql` — i.e. `{#each bodyModes as mode}<option value={mode}>{mode}</option>{/each}` renders the internal identifier verbatim, camelCase and all.
- **Why it's wrong**: `formUrlEncoded` and `multipartForm` are implementation identifiers, not user-facing copy. Every other client in this space shows "Form URL Encoded" / "Multipart Form" / "GraphQL". This happens in two separate places in the file (the live Body tab and the response-example editor), so it's a copy problem, not a one-off.
- **Proposed fix**: Introduce a `bodyModeLabels` lookup (`{ formUrlEncoded: 'Form URL Encoded', multipartForm: 'Multipart Form', ... }`) and use it in both places. This becomes moot if A1-01's segmented control replaces the select, since the control's own labels would need to be human-readable anyway.
- **Shared primitive it should use**: n/a (copy fix), but should live alongside the new `SegmentedControl` for Body mode

### A1-08 — Vars tab's KeyValueTable silently drops variable overlay, bulk edit, and the computed data-type
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:9742-9748`; `frontend/src/lib/KeyValueTable.svelte` (no `description` rendering anywhere in the template, despite the type declaring `description?: string` at line 7 and the prop at line 78)
- **What the user sees**: In the Vars tab, request-variable rows behave like a stripped-down version of the same table used for Params/Headers two tabs over — no bulk edit toggle, no `{{variable}}` highlighting/tooltip in the value cell. The row-mapping code even computes `description: v.dataType` (line 9744) to pass the variable's data type through, but `KeyValueTable` has no column or rendering path for `description` anywhere in its template — that data is computed and then thrown away.
- **Why it's wrong**: Same component, five call sites in the file, wildly different prop sets (`variableOverlay`, `showBulkEdit`, `busy` present or absent) with no visible reason tied to what Vars conceptually needs. The dead `dataType` plumbing suggests a feature (showing the variable's type) was intended but never wired up on the display side.
- **Proposed fix**: Either give Vars the same `variableOverlay`/`showBulkEdit`/`busy` treatment as Params/Headers for consistency, or if Vars intentionally needs a "Type" column, add real rendering for `description`/`dataType` to `KeyValueTable` instead of silently discarding it.
- **Shared primitive it should use**: `KeyValueTable` (fix in place — this is about consistent prop usage, not a new component)

### A1-09 — "Add row" vs. "Add File" — inconsistent add-action copy in the same Body tab
- **Severity**: minor
- **Where**: `frontend/src/lib/KeyValueTable.svelte:477`; `frontend/src/lib/MultipartTable.svelte:235`; `frontend/src/lib/FileBodyTable.svelte:131`
- **What the user sees**: Switching Body mode between form-urlencoded/multipart (both say "Add row") and file (says "Add File") changes the add-button's copy for what is functionally the same action ("append a new row to this table").
- **Why it's wrong**: Trivial but visible drift — three tables that look identical otherwise (same `.kv-table` class, same drag/move/remove icon-buttons) disagree on the one piece of call-to-action copy.
- **Proposed fix**: Standardize on one phrasing, e.g. always "Add row," or context-specific but consistently-cased ("Add row" / "Add file" — lowercase "file" to match "row").
- **Shared primitive it should use**: n/a (copy fix)

### A1-10 — Body tab has three unrelated toolbar shapes depending on protocol
- **Severity**: minor
- **Where**: gRPC: `frontend/src/App.svelte:9396-9406` (`.grpc-method-controls`, a flex row of button/select/button) and `:9410-9419` (`.field-grid`); WebSocket: `:9462-9475` (`.ws-live-controls` + `.button-row.compact`); HTTP: `:9509-9517` (`.field-row`)
- **What the user sees**: Opening the Body tab for a gRPC request shows a "Load methods / [select] / Generate" flex toolbar, then a `field-grid` block below it. Opening it for WebSocket shows a status pill plus a `.button-row.compact` group (Connect/Send selected/Disconnect). Opening it for HTTP shows the single `field-row` from A1-01. Three genuinely different layout systems for what is conceptually the same job: "configure the payload/interaction for this request type."
- **Why it's wrong**: This is the concrete version of the brief's "toolbars that differ in structure between two panes that do the same job" — except here it's the *same tab*, just branching on protocol, which makes the inconsistency more jarring since users encounter it by switching a single dropdown (protocol/method) rather than navigating to a different screen.
- **Proposed fix**: Define one `BodyToolbar` layout (label/segment row, optional secondary action group) and have gRPC/WebSocket/HTTP each populate it rather than hand-rolling their own container classes.
- **Shared primitive it should use**: `PaneToolbar`

### A1-11 — Dead `sparql` body-mode branch is unreachable from the UI
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:1134` (`bodyModes` array) and `:9525` (`activeRequest.body.mode === 'text' || activeRequest.body.mode === 'sparql'`)
- **What the user sees**: Nothing directly, but a dead code path exists: `'sparql'` is checked against `activeRequest.body.mode` but is never one of the options in the `bodyModes` array that drives the Body-mode `<select>`, so this branch cannot be reached through the UI.
- **Why it's wrong**: Signals unfinished/orphaned feature work (a SPARQL body mode that was partially built and then not wired into the mode selector), which is the kind of loose end that compounds the "feels unfinished" impression across the app.
- **Proposed fix**: Either add `'sparql'` to `bodyModes` (if the feature is meant to exist) or remove the dead check if it isn't.
- **Shared primitive it should use**: n/a

### A1-12 — "App" tab is a permanent, unexplained placeholder
- **Severity**: major
- **Where**: `frontend/src/App.svelte:1065` (tab registration) and `:9776-9777` (`<div class="empty-appState">Request app runtime surface</div>`)
- **What the user sees**: An always-visible "App" tab (12th in the row, between Docs and Settings) that, when clicked, shows only the sentence "Request app runtime surface" — internal/debug-sounding copy with no explanation of what the feature is or when it'll do something.
- **Why it's wrong**: The request-pane tab bar is already long (11 tabs); adding a permanently-inert one makes the whole toolbar look unfinished and adds to the tab-overflow problem described in A1-13. There's no feature flag or conditional guarding it — every user, on every request, sees a tab that does nothing.
- **Proposed fix**: Hide the tab until the feature has real content, or gate it behind a flag/preview toggle rather than shipping it to everyone as dead weight.
- **Shared primitive it should use**: n/a

### A1-13 — Request tab bar has no overflow handling, just a bare scrollbar
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:9315-9330`; CSS at `frontend/src/style.css:2514-2521` (`.request-side > .subtabs { flex-wrap: nowrap; overflow-x: auto; scrollbar-width: thin; }`) — contrast with `frontend/src/lib/workbench/CommandOverflowMenu.svelte`, used elsewhere in the workbench (`SidebarHeader.svelte`, `WorkspaceCommandBar.svelte`) for exactly this kind of constrained-width toolbar
- **What the user sees**: With 11 tabs (Params/Body/Headers/Auth/Vars/Script/Assert/Tests/Docs/App/Settings) and the response pane in side-by-side orientation (narrowing the request pane), tabs past the visible edge are reachable only by horizontally scrolling a thin scrollbar, with no fade/arrow/affordance indicating there's more.
- **Why it's wrong**: The app already has a purpose-built `CommandOverflowMenu` component for "more items than fit" (used in the sidebar header and workspace command bar), but the request-pane tab bar — arguably the most tab-dense toolbar in the app — doesn't use it, and instead falls back to a silent scroll container.
- **Proposed fix**: Either collapse low-frequency tabs (App, Assert, Docs) behind a `CommandOverflowMenu`-style "More" menu when width is constrained, or at minimum add a visible scroll-edge fade so users know there's more content.
- **Shared primitive it should use**: `CommandOverflowMenu` (reuse, don't reinvent)

### A1-14 — Workbench components hardcode raw pixel values instead of design tokens
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/RequestCommandStrip.svelte:101-235` (all values like `padding: 6px 12px 8px`, `font-size: 10px`, `border-radius: 4px`); `frontend/src/lib/workbench/RequestSettingsPanel.svelte:98-113`; `frontend/src/lib/workbench/CodeEditor.svelte:362`
- **What the user sees**: Nothing directly visible today (the values happen to match the token scale), but this is exactly the drift vector the brief is worried about.
- **Why it's wrong**: `style.css:8-43` defines a deliberate, value-named token scale (`--space-4` through `--space-32`, `--font-size-9` through `--font-size-24`, `--radius-2` through `--radius-16`) specifically so spacing/type/radius can be swept consistently across all 12 themes (see the comment at `style.css:2-6`). The request builder's own components — the ones closest to the "different app per section" complaint — don't participate in that system at all; they hardcode numbers that currently duplicate the tokens by coincidence, not by reference. Any future adjustment to the scale silently won't apply here.
- **Proposed fix**: Replace hardcoded px in these three files' `<style>` blocks with the matching `var(--space-*)`/`var(--font-size-*)`/`var(--radius-*)` custom properties.
- **Shared primitive it should use**: existing token set (`--space-*`, `--font-size-*`, `--radius-*`) — enforcement, not a new primitive

### A1-15 — Two different flows create a "response example," with different verbs and different entry points
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:9818` (`<button title="Save response as example" onclick={saveResponseExample}>Example</button>`, immediate save, implementation at `:2431-2437`) vs. `frontend/src/App.svelte:9870` (`<button class="primary" onclick={beginCreateResponseExample}>New example</button>`, opens a name-first inline form, implementation at `:2443-2453`)
- **What the user sees**: A small "Example" button always visible in the response summary bar (immediately saves an auto-named example and jumps to the Examples tab) and a separate, prominent "New example" primary button inside the Examples tab itself (opens a naming dialog first).
- **Why it's wrong**: Both create a response example from the currently active response, but with different verbs ("Example" vs. "New example"), different visual weight (plain vs. `.primary`), and different interaction models (instant-save-then-rename-later vs. name-first). A user who tries one and later finds the other has no reason to expect they do almost the same thing.
- **Proposed fix**: Pick one flow. If the name-first dialog is the intended pattern, make the response-summary "Example" button open the same dialog instead of auto-saving; if instant-save-then-rename is preferred, make "New example" do the same and drop the separate naming step.
- **Shared primitive it should use**: n/a (flow consolidation)

### A1-16 — Docs tab is edit-only, no rendered preview
- **Severity**: polish
- **Where**: `frontend/src/App.svelte:9775` (`<CodeEditor ... language="markdown" ariaLabel="Request documentation" ...>`)
- **What the user sees**: Request documentation is authored as raw Markdown source with syntax highlighting but is never rendered — there's no preview toggle, split view, or read mode anywhere in this tab (confirmed no `preview`/`MarkdownPreview` reference exists in the codebase).
- **Why it's wrong**: Every comparable client renders Markdown docs for at least a read view; shipping only the raw-source editor makes "Docs" function more like a second Scripts/Tests editor than actual documentation.
- **Proposed fix**: Add an Edit/Preview toggle (reusing the same toolbar slot pattern as `CodeEditor`'s existing Search/Format buttons) that renders the Markdown when not actively editing.
- **Shared primitive it should use**: `CodeEditor` (extend with an optional preview mode)

## Cross-cutting primitives this area needs

- **SegmentedControl** — for any small, mutually-exclusive mode choice (Body mode chief among them) instead of a labeled full-width `<select>`. Should support an optional trailing icon-button cluster (à la Postman's Body row).
- **IconButton** — a real component wrapping `.icon-button` that takes an icon name and renders SVG, so no consumer can regress to literal `x`/`^`/`v`/`::` glyph text again.
- **FieldRow** — a label+control pairing primitive that always emits a proper `<label for>`/`aria-labelledby` association, replacing the current pattern of a bare `<span class="field-label">` next to an unlabeled input/select. Should also settle the `field-row` (120px) vs `field-grid` (140px) column-width split into one width, since nothing currently justifies two.
- **PaneToolbar** — one toolbar shape for "here are the controls to configure this pane's content," so gRPC's method-controls row, WebSocket's live-controls row, and the (soon-to-be) Body segmented control don't each invent their own container.
- **CommandOverflowMenu (reuse)** — already exists and is used in the sidebar/workspace bar; the request-pane's 11-tab `.subtabs` row and any other tab strip that can overflow should use it instead of a bare `overflow-x: auto` scrollbar.
- **PaneSection / card convention** — an explicit, app-wide decision for which tab/pane content gets a bordered card (like Settings currently does alone) vs. bare content, applied consistently rather than per-component judgment calls.
- **Design-token enforcement in scoped styles** — the `--space-*`/`--font-size-*`/`--radius-*` scale in `style.css` should be the only source of these values; workbench components currently hardcode raw px that happens to match it today but isn't guaranteed to stay in sync. Worth a lint rule or at least a sweep.
- **Enum-label lookup convention** — a shared helper/pattern for turning internal camelCase mode identifiers (`formUrlEncoded`, `multipartForm`, etc.) into human-readable option text, so no more raw identifiers leak into `<option>` text.
