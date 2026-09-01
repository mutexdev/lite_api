# A10 — Design tokens, CSS, and the measured case for a uniform system

Scope: `frontend/src/style.css` (5,897 lines / ~130 KB) plus every `<style>` block across 73 `.svelte` files. Read-only audit; all counts below were produced with `grep`/`awk`/`python3` against the current tree, not estimated.

## Headline numbers

| Metric | Count |
|---|---|
| Distinct font sizes in effective use (11 tokens ∪ 15 hardcoded literals) | **19** |
| Distinct `padding` literal values (non-token) | **27** (186 occurrences) |
| Distinct `margin` literal values (non-token) | **14** (178 occurrences) |
| Distinct `gap` literal values (non-token) | **19** (84 occurrences) |
| Distinct `border-radius` literal values (non-token) | **11** (71 occurrences), vs. 12 radius tokens already defined |
| Distinct interactive-control heights (`min-height`, ≤50px, buttons/inputs/rows) | **12** (20–44px) |
| Hardcoded colour literals (`#hex`/`rgb`/`rgba`) outside the 13 theme blocks | **92 occurrences / 84 distinct values** |
| Distinct `<button>` "variant" classes in markup | **~18** (416 `<button>` tags; only 108 carry any class) |
| Themes defined (`:root` light + `html[data-theme="dark"]` + variants) | **13** (2 base + 11 variants) |
| Custom properties defined in `:root` | **128** (129 unique names defined anywhere in the file) |
| Tokens defined in `:root` but never referenced anywhere (css/svelte/ts) | **11** |
| Tokens referenced (`var(--x)`) but defined **nowhere** in the file | **8 with no fallback (critical)** + 10 more that only work because of an inline `var(--x, …)` fallback |

## Token inventory

`:root` (`frontend/src/style.css:1-150`) defines 128 custom properties: 81 colour tokens (re-overridden per theme), 18 `--space-*`, 11 `--font-size-*`, 12 `--radius-*`, plus a handful of one-offs (`--app-zoom`, `--code-font-family`, `--on-accent`, `--on-dark`, `--on-focus`).

**(a) Defined but never used** (`comm` of `:root` names against every `var(--x)` / string-literal token reference in `.css`, `.svelte`, and `.ts`):
`--font-size-9`, `--font-size-20`, `--font-size-22`, `--radius-2`, `--radius-10`, `--radius-12`, `--radius-14`, `--radius-16`, `--space-11`, `--space-28`, `--space-32` — 11 tokens, all in the spacing/radius/font-size scales, i.e. the scale itself is bigger than what the app actually draws from it.

**(b) Used but not defined anywhere — critical.** These resolve to the CSS-invalid value (effectively `unset`/transparent) in every theme, in every mode:
- `--code-font` — `frontend/src/style.css:4614`, `:4698` (`font-family: var(--code-font);`, no fallback). The real token is `--code-font-family` (defined `style.css:50`, used correctly 20+ other places). This is a typo, not a design choice.
- `--panel` — `frontend/src/style.css:4636`, `:5638`
- `--panel-soft` — `frontend/src/style.css:5612`, `:5634`
- `--success` — `frontend/src/style.css:2547` (`.ws-event-row.received { border-left: 3px solid var(--success); }`)
- `--surface-2` — `frontend/src/style.css:4490`
- `--warning-bg-soft` — `frontend/src/style.css:3838`, `:3861`, plus `frontend/src/lib/workbench/CodeEditor.svelte:156`, `frontend/src/lib/workbench/ResponseInspector.svelte:474,495`
- `--warning-border` — `frontend/src/style.css:1546`, `:3836`, `:3859`, plus `CodeEditor.svelte:156`, `ResponseInspector.svelte:474`. The file's own author has already flagged half of this: a comment at `frontend/src/style.css:1909-1910` says *"No --warning-border exists in the palette; --warning-strong at low alpha is what the other warning surfaces here settle for"* — and then five other call sites use `var(--warning-border)` directly anyway, with no fallback.
- `--warning-soft` — `frontend/src/style.css:4993`, `:5025`

**(c) Tokens only saved by a fallback** — these reveal the token set is known to be incomplete, and a substitute value was written in instead of fixing the palette:
- `var(--surface-raised, var(--surface, #fff))` — `frontend/src/lib/SuggestionListbox.svelte:108` (falls all the way to a hardcoded white, which is wrong in every dark theme if the outer fallback is ever exercised)
- `var(--surface-raised, var(--surface-soft))` — `frontend/src/lib/workbench/WorkspaceWindowPicker.svelte:466`, and 4 call sites in `frontend/src/lib/workbench/ResponseInspector.svelte:475,478,481,483`
- `var(--surface-hover, var(--accent-tint))` — `frontend/src/lib/workbench/CodeEditor.svelte:154`
- `var(--surface-hover, var(--surface-alt))` — `frontend/src/lib/workbench/WorkspaceWindowPicker.svelte:474` (note: two different fallback targets for the same nonexistent `--surface-hover` token, in two files)
- `var(--selection-bg, var(--focus-ring-strong))` — `CodeEditor.svelte:155`
- `var(--method-color, var(--muted-strong))` — `style.css:1461` (this one is legitimate: `--method-color` is deliberately set per-row via `[data-method="GET"] { --method-color: var(--method-get); }`, `style.css:1470-1474`)
- `var(--request-pane-size, 52%)`, `var(--row-depth, 1)` — legitimate JS-set layout variables (`App.svelte:9126`, `App.svelte:8615/8663`), not palette gaps.

Net: **`--surface-raised` and `--surface-hover` are used as if they were real palette tokens in 7 places across 3 files, but neither is ever defined** in `style.css`. Every one of those call sites is silently relying on its own local guess for what the "raised surface" or "hover surface" color should be, and the guesses disagree (`#fff`, `--surface`, `--surface-soft`, `--accent-tint`, `--surface-alt`).

## Hardcoded value audit

Outside the 13 theme blocks (`style.css:1-624`), literal colours appear **92 times (84 distinct values)**:
- `frontend/src/style.css`: 34 occurrences, e.g. `:3875-3876` (`#05070b`/`#d7f7dd`), `:3925-3933` (stone/emerald status swatch), `:4142-4186` (six `rgba(...)` glyphs for grpc/ws status dots), `:4791-4821` (amber warning-badge block, `#a16207`/`#ca8a04`/`#fff7ed`/`#b77903`/`#fef3c7`/`#422006`), `:5264-5278` (a second, differently-valued green/amber/red status-badge triple), `:6249` (`color-mix(in srgb, #d99a26 52%, var(--border))`).
- `frontend/src/App.svelte`: 40 occurrences, of which **39 are the theme-preview swatch colours at `App.svelte:1156-1170`** — every one of the 13 theme definitions (background/sidebar/accent) is hand-copied a second time here as a JS literal, fully independent of the actual CSS values in `style.css:1-624`. Change a theme's palette in CSS and this preview goes stale silently. One more literal at `App.svelte:11193` (`#2f8cff` default value for a `<input type="color">`).
- `frontend/src/lib/BrandMark.svelte`: 7 (logo SVG fills — acceptable, brand mark is deliberately theme-invariant, but worth confirming intentionally).
- `frontend/src/lib/SuggestionListbox.svelte`: 4, `frontend/src/lib/workbench/LazyCodeEditor.svelte`: 2, one each in `ResponseInspector.svelte`, `HistoryPanel.svelte`, `SidebarActionMenu.svelte`, `GenerateDocsModal.svelte`, `DiscoveryModal.svelte`.

Hardcoded font family: the UI sans-serif stack is written out **3 times** instead of using a token (there is a `--code-font-family` token but no `--font-family`/`--ui-font-family` token for the UI face):
- `style.css:143` — `Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`
- `style.css:2156` and `style.css:2412` — `Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`

These three are not even the same string — `style.css:143` includes `ui-sans-serif` in the stack, the other two omit it. Three independent, silently-diverging copies of "the app font."

## The scale audit

Measured directly with a property-parser over every `padding`/`margin`/`gap`/`border-radius` declaration in `style.css` + all `.svelte` `<style>` blocks (literal values only; `var()` uses excluded):

**padding** — 27 distinct literal values, 186 occurrences: `0`(53), `10px`(19), `8px`(18), `12px`(15), `4px`(10), `6px`(10), `9px`(8), `5px`(7), `14px`(7), `2px`(6), `7px`(5), `18px`(4), `11px`(4), `1px`(2), `3px`(2), `28px`(2), `22px`(2), `20px`(2), plus singletons `15px, 24px, 26px, 36px, 17px, 210px, 0.5rem, 1, 3`.

**margin** — 14 distinct literal values, 178 occurrences: `0`(126), `10px`(9), `4px`(8), `12px`(6), `2px`(5), `8px`(5), `5px`(4), `6px`(3), `14px`(3), `9px`(2), `-1px`(2), `-4px`(2), `7px`(2), `16px`(1).

**gap** — 19 distinct literal values, 84 occurrences: `8px`(14), `6px`(13), `5px`(10), `10px`(9), `7px`(5), `0`(5), `2px`(4), `9px`(4), `3px`(3), `12px`(3), `0.5rem`(3), `4px`(2), `14px`(2), `13px`(2), plus singletons `18px, 0.75rem, 0.25rem, 1px, 20px`.

**border-radius** — 11 distinct literal values, 71 occurrences: `0`(19), `8px`(11), `50%`(8), `7px`(8), `6px`(8), `999px`(5), `4px`(4), `12px`(3), `9px`(3), `10px`(1), `5px`(1) — layered on top of the 12 `--radius-*` tokens that already exist for most of these same magnitudes (`--radius-4` through `--radius-16`, `--radius-pill`), i.e. authors keep re-typing `8px`/`999px` instead of reaching for `var(--radius-8)`/`var(--radius-pill)`.

**font-size** — 11 tokens (`9,10,11,12,13,14,16,18,20,22,24px`) plus 15 hardcoded literals not on that scale (`9.5, 10.5, 11.5, 12.5, 15, 19, 21, 32px` and duplicate re-typings of `9,10,11,12,13,14,18px`), union = **19 distinct sizes** rendered in the app. Literal-only breakdown: `11px`(20), `12px`(18), `10px`(10), `15px`(5), `13px`(5), `9px`(3), `9.5px`(2), `18px`(2), `12.5px`(2), `11.5px`(2), and singletons `32px, 21px, 19px, 14px, 10.5px`.

The token set itself is part of the problem: `--space-*` defines a value at nearly every integer from 1–8px then jumps (`1,2,3,4,5,6,7,8,10,11,12,14,16,18,20,24,28,32`), and `--radius-*` similarly covers almost every integer 2–16px. A "scale" that offers a token for every value doesn't constrain anything — it's a lookup table dressed as a scale, which is exactly why hand-typed literals keep appearing next to it instead of forcing a choice between a small number of steps.

## Button/control taxonomy

416 `<button>` elements across the app (`find … | xargs grep -c '<button'`). Only **108** carry a `class` attribute; the remaining **~308 are bare `<button>`** and get nothing but the single global reset at `style.css:654-662` (`border: 1px solid var(--border-strong); background: var(--surface); min-height: 32px; padding: 0 var(--space-12); border-radius: var(--radius-6);`) plus `:hover`/`:disabled` (`style.css:664-671`).

Distinct classes actually applied to `<button>` markup, with counts: `icon-button`(51), `primary`(29), `danger-button`(5), `command-icon`(4), `var-value-editable-display`(3), `var-save-button`(3), `secret-toggle-button`(3), `ghost`(3), `drag-handle`(3), `tab-select`(2), and 12 more used exactly once (`workspace-button`, `variable-chip`, `terminal-session-button`, `subtle`, `openapi-sync-open-request`, `notification-button`, `network-sort-button`, `link-button`, `import-drop-target`, `grpcurl-action`, `git-status`, `cookie-button`, `command-status`, `command-palette-button`).

Only two real variant rules exist globally: `button {}` (base) and `button.primary {}` (`style.css:654-682`). `.icon-button` (`style.css:931-936`, 32×32px) and `.icon-button.ghost` (`style.css:938-942`) are the only other centrally-defined variant. **`.icon-button.subtle` is used but never defined**: `frontend/src/lib/views/devtools/TerminalTab.svelte:48` applies `class="icon-button subtle"` for a close button, while the equivalent "clear" affordance elsewhere (`App.svelte:11219`, `App.svelte:11280`, `SidebarSearch.svelte:30`) uses `class="icon-button ghost"`, which *is* defined. Same UI element (transparent icon button), two different modifier names, one of which is a no-op class.

Everything else — `danger-button` (`style.css:3720-3726`), the one-off `.workspace-button`, `.terminal-session-button`, `.row-menu-button`, `.new-request-button`, `.run-button`, `.open-button`, `.notification-button`, `.cookie-button`, `.close-button`, `.command-palette-button`, `.link-button`, `.copy-button`, `.network-sort-button` — is a component-local reinvention of button styling, each with its own padding/height/radius. Measured interactive-control heights (`min-height` ≤ 50px, i.e. actual rows/buttons/inputs, container panels excluded): **12 distinct values** — `20, 22, 24, 26, 28, 30, 32, 34, 36, 38, 42, 44px`.

## Theme coverage

13 themes total: `:root` (light default), `html[data-theme="dark"]`, and 11 `html[data-theme-variant="…"]` overlays layered on top per `App.svelte:2002-2003` (`dataset.theme = mode; dataset.themeVariant = variant`), both attributes set on `<html>` simultaneously, variant rules winning ties by source order (they're declared after the base blocks, `style.css:238` onward).

Light variants (`light-monochrome` `style.css:238`, `light-pastel` `:254`, `catppuccin-latte` `:271`, `vscode-light` `:327`) cascade on top of `:root` alone; dark variants (`dark-monochrome` `:377`, `dark-pastel` `:396`, `catppuccin-frappe` `:421`, `catppuccin-macchiato` `:462`, `catppuccin-mocha` `:503`, `nord` `:544`, `vscode-dark` `:585`) cascade on top of `:root` + `html[data-theme="dark"]`. Verified: every one of the 81 colour tokens defined in `:root` **is** re-defined in the `dark` block except `--on-accent`, `--on-dark`, `--on-focus` (`style.css:95-96`) — those 3 are intentionally theme-invariant fixed-white text colors and stay correct in both modes. No base-level "invisible text" bug exists between light and dark.

Variant blocks themselves are much smaller (13-45 tokens each vs. 81 available), which is by design — they only override accent/surface/rail colours and inherit shadows/syntax/status colours from the light or dark base. That's a legitimate pattern, not a bug, **except** for the 8 tokens flagged in the token inventory above (`--panel`, `--panel-soft`, `--success`, `--surface-2`, `--warning-bg-soft`, `--warning-border`, `--warning-soft`, `--code-font`) which are undefined in *every* theme including the two bases — those are real, cross-theme breakage, not incomplete variant customization.

## Focus, motion, contrast

**Focus**: no universal `button:focus-visible`/`:focus-visible` rule exists anywhere. Only `input:focus, select:focus, textarea:focus` (`style.css:699-703`) get the shared treatment (`border-color: var(--focus); box-shadow: 0 0 0 2px var(--focus-ring)`). Buttons get focus styling only where a component author happened to add it: `.sidebar-resizer:focus-visible` (`:831`), `.tree-chevron:focus-visible` (`:1081`), `.row-menu-button:focus-visible` (`:1168`), `.response-splitter:focus-visible` (`:1973`), `.cm-variable-valid/invalid:focus-visible` (`:2139-2140`), `.devtools-network-table … :focus-visible` (`:2902`), `.import-drop-target:focus-visible` (`:5620`), `.import-preview-row:focus` (`:5623`), plus three component-local rules (`SidebarActionMenu.svelte:129,134`, `WorkspaceWindowPicker.svelte:385`, `CommandOverflowMenu.svelte:192`). That's **11 ad hoc focus treatments** covering a handful of the 416 buttons in the app; the rest (~400) fall back to the browser's native default outline, which will look and behave differently per OS/browser and is never checked against the app's own dark palette.

**Motion**: handled correctly and centrally — a single universal `@media (prefers-reduced-motion: reduce) { *, *::before, *::after { … !important } }` block at `style.css:3412-3419` covers all 7 `transition:`/`animation:` declarations in the codebase (2 in `style.css`, 4 in `WorkspaceWindowPicker.svelte`, 1 in `CodeEditor.svelte` via CodeMirror theme extension `CodeEditor.svelte:164`). No gaps found here.

**Contrast**: `prefers-contrast: more` is handled in exactly **2 places** — `WorkspaceWindowPicker.svelte:684` and `CodeEditor.svelte:163` — and nowhere else, not even as a global fallback in `style.css`. Compare to motion, which has a universal catch-all; contrast has none. Any user with `prefers-contrast: more` gets thicker text-decoration and a stronger focus outline only inside the workspace picker and the code editor, and the stock experience everywhere else in the app.

## Findings

### A10-01 — `--warning-border` used 5× and `--warning-bg-soft` used 6×, neither ever defined
- **Severity**: critical
- **Where**: `frontend/src/style.css:1546`, `:3836`, `:3838`, `:3859`, `:3861`; `frontend/src/lib/workbench/CodeEditor.svelte:156`; `frontend/src/lib/workbench/ResponseInspector.svelte:474,495`
- **Why it's wrong**: Neither custom property is defined in `:root`, any theme variant, or anywhere else in the file. `var(--warning-border)`/`var(--warning-bg-soft)` with no fallback resolves to the CSS-wide keyword `unset`, so the declared `border`/`background` is dropped — the warning banner in the unsaved-tabs dialog and the search-match highlight in the code editor render with no border and no fill, in every theme. The codebase's own comment at `style.css:1909-1910` documents that the author already knew `--warning-border` "doesn't exist," yet 5 call sites use it directly.
- **Proposed fix**: Add `--warning-border` and `--warning-bg-soft` to `:root` and the `dark` base block (e.g. `color-mix(in srgb, var(--warning-strong) 40%, transparent)` and a low-alpha warning fill, matching the pattern already used at `style.css:1915`), then delete the now-obsolete comment.

### A10-02 — `--code-font` typo silences monospace rendering for OpenAPI spec viewer/diff
- **Severity**: critical
- **Where**: `frontend/src/style.css:4614`, `:4698`
- **Why it's wrong**: The correct token, `--code-font-family`, is defined at `style.css:50` and used correctly in 20+ other rules. `--code-font` (missing the `-family` suffix) is never defined anywhere, so these two rules silently inherit the surrounding UI sans-serif font instead of monospace. The OpenAPI spec viewer and the spec-diff cell are the two places in the whole app that should visually look like a code/JSON viewer and instead look like body text — this is a direct instance of the "different app in each section" problem the audit is measuring.
- **Proposed fix**: `s/var(--code-font)/var(--code-font-family)/` at both sites.

### A10-03 — `--surface-raised` and `--surface-hover` are load-bearing but never defined, and their fallbacks disagree
- **Severity**: major
- **Where**: `frontend/src/lib/SuggestionListbox.svelte:108` (`var(--surface-raised, var(--surface, #fff))`), `frontend/src/lib/workbench/WorkspaceWindowPicker.svelte:466,474` (`var(--surface-raised, var(--surface-soft))`, `var(--surface-hover, var(--surface-alt))`), `frontend/src/lib/workbench/ResponseInspector.svelte:475,478,481,483` (`var(--surface-raised, var(--surface-soft))` ×4), `frontend/src/lib/workbench/CodeEditor.svelte:154` (`var(--surface-hover, var(--accent-tint))`)
- **Why it's wrong**: Two tokens that read like they belong to the core palette (`--surface-raised`, `--surface-hover`) are referenced in 7 places across 4 components but defined in zero themes. Every call site is guessing at a fallback, and the guesses conflict: `--surface-hover` falls back to `--accent-tint` in one file and `--surface-alt` in another; `--surface-raised` falls back through `--surface-soft` in three files but through `--surface` (and ultimately a hardcoded `#fff`) in a fourth. Result: the "hovered row" and "raised card" surface colour is inconsistent by construction, and the `#fff` tail in `SuggestionListbox.svelte:108` will visibly break in every dark theme if `--surface` is ever unset.
- **Proposed fix**: Formally add `--surface-raised` and `--surface-hover` to the token set (light + dark bases + variants, since they're clearly meant to vary per theme), pick one canonical mapping (e.g. `--surface-raised: var(--surface-soft)`, `--surface-hover: var(--accent-tint)` — the two values already used most often), and drop the fallbacks so a future gap fails loudly instead of silently disagreeing.

### A10-04 — No unified focus-visible treatment; ~400 of 416 buttons get the OS default outline
- **Severity**: major
- **Where**: `frontend/src/style.css:699-703` (the only broad focus rule, and it's input/select/textarea-only)
- **Why it's wrong**: `--focus`, `--focus-ring`, `--focus-ring-strong` tokens exist and are well-formed (`style.css:72-74`, redefined per theme), but are wired up in only 11 scattered, component-specific `:focus`/`:focus-visible` rules (listed in "Focus, motion, contrast" above). The base `button {}` rule (`style.css:654-662`) has no focus treatment at all. Keyboard users tabbing through the ~308 bare `<button>` elements and most of the 108 classed ones see the browser's native focus ring — a different colour and shape from the app's `--focus` token, and inconsistent across Chromium/WebKit/Firefox in Wails' embedded webview.
- **Proposed fix**: Add one rule — `button:focus-visible, [role="button"]:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }` — next to the existing `button {}` block, then delete the 11 component-local reimplementations that just replicate this (keep only the ones with a legitimately different shape, e.g. inset rings on rows).

### A10-05 — The token "scale" isn't a scale: 18 space tokens, 12 radius tokens, 11 font-size tokens, plus 27/11/19 more literal values layered on top
- **Severity**: major
- **Where**: `frontend/src/style.css:1-150` (token definitions) vs. the padding/margin/gap/radius/font-size literal counts in "The scale audit" above
- **Why it's wrong**: `--space-*` defines a token at almost every integer 1–8px and again at 10–32px — there is effectively no gap a hand-typed literal would need to fill, yet 27 distinct hardcoded `padding` values, 14 `margin` values, and 19 `gap` values exist anyway (386 occurrences combined), because nothing about the token names communicates which ones are the "intended" steps vs. which exist only because someone once needed exactly `11px`. A scale is supposed to be a small, memorable set of choices; this one has as many entries as the literals it's failing to replace.
- **Proposed fix**: see "Proposed uniform system" below — collapse to a 6-step spacing scale, 5-step radius scale, and 7-step type scale, remove the rest, and run a follow-up pass mapping every literal in this report's tables to its nearest surviving step.

## Proposed uniform system

**Spacing scale** (replace 18 `--space-*` tokens + 27/14/19 literal padding/margin/gap values with 6 steps):

| New token | Value | Replaces (old tokens / literals) |
|---|---|---|
| `--space-xs` | 4px | `--space-1..5` (1,2,3,4,5px), literals `1px,2px,3px,4px,5px` |
| `--space-sm` | 6px | `--space-6,7` (6,7px), literals `6px,7px` |
| `--space-md` | 8px | `--space-8` (8px), literal `9px` (round down) |
| `--space-lg` | 12px | `--space-10,11,12` (10,11,12px), literals `10px,11px,12px` |
| `--space-xl` | 16px | `--space-14,16,18` (14,16,18px), literals `14px,15px,17px,18px` |
| `--space-2xl` | 24px | `--space-20,24,28,32` (20,24,28,32px), literals `20px,22px,24px,26px,28px,32px,36px` |

**Radius scale** (replace 12 `--radius-*` tokens + 11 literals with 4 steps + pill):

| New token | Value | Replaces |
|---|---|---|
| `--radius-sm` | 4px | `--radius-2,3,4` (2,3,4px) |
| `--radius-md` | 6px | `--radius-5,6` (5,6px), literal `5px` (round up) |
| `--radius-lg` | 8px | `--radius-7,8` (7,8px), literals `7px,9px` |
| `--radius-xl` | 12px | `--radius-10,12,14,16` (10,12,14,16px), literal `10px` |
| `--radius-pill` | 999px | unchanged (`--radius-pill`, literal `999px`) |
| *(keep `50%`)* | — | circular avatars/dots, not a radius-scale concern |

**Type scale** (replace 11 `--font-size-*` tokens + 15 literals with 7 steps):

| New token | Value | Replaces |
|---|---|---|
| `--text-xs` | 10px | `--font-size-9,10`, literals `9px,9.5px,10px,10.5px` |
| `--text-sm` | 11px | `--font-size-11`, literals `11px,11.5px` |
| `--text-base` | 12px | `--font-size-12`, literal `12.5px` |
| `--text-md` | 13px | `--font-size-13`, literal `14px` (round down for secondary chrome; use `--text-lg` where 14px is body copy) |
| `--text-lg` | 14px | `--font-size-14` |
| `--text-xl` | 18px | `--font-size-16,18`, literals `15px,19px,21px` |
| `--text-2xl` | 24px | `--font-size-20,22,24`, literal `32px` (dedicated hero/empty-state size, keep as a named exception if truly needed) |

**Button variants** (replace the 18 ad hoc button classes with 5 variants × implicit sizing from context):
- `.btn-primary` (today's `button.primary`, `style.css:673-682`) — accent-filled, for the single primary action per view.
- `.btn-secondary` (today's bare `button {}`, `style.css:654-662`) — the current default look, kept as the explicit "secondary" name so it stops being "whatever a bare `<button>` happens to render as."
- `.btn-ghost` (unify `.icon-button.ghost` + the dead `.icon-button.subtle` + one-offs like `.link-button`) — transparent until hover.
- `.btn-danger` (today's `.danger-button`, `style.css:3720-3726`) — destructive actions.
- `.btn-icon` (today's `.icon-button`, `style.css:931-936`) — 32×32px square, icon-only.
- Sizing: standardize the measured 12 interactive-control heights (20–44px) down to 2 sizes — 28px (`sm`, for dense rows/toolbars) and 34px (`md`, for primary forms/dialogs) — and drop the rest.

**Focus treatment**: one rule, `:is(button, [role="button"], a, input, select, textarea):focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }`, placed once near the existing `button {}`/`input,select,textarea {}` reset (`style.css:654`, `:695`), replacing the 11 scattered component-local versions except where a component genuinely needs an inset ring instead of an outset outline (row selection, `.tree-chevron`, `.row-menu-button`).

**Font stack**: add `--font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;` to `:root` (the `style.css:143` version, since it's the most complete), replace the two divergent copies at `style.css:2156` and `:2412`, and use it as `font-family: var(--font-family)`.
