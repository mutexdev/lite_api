# A4 — Shell, tabs, and layout

## Summary

- The tab strip carries no per-tab unsaved/dirty indicator even though the app already computes `command.dirty` — a user has to click into a tab to learn it has unsaved changes.
- The single "change response orientation" command is rendered by **two different controls with two different icon languages** (an SVG stroke icon in the command bar, a Unicode arrow glyph in the request toolbar) a few hundred pixels apart.
- `WorkspaceWindowPicker.svelte` (695 lines) reimplements the entire modal shell — backdrop, focus trap, Escape handling, focus return — from scratch instead of using the shared `Modal.svelte`, which already implements and tests all four guarantees. It also uses its own border-radius scale, its own backdrop blur, its own z-index, and its own spinner animation, duplicating `.loader`/`@keyframes spin`.
- The main workbench tab strip has none of the ARIA tabs pattern (`role="tablist"`, `aria-selected`, roving tabindex, arrow-key navigation) that the Response pane's subtabs strip *in the same view* already implements correctly — two visually similar widgets, one accessible, one not.
- Both split dividers (sidebar, request/response) are fully invisible at rest — no rule, no grip — so there is no visual affordance that they're draggable until the cursor happens to land on an 7–8px hit target.
- Focus-ring treatment is inconsistent: the command bar's buttons get the browser's unstyled default outline, `WorkspaceWindowPicker` defines its own `outline: 2px solid var(--focus)` (positive offset), and other shell controls use a third pattern (`outline: 2px solid var(--accent)`, negative offset, inset).
- `WorkspaceCommandBar.svelte` and `WorkspaceWindowPicker.svelte` never use the app's `--space-*`/`--radius-*` token scale — every spacing and radius value in both files is a raw px literal, and two referenced tokens (`--surface-raised`, `--surface-hover`) don't exist anywhere in `style.css`.
- Tab-strip overflow is a bare `overflow-x: auto` scrollbar with no fade/chevron cue, and there is no drag-to-reorder and no middle-click-to-close — three conventions users bring from VS Code/Chrome/Postman that are simply absent.
- Close-button glyphs are inconsistent: the Notifications dialog closes with a literal lowercase `x`; every other close affordance in the shell uses `×` (U+00D7).
- The command bar defines its own responsive breakpoints (1180 / 800 / 610px) unrelated to the shell's own reflow breakpoint (960px), so the chrome and the layout it sits above can visually decouple in the 960–1180px band.

## Findings

### A4-01 — Tabs show no unsaved/dirty indicator
- **Severity**: major
- **Where**: `frontend/src/App.svelte:8846-8860` (tab markup); dirty state already computed at `frontend/src/lib/workbench/RequestCommandStrip.svelte:28-32`
- **What the user sees**: An open tab shows only a method badge, a name, and a close ×. A request with unsaved edits looks identical to a saved one in the strip — the only place "Unsaved" appears is a small pill inside the request toolbar below, which is only visible once that tab is already active.
- **Why it's wrong**: Postman, VS Code, and Bruno all mark unsaved tabs at the strip itself (a dot that swaps for the close control on hover), precisely so the signal is visible across every open tab at once, not just the active one. Here the same `dirty` boolean the toolbar already reads (`RequestCommandStrip.svelte:28`) is never surfaced per-tab, so a user with 6 tabs open cannot tell which ones have unsaved work without clicking through each.
- **Proposed fix**: Thread a `dirty` flag into `workbenchTabs` (or resolve it the same way `tabLabel`/`tabMethod` resolve against `collections`) and render a small dot in `.tab-select` that swaps to `.tab-close` on hover/focus, matching the VS Code convention already implied by the existing 20px close-button slot.
- **Shared primitive it should use**: a `TabDirtyDot` treatment shared between the workbench tab strip and any future tab-like list, keyed off the same `command.dirty` logic in `types.ts`.

### A4-02 — One command, two different controls
- **Severity**: major
- **Where**: `frontend/src/lib/workbench/WorkspaceCommandBar.svelte:162-164` (SVG icon button, "Change response orientation (⌘J)") vs `frontend/src/lib/workbench/RequestCommandStrip.svelte:86-96` (`⇄`/`⇅` text-glyph button, no shortcut shown); both call the identical `toggleResponsePaneOrientation` (wired at `frontend/src/App.svelte:6795-6796` and `frontend/src/App.svelte:9136`).
- **What the user sees**: Two visually unrelated buttons — one a stroke-icon square glyph in the top command bar, one a plain Unicode arrow character in the toolbar directly under the request URL — that do the exact same thing. Only one of the two advertises the ⌘J shortcut.
- **Why it's wrong**: This is the single clearest instance of "looks like a different application in each section" the audit was asked to find: identical functionality, two icon vocabularies, two toolbars, inconsistent shortcut disclosure.
- **Proposed fix**: Keep one visible control for this command. If both toolbars need it (compact/wide layouts), render the same SVG icon component in both places and show the ⌘J hint in both titles.
- **Shared primitive it should use**: a single `OrientationToggleButton` component (SVG icon + shared title/shortcut string) used by both `WorkspaceCommandBar` and `RequestCommandStrip`.

### A4-03 — WorkspaceWindowPicker reimplements the modal shell instead of using Modal.svelte
- **Severity**: critical
- **Where**: `frontend/src/lib/workbench/WorkspaceWindowPicker.svelte:1-333` (backdrop, `dialogElement`, `trapTab`, `handleDialogKeydown`, focus-restore `onMount`) vs the shared `frontend/src/lib/modals/Modal.svelte:1-155`, which already implements `inert` on the app shell, a tested focus trap, Escape-to-close, and focus return with `preventScroll`.
- **What the user sees**: A dialog that looks and behaves like a different product from every other dialog in the app: its own backdrop blur (`backdrop-filter: blur(7px) saturate(0.88)` — no other dialog in the codebase uses `backdrop-filter` at all), its own z-index (65, vs 50 for every `Modal.svelte`-based dialog at `frontend/src/style.css:3779`), its own border-radius (12px container / 7px close button, vs the shared `.prompt-dialog`'s `var(--radius-8)` at `style.css:3792`), an "eyebrow" label pattern (`WorkspaceWindowPicker.svelte:211`) used nowhere else in the app, and its own spinner (`.spinner`, `@keyframes workspace-picker-spin`, `WorkspaceWindowPicker.svelte:659-668`) duplicating the app's existing `.loader`/`@keyframes spin` (`style.css:771-783`).
- **Why it's wrong**: `Modal.svelte`'s own top comment documents exactly why this matters — it was written after measuring that 27 of 29 hand-rolled dialogs were missing `inert`, a focus trap, or Escape-to-close. `WorkspaceWindowPicker` is a 29th hand-rolled dialog, built after that consolidation, that re-introduces the same category of duplicated (and now doubly-untested) logic, plus a visual language no other dialog shares.
- **Proposed fix**: Rebuild on `Modal.svelte` (`labelledBy`, `onClose`, `dialogClass="workspace-picker"`, `describedBy`, `data-modal-autofocus` on the workspace list). Drop the bespoke `trapTab`/`handleDialogKeydown`/focus-restore code entirely — `Modal.svelte` already does it. Reuse `.prompt-backdrop`/`.prompt-dialog` sizing tokens and the shared `.loader` spinner instead of inventing new ones.
- **Shared primitive it should use**: `Modal.svelte`, and the shared `.loader` spin animation.

### A4-04 — The workbench tab strip has no ARIA tabs semantics; the response subtabs (same screen) do
- **Severity**: major
- **Where**: `frontend/src/App.svelte:8842-8886` (`<nav class="tabs" aria-label="Open tabs">`, plain `<button>`s, no `role`, no `aria-selected`, no keydown handler) vs `frontend/src/App.svelte:9820-9848` (`role="tablist"`, `role="tab"`, `aria-selected`, roving `tabindex`, `onkeydown={responseTabKeydown}` defined at `App.svelte:5351`).
- **What the user sees**: Nothing visually — but a screen-reader or keyboard user gets a completely different interaction model for what looks like the same control 40px below: the response pane's small tab row announces itself as a tab list and supports arrow-key navigation; the primary, far more important request tab strip above it does not.
- **Why it's wrong**: This is the accessibility twin of A4-02 — one part of the shell already solved this correctly and the other part, doing the more important job, didn't reuse the pattern. There is also a third, unrelated tabs implementation at `frontend/src/App.svelte:11331-11334` (`.tabs.compact` — the .env editor's Table/Raw toggle), which is a plain button-group with none of the ARIA-tabs semantics either — three different "tabs" idioms live in the app simultaneously.
- **Proposed fix**: Apply `role="tablist"` to `nav.tabs`, `role="tab"` + `aria-selected` + roving `tabindex` to each `.tab-select`, and reuse (or extract) `responseTabKeydown`'s arrow-key logic for the workbench strip.
- **Shared primitive it should use**: a shared `TabStrip`/`useRovingTabs` helper used by the workbench strip, the response subtabs, and `.tabs.compact`.

### A4-05 — Both resize dividers are invisible until the pointer is already on them
- **Severity**: major
- **Where**: sidebar divider — `frontend/src/style.css:812-834` (`.sidebar-resizer { background: transparent; ... } .sidebar-resizer:hover, :focus-visible { background: var(--accent); }`); response divider — `frontend/src/style.css:1956-1983` (`.response-splitter`, same pattern).
- **What the user sees**: No line, no grip dots, no color at rest between the sidebar and the workbench, or between the request and response panes. The only signal that the boundary is draggable is a `col-resize`/`row-resize` cursor that appears once the mouse is already within a 7–8px hit zone (`width: 7px` at `style.css:819`; `.response-splitter` similarly narrow).
- **Why it's wrong**: The audit brief specifically asks whether the divider is discoverable — it is not. A first-time user has no visual cue that the panes are resizable at all; they'd have to accidentally mouse over a few-pixel-wide strip to find out. Compare to Postman/VS Code, which show a persistent 1px seam plus a hover-highlighted grip.
- **Proposed fix**: Give both dividers a subtle always-on `border-left`/`border-top` (e.g. `var(--border)`) so the seam reads as a boundary at rest, reserving the `var(--accent)` highlight for hover/focus/drag as today.
- **Shared primitive it should use**: a shared `ResizeHandle` visual treatment (idle seam + hover/focus/drag states) applied to `.sidebar-resizer`, `.response-splitter`, and the devtools drawer resizer at `App.svelte:11894`.

### A4-06 — Three incompatible focus-ring treatments inside the shell
- **Severity**: major
- **Where**: command bar buttons — no `:focus-visible` rule anywhere in `frontend/src/lib/workbench/WorkspaceCommandBar.svelte` and no matching global `button:focus-visible` rule in `style.css` (browser default outline applies); `WorkspaceWindowPicker.svelte:385-388` (`outline: 2px solid var(--focus); outline-offset: 2px;`); tree/menu controls — `frontend/src/style.css:1081-1083`, `1168-1170`, `2902-2904` (`outline: 2px solid var(--accent); outline-offset: -2px;`).
- **What the user sees**: Tabbing through the app produces a different-looking focus indicator depending on which shell surface you're in — an unstyled browser ring in the command bar, an outward orange-blue ring in the workspace picker, an inward accent ring in the sidebar tree and devtools table.
- **Why it's wrong**: Focus-ring consistency is both an accessibility requirement (predictability for keyboard users) and one of the most visible "this is one coherent app" signals. Three different colors/offsets for the same concept undercuts both.
- **Proposed fix**: Pick one focus-ring recipe (the inset `var(--accent)` pattern is already the most widely used) and apply it via a shared `:focus-visible` rule for `button, [role="tab"], [role="option"]` rather than repeating or omitting it per component.
- **Shared primitive it should use**: a single global `:focus-visible` rule in `style.css`, removing the bespoke one in `WorkspaceWindowPicker.svelte`.

### A4-07 — Ghost design tokens referenced only by WorkspaceWindowPicker
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/WorkspaceWindowPicker.svelte:466` (`background: var(--surface-raised, var(--surface-soft));`) and `:474` (`background: var(--surface-hover, var(--surface-alt));`)
- **What the user sees**: Nothing today — both always resolve to their fallback values — but it's evidence the component was authored against a token vocabulary (`--surface-raised`, `--surface-hover`) that does not exist anywhere else in the 130KB `style.css`, in any of the app's theme blocks.
- **Why it's wrong**: A component quietly depending on tokens nobody else defines is a maintenance trap: if the fallback syntax is ever "cleaned up" by someone who assumes the token is real, the workspace picker silently loses its background color in every theme.
- **Proposed fix**: Replace both with the actual tokens already used everywhere else (`var(--surface-soft)`, `var(--surface-alt)`), matching what they resolve to today.
- **Shared primitive it should use**: the existing `--surface-*` token set; no new tokens needed.

### A4-08 — Close-button glyph is inconsistent (`x` vs `×`)
- **Severity**: polish
- **Where**: `frontend/src/lib/modals/NotificationsModal.svelte:37` (`>x</button>`, lowercase Latin x) vs `frontend/src/App.svelte:8859` and `:8882` (tab close, `>×</button>`, U+00D7) and `frontend/src/lib/workbench/WorkspaceWindowPicker.svelte:221` (dialog close, `>×</button>`, U+00D7).
- **What the user sees**: Two visually different glyphs doing the same job — the multiplication sign is wider and vertically centers differently than a lowercase letter — inside otherwise identically-styled `.icon-button`/`.close-button` boxes.
- **Why it's wrong**: Small, but it's exactly the kind of per-screen drift that adds up to "every section was built separately."
- **Proposed fix**: Standardize on the `×` (U+00D7) glyph already used everywhere else, or better, the app's existing SVG icon set.

### A4-09 — Tab-strip overflow has no visual cue
- **Severity**: minor
- **Where**: `frontend/src/style.css:1630-1634` (`.tabs { flex-wrap: nowrap; overflow-x: auto; scrollbar-width: thin; }`)
- **What the user sees**: Once enough tabs are open that they don't fit, the strip simply becomes horizontally scrollable with a thin native scrollbar. There is no fade/gradient at the clipped edge and no ">>" overflow menu, so a user has no way to know more tabs exist unless they happen to scroll or use the ⌘1–9 shortcuts.
- **Why it's wrong**: VS Code and Chrome both signal overflow explicitly (an overflow chevron listing hidden tabs). Silent scroll-only overflow is easy to miss, especially since the scrollbar is `thin` and themed to blend into `--surface`.
- **Proposed fix**: Add a directional fade mask at each clipped edge (`mask-image: linear-gradient(...)`) at minimum; consider an overflow "▾" menu listing off-screen tabs for parity with VS Code.

### A4-10 — No drag-to-reorder, no middle-click-to-close
- **Severity**: major
- **Where**: `frontend/src/App.svelte:8842-8886` — no `draggable` attribute, no `dragstart`/`dragover`/`drop` handlers, and no `onauxclick`/middle-click handling anywhere in the tab markup or its surrounding script.
- **What the user sees**: Tabs can only be reordered by closing and reopening in a different order (there is no reorder at all), and the only way to close a tab is the small 20px × button or ⌘W — middle-click, the near-universal browser/editor convention, does nothing.
- **Why it's wrong**: These are two of the most basic tab-strip conventions carried over from every browser and from VS Code/Postman/Bruno. Their absence is a large part of why the tab strip "doesn't feel like" the rest of the API-client landscape the user is used to.
- **Proposed fix**: Add HTML5 drag-and-drop reordering within `workbenchTabs`, persisted via the same tab-lifecycle plumbing already used for close (`lib/workbench/tabLifecycle.ts`), and an `onauxclick` handler on `.tab` that triggers the same `beginTabLifecycleAction('close-active', tab.id)` used by the × button.

### A4-11 — Command bar breakpoints don't align with the shell's own reflow breakpoint
- **Severity**: minor
- **Where**: `frontend/src/lib/workbench/WorkspaceCommandBar.svelte:221,226,232` (`@media (max-width: 1180px)`, `800px`, `610px`) vs the shell's own responsive collapse at `frontend/src/style.css:5831` (`@media (max-width: 960px)`, where the sidebar becomes an overlay and `.request-workbench` stacks vertically).
- **What the user sees**: Between roughly 960–1180px width, the command bar has already started hiding labels (`.cookie-button span`, `.run-button span`, etc. at `WorkspaceCommandBar.svelte:223`) while the sidebar/workbench layout underneath hasn't changed shape yet, and vice versa below 960px where the sidebar flips to an overlay while the command bar's own second breakpoint (800px) hasn't triggered. The chrome and the content it sits above are reflowing on unrelated schedules.
- **Why it's wrong**: Five different hardcoded breakpoints (1180, 960, 800, 680, 610) are scattered across `WorkspaceCommandBar.svelte` and `style.css` with no shared source of truth, so the shell can visibly reflow in stages that don't correspond to each other.
- **Proposed fix**: Define the shell's breakpoints once (e.g. as CSS custom properties or a documented set: compact/medium/wide) and have both the command bar and the workbench grid key off the same values.

### A4-12 — Dead responsive rule for `.topbar`
- **Severity**: polish
- **Where**: `frontend/src/style.css:5875` (`@media (max-width: 960px) { .topbar { grid-template-columns: 1fr; } ... }`) vs the base `.topbar` rule at `frontend/src/style.css:1508-1516`, which is `display: flex; flex-direction: column;`, not a grid.
- **What the user sees**: Nothing — the rule is a no-op, since `grid-template-columns` has no effect on a flex container.
- **Why it's wrong**: It's small, but it's a leftover from an earlier layout (`.topbar` was presumably a grid at some point) that nobody removed. It's exactly the kind of drift that accumulates when a shell is built up section by section rather than as one system — worth a pass to find its siblings.
- **Proposed fix**: Delete the dead rule (or restore the intended effect if `.topbar` was meant to reflow at 960px in some other way).

## Cross-cutting primitives this area needs

- **`TabStrip` / roving-tabindex helper** — one accessible tabs implementation (ARIA roles, arrow-key nav, drag-reorder, middle-click-close, overflow affordance, dirty-dot) shared by the workbench tab strip, the response subtabs, and the `.tabs.compact` toggle group instead of three separate hand-rolled versions.
- **`ResizeHandle`** — one visual/interactive treatment (idle seam, hover/focus/drag highlight, keyboard step) for every divider in the shell: sidebar, response split, devtools drawer.
- **Single dialog shell** — `Modal.svelte` already exists and is well-designed; every dialog, including `WorkspaceWindowPicker`, should be built on it rather than reimplementing focus-trap/Escape/inert/spinner logic per dialog.
- **One icon vocabulary** — either the existing SVG stroke-icon set (used well in `WorkspaceCommandBar`) or a documented small glyph set, but not both interchangeably for the same command (orientation toggle, close buttons).
- **A single global `:focus-visible` rule** for interactive shell controls, so keyboard focus looks the same everywhere instead of three different treatments.
- **A shared breakpoint scale** (e.g. `--bp-compact`, `--bp-medium`) referenced by both the command bar and the workbench grid, instead of five independently-chosen pixel values.

## Token audit

Hardcoded / non-token values found in the shell that should be tokens:

| File:Line | Value | Should be |
|---|---|---|
| `lib/workbench/WorkspaceCommandBar.svelte:179,181` | `gap: 8px; padding: 5px 8px;` | `var(--space-8)` |
| `lib/workbench/WorkspaceCommandBar.svelte:186` | `gap: 3px;` | `var(--space-3)` |
| `lib/workbench/WorkspaceCommandBar.svelte:197` | `padding: 4px 7px;` | `var(--space-4) var(--space-7)` |
| `lib/workbench/WorkspaceCommandBar.svelte:206` | `width: 16px; height: 16px; stroke-width: 1.6;` | raw icon sizing, no token exists — worth adding an `--icon-size-16` token given how often 16px SVGs recur across the shell |
| `lib/workbench/WorkspaceCommandBar.svelte:207,210` | `max-width: 150px; width: min(150px, 16vw);` | not tokenized (acceptable as a one-off max-width, but note it duplicates in two places) |
| `lib/workbench/WorkspaceCommandBar.svelte:220` | `min-width: 14px; padding: 1px 3px; font-size: 9px;` | `var(--space-*)`, `var(--font-size-9)` (the font-size token exists and isn't used here) |
| `lib/workbench/WorkspaceWindowPicker.svelte:341,348` | `padding: clamp(12px, 3vw, 28px); max-height: min(680px, calc(100dvh - 2 * clamp(12px, 3vw, 28px)));` | raw px, no token |
| `lib/workbench/WorkspaceWindowPicker.svelte:353` | `border-radius: 12px;` | `var(--radius-12)` (token exists, unused here) |
| `lib/workbench/WorkspaceWindowPicker.svelte:365,373-379` | `padding: 18px 18px 14px; font-size: 10px/18px;` | `var(--space-*)`, `var(--font-size-*)` |
| `lib/workbench/WorkspaceWindowPicker.svelte:393-406` | `width: 30px; height: 30px; border-radius: 7px; font-size: 21px;` | `var(--radius-7)` exists and is unused; 21px close-glyph size is unique to this file |
| `lib/workbench/WorkspaceWindowPicker.svelte:449-466` | `max-height: min(52dvh, 420px); gap: 6px; padding: 12px 14px; grid-template-columns: 34px ...; min-height: 56px; padding: 8px 10px; border-radius: 9px;` | `var(--space-*)`, `var(--radius-8)`/`var(--radius-10)` |
| `lib/workbench/WorkspaceWindowPicker.svelte:466,474` | `var(--surface-raised, var(--surface-soft))`, `var(--surface-hover, var(--surface-alt))` | these two custom properties do not exist in `style.css` — replace with the real tokens directly (see A4-07) |
| `lib/workbench/WorkspaceWindowPicker.svelte:484-495` | `width: 32px; height: 32px; border-radius: 8px; font-size: 13px;` | `var(--radius-8)` exists and is unused |
| `lib/workbench/WorkspaceWindowPicker.svelte:343` | `backdrop-filter: blur(7px) saturate(0.88);` | unique to this file; no other dialog uses `backdrop-filter` — either standardize it as a token/mixin or drop it so all dialogs match |
| `lib/workbench/WorkspaceWindowPicker.svelte:338` | `z-index: 65;` | every other dialog (via `Modal.svelte`'s `.prompt-backdrop`) uses `z-index: 50` (`style.css:3779`) — inconsistent stacking context |
| `lib/workbench/WorkspaceWindowPicker.svelte:553-566` | `min-height: 180px; padding: 28px; width/height: 38px; border-radius: 10px; font-size: 19px;` | `var(--space-*)`, `var(--radius-10)` (exists, unused) |
| `lib/workbench/WorkspaceWindowPicker.svelte:571-606` | full `.create-workspace` block: `padding: 11px 14px 12px; gap: 6px; font-size: 11px/12px/11.5px; border-radius: 7px;` | `var(--space-*)`, `var(--font-size-*)`, `var(--radius-7)` |
| `lib/workbench/WorkspaceWindowPicker.svelte:611-668` | `footer`/`.actions`/`.spinner` block: `padding: 12px 14px; gap: 14px; font-size: 10px/11.5px; height: 18px; width/height: 12px` | `var(--space-*)`, `var(--font-size-*)` |
| `lib/workbench/WorkspaceWindowPicker.svelte:659-668` | `.spinner`/`@keyframes workspace-picker-spin` | duplicate of `style.css:771-783` (`.loader`/`@keyframes spin`) — should reuse that instead of a second spin animation with different stroke geometry |
| `style.css:812-834` (`.sidebar-resizer`) | `width: 7px; left: calc(var(--sidebar-width, 312px) - 3px);` | no token for divider hit-width exists across the three dividers in the shell (sidebar, response split, devtools drawer) — worth a shared `--divider-hit-width` |
