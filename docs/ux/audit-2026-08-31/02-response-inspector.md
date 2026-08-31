# A2 — Response inspector

## Summary

- The response body is a plain `<pre>` (`ResponseInspector.svelte:410`) with zero syntax coloring, while the request body one pane over uses full CodeMirror highlighting via `liteApiHighlightStyle` (`CodeEditor.svelte:120`). This is the single biggest reason the response pane reads as a different application.
- The toolbar is two permanently-stacked rows (`.response-inspector-toolbar` + `.response-search`, lines 356–380) mixing a `<select>`, long-text buttons, live-region spans, and Previous/Next text buttons — no icon-button vocabulary, though the app already has one (`FileBodyTable.svelte`, `KeyValueTable.svelte`, `CommandOverflowMenu.svelte`).
- Truncation/size state is stated three times in three different wordings (toolbar span line 360, warning banner 372–374, footnote 412).
- Every other sub-view (headers, metadata/trailers, timeline, compare) hand-rolls its own toolbar shape — none share a component, and no two look alike.
- "JSON tree" (line 400) is not a tree: it lists root-level keys only and renders each value's nested content as unhighlighted `JSON.stringify` text (`jsonTree.ts:41-61`).
- Three search/filter boxes (`response-search` search, header search, timeline search) drive `aria-live="polite"` count spans that re-announce on every keystroke.
- The response body has no keyboard shortcut to reach its own search box; the request editor's search is reachable via CodeMirror's default `Mod-f` binding.
- The component's `<style>` block (lines 466–546) never references the app's `--space-*` / `--font-size-*` token scale (`style.css:8-36`) — every gap and padding is a bare pixel literal.
- Four visually distinct "can't show this" containers exist in one file (empty state, TLS alert, generic warning, binary card) with no shared primitive.

## Findings

### A2-01 — Response body has no syntax highlighting at all
- **Severity**: critical
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:410`
- **What the user sees**: A JSON or XML response renders as flat black-on-surface (or white-on-surface in dark mode) text inside a plain `<pre>`. Meanwhile the **Body** tab of the request pane, one splitter away, renders the same content type with full key/string/number/boolean coloring.
- **Why it's wrong**: This is the concrete, measurable version of "the app looks like a different application in each section." The request/response panes sit side by side in the same window and diverge completely in typographic treatment for what is often literally the same JSON shape (e.g. echo endpoints).
- **Proposed fix**: Paint `safeDisplay` with the same token palette CodeMirror uses for the request editor (see the dedicated comparison section below). Do not invent new colors — reuse `--syntax-*` custom properties from `style.css` so every installed theme (Nord, Catppuccin, VS Code, etc.) stays correct automatically, exactly as `syntaxHighlight.ts` already documents as the rule for the request editor.
- **Shared primitive it should use**: a new `ResponseBodyView` (or extending `CodeEditor` with a read-only mode) that shares `liteApiHighlightStyle`.

### A2-02 — "JSON tree" is a flat root-key list, not a tree, and its contents are unhighlighted
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:400-408`, backed by `frontend/src/lib/workbench/jsonTree.ts:41-61`
- **What the user sees**: Toggling "JSON tree" (line 370) replaces the body with a list of `<details>` elements, one per root-level key (line 402-403). Opening one reveals the value as a single `<pre>{entry.text}</pre>` — a `JSON.stringify(child, null, 2)` dump with no coloring and no further expand/collapse for nested objects or arrays.
- **Why it's wrong**: The label promises the interaction Postman/Insomnia users expect (a fully collapsible, colorized structural view). What's shipped is one level of accordion over otherwise identical plain text — it doesn't even fix A2-01 for the content it does show.
- **Proposed fix**: Either (a) rename the control to something honest like "Fields" until a real recursive tree exists, or (b) build an actual bounded recursive tree component and, at minimum, run each entry's `text` through the same highlighter chosen for A2-01 so opening a field doesn't trade "no coloring" for "no coloring, in an accordion."
- **Shared primitive it should use**: `JsonTree` (new, recursive) or, short-term, the same highlighter component as A2-01 applied to `entry.text`.

### A2-03 — Body toolbar mixes a `<select>`, long-text buttons, and live spans in one crowded row
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:356-371` (`.response-inspector-toolbar`)
- **What the user sees**: Left to right: a native `<select>` (Pretty/Raw/Base64/Hex, line 357-359), a byte-count `<span aria-live>` (360), a "Copy preview" button plus a status span (361), a "Download" button (362), and then conditionally "Render full" (364), a `<small>` disabled-reason (366), "Load more" (369), and "JSON tree" (370) — up to 6 controls of three different shapes crammed into one `flex-wrap` row.
- **Why it's wrong**: Compare to Postman's response toolbar: a left cluster (Pretty | Raw | Preview segmented toggle, a format dropdown, a Tree toggle) and a right cluster of small icon buttons (copy, search, wrap, download), with anything situational tucked behind an overflow "…" menu. This toolbar has no left/right grouping, no icon buttons, and no overflow — every control is always visible and always full-width text, so the row reflows unpredictably (`flex-wrap`, line 471) as conditional buttons appear/disappear.
- **Proposed fix**:
  - **Left group**: a segmented control for Pretty/Raw/Preview (the current `<select>`'s Base64/Hex options move into a small format dropdown next to it, mirroring Postman's "language" dropdown), plus the Tree toggle.
  - **Right group**: icon buttons — copy (glyph, `title`/`aria-label` carry the text now spelled out in the button label), search (opens the find bar, see A2-04), download. Byte-count text (`bytes.toLocaleString()`) moves to a single, non-interactive status readout, not a stray `<span>` between unrelated buttons.
  - **Overflow**: "Render full", "Load more", and the disabled-reason `<small>` are conditional/situational — collapse them into the truncation notice itself (see A2-05) rather than the toolbar, or into a `CommandOverflowMenu`-style "…" menu when they must be button-shaped.
- **Shared primitive it should use**: `SegmentedControl` (view mode), `IconButton` (copy/search/download), `PaneToolbar` (left/right grouping shell) — none of these exist yet; `CommandOverflowMenu.svelte` is the closest existing precedent for the icon-button visual language (32px targets, `title`+`aria-label`, SVG glyphs).

### A2-04 — Search is a permanently-visible second toolbar row, not a find bar
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:375-380` (`.response-search`)
- **What the user sees**: A second full-width row, always rendered under the first toolbar, holding a text input, a match-count span, and two text buttons labeled "Previous" and "Next".
- **Why it's wrong**: This permanently taxes vertical space for a response that might never be searched, and it looks like a distinct feature area rather than an extension of the toolbar above it. It's also a second, bespoke search implementation: the request editor already has search via CodeMirror's built-in panel — `CodeEditor.svelte:355` wires a "Search" button to `openSearchPanel(view)`, which opens CodeMirror's own floating find/replace UI (with its own Previous/Next, match count, and regex/case options) bound to the standard `Mod-f` keymap included in `basicSetup`. The response pane reinvents the same concept with different visuals, different affordances, and no keybinding.
- **Proposed fix**: Collapse this into a find bar that appears only when the search icon button (A2-03) is activated or `Mod-f` is pressed while the body has focus, positioned as an overlay/inline bar rather than a static row — matching the CodeMirror pattern the request pane already trained the user on one pane over. If the highlighting fix adopts a read-only CodeMirror instance (option a below), this can be retired entirely in favor of `openSearchPanel`.
- **Shared primitive it should use**: `InlineFindBar` (or CodeMirror's own search panel, see the highlighting section).

### A2-05 — Truncation/size state is repeated three times with three different wordings
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:360`, `:372-374`, `:412`
- **What the user sees**:
  1. Toolbar span (360): `showing 131,072 of 812,004 bytes` (only when `bodyTruncated`) or `812,004 bytes`.
  2. A separate warning banner (372-374), shown only when `bytes > fullRenderLimit`: `Large response: automatic rendering is bounded to 128 KB. Download for the full payload.`
  3. A footnote under the body (412), shown whenever `bodyTruncated`: `Showing the first 131,072 of 812,004 bytes. Download the body to inspect all content.` (second sentence only appears in one specific sub-case).
- **Why it's wrong**: All three describe the same fact (how much of the body is currently visible) with overlapping but non-identical numbers and phrasing, at three different places in the DOM (top toolbar, mid-panel banner, bottom-of-body caption). A user skimming for "is this the whole response?" has to reconcile three sentences instead of reading one.
- **Proposed fix**: One truncation notice, one place — directly under the toolbar, replacing both the banner (372-374) and the footnote (412). It should state the byte range once, and host the "Render full" / "Load more" / "Download" actions as its own controls rather than scattering them into the main toolbar (see A2-03). The toolbar's byte-count span (360) becomes redundant once this exists and should be removed, not triplicated.
- **Shared primitive it should use**: `TruncationNotice` (new), replacing three ad hoc call sites.

### A2-06 — Every sub-view toolbar is a structurally different one-off
- **Severity**: major
- **Where**:
  - Body: `ResponseInspector.svelte:356-380` (two rows, see A2-03/A2-04)
  - Headers: `ResponseInspector.svelte:428` — one row: search input, count span, "Copy headers" button, copy-status span
  - Metadata/Trailers: `ResponseInspector.svelte:432` — one row: count span (not a search box), "Copy {tab}" button, copy-status span
  - Timeline: `ResponseInspector.svelte:435` — one row: search input, phase `<select>`, count span, "Copy" button, "Export"/"Saving…" button (five controls in one line)
  - Compare: `ResponseInspector.svelte:414-424` — a `<label><select>` picker, then ad hoc `<h4>`-headed sections, a bespoke `.compare-badge` color scheme, and its own "Show unchanged body/header rows" checkbox
- **Why it's wrong**: Search-plus-copy exists in three shapes: body's search is a separate row from its copy button (which lives in the *other* toolbar row above); headers' search and copy share one row; timeline crams search, a filter dropdown, count, copy, and export into a single line — visibly more crowded than the body's two-row layout it sits one tab away from. Metadata/Trailers has no search at all even though headers (its sibling tab) does. None of these five toolbars share a CSS class beyond `display:flex` layout (`.response-inspector-toolbar, .response-search, .timeline-tools` at line 471 share only flex/gap/border, not structure). A user learns a different control layout for every tab of the same panel.
- **Proposed fix**: Define one `PaneToolbar` shape (left: mode/filter controls, right: icon actions) and one `InlineFindBar` shape, and reuse both across body/headers/metadata/trailers/timeline. Give Metadata/Trailers the same search affordance as Headers if searching those small key/value lists is worth supporting, or explicitly drop the copy-only row into the same toolbar shell so at least the chrome matches.
- **Shared primitive it should use**: `PaneToolbar`, `InlineFindBar`, `IconButton` — reused, not reinvented, per sub-view.

### A2-07 — Three `aria-live="polite"` regions re-announce on every keystroke
- **Severity**: major (accessibility)
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:377` (`{matches.length === 0 ? 'No matches' : ...}`, driven by `search` at line 76), `:428` (`{filteredHeaders.length === 0 ? ... }`, driven by `headerSearch` at line 87), `:435` (`{filteredTimeline.length} of {timeline.length}`, driven by `timelineSearch` at line 78)
- **What the user sees / hears**: Nothing visually wrong, but a screen reader user typing into the response search, header search, or timeline search box gets a new "polite" announcement queued on every single character typed, because the bound `$derived` (`matches`, `filteredHeaders`, `filteredTimeline`) recomputes on each keystroke and the live region's text content changes each time.
- **Why it's wrong**: This is exactly the "aria-live noise" the audit brief called out. Constant interruption during typing is worse than no live region at all — it trains screen reader users to disable verbosity or avoid the search boxes.
- **Proposed fix**: Debounce the live announcement (e.g., only update the live text ~300ms after the last keystroke, or on blur/Enter), or drop `aria-live` from the count span entirely and instead announce match position only when `Enter`/Next/Previous is pressed (mirroring how `searchKeydown` at line 236-240 already gates match navigation on Enter).
- **Shared primitive it should use**: a debounced live-count helper shared by the three search boxes, or fold into `InlineFindBar`/`PaneToolbar` from A2-04/A2-06 so the fix is made once.

### A2-08 — No keyboard path to the response body's own search
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:376` (the `<input aria-label="Search response body">`); contrast with `frontend/src/lib/workbench/CodeEditor.svelte:112` (`basicSetup`, which wires CodeMirror's default `Mod-f` search keymap) and `:355` (explicit "Search" button calling `openSearchPanel`)
- **What the user sees**: There is no `Mod-f`/`Ctrl+F` binding, and no entry in `frontend/src/lib/keybindings.ts` or `frontend/src/lib/shortcuts.ts`, that focuses the response search input — a grep for `response-search`/`Search response`/`responseSearch` across those files and `App.svelte` returns nothing. The only way to reach it is a mouse click into the always-visible row (A2-04).
- **Why it's wrong**: The request body one pane over supports the platform-standard find shortcut. The response body — which is at least as likely to need searching, since it's often the larger, server-generated payload — has no shortcut at all.
- **Proposed fix**: Bind `Mod-f` (when the response body/pane has focus) to open/focus the find bar from A2-04. This becomes close to free if the highlighting fix adopts a read-only CodeMirror instance, since `openSearchPanel`'s keymap comes along with `basicSetup`.
- **Shared primitive it should use**: `InlineFindBar` with a registered shortcut, or CodeMirror's own search keymap.

### A2-09 — Hardcoded pixel values throughout, no `--space-*`/`--font-size-*` tokens
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:466-546` (the entire `<style>` block), e.g. `:471` `padding:8px 10px; ... gap:7px`, `:472` `font-size:11px`, `:473` `padding:12px`, `:483` `padding:8px`, `:485` `gap:8px`, `:493` `gap:6px`
- **Why it's wrong**: `frontend/src/style.css:8-25` defines a complete `--space-1` (1px) through `--space-32` (32px) scale, and `:26-36` a matching `--font-size-9` through `--font-size-24` scale, both already consumed elsewhere in the app (e.g. `style.css:912` `.rail-section.compact { gap: var(--space-7); }`). `ResponseInspector.svelte` never references a single one of these tokens — every gap, padding, and font-size in its style block is a bare literal that happens to match a token value (7px = `--space-7`, 11px = `--font-size-11`, etc.) without using it. This is low-severity individually but is exactly the kind of per-file drift that compounds into "feels like a different app": a future global density change (e.g. bumping `--space-8`) silently stops applying to this one pane.
- **Proposed fix**: Replace every literal in the style block with the matching `--space-*`/`--font-size-*` custom property.
- **Shared primitive it should use**: none new — just consistent use of the existing token scale.

### A2-10 — Response toolbar uses long-text buttons where the rest of the app uses icon buttons
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/ResponseInspector.svelte:361` (visible text "Copy preview", `aria-label="Copy visible response preview"`), `:362` (visible text "Download", `aria-label="Download exact response body"`), `:364` ("Render full"), `:369` ("Load more"), `:370` ("JSON tree")
- **Why it's wrong**: The app already has an established icon-button vocabulary — 32px square buttons with `title`/`aria-label` and a glyph, no visible text — used for copy/move/remove actions in `frontend/src/lib/FileBodyTable.svelte:119-122`, `frontend/src/lib/KeyValueTable.svelte:463-466`, and `App.svelte:9449-9452` (gRPC stream Send/Gen/Remove), styled by `.icon-button` in `style.css:931-935`. The response toolbar is the one place still spelling out full sentences on buttons for equivalent actions (copy, download), which is both visually inconsistent and makes the already-crowded row (A2-03) wider than it needs to be.
- **Proposed fix**: Convert Copy/Download (and, if kept as buttons at all, Render full/Load more) to icon buttons using the existing `.icon-button` styling, moving the descriptive text into `title`/`aria-label` only, matching every other actionable icon in the app.
- **Shared primitive it should use**: `IconButton` (existing `.icon-button` class, formalized as a component).

### A2-11 — Four different "nothing/something's-wrong" card treatments in one file
- **Severity**: polish
- **Where**: `response-empty-state` (`:350-354`, styled `:468-470` — centered grid, no border, muted text only), `response-tls-warning` (`:330-345`, styled `:528-545` — bordered `role="alert"` box via `.response-warning` plus TLS-specific nested classes), generic `.response-warning` (`:347`, `:372-374`, `:395` — bordered/background note, no card treatment), `binary-response-card` (`:388-393`, styled `:475-476` — bordered card with `border-radius` and background)
- **Why it's wrong**: These are all conceptually the same thing — "the pane cannot show you a body right now, here's why and what to do" — rendered with four different visual containers (plain centered text vs. alert box vs. plain warning strip vs. card-with-radius-and-background) inside a single component.
- **Proposed fix**: Converge on one `EmptyState`/`StateCard` primitive with tone variants (`info`, `warning`, `error`), used for the empty state, TLS failure, generic warnings, and the binary-response card alike.
- **Shared primitive it should use**: `EmptyState` (variants: neutral/warning/error).

## Cross-cutting primitives this area needs

- **`PaneToolbar`** — a left-group/right-group flex shell (mode controls left, icon actions right, overflow menu for anything situational), replacing the five one-off toolbar rows (`.response-inspector-toolbar`, `.response-search`, headers row, metadata/trailers row, `.timeline-tools`).
- **`SegmentedControl`** — Pretty/Raw/Preview (and similar mode toggles), replacing the bare `<select>` at line 357.
- **`IconButton`** — formalizing the existing `.icon-button` class (`style.css:931`) into a real component with `title`+`aria-label`+glyph, used for copy/search/download/wrap everywhere instead of long-text buttons.
- **`InlineFindBar`** — an overlay/collapsible find bar bound to `Mod-f`, replacing the always-visible `.response-search` row and unifying it with the headers/timeline search inputs.
- **`TruncationNotice`** — single source of truth for "how much of this body are you looking at," replacing the toolbar span, the warning banner, and the footnote (A2-05).
- **`EmptyState`** — tone-variant container for empty/warning/error/binary states (A2-11).
- **A response body highlighter** — see below; this is the one with the most implementation weight and is broken out on its own.

## Response syntax highlighting: implementation options

### (a) Read-only CodeMirror instance

Mount a `EditorView` configured `editable: false` (or `EditorView.editable.of(false)` + `EditorState.readOnly.of(true)`), reusing `Prec.highest(syntaxHighlighting(liteApiHighlightStyle, { fallback: true }))` exactly as `CodeEditor.svelte:120` does, plus `json()`/`xml()` language packages selected by `previewKind()` (`response.ts:120-129`).

- **Parity**: Exact visual match with the request body editor — same tokenizer (lezer), same color mapping, same theme-swap behavior — because it's the literal same `liteApiHighlightStyle` object, not a re-derivation of it. Zero risk of the two panes drifting apart over time.
- **Large payloads**: CodeMirror only renders the visible viewport's DOM, so a highlighted 1 MB body (the current `fullRenderLimit`) stays cheap to scroll, unlike a giant `<pre>` full of `<span>`/`<mark>` nodes. The existing byte-budget/truncation machinery (`sliceUtf8`, `automaticPreviewLimit`, `fullRenderLimit`, `renderFull`) is unaffected — it still governs what string is handed to the editor as `doc`; CodeMirror doesn't need to know why the string stops where it does.
- **Search**: Gets `openSearchPanel`/`Mod-f` for free (same mechanism as `CodeEditor.svelte:355`), which would let A2-04 and A2-08 be closed as a side effect rather than separate work. The existing bespoke `matches`/`markedParts`/`<mark class="current-match">` machinery (lines 155, 226-230, 250-261, 410) would be **retired**, not reused — CodeMirror's search uses its own decoration-based match highlighting, so keeping the current `<mark>` feature alongside it would be redundant and would fight for visual ownership of the same text.
- **Cost**: Mount/unmount lifecycle per response identity (`responseIdentity`, line 82) needs the same care `CodeEditor.svelte` already takes (`synchronizeEditor`, `configureEditor`, scroll/selection restoration) — this is a known, already-solved pattern in this codebase, not new risk. No new dependency: CodeMirror is already bundled.
- **Base64/Hex views**: These aren't "code" — mount with no language extension (plain `EditorView` content) for those, still monospaced and still gets free virtualization/search, just no token coloring (nothing to color).

### (b) Standalone tokenizer painting spans with `--syntax-*` vars

A small pure function (parallel to `jsonTree.ts`) that walks `safeDisplay` and wraps tokens in `<span style="color:var(--syntax-string)">`, etc., rendered inside the existing `<pre class="response-body">`.

- **Footprint**: No new component lifecycle, no CodeMirror mount — keeps today's rendering model (plain string → DOM via `{#each}`/`<pre>`).
- **Composing with search**: The existing `markedParts()` (lines 250-261) already splits `safeDisplay` into matched/unmatched segments for the `<mark>` feature. A tokenizer would need to produce token boundaries that are then re-split at match boundaries too (or vice versa) — solvable, but it's two range-based text transforms that have to compose correctly rather than one CodeMirror decoration layer handling both. This is the main integration cost of option (b) that option (a) sidesteps.
- **Truncation**: Composes cleanly — the byte-budget slicing already happens before this text reaches the template (`safeDisplay`, line 111), so the tokenizer just runs on whatever string it's handed, same as today's `<pre>`.
- **Coverage & drift risk**: Would realistically only be worth hand-building for JSON (and maybe XML) given `previewKind()`'s branches — other kinds already render as image/html/pdf/binary/ws-log, not code text. Because it's a second tokenizer independent of the lezer grammar `CodeEditor.svelte` uses, it creates a second place that has to agree with `syntaxHighlight.ts`'s documented rule ("`--syntax-invalid` is the only red," enforced today by `syntaxHighlight.test.mts` against the lezer tag mapping) — a second, hand-rolled JSON colorer has no such test coverage by default and could quietly diverge from the request editor's palette rules over time.
- **Large payloads**: No free virtualization — a fully-tokenized 1 MB body still means one giant DOM subtree of `<span>`s, same node-count problem the current `<mark>`-based search highlighting already has today (this isn't a regression, but it isn't a fix either).

### (c) Reusing `boundedJsonTree`

`jsonTree.ts` already exists and is wired to "JSON tree" mode (`ResponseInspector.svelte:400-408`). It is not a highlighter — see A2-02 — and reusing it as-is doesn't touch A2-01 at all, since the default **Pretty** view (what's on screen the vast majority of the time) never calls it; only the opt-in tree toggle does.

- What it *does* already solve, usefully, is bounding: `JSON_TREE_MAX_ENTRIES = 100` root entries and `JSON_TREE_BUDGET = 96 * 1024` chars total (`jsonTree.ts:14,17`) keep a single huge document from locking up that view.
- Its real value here is as a **complement**, not an alternative: whichever of (a) or (b) is chosen for the main body view should also be applied to each `entry.text` at line 403, so opening a tree entry doesn't regress to unhighlighted text. It does not, on its own, address the core complaint.

### Recommendation

Adopt **(a)**, a read-only CodeMirror instance reusing `liteApiHighlightStyle` verbatim. It's the only option that guarantees the request/response panes can never visually drift apart (same tokenizer, same style object), and it collapses three separate findings (A2-01 highlighting, A2-04 search-row, A2-08 missing shortcut) into one implementation instead of three. The integration cost — mounting CodeMirror per response identity — is a pattern this codebase has already solved once in `CodeEditor.svelte` and can adapt rather than invent. Apply the same instance's highlighter to `jsonTree.ts` entries (option c) rather than building a second tokenizer for that surface.
