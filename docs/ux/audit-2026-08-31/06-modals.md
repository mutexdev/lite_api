# A6 — Modals and dialogs

Scope: `frontend/src/lib/modals/**` (31 files, excluding the shared `Modal.svelte` shell) plus `frontend/src/lib/views/mcp/McpApprovalModal.svelte`. 32 dialogs audited.

## Summary

- All 32 dialogs render through the shared `Modal.svelte` shell — nothing skips it, and `App.svelte` has no leftover inline `role="dialog"` markup. That part of the extraction (US-036) is 100% conformant.
- `Modal.svelte` itself only supplies: focus trap, `inert` app-shell, Escape-to-close, backdrop-click-to-close, focus return, and the `aria-modal`/`aria-labelledby`/`aria-busy` attributes. It supplies **no** header, footer, size scale, or destructive variant — every caller hand-builds its own header markup, its own `<div class="button-row">` footer, and picks its own width. That is the single biggest source of drift.
- **One real destructive-styling bug**: `RemoveCollectionModal` styles its "Remove" button `class="primary"` (filled accent) instead of `class="danger-button"`, the only one of five delete/remove confirmations to do so.
- **Footer button order is actually disciplined**: 26 of 27 dialogs with a clear neutral/primary pair put the neutral action (Cancel/Close) on the left and the primary/danger action on the right. The one exception, `McpApprovalModal`, puts its primary button in the *middle* of three — deliberately, per its own code comments, for a security reason. Two other three-button dialogs (`SyncSettingsModal`, `UnsavedTabsModal`) put a *destructive* button first/leftmost, which is a different and less-defensible pattern.
- Initial focus is set three different, uncoordinated ways: an opt-in `data-modal-autofocus` attribute (7 dialogs), imperative `bind:this` + manual `.focus()` calls in `App.svelte` that bypass Modal's own mechanism entirely (4 dialogs), and "whatever is first in DOM order" for everyone else — which for 3 rename/clone dialogs means the dialog opens focused on the × close button instead of the name field.
- 26 of 32 close buttons use a literal `x` character as their only content, which becomes the button's accessible name ("x, button") because `title="Close"` is not read as the name when there's visible text content. Only 2 dialogs use a real `×` glyph plus an explicit `aria-label`.
- `class="modal-footer"` is applied in 4 files and has zero matching CSS rule anywhere in `style.css` — a dead class.
- 3 dialogs (`NotificationsModal`, `GlobalSearchModal`, `CommandPaletteModal`) don't compose with `.prompt-dialog` at all; they redefine border/radius/background/shadow from scratch, so a future shell restyle has to be applied in 4 places instead of 1.
- Dialog width is 12+ hand-picked pixel values (380–1120px) with no named size scale.
- Only 3 of 32 dialogs (`DiscoveryModal`, `UnsavedTabsModal`, `McpApprovalModal`) wire their local `busy` state through to Modal's `aria-busy`, though at least 14 others track a local busy flag and disable buttons during a save.

## Conformance table

| File | Uses Modal.svelte | Title style | Body layout | Footer button order | Primary label | Destructive styling | Busy → aria-busy | Backdrop-close | Initial focus | Width |
|---|---|---|---|---|---|---|---|---|---|---|
| `confirm/GitNotFoundModal.svelte` | yes | Title Case ("Git Required") | free text | Close (primary) only | Close | n/a | no | yes | first focusable (Close) | 460 |
| `codegen/GrpcurlCommandModal.svelte` | yes | Title Case | `<pre>` code block | Close, Copy(primary) | Copy | n/a | no | yes | first focusable (×) | 760 |
| `confirm/RemoveCollectionModal.svelte` | yes | Title Case | free text + `<code>` path | Cancel, Remove(**primary**) | Remove | **wrong — uses `.primary`, not `.danger-button`** | no | no | first focusable (×) | 460 |
| `confirm/ImportReplaceModal.svelte` | yes | sentence case + "?" | free text | Cancel, Replace collections(danger) | Replace collections | correct (`.danger-button`) | no | no | Cancel (manual focus, App.svelte:3809) | 460 |
| `confirm/PromptDialogModal.svelte` | yes | Title Case | dynamic label/input list | Cancel, Continue(primary) | Continue | n/a | no | yes | first focusable (×) | 460 |
| `confirm/DeleteRequestModal.svelte` | yes | Title Case | free text | Cancel, Delete(danger) | Delete | correct | no | no | first focusable (×) | 460 |
| `openapi/SpecViewerModal.svelte` | yes | Title Case | meta + `<pre>` | Close, Copy(primary) | Copy | n/a | no | yes | first focusable (×) | 860 |
| `confirm/DeleteFolderModal.svelte` | yes | Title Case | free text | Cancel, Delete(danger) | Delete | correct | no | no | first focusable (×) | 460 |
| `codegen/ResponseExampleCodeModal.svelte` | yes | Title Case + " - {name}" | select + `<pre>` | Close, Copy(primary) | Copy | n/a | no | yes | first focusable (×) | 760 |
| `collection/RenameCollectionModal.svelte` | yes | Title Case | form-grid (1 field) | Cancel, Rename(primary) | Rename | n/a | no | yes | **first focusable (×) — name field not autofocused** | 460 |
| `codegen/RequestCodeModal.svelte` | yes | Title Case | select + `<pre>` | Close, Copy(primary) | Copy | n/a | no | yes | first focusable (×) | 760 |
| `confirm/ItemInfoModal.svelte` | yes | Title Case ("Info") | `<table>` | **none — no footer** | — | n/a | no | yes | first focusable (×) | 560 |
| `confirm/CreateExampleModal.svelte` | yes | Title Case | form-grid (2 fields) | Cancel, Create Example(primary) | Create Example | n/a | no | yes | name field (manual focus, App.svelte:2451-2452) | 460 |
| `search/CommandPaletteModal.svelte` | yes | sentence case | live-filtered list | none (list is the body) | — | n/a | no | yes | search input (`data-modal-autofocus`) | 720 |
| `confirm/NewRequestModal.svelte` | yes | sentence case | form-grid + subtitle in header | Cancel, Create request(primary) | Create request | n/a | no | yes | name field (`data-modal-autofocus`) | 420 |
| `confirm/OAuth2AuthorizationModal.svelte` | yes | Title Case | iframe + inline controls | **no Cancel/Confirm pair** — "Open in System Browser" then "Submit Callback"(primary), inline in body | Submit Callback | n/a | no | no | first focusable (×) | 1060 |
| `confirm/DeleteFlowModal.svelte` | yes | Title Case | free text | Cancel, Delete(danger) | Delete | correct | no | no | first focusable (×) | 460 |
| `openapi/SyncSettingsModal.svelte` | yes | Title Case | form-grid + segmented control | **Disconnect sync(danger), Cancel, Save(primary)** — danger leftmost | Save | correct, but leftmost/first-tab-stop | no | yes | first focusable (×) | 540 |
| `openapi/SpecDiffModal.svelte` | yes | Title Case | meta + badges + diff grid | Close only | Close | n/a | no | yes | first focusable (×) | 1120 |
| `confirm/UnsavedTabsModal.svelte` | yes | sentence case | `<ul>` list | **Discard & Close(danger), Cancel, Save & Close(primary)** — danger leftmost, no header × | Save & Close | correct, but leftmost/first-tab-stop | **yes** | no | Cancel (manual focus, App.svelte:4354) | 520 |
| `search/GlobalSearchModal.svelte` | yes | Title Case | live-filtered list | none | — | n/a | no | yes | search input (`data-modal-autofocus`) | 720 |
| `NotificationsModal.svelte` | yes | Title Case | tabs + list/detail split | header utility buttons only ("Mark all as read" / "Clear all"), no Cancel/Confirm | — | n/a | no | yes | first focusable (×) | 820 |
| `collection/CloneCollectionModal.svelte` | yes | Title Case | form-grid (3 fields) | Cancel, Create(primary) | **Create** (inconsistent with sibling "Clone") | n/a | no | yes | **first focusable (×) — name field not autofocused** | 460 |
| `collection/GenerateDocsModal.svelte` | yes | Title Case | mixed prose + checkbox list | Cancel, Generate(primary) | Generate | n/a | no | yes | first focusable (×) | 560 |
| `collection/CloneFolderModal.svelte` | yes | Title Case | form-grid + toggle | Cancel, Clone(primary) | Clone | n/a | no | yes | **first focusable (×) — name field not autofocused** | 460 |
| `collection/RenameFolderModal.svelte` | yes | Title Case | form-grid + toggle | Cancel, Rename(primary) | Rename | n/a | no | yes | name field (`data-modal-autofocus`) | 460 |
| `collection/CloneRequestModal.svelte` | yes | Title Case | form-grid + toggle | Cancel, Clone(primary) | Clone | n/a | no | yes | name field (`data-modal-autofocus`) | 460 |
| `collection/RenameRequestModal.svelte` | yes | Title Case | form-grid + toggle | Cancel, Rename(primary) | Rename | n/a | no | yes | name field (`data-modal-autofocus`) | 460 |
| `collection/ShareCollectionModal.svelte` | yes | Title Case | format-card grid | Cancel/Close, Proceed(primary) | **Proceed** (vague vs. other modals' specific verbs) | n/a | no | yes | first focusable (×) | 720 |
| `collection/NewFolderModal.svelte` | yes | Title Case | form-grid + toggle | Cancel, Create(primary) | Create | n/a | no | yes | name field (`data-modal-autofocus`) | 460 |
| `DiscoveryModal.svelte` | yes | **casual sentence case** ("Bring your setup across") | sectioned prose + inline actions | "Not now" only (per-row Import/Trust buttons inline in body) | — | n/a | **yes** | yes | first focusable (×) | 460 |
| `views/mcp/McpApprovalModal.svelte` | yes | sentence case + "?" | prose sentence + optional list | **Deny, Allow once(primary), Allow and remember** — primary in the middle, no header × (deliberate) | Allow once | n/a (intentionally undecorated, see file comments) | **yes** | no | Deny (`data-modal-autofocus`, deliberate) | 460 |

## Findings

### A6-01 — RemoveCollectionModal styles its destructive action as primary, not danger
- **Severity**: critical
- **Where**: `frontend/src/lib/modals/confirm/RemoveCollectionModal.svelte:22`
- **What the user sees**: The "Remove" button in the Remove Collection dialog is rendered in the app's filled accent/blue color (`class="primary"`), identical in weight and color to every non-destructive "Save"/"Create"/"Rename" button in the app.
- **Why it's wrong**: Every sibling destructive confirmation — `DeleteRequestModal.svelte:25`, `DeleteFolderModal.svelte:26`, `DeleteFlowModal.svelte:54`, `ImportReplaceModal.svelte:27` — uses `class="danger-button"` (outlined, `var(--danger)` text/border). RemoveCollectionModal is the only one of five that doesn't, so it's also the only delete-family action that looks identical to "Save".
- **Proposed fix**: Change `class="primary"` to `class="danger-button"` on line 22.
- **Shared primitive it should use**: a `destructive` variant on the eventual footer-button primitive (see Modal contract below), so this can't be hand-typed wrong per file again.

### A6-02 — Destructive button is first in DOM/tab order in two 3-button dialogs
- **Severity**: major
- **Where**: `frontend/src/lib/modals/confirm/UnsavedTabsModal.svelte:49-54` (Discard & Close), `frontend/src/lib/modals/openapi/SyncSettingsModal.svelte:57` (Disconnect sync)
- **What the user sees**: In both dialogs the footer reads left-to-right as **danger, cancel, confirm** — the opposite of the "safe action first" idea `McpApprovalModal`'s own comments articulate for Deny. In UnsavedTabsModal there is also no header × button, so the destructive "Discard & Close" button is the very first focusable element in the dialog.
- **Why it's wrong**: Only an imperative override in `App.svelte` keeps this safe today — `App.svelte:4354` calls `tabLifecycleCancelButton?.focus(...)` after the dialog mounts, overriding Modal.svelte's own default (which would otherwise focus the first focusable element, i.e. the danger button). That override is easy to lose in a future refactor (it already isn't the modal's own responsibility), and even with it in place, Tab from the Cancel button one more time lands back on Discard & Close before Save & Close, and Shift+Tab from Discard & Close wraps to Save & Close — a destructive action is always one keystroke from either neutral button in this dialog's tab cycle.
- **Proposed fix**: Reorder both footers to Cancel, [danger], [primary] (Cancel first, matching all other dialogs), or if the danger action must lead visually, use `data-modal-autofocus` on the Cancel button directly inside the component instead of relying on a parent-side `bind:this` + `tick().then(focus)` pattern.
- **Shared primitive it should use**: a fixed three-slot footer (neutral, [optional destructive], primary) in the Modal contract, so component authors can't accidentally reorder these.

### A6-03 — Three different, uncoordinated initial-focus mechanisms
- **Severity**: major
- **Where**: compare `frontend/src/lib/modals/collection/RenameFolderModal.svelte:32` (`data-modal-autofocus` on the name input) with `frontend/src/lib/modals/collection/RenameCollectionModal.svelte:23-28` (same interaction, no such attribute) and `frontend/src/App.svelte:2451-2452` / `:3809` / `:4354` (imperative `.focus()` calls that bypass Modal.svelte's mechanism entirely for `CreateExampleModal`, `ImportReplaceModal`, `UnsavedTabsModal`)
- **What the user sees**: Opening "Rename Folder", "Rename Request", or "Clone Request" puts the cursor straight in the name field, ready to type. Opening "Rename Collection", "Clone Collection", or "Clone Folder" — the exact same kind of dialog — puts focus on the × close button instead; the user has to click or Tab into the field before typing.
- **Why it's wrong**: `Modal.svelte:108-119` already implements one canonical opt-in (`data-modal-autofocus`) specifically so a dialog can "name its own first field." Three collection dialogs (`RenameCollectionModal`, `CloneCollectionModal`, `CloneFolderModal`) simply never adopted it, while three other structurally-identical dialogs (`RenameFolderModal`, `CloneRequestModal`, `RenameRequestModal`, `NewFolderModal`, `NewRequestModal`) did. Meanwhile a second, separate mechanism — binding the field/button via `bind:this` and calling `.focus()` from `App.svelte` after `tick()` — is used for three more dialogs, duplicating what the shell already offers.
- **Proposed fix**: Add `data-modal-autofocus` to the name input in `RenameCollectionModal.svelte`, `CloneCollectionModal.svelte`, and `CloneFolderModal.svelte`; migrate the three `App.svelte` imperative-focus call sites (`CreateExampleModal`'s name input, `ImportReplaceModal`'s Cancel button, `UnsavedTabsModal`'s Cancel button) onto `data-modal-autofocus` inside their own components instead.
- **Shared primitive it should use**: `data-modal-autofocus` as the *only* sanctioned initial-focus mechanism; delete the imperative pattern.

### A6-04 — Close-button accessible name is the literal letter "x" in 26 of 32 dialogs
- **Severity**: major
- **Where**: e.g. `frontend/src/lib/modals/confirm/DeleteRequestModal.svelte:17`, `frontend/src/lib/modals/collection/RenameFolderModal.svelte:27`, and 24 more (full list: every file in the "x" column of the conformance table)
- **What the user sees**: Nothing visually broken — the × renders fine. But a screen reader announces this control as "x, button", not "Close, button" or "Cancel, button".
- **Why it's wrong**: `<button class="icon-button" title="Close" ...>x</button>` has visible text content ("x"), and per the accessible-name computation, visible text content wins over `title` — `title` only becomes the accessible name when the element has none. So the `title="Close"`/`title="Cancel"` attribute is a mouse-hover tooltip only; it does not reach assistive technology as the button's name here. Only `CommandPaletteModal.svelte:36` (`aria-label="Close command palette"`) and `NewRequestModal.svelte:34` (`aria-label="Close new request"`) get this right — and they're also the only two using a real `×` glyph instead of the letter "x".
- **Proposed fix**: Add `aria-label` (matching the existing `title` text, e.g. `aria-label="Cancel"` / `aria-label="Close"`) to all 26 remaining icon-buttons, and standardize the glyph to `×` (U+00D7) instead of the letter `x` for the 26 that use it.
- **Shared primitive it should use**: fold the close button into Modal.svelte's own header slot/primitive (see contract below) so the aria-label and glyph are supplied once, not per file.

### A6-05 — `.modal-footer` is a dead CSS class
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/collection/ShareCollectionModal.svelte:127`, `frontend/src/lib/modals/confirm/UnsavedTabsModal.svelte:48`, `frontend/src/lib/modals/confirm/ImportReplaceModal.svelte:25`, `frontend/src/lib/modals/openapi/SyncSettingsModal.svelte:56`
- **What the user sees**: No visible difference — the class does nothing.
- **Why it's wrong**: `grep -rn "modal-footer" frontend/src/style.css` returns zero matches. Four files apply `class="button-row modal-footer"` while the other 28 apply plain `class="button-row"`, suggesting a footer-specific style existed at some point (or was planned) and either got removed from `style.css` without cleanup, or was copy-pasted forward from one file to the next without ever being defined.
- **Proposed fix**: Either delete `modal-footer` from these four files, or — better — give the *actual* footer primitive in the Modal contract a real, defined class and apply it everywhere instead of `button-row`.
- **Shared primitive it should use**: the Modal contract's own `<footer>` slot.

### A6-06 — Verb/label mismatch across the Clone/Create/New family
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/collection/CloneCollectionModal.svelte:88` (button text "Create"/"Creating…") vs. `frontend/src/lib/modals/collection/CloneFolderModal.svelte:92` and `frontend/src/lib/modals/collection/CloneRequestModal.svelte:100` (button text "Clone"/"Cloning…")
- **What the user sees**: The "Clone Collection" dialog's confirm button says "Create", while the near-identical "Clone Folder" and "Clone Request" dialogs say "Clone" for the same conceptual action.
- **Why it's wrong**: Same title verb ("Clone X"), same body layout, same busy-label pattern (`'clone X' ? 'X-ing…' : 'Verb'`), different verb on the button itself. A user who clones a folder then a collection sees the button text change for no reason.
- **Proposed fix**: Change `CloneCollectionModal`'s primary label (and its busy-state string, currently `'Creating...'`) to "Clone"/"Cloning…" to match its siblings.

### A6-07 — Title case is inconsistent, including between direct siblings
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/confirm/NewRequestModal.svelte:31` ("New request", sentence case) vs. `frontend/src/lib/modals/collection/NewFolderModal.svelte:26` ("New Folder", Title Case)
- **What the user sees**: Opening "New Folder" from the sidebar and "New Request" right after shows two different capitalization conventions for what the user perceives as the same family of action.
- **Why it's wrong**: 26 of 32 dialog titles use Title Case; 6 use sentence case (`CommandPaletteModal` "Command palette", `NewRequestModal` "New request", `UnsavedTabsModal` "Unsaved changes", `ImportReplaceModal` "Replace existing collections?", `DiscoveryModal` "Bring your setup across", `McpApprovalModal` "Contact a new destination?" / "Let a flow step use a secret?"). Two of those six additionally end in a question mark, a punctuation style no Title Case dialog uses.
- **Proposed fix**: Pick one convention (Title Case, no terminal punctuation, is the 26-dialog majority) and apply it everywhere; reserve a question-mark title specifically for confirmations that are phrased as a yes/no question (which is a legitimate, distinct pattern worth keeping *consistently*, not mixing with declarative Title Case titles).

### A6-08 — DiscoveryModal's local styles don't use the design-token scale
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/DiscoveryModal.svelte:191-239`
- **What the user sees**: Section headings, paths, and guidance text in the first-run "Bring your setup across" dialog render at font sizes that are subtly off from the rest of the app's typographic rhythm.
- **Why it's wrong**: `style.css:26-36` defines a closed, px-based scale (`--font-size-9` through `--font-size-24`). DiscoveryModal's component-scoped `<style>` block instead hardcodes `0.9rem`, `0.75rem`, `0.8rem` (lines 200, 217, 224) and a raw fallback color `var(--border, rgba(0, 0, 0, 0.12))` (line 194) — none of which map cleanly onto a token (`0.9rem` = 14.4px, between `--font-size-14` and `--font-size-16`; `0.8rem` = 12.8px, between `--font-size-12` and `--font-size-13`). Contrast this with `McpApprovalModal.svelte:211-272`, the only other dialog with a local `<style>` block, whose own comment explicitly explains why it reaches for `var(--font-size-12/13)`, `var(--space-*)`, and `var(--warning-*)` tokens exclusively rather than inventing new values.
- **Proposed fix**: Replace the three `rem` font-size values with the nearest `--font-size-*` token, and the `rgba(0,0,0,0.12)` fallback with `var(--border)` alone (Modal.svelte already guarantees `--border` is defined wherever a dialog renders).
- **Shared primitive it should use**: none needed beyond following the existing token scale — this is a "use what's already there" fix, not a new primitive.

### A6-09 — Three dialogs don't compose with `.prompt-dialog`
- **Severity**: minor
- **Where**: `frontend/src/style.css:3993-4005` (`.global-search-modal`, used by both `GlobalSearchModal.svelte` and `CommandPaletteModal.svelte`) and `frontend/src/style.css:4262-4270` (`.notification-modal`)
- **What the user sees**: No visible difference today — these three currently look right.
- **Why it's wrong**: Every other dialog gets its box treatment (border, border-radius, background, box-shadow, base padding) by extending `.prompt-dialog` via `dialogClass="prompt-dialog <extra-class>"`. These three instead set `dialogClass="global-search-modal"` / `dialogClass="notification-modal"` with no `prompt-dialog` in the class list at all, so their CSS fully re-declares the same properties from scratch. If the shared shell's radius, shadow, or border color ever changes, three separate rules need to be found and updated by hand instead of one.
- **Proposed fix**: Add `prompt-dialog` to both `dialogClass` strings and drop the duplicated `border`/`border-radius`/`background`/`box-shadow` declarations from `.global-search-modal` and `.notification-modal`, keeping only their width/grid overrides.

### A6-10 — No named size scale for dialog width
- **Severity**: minor
- **Where**: `frontend/src/style.css` — 12+ distinct `width: min(...)` declarations across dialog classes (420, 460, 520, 540, 560, 720, 760, 820, 860, 1060, 1120px)
- **What the user sees**: Nothing broken, but widths look arbitrary next to each other — e.g. `ItemInfoModal` (a two-row table) is 560px, wider than `RenameFolderModal`'s full form (460px), while `ShareCollectionModal`'s card grid and `CommandPaletteModal`'s single search input are both exactly 720px by coincidence rather than by rule.
- **Proposed fix**: Define 3-4 named size tokens (e.g. `sm` 420px, `md` 460px — the default, `lg` 720px, `xl` 1060px+) and have each dialog opt into one instead of inventing a pixel value.

### A6-11 — `aria-busy` is wired for 3 of 32 dialogs despite ~14 having async saves
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/DiscoveryModal.svelte:75`, `frontend/src/lib/modals/confirm/UnsavedTabsModal.svelte:23`, `frontend/src/lib/views/mcp/McpApprovalModal.svelte:99` pass `busy` to `<Modal busy={...}>`; e.g. `frontend/src/lib/modals/collection/RenameCollectionModal.svelte:33`, `frontend/src/lib/modals/collection/CloneCollectionModal.svelte:87`, `frontend/src/lib/modals/confirm/DeleteRequestModal.svelte:28` track a local `busy: string` prop, disable their submit button on it, but never forward it to `Modal`'s `aria-busy`
- **Why it's wrong**: A screen reader user gets no `aria-busy` signal while these ~14 dialogs are saving/deleting/renaming — only the button's `disabled` state changes, which isn't announced as "busy."
- **Proposed fix**: Forward each dialog's existing `busy` value into `<Modal busy={...}>`; this is a one-line addition to `<Modal ...>` per file since the state already exists everywhere it's needed.

### A6-12 — OAuth2 callback field doesn't submit on Enter
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/confirm/OAuth2AuthorizationModal.svelte:48-53`
- **What the user sees**: After pasting the OAuth2 redirect URL into the "Callback URL" field, pressing Enter does nothing; the user must reach for the mouse and click "Submit Callback".
- **Why it's wrong**: 16 of 32 dialogs wrap their content in `<form on:submit|preventDefault={...}>` so Enter-in-field submits the primary action (e.g. every Rename/Clone/New dialog). OAuth2AuthorizationModal is not one of them and has no `on:keydown` fallback either, so it's the one text-entry dialog in the app where Enter is silently inert.
- **Proposed fix**: Wrap the callback-URL field (and the "Submit Callback" button) in a `<form on:submit|preventDefault={submitOAuth2CallbackURL}>`, matching every other single-field confirm dialog.

## Cross-cutting primitives this area needs

1. **A `ModalHeader` primitive** — `<h2>` + close button, with the aria-label/glyph/close-handler wiring done once instead of retyped 32 times (this alone would have prevented A6-04).
2. **A `ModalFooter` primitive** with a fixed slot order (neutral → [destructive] → primary) and a real, defined CSS class — retiring the currently-dead `modal-footer` class (A6-05) and preventing reordering mistakes like A6-02.
3. **A `variant="destructive"` prop on the primary/confirm button primitive** so "danger-button vs primary" (A6-01) is a typed choice, not a string literal that can be typo'd.
4. **A named size scale** (`size="sm" | "md" | "lg" | "xl"`) replacing hand-picked pixel widths (A6-10), with `.prompt-dialog` as the mandatory base every dialog composes with, closing the three dialogs that currently don't (A6-09).
5. **One sanctioned initial-focus mechanism** — `data-modal-autofocus`, already built into `Modal.svelte` — with the three dialogs missing it (A6-03a) and the three dialogs bypassing it via `App.svelte` imperative focus (A6-03b) both migrated onto it.
6. **A copy-style guide entry**: Title Case, no terminal punctuation, for declarative dialog titles; sentence case + "?" reserved specifically for yes/no confirmation phrasing — applied consistently instead of the current 26/6 split (A6-07), and paired with a verb-choice convention (Create vs. Clone vs. Rename, matching the invoking title) to close gaps like A6-06.

## Proposed Modal contract

`Modal.svelte` should keep owning what it already owns well (focus trap, `inert`, Escape, backdrop-click, focus return, `aria-modal`/`aria-labelledby`/`aria-describedby`) and additionally own:

- **Header**: a `header` snippet/prop taking a title string (rendered as `<h2>`, Title Case enforced by convention/lint, no terminal punctuation unless the dialog is phrased as a question) and an optional subtitle line. The close button (× glyph, `aria-label` derived from the title, e.g. `Close ${title}`) is rendered by Modal itself, not passed in — this is what would have made A6-04 structurally impossible. A dialog that must not offer a close affordance (like `McpApprovalModal`) opts out with an explicit `showClose={false}`, not by simply omitting a `<header>` close button by hand.
- **Body**: unchanged — a plain slot, since body layout (form-grid, table, prose, list) is legitimately dialog-specific and shouldn't be constrained.
- **Footer**: a `footer` prop/snippet accepting an ordered list of button descriptors (`{ label, kind: 'neutral' | 'primary' | 'destructive', onClick, disabled, type }`), always rendered neutral-first, primary-or-destructive-last, in one shared `<footer class="button-row">` — removing the ad hoc `<div class="button-row">` / `<div class="button-row modal-footer">` split (A6-05) and making the button-order convention mechanical rather than remembered (A6-02).
- **Size**: a `size="sm" | "md" | "lg" | "xl"` prop mapped to the token widths in A6-10, replacing free-form `dialogClass` width classes; `dialogClass` remains available only for layout-specific overrides (grid templates, max-height), not box treatment.
- **Destructive variant**: expressed only through the footer descriptor's `kind: 'destructive'` (→ `.danger-button`), never through a raw `class="primary"` on a hand-written button — this is the structural fix for A6-01.
- **Busy state**: `Modal` already accepts `busy`; every dialog with a local busy flag should be required (by convention, ideally by a lint rule scanning for a local `busy`/`disabled` pattern without a matching `<Modal busy=...>`) to forward it, closing A6-11.
- **Initial focus**: `data-modal-autofocus` remains the only sanctioned mechanism; the imperative `bind:this` + `App.svelte`-side `.focus()` pattern (A6-03b) should be deleted once the 3 call sites move the attribute into their own component.
