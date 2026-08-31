# Handoff — shell chrome and Preferences (implementer X4)

Scope worked: `frontend/src/lib/views/preferences/**`, `WorkspaceCommandBar.svelte`,
`RequestCommandStrip.svelte`, `CommandOverflowMenu.svelte`, `layout.ts`, `preferences.ts`,
`mcpSettings.ts`, plus new files under those directories and `frontend/test/`. Nothing outside
that was touched. Everything marked **PASTE** is a change in a file that belongs to someone else.

**Line numbers are not given for App.svelte.** They moved twice while I was working — the
preferences panel was at `:11889` when I started and `:11395` when I finished — so every paste
below is anchored on the text it replaces. Grep for the anchor, do not trust a number.

## Findings closed

| Finding | Severity | Status |
| --- | --- | --- |
| A4-02 one command, two controls, two icon languages | major | closed — `OrientationToggleButton.svelte`, both call sites |
| A1-02 per-request "Run" runs the whole collection and can no-op silently | critical | **closed in the component**; the empty-selection *disable* needs one PASTE line |
| A4-11 command-bar breakpoints unrelated to the shell's | minor | closed — one scale in `layout.ts`, enforced by `layout.test.mts` |
| A7-06 no panel navigation; the import "waterfall" | minor | closed — `PreferencesPanel.svelte`; needs PASTE 1 to be mounted |
| A7-08 subtitle summarises two arbitrary settings | polish | closed in `PreferencesPanel.svelte` (same PASTE) |
| A6-11 equivalent — async writes with no busy feedback | — | closed — `SettingRow` `busy`, used by Cache and MCP |

Deliberately unchanged: the instant-write save model (no Save/Cancel anywhere; still
test-enforced), `.keybindings-table-wrap`'s bounded 400px scroller, and every `data-testid` and
prop/callback signature `App.svelte` mounts.

---

## What the audit got wrong

### A7-06's headline claim is wrong twice over, and I measured both

> "opening Preferences repeatedly re-triggers 8 module-resolution promises", "8 sequential
> dynamic-import waterfalls"

**They were never sequential.** `{#await import(…)}` compiles to `$.await(node, () => import(…),
…)`. `$.await` calls that thunk inside `block()` (`svelte/src/internal/client/dom/blocks/await.js`),
and `block()` creates a `BLOCK_EFFECT` that runs as the parent fragment is built. All eight
`import()` calls went out in the same synchronous pass, in parallel. Confirmed by compiling the
markup and reading the output, not by reading the docs.

**There was no second-level waterfall either.** Every section chunk statically imports
`SettingRow`/`SettingSection`, which the bundler emits as one shared chunk — but Vite compiles
each dynamic import to `__vitePreload(…, __vite__mapDeps([…]))` carrying the **full transitive**
dep list, so the shared chunk is preloaded in the same tick as the section needing it. Verified in
the built bundle:

```
before.js:  e(()=>import(`./AppearanceSection-B_tdrccY.js`), __vite__mapDeps([0,1,2,3,4,5,6]))
                                                    indices 2 and 4 are SettingRow / SettingSection
```

I wrote a version that hoisted the two primitives into the `Promise.all` to "flatten the graph",
measured it, found it bought exactly nothing, and deleted it. That is recorded in the component's
comment so nobody re-adds it.

**What is actually true** is the audit's own phrase, "repopulates from a blank state" — but the
cause is not the imports. Eight *independent* await blocks settle independently, so the stack
painted in up to eight passes in chunk-arrival order, each insertion moving the scroll height
under the pointer; and each block re-enters its pending branch on every mount, so the panel
flashed empty on **every** reopen even though the modules had been resolved since the first one.
That is the thing fixed.

### A4-11 under-counts by one, and one of the rules it flagged was already dead

`RequestCommandStrip.svelte` had a sixth breakpoint (640px) the finding does not mention. Its
`@media (max-width: 1180px)` block set `grid-template-columns` to the **identical** value its own
base rule already declares — a rule that had never done anything since it was written. Deleted,
and `layout.test.mts` now fails if a media query restates the base grid unchanged.

### A4-07's ghost tokens are no longer ghosts

`--surface-hover` is now declared in `:root` (`style.css:124`, `var(--surface-alt)`) — landed from
the token pass while this wave was in flight. The A4-07 note in `04-shell-tabs-layout.md` is stale.

### A7-04's follow-up: the 720px preferences media block is dead

`SettingRow` was pinned to 720px "to stay in step with the block style.css already uses". Every
rule in that block targets an anatomy the row migration retired (see PASTE 3), so it is dead and
720 was in step with nothing. `SettingRow` now stacks at the shell's own 680.

---

## New files

**`frontend/src/lib/workbench/OrientationToggleButton.svelte`** — the one control for "change
response orientation". Holds the command bar's existing SVG (moved, not redrawn), the ⌘J hint, and
one label string used as both `title` and `aria-label`. The mark changes with the current
orientation, so the control has a visible state in both toolbars instead of being stateful in one
and static in the other.

**Why it is not `ui/IconButton.svelte`, and what `lib/ui/` needs.** `IconButton` takes an
`IconName`, and the shared set has no split-pane glyph — its nineteen names are
search/copy/format/chevrons/etc. Adding one is an edit to `lib/ui/`, which is another
implementer's. Rather than invent a *second* inline SVG beside the one that already exists, this
component holds the single copy. See PASTE 5 for the glyph, ready to move.

**`frontend/src/lib/views/preferences/PreferencesPanel.svelte`** — the panel shell: header and
subtitle (A7-08), the sticky section index (A7-06), and the one place the eight sections are
loaded. A `<script module>` block holds `PREFERENCE_SECTIONS`, `loadPreferenceSections()` and the
module-scope `settled` cache, so:

- first open — one `Promise.all`, one await block, one paint;
- every later open — `settled` is read synchronously, so the stack renders in the frame the panel
  mounts, with **no** promise and no await block at all.

Takes eight prop *bags* (`appearance`, `display`, …), each typed `ComponentProps<typeof Section>`,
rather than ~55 flat props. Restating the props would have been a second copy of eight prop lists
to keep in step — the exact duplication behind most of the A7 findings. Spreading one flat bag
into all eight was the other option and is worse: Svelte warns at runtime for every prop a
component was handed and does not declare, which here is roughly fifty warnings per section per
open.

**The index is a sticky strip above the column, not a rail beside it.** A left rail takes 180–200px
out of the content column at exactly the widths where `docs/qa/m2-root-fail-preferences-overflow.jpeg`
lived. A strip costs height, which this panel has, instead of width, which it does not. The 1024×768
fix is untouched.

---

## The A7-06 measurement

Method: compile `App.svelte`'s current preferences stack and `PreferencesPanel.svelte` with
`svelte/compiler` and count what the runtime actually creates. Pinned as a test, so the number
cannot regress silently (`preferencesRows.test.mts`, "the panel loads every section in one await
block, not eight").

| | before | after |
| --- | --- | --- |
| `{#await import(…)}` blocks in the panel | **8** | **1** |
| `$.await()` calls in the compiled output | **8** | **1** |
| `import()` expressions per **first** open | 8 | 8 |
| `import()` expressions per **later** open | 8 | **0** |
| await blocks entered per **later** open | 8 | **0** |
| paint passes for the section stack | up to **8**, chunk-arrival order | **1** |
| chunks fetched on first open | 8 sections + 1 shared, preload-flat | unchanged |
| section chunk bytes | 25,811 B | 26,483 B (+672 B, the busy affordance) |
| shared `SettingSection` chunk | 2,755 B | 3,184 B |

Read the two rows that matter as one sentence: the panel used to be assembled from up to eight
separate paints on **every** open, and is now assembled from one on the first open and zero on
every open after it. The byte cost of that is +1,101 B across nine already-deferred chunks.

Not claimed: a wall-clock number. There is no render harness in this repo and adding a DOM
library to get one was out of scope; the counts above are exact and the paints follow from them.

---

## File by file

- **`lib/workbench/layout.ts`** — added `SHELL_BREAKPOINTS` (`wide: 1180`, `medium: 960`,
  `compact: 680`) and `SHELL_BREAKPOINT_WIDTHS`. These are the numbers `style.css` already uses for
  the shell itself; the constant names the existing scale rather than inventing one. CSS cannot read
  it, so the components still write literals and `layout.test.mts` is the enforcement.
- **`WorkspaceCommandBar.svelte`** — orientation button replaced by the shared component; reads the
  orientation from `workspaceStore.preferences` rather than taking a prop, so it needs no App.svelte
  change and cannot disagree with the request strip. Breakpoints 1180/800/610 → 1180/960/680.
- **`RequestCommandStrip.svelte`** — A1-02 and A4-02 (below); the dead 1180px block removed; 640 → 680.
- **`CommandOverflowMenu.svelte`** — **not edited.** Its own SVGs are the geometry `Icon.svelte`
  was drawn from, and its `⌄` chevron is inside a menu trigger, not a duplicated command. Nothing in
  my findings applies to it, and a repaint for its own sake is churn.
- **`lib/preferences.ts`, `lib/mcpSettings.ts`** — **not edited.** No finding in my list is about
  either; the normalizers and the MCP status/audit mapping were already correct.
- **`SettingRow.svelte`** — added `busy` / `busyLabel` (see below); stacks at 680 rather than 720.
- **`CacheSection.svelte`** — busy feedback on both toggles and both Clear buttons; `clearFileCache`
  and `clearSSLSessionCache` widened from `() => void` to `() => Promise<void> | void` (App's
  handlers were already `async`; the narrow type simply discarded the promise, which is why there
  was nothing to wait on). The `state` prop is destructured as `state: appState` — Svelte reads
  `$state` as a *store subscription* whenever a local named `state` is in scope, so the rune does not
  compile otherwise. The prop name is unchanged; App.svelte's call site is untouched.
- **`McpSection.svelte`** — still legacy syntax, as instructed. Added an `applying` field tracking
  which of the three MCP writes is in flight, cleared in `finally`. Also normalised four `...` to
  `…`: the repo is 61 `…` to 8 `...`, and every ASCII one was in this file.

### A1-02 in detail

Two halves, and only one of them can live in this component.

**Half one, done here — it no longer looks like a per-request action.** The button read "Run", was
styled identically to Save and Send, and sat between them. It is now:

- **labelled** "Run collection", not "Run";
- **drawn as a different kind of button** — no border and no fill at rest, in the muted colour the
  row's context chips already use for "about your surroundings" information, with the shared
  `list` icon;
- **fenced off** by a rule from the two request-scoped buttons, and moved to the *start* of the
  group. It was in the middle, which is what made it read as a sibling of both. Reading left to
  right the row is now "something else" | "this request".

**Half two, needs PASTE 2 — it must never silently do nothing.** `runCollection()` opens with
`if (selectedItemIds.length === 0) return`, and the `disabled` prop
(`busy !== '' || hasActiveHTTPTransport`) knows nothing about that, so the button rendered
enabled, took the click and returned. Nothing inside this component can know the runner selection,
so it takes a new **optional** `runSelectionCount` prop:

- `0` → **disabled**, with the reason in `title` and `aria-label`: *"Nothing is selected in the
  Collection Runner. Open the Runner and choose requests before running the collection X."*
- `n > 0` → enabled, showing the count in a pill and naming it: *"Run the 3 requests selected in
  the Collection Runner for the collection X. This does not send the request open here — use Send
  for that."*
- `undefined` (today, until PASTE 2 lands) → it cannot be disabled, so it falls back to the other
  half of the requirement and **says what it will run**: *"Runs the Collection Runner's current
  selection for this collection. This does not send the request open here — use Send for that."*

**The part I could not fix and you should consider.** The audit's preferred fix — "route to the
Runner view directly rather than requiring a pre-existing selection" — is a behaviour change in
`runCollection()`, not a prop. Today an empty selection is a dead end: the user must know to open
the Runner, select, come back. Better: when `selectedItemIds.length === 0`, set
`activeView = 'runner'` and return, so the button always does something and that something is
"show me what I would be running". That is one line in `App.svelte` and it is not mine; it is
PASTE 2b, and if you take it, the disable in PASTE 2a becomes unnecessary and should be dropped
rather than kept alongside it.

### A6-11's equivalent, and what I did *not* wrap

`SettingRow` gained `busy` / `busyLabel`: a word inside the label, `aria-busy` on the row, cleared
in `finally` so a refused write does not leave a row saying "Saving…" forever.

Applied to the four Cache actions and the three MCP writes — the ones that go to Go and back
(re-measuring the cache directory, walking it to clear, binding a socket). Until that round trip
lands, `state.preferences` still holds the **old** value, so the checkbox the user just clicked
renders unchecked again and the panel looks like it refused the click.

Deliberately **not** applied to: the theme, zoom, font, cookie, SSL and OAuth2 toggles (they write
and are done; a spinner on each would be noise), and the two Browse buttons in General (a native
file dialog is its own feedback). The still-open real finding is the missing debounce noted in
`handoff-preferences.md` follow-up 2 — `request-timeout-input`, `autosave-interval-input` and
`code-font-input` still write on every keystroke. Unchanged, for the same reason it was left
before: it is a behaviour change, not a layout one.

---

## PASTE 1 — `frontend/src/App.svelte`: mount the panel (A7-06, A7-08)

Anchor: the whole `{:else if activeView === 'preferences'}` branch, from
`<section class="panel preferences-panel">` down to the `</section>` immediately before
`{:else if activeView === 'features'}`. That is the header, `<div class="settings-stack">` and all
eight `{#await import('./lib/views/preferences/…')}` blocks. Replace **all** of it with:

```svelte
        <PreferencesPanel
          mcpEnabled={appState.preferences.mcp?.enabled ?? false}
          themeModeLabel={selectedThemeMode}
          proxyLabel={proxyModeLabel(preferencesProxyMode(appState.preferences))}
          appearance={{
            state: appState,
            selectedThemeMode,
            themeModes,
            lightThemeVariants,
            darkThemeVariants,
            updateThemeMode,
            updateThemeVariant
          }}
          display={{
            appZoomPercentage,
            zoomPercentages,
            zoomDefaultPercentage,
            codeFont,
            codeFontSize,
            resetZoomPercentage,
            setZoomPercentage,
            updateCodeFont,
            updateCodeFontSize
          }}
          general={{
            state: appState,
            customCaFileName,
            browseDefaultLocation,
            clearDefaultLocation,
            browseCustomCaCertificate,
            clearCustomCaCertificate,
            updateAutoSavePreferences,
            updateRequestPreferences
          }}
          oauth2={{ state: appState, updateAppearancePreferences }}
          keybindings={{
            state: appState,
            keyBindingSections,
            keyBindingPreset: activeKeyBindingPreset,
            updateKeyBindingPreset,
            visibleKeyBindingEntries,
            keyBindingDisplayValue,
            keyBindingCanEdit,
            keyBindingIsCustomized,
            keybindingDraft,
            keybindingsAreEnabled,
            keybindingError,
            recordingKeybindingAction,
            formatKeyBinding,
            beginRecordKeyBinding,
            recordKeyBinding,
            stopRecordKeyBinding,
            resetKeyBinding,
            resetAllKeyBindings,
            updateKeybindingsEnabled
          }}
          proxy={{
            state: appState,
            preferencesProxyMode,
            updatePreferencesProxy,
            updatePreferencesProxyAuth,
            updatePreferencesProxyConfig,
            updatePreferencesProxyMode
          }}
          cache={{
            state: appState,
            fileCacheSize,
            formatRuntimeBytes,
            updateFileCache,
            updateSSLSessionCache,
            clearFileCache,
            clearSSLSessionCache
          }}
          mcp={{ state: appState, onUpdateMcp: updateMcpPreferences, onCopyCommand: copyText }}
        />
```

and add to the import block at the top of `App.svelte`:

```ts
  import PreferencesPanel from './lib/views/preferences/PreferencesPanel.svelte'
```

**A static import is correct here and is not a regression of US-036.** What US-036 kept out of the
initial chunk is the eight sections' markup, and all eight are still dynamic. The panel shell is a
header, a nav and a loader; making it dynamic too would put the eight imports one level deeper in
the graph and cost a round trip to save under 2 KB.

Three notes for whoever pastes this:

1. The `<h2>Preferences</h2>` header and the `<p class="panel-subtitle">` move *into* the
   component. Do not leave the old header behind — you would get two.
2. `openKeyboardShortcuts()`'s `document.querySelector('details.keybindings-disclosure')` still
   works: the disclosure is unchanged and is still inside the panel's DOM.
3. `test/mcpSection.test.mts` already accepts both locations. I relaxed it *before* the move
   (it pinned "App.svelte contains `<div class="settings-stack">`" and "App.svelte renders
   `<McpSectionComponent`"), so it passes today against `App.svelte` and will pass after the paste
   against `PreferencesPanel.svelte`. The invariants it checks — lazily imported, rendered inside
   the stack, given `updateMcpPreferences` and `copyText` — are unchanged and still enforced.

## PASTE 2a — `frontend/src/App.svelte`: stop "Run collection" no-op'ing (A1-02)

Anchor: the `<RequestCommandStrip` mount. Add two props beside `disabled` and `orientation`:

```svelte
            disabled={busy !== '' || hasActiveHTTPTransport}
            orientation={responsePaneOrientation}
            runSelectionCount={runnerSelectedCount}
            runCollectionName={activeCollection?.name ?? ''}
```

`runnerSelectedCount` already exists as a `$derived` in `App.svelte` — no new state. Both props are
optional and default to today's behaviour, so pasting one without the other is safe.

## PASTE 2b — `frontend/src/App.svelte`: the better fix, if you want it (A1-02)

Anchor: `runCollection`, the line `if (selectedItemIds.length === 0) return`.

```ts
    if (selectedItemIds.length === 0) {
      // An empty runner selection used to end here — the button took the click
      // and did nothing, with no way for the user to learn that a selection is
      // what it wanted. Showing the Runner answers the question the click asked
      // ("what would this run?") instead of discarding it.
      activeView = 'runner'
      return
    }
```

If you take this, **drop `runSelectionCount` from PASTE 2a** and leave only `runCollectionName`.
A button that always does something must not also be disabled; keeping both would be two answers
to one question, which is the shape of half the findings in this audit.

## PASTE 3 — `frontend/src/style.css`: dead rules

The list in `handoff-preferences.md` PASTE 1 still stands and I have not duplicated it. Three
additions and one correction:

**Now also dead.** `.preferences-panel .settings-stack > section` (`:5331`) no longer matches:
sections are wrapped in a `.preferences-section` scroll anchor, which restates the same
`min-width: 0; max-width: 100%` inside the component. Either delete it or relax the selector to
`.preferences-panel .settings-stack section`; do not simply delete it without checking, because
`.settings-stack` is also used outside Preferences.

**The whole `@media (max-width: 720px)` block (`:5654`) can go.** `handoff-preferences.md` already
lists all five of its rules as dead individually — `.default-location-control`,
`.cache-preference-card`, `.keybindings-preference-section`, `.keybindings-disclosure summary`,
`.keybindings-section-actions` — so nothing survives it. Delete the block, not just its contents.

**A4-12 confirmed still dead.** `@media (max-width: 1180px) { .topbar { grid-template-columns: 1fr } }`
(`:2454`) is a no-op: `.topbar` is `display: flex`. Not mine to remove.

**Correction to `handoff-preferences.md` PASTE 1:** it lists `.theme-mode-selector` at `:5295` and
the `@media (max-width: 720px)` block at `:5439-5457`. Both have moved (`:5654` now). Grep, do not
trust the numbers — that handoff says so itself and it is right.

## PASTE 4 — `frontend/src/style.css`: optional, the breakpoint scale as tokens

`layout.ts` now names the shell's three widths and `layout.test.mts` enforces them across the two
command components and `SettingRow`. `style.css` is not covered, because it is not mine and because
media queries cannot read custom properties anyway (`@media (max-width: var(--bp))` is invalid CSS
— this is a specification limit, not a tooling one). If you want the scale documented where the
other 1180/960/680 queries live, the honest version is a comment, not a token:

```css
/* Shell breakpoints — the ONLY three widths at which this app changes shape.
   Mirrored in lib/workbench/layout.ts as SHELL_BREAKPOINTS, which is what the
   two command components are held to by layout.test.mts. Media queries cannot
   read a custom property, so this comment is the link. Adding a fourth number
   here is how the command bar and the shell drifted apart in the first place.
     1180  wide     topbar, recovery list, preferences theme variants
      960  medium   sidebar becomes an overlay, workbench stacks
      680  compact  panes become columns                                    */
```

The strays that block a clean sweep are `900px` (`:2448`, `.runner-workbench`), `1100px` (`:3257`)
and `720px` (`:5654`, dead — see PASTE 3). None is mine.

## PASTE 5 — `frontend/src/lib/ui/Icon.svelte`: the missing glyph

Add `'layout-split'` to `IconName` and `iconNames`, and this branch to the `{#if}` chain:

```svelte
  {:else if name === 'layout-split'}
    <!-- A frame with one divider: the pane arrangement itself, not an arrow
         suggesting movement. Used by the response-orientation toggle, which the
         audit found drawn as a stroke SVG in one toolbar and as a `⇄`/`⇅` text
         glyph in another for the same command. -->
    <rect x="2.5" y="3" width="15" height="14" rx="2" /><path d="M10 3v14" />
```

Note `Icon.svelte` uses `stroke-width: 1.7` and this mark was drawn at `1.6`, which is where it
came from — the command bar. Redraw at 1.7 with the rest of the set rather than adding an
exception; the difference at 16px is invisible and the exception is not.

Once it exists, `OrientationToggleButton.svelte` should become a thin wrapper over `IconButton`
(which is `28px`, matching the request strip; the command bar's cell is `30px`, so either the
command-bar `.command-icon` sizing moves to 28 or `IconButton` grows a size). Everything else in
that file — the two orientations, the one label string, the ⌘J hint — stays as it is. There is one
drawing to move, on purpose.

---

## Follow-ups this pass did not close

1. **`A4-01`, `A4-04`, `A4-05`, `A4-09`, `A4-10`** — all live in `App.svelte`'s tab strip or
   `style.css`'s dividers. Untouched; not in my list and not in my files.
2. **`WorkspaceWindowPicker.svelte`** — A4-03/A4-06/A4-07 were closed by an earlier pass and the
   file is not mine. Its `@media (max-width: 520px)` is a fourth breakpoint outside the scale;
   `layout.test.mts` deliberately does not cover that file so as not to fail on someone else's
   in-flight work. Add it to the list in that test once the picker is quiet.
3. **`McpSection` is still the one legacy-syntax file in the preferences directory.** Unchanged
   from `handoff-preferences.md` follow-up 1, and my `applying` field is written in the same
   legacy style rather than half-converting it.
4. **The debounce** (`handoff-preferences.md` follow-up 2) is still open and still real.
5. **`SettingRow`'s `busy` is not wired to the Keybindings reset.** `resetAllKeyBindings` is async
   and gives no feedback either; it is a `() => void` in the section's prop type, same as Cache's
   clears were. Widening it is the same three-line change, and I stopped at the two sections the
   finding named rather than sweeping.

---

## Verification

From `frontend/`, all three run to completion on the tree as I left it:

- **`npm run check`** — **287 files, 0 errors, 0 warnings.**
- **`npm test`** — **1334 tests, 1334 pass, 0 fail, 0 skipped.**
- **`npm run lint`** — clean, whole repo, no output.
- **`npm run build`** — succeeds. The only warning is the pre-existing "chunks larger than 500 kB"
  notice about `codemirror` and `index`, which predates this wave.

All three were green on the whole repository, not on a subset, and none of the failures other
implementers reported earlier in the wave were present when I ran them. Tests added: 3 in
`layout.test.mts` (shell breakpoint scale, the allow-list over media queries, the dead-grid guard)
and 6 in `preferencesRows.test.mts` (one await block, module-scope cache, index/anchor parity,
subtitle, the two busy assertions). `test/mcpSection.test.mts` was relaxed to be location-agnostic
— it checks the same four invariants and now finds them wherever the settings stack lives.
