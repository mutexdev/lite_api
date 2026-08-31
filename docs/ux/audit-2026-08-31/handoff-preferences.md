# Handoff — A7 Preferences / MCP (implementer W-B)

Scope worked: `frontend/src/lib/views/preferences/**` and new files under `frontend/test/`.
Nothing outside that was touched. Everything below marked **PASTE** is a change I could not
make because the file belongs to another implementer.

## Findings closed

| Finding | Severity | Status |
| --- | --- | --- |
| A7-01 no shared settings-row primitive | critical | closed — `SettingRow.svelte` + `SettingSection.svelte`, all 8 sections migrated |
| A7-02 bespoke pill group duplicating `.segmented` | major | closed — theme mode now uses `ui/SegmentedControl.svelte` |
| A7-03 `data-tone` read by no stylesheet | major | closed — one `.status-tone` element and one six-value palette, shared with the audit badges |
| A7-04 five different content max-widths | major | closed — one 780px cap, declared once in `SettingSection` |
| A7-05 Proxy's raw checkbox in a value column | minor | closed — normal boolean row |
| A7-07 dead `oauth2-browser-toggle` class | polish | closed — removed |
| A7-06 no in-panel nav, 8 import waterfalls | minor | **not done** — lives entirely in `App.svelte` |
| A7-08 panel subtitle | polish | **not done** — lives in `App.svelte:11751` |

Deliberately unchanged, as instructed: the 1024×768 overflow fix (`.keybindings-table-wrap`'s
bounded 400px scroller is intact and now guarded by a test) and the instant-write save model
(no Save/Cancel anywhere; a test now fails if one appears).

## New files

**`frontend/src/lib/views/preferences/SettingRow.svelte`** — the one row. Three presentations of
the same component, so a section cannot invent a fourth:

- **boolean** — supplying `onCheckedChange` makes the row own the checkbox, rendered inline with
  its text in a `.inline-toggle`. This is the app's only boolean control.
- **field** — caller supplies the control; label left, control right.
- **stacked** — `stacked`, for a control too wide for a column (theme variant cards, keybindings
  table).

Props: `label`, `description`, `badge`, `disabled`, `labelFor`, `checked`, `onCheckedChange`,
`checkboxId`, `checkboxAriaLabel`, `control` (Snippet), `stacked`, `data-testid`.

`data-testid` is spelled as the attribute, not `testId`, on purpose: several suites grep the
`.svelte` sources for the literal `data-testid="…"` (there is no render harness), and a
differently-named prop would have emptied them while every test still passed. It lands on the
checkbox for a boolean row, on the row element otherwise.

The row owns the **label column width (200px, declared once)** and the **control caps**
(`input[type=number]` 120px, `select` 260px, free text flexes). The control caps are written with
`:global()` because slotted markup carries the caller's scope class, not the row's — without it
those rules compile to selectors that match nothing.

**`frontend/src/lib/views/preferences/SettingSection.svelte`** — `<h3>` header, optional `status`
snippet on the header's right end, optional section-level `note`, and **the one content width:
`max-width: 780px`, declared here and nowhere else**. It is a literal, not a `--` token, because a
new token has to be added to `:root` in `style.css` to mean anything and that file is not mine.
If you would rather it be a token, see PASTE 3.

**`frontend/test/preferencesRows.test.mts`** — 11 tests: every section imports and renders both
primitives; no section reintroduces one of the twelve retired grid classes; no section renders its
own checkbox; no section hand-rolls a pill group and exactly one SegmentedControl exists in the
panel; exactly one content width and one label-column width in the whole directory; every
`data-tone` value emitted has a rule that reads it; no Save/Apply/Cancel button; all 28 testids
survived the migration; the keybindings disclosure keeps the shape `App.svelte` reaches for.

## Decisions you asked me to state

**Boolean control** — always a native `<input type="checkbox">` labelled inline, owned by
`SettingRow`. No toggle switch is introduced; the app has never had one and the audit was right
that this is already consistent. A section can no longer render its own checkbox (test-enforced).

**Enum control** — always a native `<select>`, with exactly one documented exception: **theme
mode**, which uses the shared `SegmentedControl`. That follows the audit's own contract (segmented
only for 2–3 options surfaced as a primary, navigation-like choice). Proxy mode/protocol, zoom and
keybinding preset are all `<select>`. The test pins that AppearanceSection is the only file in the
directory mentioning `SegmentedControl`, so a second one is a failure, and so is losing this one.

**Content width** — 780px, the value already used by three of the five (General, default location,
Cache), so the migration moved the fewest rows.

## File by file

- **AppearanceSection** — converted to runes. `.theme-mode-selector` button group replaced by
  `SegmentedControl` (`compact={false}`, `testId="theme-mode"`, `ariaLabel="Theme mode"`). This is
  not only a repaint: the old group was three tab stops, the replacement is one with arrow-key
  navigation. Light/Dark variant grids became stacked rows; `.theme-variants` markup unchanged.
- **DisplaySection** — converted to runes. `.font-preference-grid` and `.zoom-preference-row`
  replaced by three rows. Zoom's Reset sits inside the one control cell rather than becoming a
  third column only this row had. Header keeps `data-testid="zoom-percentage-value"`.
- **GeneralSection** — converted to runes. All three of its sub-anatomies gone. The free-floating
  hint paragraphs are now each row's `description`, so a sentence is attached to the control it is
  about. The path picker (readonly input + Browse + Clear) is one control cell. **Behaviour change
  to review:** `request-timeout-input` and `autosave-interval-input` were bare text inputs with
  `inputmode="numeric"`; they are now `type="number" min="0"`, matching every other numeric field
  in the panel (MCP port, code font size). `Number(value)` on empty is 0 either way, so the write
  path is unchanged.
- **OAuth2Section** — converted to runes. One boolean row; dead `oauth2-browser-toggle` class
  removed; gained a description (it had none).
- **ProxySection** — converted to runes. Field rows throughout; "Auth enabled" is now a normal
  boolean row with `checkboxAriaLabel="App proxy auth enabled"`. Every `aria-label` is preserved —
  `test/proxyModeVocabularies.test.mts` greps for `aria-label="App proxy mode"` and its options.
- **CacheSection** — converted to runes. The two bordered cards are gone; each is a boolean row
  with a description and a Clear button in the control cell. The measured size moved next to the
  button that acts on it (new `data-testid="file-cache-size"`). The Beta badge survives as
  `SettingRow`'s `badge` prop.
- **KeybindingsSection** — converted to runes. Preset, Enabled and the table are SettingRows. The
  Enabled toggle and Reset Default are no longer absolutely positioned over the section corner.
  **The `<details class="keybindings-disclosure">` and its `<summary>` had to stay** —
  `App.svelte:6744 openKeyboardShortcuts()` queries `details.keybindings-disclosure`, opens it and
  focuses the `summary`. Renaming either would break the Keyboard Shortcuts command with no
  compile error; there is now a test for it. `.keybindings-table-wrap` also keeps its name because
  the 400px bounded scroller that fixes the 1024×768 overflow is keyed on it in `style.css`.
- **McpSection** — rows migrated, status severity fixed, second `SettingSection` for Recent
  activity. **This is the one file still in legacy (`export let` / `$:`) mode.** Four source-text
  tests pin the exact spelling of its wiring, and `test/mcpSection.test.mts:98` matches the literal
  `maskedCommand = maskToken(pairingCommand)`, which `$derived` cannot produce. Converting it means
  editing that test in the same change, which is outside my ownership. See "Follow-ups".

### A7-03 in detail

The status line and the audit badges are now the same element (`.status-tone`) with one six-value
tone vocabulary, in two form factors: `.badge` (small, uppercase, nowrap) for the per-call
outcomes, plain for the status line, which is a sentence and can carry a backend error after its
state word.

- `running`, `ok` → `--success` on `--success-bg`
- `warning`, `denied` → `--warning-text` on `--warning-bg-soft`, `--warning-border`
- `error` → `--danger-strong` on `--danger-bg`, `--danger-border`
- `off` → `--muted`, no fill

The badges' `data-outcome` attribute became `data-tone` so both read one vocabulary; nothing in
the repo selected on `data-outcome` (checked repo-wide).

## PASTE 1 — `frontend/src/style.css`, rules that are now dead

Verified with a repo-wide grep: no `.svelte` file applies any of these class names any more. Line
numbers are as of my last read; grep the selector rather than trusting them.

Delete outright:

```
.theme-mode-selector                 (:5295)   ← replaced by SegmentedControl
.theme-mode-selector button          (:5306)
.theme-mode-selector button.selected (:5314)
.theme-variant-section               (:5321)
.settings-section-actions            (:5395)
.keybindings-preference-section      (:5402)
.keybindings-section-actions         (:5432)
.font-preference-grid                (:5438)
.font-preference-grid input          (:5447)
.zoom-preference-row                 (:5451)
.zoom-preference-row select          (:5460)
.general-preferences-grid            (:5464)
.path-picker-row                     (:5471)
.compact-preference-grid             (:5499)
.compact-preference-grid input       (:5503)
.default-location-grid               (:5507)
.default-location-control            (:5512)
.default-location-input              (:5519)
.cache-preference-card               (:5523)
.cache-preference-card strong        (:5536)
.cache-preference-card p             (:5540)
.cache-preference-card .cache-size   (:5546)
```

Also dead — `.keybindings-disclosure summary` (:5411). Every one of its declarations is now
re-stated in the component, and its `padding-right: 210px` reserved a gutter for controls that no
longer float there; the component sets `padding-right: 0` to defeat it. Delete the rule and then
delete the `padding-right: 0` line from `KeybindingsSection.svelte`'s style block (leave the rest
of that rule — it is the section's heading treatment).

Inside the `@media (max-width: 720px)` block, these four are dead and the block should be reduced
to whatever else it contains:

```
.default-location-control, .cache-preference-card { grid-template-columns: minmax(0, 1fr); }
.keybindings-preference-section { display: grid; gap: var(--space-8); }
.keybindings-disclosure summary { padding-right: 0; }
.keybindings-section-actions { position: static; }
```

Now redundant but **not** dead — leave the rule, drop one line:

```
.theme-variants { … max-width: 920px; }   ← the 780px section cap always wins; delete the max-width
```

Keep: `.field-grid` and its 620px cap (still used by App.svelte, the codegen modals and the flow
views), `.inline-toggle`, `.settings-disabled`, `.settings-section-header`, `.preference-value`,
`.selected-path-chip`, `.beta-badge`, `.keybindings-disclosure`, `.keybindings-summary-title`,
`.keybindings-summary-status`, `.keybindings-table*`, `.keybinding-input`, `.keybinding-error`,
`.keybinding-section-row`, `.theme-variant-card`, `.theme-preview*`, `.copy-button`.

## PASTE 2 — `frontend/src/App.svelte`, A7-06 and A7-08

Not attempted; recorded so the next pass has them.

- **A7-06** (`App.svelte:11827-11940`): the 8 `{#await import('./lib/views/preferences/*.svelte')}`
  blocks re-run their dynamic `import()` on every panel mount, and there is no anchor nav. The
  cheap half is hoisting the imports to module scope so they resolve once per session; the other
  half is a sticky list of the 8 section names with scroll-to behaviour. Note that the panel is now
  9 `<section>` elements, not 8 — `McpSection` renders "AI access (MCP)" and "Recent activity" as
  two siblings — so a nav built by counting sections needs to know that.
- **A7-08** (`App.svelte:11751`): subtitle reads `Theme {mode} · Proxy {label}`. If you want MCP
  state in it, `state.preferences.mcp?.enabled` is the flag; the *listening* state is only known to
  `GetMCPStatus`, which the panel fetches on mount, so the header cannot show it without lifting
  that fetch.

## PASTE 3 — optional, if you want the width as a token

If you would rather the settings column width be a palette entry, add to `:root` in `style.css`:

```css
  --settings-content-max-width: 780px;
```

then in `SettingSection.svelte` replace `max-width: 780px;` with
`max-width: var(--settings-content-max-width);` and relax the assertion in
`test/preferencesRows.test.mts` ("the settings content width is declared once, in SettingSection")
to look for the token instead of a px literal. Note `test/designTokens.test.mts` requires that a
token used anywhere is declared in `:root` and used without a fallback, so both halves have to
land together.

## Follow-ups the audit did not name

1. **McpSection is the last legacy-mode file in the directory.** Relax
   `test/mcpSection.test.mts`'s `maskedCommand = maskToken(pairingCommand)` regex to tolerate
   `$derived(…)` and the file converts mechanically. Leaving it half-converted — runes markup,
   legacy state — would be worse than either.
2. **The debounce the contract asks for is not implemented.** `request-timeout-input`,
   `autosave-interval-input` and `code-font-input` still write on every keystroke. I left them
   alone because the brief said to preserve the instant-write model exactly, and a 300ms debounce
   is a behaviour change, not a layout one. It is a real finding and it is still open.
3. **`frontend/src/lib/statusTone.ts` landed from the A8 pass while I was working.** It is about
   HTTP result grading (`success`/`warning`/`danger`/`idle` → `.ok`/`.warn`/`.bad`), and MCP's
   vocabulary is `running`/`warning`/`off` and `ok`/`denied`/`error`, so I did not couple to it —
   the mapping is not one-to-one and its global classes are text colours, not badge fills. Worth a
   deliberate look at whether the two vocabularies should merge; if they should, McpSection's
   `.status-tone` block is the only place to change.
4. **`.settings-hint` never existed in `style.css`.** It was declared identically in two component
   style blocks (GeneralSection and McpSection), which is why hint text looked right. Both copies
   are gone; the type is now `SettingRow`'s `.setting-row-description` and `SettingSection`'s
   `.setting-section-note`, which are deliberately identical.
5. **The Proxy `aria-label`s disagree with their visible labels** — the select says "Mode", the
   `aria-label` says "App proxy mode". I kept both because `test/proxyModeVocabularies.test.mts`
   greps for the aria-label, but a screen reader announces a name the sighted user cannot see. Once
   that test is free to change, give the controls `id`s, point `SettingRow`'s `labelFor` at them
   and drop the aria-labels.

## Verification

From `frontend/`, at the time of writing:

- `npm run check` — **271 files, 0 errors, 0 warnings**.
- `npm test` — 1190 tests, 1187 pass, **3 fail, none in my files**: all in
  `test/bodyHighlight.test.mts` / `test/bodyMode.test.mts`, two of them named `BUG: …`, all
  belonging to the concurrent response-pane pass (the count moved from 5 to 3 while I was writing
  this, as that agent landed fixes). The seven suites that touch my surface —
  `preferencesRows`, `mcpSection`, `mcpAuditView`, `proxyModeVocabularies`, `preferences`,
  `keybindings`, `designTokens` — are **117/117**.
- `npm run lint` — clean over `src/lib/views/preferences` and `test/preferencesRows.test.mts`.
  A whole-repo `eslint .` reported 2 `no-undef` errors in `src/App.svelte` (`changeBodyMode`,
  `changeBodyFormat`) from another implementer's in-flight edit.
