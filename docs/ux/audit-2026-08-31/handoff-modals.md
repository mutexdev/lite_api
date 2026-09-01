# Handoff — A6 (modals) and A4-03 (workspace picker)

Implementer W-A. Scope edited: `frontend/src/lib/modals/**`,
`frontend/src/lib/workbench/WorkspaceWindowPicker.svelte`, plus one new test file.
`frontend/src/lib/views/mcp/McpApprovalModal.svelte` was read and deliberately left alone.

Findings closed: **A6-01, A6-02, A6-03, A6-04, A6-05, A4-03**, and — as a
consequence of rewriting the picker's `<style>` block — **A4-07** and the
picker's half of **A4-06** and **A4-08**.

---

## What changed, file by file

### A6-04 — the close button (29 files)

Every hand-written header close button is now
`<IconButton icon="close" label="Close" … />` from `src/lib/ui/IconButton.svelte`.
27 of them had the literal letter `x` as their only content, so their computed
accessible name was "x"; the two that used `×` (`NewRequestModal`,
`CommandPaletteModal`) had correct `aria-label`s but still a bare glyph. The
label is now supplied once, by the type, and is both the aria-label and the
tooltip.

`DiscoveryModal`, `NotificationsModal`, `codegen/GrpcurlCommandModal`,
`codegen/RequestCodeModal`, `codegen/ResponseExampleCodeModal`,
`collection/CloneCollectionModal`, `collection/CloneFolderModal`,
`collection/CloneRequestModal`, `collection/GenerateDocsModal`,
`collection/NewFolderModal`, `collection/RenameCollectionModal`,
`collection/RenameFolderModal`, `collection/RenameRequestModal`,
`collection/ShareCollectionModal`, `confirm/CreateExampleModal`,
`confirm/DeleteFlowModal`, `confirm/DeleteFolderModal`,
`confirm/DeleteRequestModal`, `confirm/GitNotFoundModal`,
`confirm/ItemInfoModal`, `confirm/NewRequestModal`,
`confirm/OAuth2AuthorizationModal`, `confirm/PromptDialogModal`,
`confirm/RemoveCollectionModal`, `openapi/SpecDiffModal`,
`openapi/SpecViewerModal`, `openapi/SyncSettingsModal`,
`search/CommandPaletteModal`, `search/GlobalSearchModal`.

Every `data-testid` on those buttons is preserved (`testId="modal-close-button"`).

Deliberate uniformity change: the label is `"Close"` on all 29, including the
seven that previously said `title="Cancel"` and the two that said
`aria-label="Close command palette"` / `"Close new request"`. One control, one
name.

### A6-01 — `confirm/RemoveCollectionModal.svelte`

`class="primary"` → `class="danger-button"` on Remove, matching its four
siblings.

### A6-02 — footer order

- `confirm/UnsavedTabsModal.svelte`: was danger, Cancel, primary → now
  **Cancel, Discard & Close, Save & Close**.
- `openapi/SyncSettingsModal.svelte`: was danger, Cancel, primary → now
  **Cancel, Disconnect sync, Save**.

`McpApprovalModal`'s Deny-first order is untouched; its own comments argue for
it and the new test excludes it by name.

### A6-03 — one initial-focus mechanism

`data-modal-autofocus` added to:

- `collection/RenameCollectionModal.svelte` — name input
- `collection/CloneCollectionModal.svelte` — name input
- `collection/CloneFolderModal.svelte` — folder-name input
- `confirm/CreateExampleModal.svelte` — example-name input
- `confirm/PromptDialogModal.svelte` — the **first** prompt input only
- `confirm/ImportReplaceModal.svelte` — Cancel button
- `confirm/UnsavedTabsModal.svelte` — Cancel button
- `workbench/WorkspaceWindowPicker.svelte` — Cancel button

### A6-05 — `modal-footer`

Removed from all four files (`ShareCollectionModal`, `UnsavedTabsModal`,
`ImportReplaceModal`, `SyncSettingsModal`). They now use the plain
`class="button-row"` that the other 28 dialogs use.

### A4-03 — `workbench/WorkspaceWindowPicker.svelte`

Rewritten onto `Modal.svelte`. Deleted from the component: its own backdrop
(`z-index: 65`, `backdrop-filter: blur(7px) saturate(0.88)`), `trapTab`,
`focusableElements`, the Escape branch of `handleDialogKeydown`, the
`onMount` focus-save/restore, `role="dialog"`/`aria-modal`/`aria-labelledby`/
`tabindex="-1"` on its own box, the 12px/7px radii, the "eyebrow" label, the
bespoke `button:focus-visible`, `@keyframes workspace-picker-spin`, and the
hand-written button/input styling that duplicates the global `button` and
`input` rules.

Kept local (Modal has no opinion on any of it): the listbox roving tabindex and
its Arrow/Home/End/Enter keys — now bound per option rather than to the dialog
box — the create-workspace form, the option row layout, and a 12px inline
spinner that now drives the app's existing `@keyframes spin` instead of a
private copy (`.loader` itself is a 36px page-level spinner and is the wrong
size inside a button label).

Also closed while in there:

- **A4-07** — `var(--surface-raised, …)` and `var(--surface-hover, …)`, the two
  tokens declared nowhere in `style.css`, replaced with the values they had
  always silently resolved to (`--surface-soft`, `--surface-alt`).
- **A4-06 (picker half)** — the bespoke `outline: 2px solid var(--focus)` ring
  is gone; the picker now looks like everything else. See the style.css item
  below for the rule that should replace it globally.
- **A4-08 / raw literals** — every spacing, radius and font-size in the file is
  now a `--space-*` / `--radius-*` / `--font-size-*` token.

The dialog title changed from "Open a workspace" to "Open a Workspace" (A6-07's
Title Case majority); the "New window" eyebrow is dropped because the
description line under the title already says the window part.

### `modals/Modal.svelte`

One line in `focusableItems()`: focusable candidates are now also filtered by
`el.tabIndex >= 0`. This surfaced when the picker moved onto the shell — a
roving-tabindex listbox makes every unselected option a
`<button tabindex="-1">`, which matches `button:not([disabled])` but is not
reachable by Tab, so the trap was computing its first and last stops from
elements the user can never land on. The picker's own trap had the identical
flaw, so this is not a regression it introduced; it is simply now fixable in
one place.

### `frontend/test/modalConformance.test.mts` (new)

Ten source-text assertions over all 33 dialogs, in the `readFileSync` style of
`brandMark.test.mts`: the sweep sees every dialog (guards against a glob that
finds nothing), every dialog imports the shell and declares no `role="dialog"`
of its own, no bare `x`/`×` button content, every close affordance is
`<IconButton icon="close" label="Close">`, every footer puts all neutral actions
before the primary/destructive ones, the five delete-family confirmations use
`.danger-button` and never `.primary`, the ten name-entry dialogs carry
`data-modal-autofocus`, the two Cancel-first dialogs mark their Cancel button,
`modal-footer` stays deleted, and the picker carries none of the shell code it
used to.

Each assertion was mutation-checked: reverting the corresponding fix in the
source makes the corresponding test fail.

---

## What I could NOT do — edits needed in files I do not own

### `frontend/src/App.svelte` — four now-redundant imperative focus calls

All four target exactly the element that now carries `data-modal-autofocus`, so
each is a no-op today and safe to delete.

1. **`App.svelte:2383`**, in `promptForVariables` — delete:

   ```ts
         window.setTimeout(() => document.querySelector<HTMLInputElement>('.prompt-dialog input')?.focus(), 0)
   ```

   `PromptDialogModal` now marks its first input. (This one is also the worst of
   the four: a *global* CSS query on a timer, which would find some other
   dialog's input the moment two dialogs overlap — a case `Modal.svelte`'s own
   teardown comment says it handles.)

2. **`App.svelte:2468`** — delete the `.focus()` line, keep the `.select()`:

   ```ts
       createResponseExampleInput?.focus()      // delete this line
       createResponseExampleInput?.select()     // keep: nothing else selects the suggested name
   ```

   The `createResponseExampleInput` binding must therefore stay (`App.svelte:1016`,
   `:12136`).

3. **`App.svelte:3826`**, in `requestPlannedImport` — delete:

   ```ts
         void tick().then(() => importReplaceConfirmationCancelButton?.focus({ preventScroll: true }))
   ```

   Then `let importReplaceConfirmationCancelButton` (`App.svelte:798`) and
   `bind:importReplaceConfirmationCancelButton` (`App.svelte:12014`) have no
   remaining reader, and the `export let importReplaceConfirmationCancelButton`
   prop in `ImportReplaceModal.svelte` can go with them. I left the prop in
   place because removing it would break the call site I cannot edit.

4. **`App.svelte:4371`** — delete:

   ```ts
       void tick().then(() => tabLifecycleCancelButton?.focus({ preventScroll: true }))
   ```

   Same follow-up: `App.svelte:634` and `App.svelte:12419` (`bind:tabLifecycleCancelButton`)
   and the matching prop in `UnsavedTabsModal.svelte` become dead.

### `frontend/src/style.css` — three rules

1. **Required for the picker to look right.** `.prompt-dialog` defaults to
   `width: min(460px, 100%)`; the picker was 580px and its option rows want it.
   Paste next to the other per-dialog width overrides (near
   `.unsaved-tabs-dialog`, ~line 3913):

   ```css
   .workspace-picker-dialog {
     width: min(580px, 100%);
   }
   ```

   Without it the picker still works, just narrower than it was.

2. **A6-05, optional.** `modal-footer` was applied in four files and matched no
   rule anywhere. I removed the class rather than invent a treatment. If a real
   footer treatment is wanted, define it once and let the eventual `ModalFooter`
   primitive apply it — do not reintroduce the bare class:

   ```css
   .button-row.modal-footer {
     justify-content: flex-end;
     margin-top: var(--space-16);
     padding-top: var(--space-12);
     border-top: 1px solid var(--border-subtle);
   }
   ```

3. **A4-06, for whoever owns the shell.** I deleted the picker's bespoke focus
   ring, so it now falls back to the browser default like the command bar. The
   single global rule the audit asks for still needs to land:

   ```css
   button:focus-visible,
   [role="tab"]:focus-visible,
   [role="option"]:focus-visible {
     outline: 2px solid var(--accent);
     outline-offset: -2px;
   }
   ```

### `frontend/src/lib/ui/` — a note, not an edit

`.icon-button` in `style.css` is 32px square; `IconButton`'s own
`.ui-icon-button` is 28px. The 29 modal close buttons therefore shrank by 4px.
That is the shared primitive's size and every other `IconButton` in the app
matches it, so I left it — flagging it only so it is not mistaken for a
rendering bug.

---

## What the audit missed

1. **A6-04's count is off by one.** 27 dialogs used the literal `x`, not 26 —
   the conformance table's "first focusable (×)" column reads as `×` for
   several dialogs that actually have the letter. There are 29 close buttons
   across 32 dialogs (three have none), all now converted.

2. **A6-03 says "four `App.svelte` imperative-focus call sites" but names
   three.** The fourth is `App.svelte:2383`, and it is a different and worse
   pattern than the other three: not `bind:this` + `.focus()` but
   `setTimeout(() => document.querySelector('.prompt-dialog input')?.focus(), 0)`
   — a global selector on a timer, resolved against whatever `.prompt-dialog` is
   in the document. Listed above as item 1.

3. **`Modal.svelte`'s focus trap mishandles roving tabindex.** Its
   `focusableSelector` collects `button:not([disabled])` regardless of
   `tabindex="-1"`, so a dialog containing a roving-tabindex list computes its
   Tab wrap points from elements Tab can never reach. No dialog hit this before
   because none had such a list; the picker does. Fixed in `Modal.svelte` (see
   above), but worth knowing when the audit's proposed `ModalFooter`/`ModalHeader`
   contract is built.

4. **The picker's `<ul role="listbox">` contains `<li role="presentation">`
   wrappers around `<button role="option">`.** That is valid, and untouched, but
   the option buttons are also `disabled` for the current workspace — a disabled
   `role="option"` is removed from the accessibility tree in some engines, so the
   "Current · already open" row may not be announced at all. Not investigated;
   out of scope for A4-03, but it is the kind of thing a keyboard pass should
   look at.

5. **A6-06 (Clone Collection says "Create") and A6-12 (OAuth2 Enter does
   nothing) were not in my task list and are untouched**, as are A6-07 through
   A6-11. A6-09's fix (`prompt-dialog` on `.global-search-modal` /
   `.notification-modal`) needs a `style.css` edit and so was out of reach too.

---

## Verification

Run from `frontend/`:

- `npm run check` — 0 errors, 0 warnings across my files. At the time of writing
  the tree-wide run reported one error in `src/lib/flowView.ts`, which belongs to
  another implementer working concurrently.
- `npm test` — `test/modalConformance.test.mts`: 10/10. Tree-wide, one failure in
  `test/flowView.test.mts`, same concurrent owner.
- `npm run lint` — clean over `src/lib/modals`, `src/lib/workbench/WorkspaceWindowPicker.svelte`,
  `src/lib/views/mcp/McpApprovalModal.svelte` and `test/`. Tree-wide there were
  errors in `src/lib/views/RunnerPanel.svelte`, same concurrent owner.
- `npx vite build` succeeds; the built CSS contains a single `@keyframes spin`
  and no `workspace-picker-spin`.
