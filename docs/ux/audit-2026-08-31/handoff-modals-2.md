# Handoff — A6 (modals), second pass

Implementer X1. Scope edited: `frontend/src/lib/modals/**` except `modals/search/**`,
`frontend/src/lib/views/mcp/McpApprovalModal.svelte`, and `frontend/test/modalConformance.test.mts`.

Findings closed: **A6-06, A6-07, A6-08, A6-10, A6-11, A6-12, A8-08, A8-09, A8-10**,
and the component half of **A6-09**. A6-01 through A6-05 were closed by the
earlier pass (`handoff-modals.md`) and are untouched.

---

## The conventions this pass decided

Stated here because the audit asked for one convention and the code cannot
express "one convention" on its own — three of the four are now pinned by
`test/modalConformance.test.mts` instead.

**1. Titles are Title Case, with no terminal punctuation.** The 26-of-32
majority. Small words (a, an, and, as, at, by, for, in, of, on, or, the, to,
with) stay lowercase unless they lead. A name that is lowercase stays lowercase
— "Generate grpcurl Command".

**2. A dialog phrased as a yes/no question keeps sentence case and its question
mark.** This is a real pattern, not the lapse the audit's 26/6 split makes it
look like: "Replace existing collections?" is being asked of you, and
"Replace Existing Collections?" reads as a headline instead. The three that
qualify are `ImportReplaceModal` and `McpApprovalModal`'s two subjects.

**3. Button labels are Title Case too** — "Mark All as Read", not "Mark all as
read"; "Disconnect Sync", not "Disconnect sync".

**4. The confirm button says the verb in its own title.** Clone X → Clone,
Rename X → Rename, Delete X → Delete, Remove X → Remove, Create X → Create, and
New X → Create, because a thing that does not exist yet cannot be "New"ed. This
is what makes A6-06 mechanical rather than remembered.

**5. One ellipsis character, `…` (U+2026).** Thirteen dialogs wrote
`'Renaming...'` while the panes one screen over wrote `'Running…'` and
`'Saving…'`. Three periods and U+2026 are different widths in every font the app
ships with, so the busy label visibly jumped depending on which dialog you had
opened.

**6. Width is a named step, never a pixel.** See A6-10 below.

---

## What changed, file by file

### A6-10 — the size scale (`Modal.svelte`, and 30 dialogs)

`Modal.svelte` gains `size: 'small' | 'medium' | 'large' | 'xlarge'`, mapped to
the audit's own four widths — 420 / 460 / 720 / 1060 — by four rules in the
shell's own scoped `<style>`.

The rules live in the component rather than in `style.css` for two reasons. The
dialog box is `Modal.svelte`'s own element, so a scoped rule reaches it and
nothing else; and Svelte's scoping hash makes `.modal-large` two classes against
`.code-generator-dialog`'s one, so a migrated dialog takes its width from the
step it named even while the old per-dialog rule is still sitting in `style.css`
waiting to be deleted. **There is therefore no window in which this half-lands
badly** — the component change is correct before and after the stylesheet
cleanup below.

The prop is deliberately `undefined` by default rather than `'medium'`. A default
would win over `.workspace-picker-dialog` by that same specificity margin and
silently narrow the picker from 580px to 460px. Absent the prop, nothing moves.

Applied as four `class:` directives, not one interpolated class name: Svelte
prunes scoped CSS it cannot statically match, and `class="… modal-{size}"` is
exactly the kind it cannot — the whole block would have compiled away and every
dialog would have quietly fallen back to 460. Verified in the built stylesheet:

```
.modal-small.svelte-10shf4z{width:min(420px,100%)}
.modal-medium.svelte-10shf4z{width:min(460px,100%)}
.modal-large.svelte-10shf4z{width:min(720px,100%)}
.modal-xlarge.svelte-10shf4z{width:min(1060px,100%)}
```

**Widths that actually move.** Ten dialogs render at a different width than
before. All of them are a step away from where they were, and a reviewer who
disagrees with one can move that dialog up a step without touching anything else:

| Dialog | was | now |
|---|---|---|
| `confirm/UnsavedTabsModal` | 520 | 460 (`medium`) |
| `openapi/SyncSettingsModal` | 540 | 460 (`medium`) |
| `confirm/ItemInfoModal` | 560 | 460 (`medium`) |
| `collection/GenerateDocsModal` | 560 | 460 (`medium`) |
| `codegen/GrpcurlCommandModal` | 760 | 720 (`large`) |
| `codegen/RequestCodeModal` | 760 | 720 (`large`) |
| `codegen/ResponseExampleCodeModal` | 760 | 720 (`large`) |
| `NotificationsModal` | 820 | 720 (`large`) |
| `openapi/SpecViewerModal` | 860 | 720 (`large`) |
| `openapi/SpecDiffModal` | 1120 | 1060 (`xlarge`) |

Unchanged in rendered width: everything at 460 (`medium`), `NewRequestModal` at
420 (`small`, losing only the `calc(100vw - 32px)` clamp, which the backdrop's
own 24px padding already covers), `ShareCollectionModal` at 720 (`large`) and
`OAuth2AuthorizationModal` at 1060 (`xlarge`).

**Not migrated**, and named in the test so migrating one means editing the list:
`search/CommandPaletteModal` and `search/GlobalSearchModal` (another owner this
wave) and `workbench/WorkspaceWindowPicker` (580px, load-bearing — it needs its
own step or its own justification).

### A6-11 — `aria-busy`

Sixteen dialogs declared a `busy` prop, disabled their buttons on it and never
told the shell. Each now forwards `busy={busy !== ''}`:
`CloneCollectionModal`, `CloneFolderModal`, `CloneRequestModal`,
`GenerateDocsModal`, `NewFolderModal`, `RenameCollectionModal`,
`RenameFolderModal`, `RenameRequestModal`, `ShareCollectionModal`,
`NotificationsModal`, `DeleteRequestModal`, `DeleteFolderModal`,
`DeleteFlowModal`, `RemoveCollectionModal`, `CreateExampleModal`,
`SyncSettingsModal`.

`busy !== ''` and not `busy === 'clone collection'` on purpose: `busy` is the
app-wide operation name, and every one of these dialogs already disables its
whole footer on `busy !== ''`. Forwarding the same expression makes what a
screen reader is told and what a sighted user can see the same fact. Forwarding
the narrower per-operation string would announce "not busy" over a dialog whose
every control is dead.

### A6-06 — the verb

- `collection/CloneCollectionModal`: `'Creating…' : 'Create'` → `'Cloning…' : 'Clone'`.
- `confirm/CreateExampleModal`: "Create Example" → "Create".
- `confirm/NewRequestModal`: "Create request" → "Create".
- `collection/ShareCollectionModal`: **"Proceed" → "Export"**. Not in the
  finding's text, but the conformance table flags it and it is the same defect:
  Proceed was the only confirm button in the app that named no action, while its
  own busy label already said "Exporting…" and the result panel above it says
  "Exported {filename}". The dialog used the word twice and the button was the
  one place it did not.

### A6-07 — Title Case

Titles: "New request" → "New Request", "Unsaved changes" → "Unsaved Changes",
"Bring your setup across" → "Bring Your Setup Across". Left as questions:
"Replace existing collections?" and `McpApprovalModal`'s two.

Buttons: "Import selected" → "Import Selected", "Trust this authority" → "Trust
This Authority", "Show/Hide collections" → "Show/Hide Collections", "Not now" →
"Not Now" (all `DiscoveryModal`); "Mark all as read" → "Mark All as Read",
"Clear all" → "Clear All" (`NotificationsModal`); "Disconnect sync" →
"Disconnect Sync" (`SyncSettingsModal`); "Replace collections" → "Replace
Collections" (`ImportReplaceModal`).

Headings inside a body, for the same reason: `DiscoveryModal`'s "API clients" and
"Certificate authority", `GenerateDocsModal`'s "Environments to include",
`ShareCollectionModal`'s "Bruno-compatible format".

**Two deliberate exceptions, both in `McpApprovalModal`.** "Allow once" and
"Allow and remember for this request in this environment" stay in sentence case.
They are not labels, they are the sentence the user is answering, and the second
is generated by `approvalRememberLabel` in `lib/mcpSettings.ts` — a file this
pass does not own, so Title-Casing the first would have split one row of three
buttons across two conventions. Left as it is on purpose; if the vocabulary
owner wants them cased, both strings move together.

### A6-08 — `DiscoveryModal`'s local styles

`0.9rem` / `0.8rem` / `0.75rem` → `var(--font-size-14)` / `var(--font-size-13)` /
`var(--font-size-12)`. Every `px` spacing value → the matching `--space-*`.
`var(--border, rgba(0, 0, 0, 0.12))` → `var(--border)`.

Also, beyond the finding: the two `opacity` rules became `color: var(--muted)`.
Faded text was a value nothing else in the app derives its greys from, `--muted`
is what `McpApprovalModal` and every other dialog reach for, and it is redefined
per theme where an opacity is not.

### A6-12 — Enter submits

`confirm/OAuth2AuthorizationModal`'s `<div class="oauth2-auth-controls">` is now
a `<form class="oauth2-auth-controls" on:submit|preventDefault=…>` and its
Submit Callback button is `type="submit"`. The class carries all the styling and
`.oauth2-auth-controls` selects by class, not tag, so nothing else moves; the
dialog's `grid-template-rows` still sees the same four children. "Open in System
Browser" stays `type="button"` so it cannot become the implicit submit.

**It was the only one.** Every other dialog that takes typed text already wraps
its fields in a form — checked, and now pinned by a test that walks every
`<input>` tag in every dialog. The two search dialogs are exempt and are not an
oversight: their input drives a live-filtered list where Return means "open the
highlighted result", which is a keydown on the list.

### A8-08 — `codegen/GrpcurlCommandModal`

`disabled={!generatedGrpcurlCommand}` on Copy, matching its two siblings.

### A8-09 — the "no environment colour" default

`collection/GenerateDocsModal` answered `#64748b` (grey) and the environment
editor answers `#2f8cff` (blue) for the identical state. The modal now answers
`#2f8cff`, because the editor's is the consequential one: its value is pre-filled
into an `<input type="color">`, so blue is what actually gets saved the moment
anyone touches that control. The swatch now predicts that rather than
contradicting it. See the App.svelte item below for tokenising the pair.

### A8-10 — the ellipsis

Twelve files swept from `...` to `…`. Pinned by a test that flags `...`
preceded by a word character, which is what separates a truncated label from
JavaScript spread syntax.

### A6-09 — `NotificationsModal` (component half only)

`dialogClass` is now `"prompt-dialog notification-modal"`. **This one is the
exception to "no bad half-landed state" above** — see the blocking `style.css`
item below.

### `frontend/test/modalConformance.test.mts`

Nine new assertions, extending the existing ten rather than living in a parallel
file. Each was mutation-checked — twelve mutations, twelve caught, each by the
assertion it was aimed at:

| Assertion | Mutation that must break it |
|---|---|
| every dialog takes its width from the named scale | drop `size` from one dialog |
| the size scale is defined once, in the shell | change a width; use a non-`class:` binding |
| a dialog that tracks busy tells the shell | drop `busy={…}` from one `<Modal>` |
| a title is Title Case, or a question in sentence case | restore "New request"; add a full stop |
| a confirm button says the verb its own title says | restore Clone Collection's "Create" |
| a dialog you can type into submits on Return | restore the OAuth2 `<div>` |
| the codegen family disables Copy | drop the grpcurl guard |
| every dialog writes an ellipsis as one character | restore `'Exporting...'` |
| local styles use the type scale, no token fallbacks | restore `0.9rem`; restore the `var(--border, …)` fallback |

One existing helper changed: `buttonRows` now matches `<footer class="button-row">`
as well as `<div>`. `NewRequestModal` writes its footer as a `<footer>`, so the
one dialog whose footer markup differs was the one dialog none of the footer
rules had ever been checked against.

---

## What I could NOT do — edits needed in files I do not own

### `frontend/src/style.css` — 1 blocking, then 12 deletions

**1. BLOCKING — must land with `NotificationsModal.svelte`.** `.prompt-dialog`
supplies `padding: var(--space-16)`; the notifications dialog is an edge-to-edge
list/detail grid whose sections pad themselves. Until this lands the dialog
renders with a 16px inset it does not want, and a 14px gap under its header.
Replace the rule at **`style.css:4462`** and add one line to **`:4474`**:

```css
.notification-modal {
  /* .prompt-dialog now supplies the border, radius, background, shadow and
     width; only this dialog's own layout is left. Composing was A6-09: these
     four box properties were re-declared here to the same values, so restyling
     the shell meant finding four rules instead of one. */
  max-height: min(680px, calc(100vh - 48px));
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr);
  overflow: hidden;
  padding: 0;
}

.notification-modal > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-12);
  padding: var(--space-16);
  margin-bottom: 0;
  border-bottom: 1px solid var(--border);
}
```

One intended side effect: `.prompt-dialog h2` now reaches the Notifications
title, so it renders at `--font-size-18` like every other dialog title instead of
at the UA default. That is the point of composing.

**2. A6-09's other half, for the `search/**` owner.** `.global-search-modal`
(`style.css:4193`) does the same duplication for `GlobalSearchModal` and
`CommandPaletteModal`. It already sets `padding: var(--space-16)`, so this one
has no blocking pairing — the component change (`dialogClass="prompt-dialog
global-search-modal"`, `dialogClass="prompt-dialog global-search-modal
command-palette"`) can land whenever, and then:

```css
.global-search-modal {
  /* .prompt-dialog supplies border, radius, background, shadow and padding. */
  width: min(720px, 100%);
  max-height: min(680px, calc(100vh - 48px));
  overflow: hidden;
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr);
  gap: var(--space-12);
}
```

Keep the explicit `width` there until those two adopt `size="large"`; the sizes
test names both files and will tell whoever migrates them.

**3. Dead now that `size` decides width.** Every rule below is width-only and its
class is applied nowhere else, so the whole rule goes and the class is already
gone from the markup:

| `style.css` | rule | was |
|---|---|---|
| 4064 | `.code-generator-dialog` | 760 |
| 4068 | `.item-info-dialog` | 560 |
| 4781 | `.openapi-settings-dialog` | 540 |
| 4785 | `.openapi-spec-dialog` | 860 |
| 4789 | `.openapi-spec-diff-dialog` | 1120 |
| 5093 | `.generate-docs-dialog` | 560 |
| 5143 | `.share-collection-dialog` | 720 |

And these three classes carry other rules, so **only the `width` declaration**
dies — leave the rest:

| `style.css` | declaration to delete |
|---|---|
| 1923 | the whole one-liner `.compact-create-dialog { width: min(420px, calc(100vw - 32px)); }` — lines 1924-1926 stay |
| 4002 | `width: min(520px, 100%);` inside `.unsaved-tabs-dialog` (the rule then holds nothing; delete it, keep `.unsaved-tabs-dialog > p` at 4014) |
| 4121 | `width: min(1060px, 100%);` inside `.oauth2-auth-dialog` — keep the height, grid and overflow |

**4. `.discovery-dialog` never had a rule at all.** It was applied by
`DiscoveryModal` and matched nothing in the stylesheet — the same shape as the
`modal-footer` the first pass deleted. Already removed from the component; there
is nothing to delete in `style.css`.

**5. Still open from `handoff-modals.md`**, unchanged and not mine: the global
`button:focus-visible` ring (A4-06), and the decision about whether a real
`.button-row.modal-footer` treatment is wanted.

### `frontend/src/App.svelte` — three items, all optional

**1. A8-09, to finish it properly.** The two "no environment colour set"
call sites now agree on `#2f8cff` but each writes it out. One is
`<input type="color">`, which takes a hex attribute and cannot take a `var()`,
so the shared thing has to be a constant, not a token. Somewhere neutral —
`src/lib/environments.ts` or alongside `formatRuntimeBytes` — put:

```ts
/** What an environment with no colour set is drawn as, everywhere. */
export const defaultEnvironmentColor = '#2f8cff'
```

then in `App.svelte:11327`:

```svelte
value={selectedGlobalEnvironment.color || defaultEnvironmentColor}
```

and delete the local `const defaultEnvironmentColor` from
`GenerateDocsModal.svelte` in favour of the import. I could not create that
module: my ownership is `lib/modals/**`, and a constant App.svelte imports does
not belong under `lib/modals/`.

**2. Two now-dead bindings.** The first pass's four imperative `.focus()` calls
have landed as deleted — good — but two of the bindings they used have no
remaining reader:

- `App.svelte:642` `let tabLifecycleCancelButton = $state(…)` and
  `:11931` `bind:tabLifecycleCancelButton`
- `App.svelte:806` `let importReplaceConfirmationCancelButton = $state(…)` and
  `:11526` `bind:importReplaceConfirmationCancelButton`

Delete those four lines and the matching `export let` in
`UnsavedTabsModal.svelte` and `ImportReplaceModal.svelte` can go with them. They
have to move together — removing the prop first breaks the `bind:` I cannot
edit — so I left both props in place. `createResponseExampleInput` is **not** in
this list: `App.svelte:2475` still calls `.select()` on it.

**3. Nothing else.** No prop or callback signature changed in this pass; `size`
and `busy` are set inside each component, not passed from App.svelte, so every
existing call site compiles untouched.

---

## What the audit got wrong

1. **A6-11 undercounts.** The finding says "3 of 32 … though at least 14 others
   track a local busy flag". It is 16, not 14 — `NotificationsModal` and
   `SyncSettingsModal` are the two the list misses, and both disable footer
   buttons on `busy !== ''` exactly like the rest.

2. **A6-09's three dialogs are two problems, not one.** `.global-search-modal`
   already declares `padding`, so adding `prompt-dialog` to those two components
   is inert until someone deletes the duplicates. `.notification-modal` declares
   none, so the same change there is *not* inert — it is a blocking pair. The
   finding presents all three as the same one-line fix; only two of them are.

3. **A6-10's "12+ distinct widths" includes non-dialogs.** Of the 24
   `width: min(…)` declarations in `style.css` (a count that includes `max-width`
   and `min-width`), only 14 are dialog boxes; the
   rest are a switch control, a sidebar, an input min-width and the
   `.support-modal`/`.golden-modal` pair, which are not built on `Modal.svelte`
   at all and so are outside this scale. Worth knowing before anyone greps for
   the count.

4. **A6-06 names two files; the family is five.** Beyond Clone Collection's
   "Create", `CreateExampleModal` said "Create Example" and `NewRequestModal`
   said "Create request" while their sibling `NewFolderModal` said "Create" — so
   three different labels for the same button across four dialogs of one family,
   not one outlier. `ShareCollectionModal`'s "Proceed" is a fifth, filed in the
   conformance table but not in the finding.

5. **The footer-order rule had a hole nobody could have seen.** The first pass's
   `buttonRows` scanner matched `<div class="button-row">` only, and
   `NewRequestModal` writes `<footer class="button-row">` — so the dialog was
   silently exempt from the footer-order and destructive-styling assertions the
   whole time. It happens to be correct; that is luck, and the scanner is fixed.

6. **`.discovery-dialog` is a second `modal-footer`.** A6-05 found one class
   applied in four files with no CSS rule. `DiscoveryModal` had another one, in
   the same shape, and the finding did not catch it because it only looked at
   `modal-footer` by name. Worth a sweep: every class in a `dialogClass` string
   should resolve to a rule.

---

## Verification

Run from `frontend/`, with five implementers editing concurrently, so these
numbers are a snapshot — other owners' failures came and went between runs.
Final state at the time of writing:

- **`npm run check`** — 287 files, **0 errors, 0 warnings**, tree-wide. In
  particular no "Unused CSS selector" for the four `.modal-*` rules, which is
  what confirms Svelte kept them rather than pruning the size scale away.
- **`npm test`** — 1311 tests, **1310 pass, 1 fail**. The failure is another
  owner's: "every section routes its settings through the shared row primitive"
  (`test/preferencesRows.test.mts`). `test/modalConformance.test.mts` alone:
  **19/19**, and each of the nine new assertions was mutation-checked (twelve
  mutations, twelve caught, each by the assertion it was aimed at).
- **`npm run lint`** — 1 error, in
  `src/lib/views/preferences/CacheSection.svelte:63` (`$state` / local `state`
  rune conflict), another owner.
  `npx eslint src/lib/modals src/lib/views/mcp/McpApprovalModal.svelte
  test/modalConformance.test.mts` exits **0**.
- **`npx vite build`** succeeds, and the built stylesheet contains all four
  scoped size rules at the specificity the design depends on.
