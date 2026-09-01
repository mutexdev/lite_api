# A7 — Preferences, MCP, notifications

## Summary
- The 1024x768 overflow bug in `docs/qa/m2-root-fail-preferences-overflow.jpeg` looks genuinely fixed: the keybindings table has its own bounded `max-height: 400px` scroll region (`frontend/src/style.css:5459-5479`) and a narrow-width media query re-stacks the location/cache grids and un-absolutely-positions the keybindings actions (`frontend/src/style.css:5439-5457`). No overflow risk found on re-check.
- There is no shared settings-row primitive. Eight sections use at least four structurally different row anatomies: `.inline-toggle` (checkbox+label inline), `.field-grid`/`.compact-preference-grid`/`.font-preference-grid`/`.zoom-preference-row`/`.default-location-grid` (label-left, control-right, each with its own column widths), `.cache-preference-card` (bordered card with title/description/toggle/button in three columns), and a plain HTML `<table>` inside a `<details>` disclosure for keybindings. No section shares another's row markup.
- Every section that shows a max-width caps content at a different number: 620px (bare `.field-grid`), 600px (font), 440px (zoom), 780px (general grid, default-location, cache card), 920px (theme variants), and the keybindings table has no cap at all (`min-width: 680px`, scrolls). Sections visually end at different right edges on the same screen.
- Boolean controls are consistent (always a native `<input type="checkbox">` inside `.inline-toggle` — no toggle-switch component exists anywhere in the app), but enum controls are not: theme mode uses a bespoke button-pill group (`theme-mode-selector`) that duplicates an existing `.segmented` component used elsewhere (e.g. `NotificationsModal.svelte:40`), while Proxy mode/protocol, zoom, and keybinding preset all use native `<select>`.
- Save semantics are actually consistent across every section audited: nothing has a Save/Cancel button, every control writes on `change`/`input` straight through `savePreferences` (e.g. `App.svelte:6444-6476`). This contradicts the audit brief's suspicion — it's a genuine strength worth preserving, not a finding.
- MCP's status line (`McpSection.svelte:183-185`) carries a `data-tone` attribute (running/warning/off) that has **zero** CSS binding anywhere in `style.css` — it renders as unstyled text — while three lines below it, the audit-outcome badges in the same section get full color treatment (`style.css:370-384`). Same section, same kind of "state severity" information, two different presentation rules.
- The Preferences shell has no side nav, tabs, or anchor list — all 8 sections render as one long vertical stack (`App.svelte:11746-11865`), each individually behind its own `{#await import(...)}`, so the panel repopulates from a blank state via 8 sequential dynamic-import waterfalls every time Preferences is opened.
- McpSection.svelte (397 lines) and KeybindingsSection.svelte are the two outliers the brief flagged as suspects, and both check out: MCP is a mini-app (own polling loop, own audit log list with its own badge system, own copy-to-clipboard affordance) that shares almost no CSS with its siblings beyond `.inline-toggle`/`.field-grid`/`.settings-hint`; Keybindings is the only section using a table+disclosure pattern instead of a flat list.
- `OAuth2Section.svelte:16` applies a class `oauth2-browser-toggle` that has no matching rule anywhere in `style.css` — dead styling hook.
- McpApprovalModal.svelte is well-executed and outside the "different app" complaint — it is a security-decision dialog, not a settings row, and its button ordering/focus/no-close-affordance choices are documented and intentional.

## Section conformance table

| Section | File | Row layout pattern | Boolean control | Enum control | Save semantics | Description text? | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Appearance | AppearanceSection.svelte | Custom button-pill group + card grid (`theme-mode-selector`, `theme-variant-card`) | n/a | Bespoke pill buttons (not `.segmented`) | Instant (`updateThemeMode`/`updateThemeVariant`) | No hint text | Diverges — reinvents segmented control |
| Display | DisplaySection.svelte | `.font-preference-grid` (2-col) + `.zoom-preference-row` (3-col) | n/a | Native `<select>` (zoom) | Instant, on `input`/`change`, no debounce | No hint text | Diverges — two more bespoke grids |
| General | GeneralSection.svelte | `.general-preferences-grid` wrapping `.inline-toggle` rows + `.field-grid.compact-preference-grid` rows + `.path-picker-row` | Checkbox in `.inline-toggle` | n/a | Instant, per-keystroke on timeout/interval inputs, no debounce | Yes, `.settings-hint` under some rows only | Diverges — 3 sub-patterns in one section |
| OAuth2 | OAuth2Section.svelte | Single `.inline-toggle` | Checkbox | n/a | Instant | No hint text | Minimal but consistent with General's toggle style |
| Keybindings | KeybindingsSection.svelte | `<details>` disclosure wrapping `<select>` + `<table>` | Checkbox (Enabled, outside disclosure) | Native `<select>` (preset) | Instant per rebind; recording is its own state machine | Yes, one `<p class="muted">` | Diverges hardest — only table-based section |
| Proxy | ProxySection.svelte | Bare `.field-grid` (label/control pairs) | Checkbox inline in `.field-grid` (no `.inline-toggle`) | Native `<select>` (mode, protocol) | Instant on `input`/`change` | No hint text | Diverges — auth-enabled checkbox breaks the boolean-control convention |
| Cache | CacheSection.svelte | `.cache-preference-card` (bordered card, 3-col) | Checkbox in `.inline-toggle` | n/a | Instant | Yes, inline `<p>` inside the card | Diverges — only card-container section |
| MCP | McpSection.svelte | Stack of `.inline-toggle` + `.field-grid.compact-preference-grid` + free-floating `<p class="settings-hint">` + custom audit list | Checkbox in `.inline-toggle` | n/a | Instant, but two-step (write then re-fetch status) | Yes, extensively | Mini-app: adds polling, audit log, copy button, status line with unstyled severity |

## Findings

### A7-01 — No shared settings-row primitive; four incompatible row anatomies
- **Severity**: critical
- **Where**: `frontend/src/lib/views/preferences/GeneralSection.svelte:21-156`, `frontend/src/lib/views/preferences/CacheSection.svelte:21-53`, `frontend/src/lib/views/preferences/KeybindingsSection.svelte:67-115`, `frontend/src/style.css:2592-2608`, `frontend/src/style.css:5372-5397`
- **What the user sees**: A toggle in General looks like a plain checkbox with a label to its right. The same kind of setting in Cache is presented as a bordered card with a bold title, a description paragraph, and the checkbox floating in a middle column next to an action button. Keybindings abandons rows entirely for a spreadsheet-style table inside a collapsed disclosure. Nothing tells the eye these are all "settings."
- **Why it's wrong**: This is the literal mechanism behind the "looks like a different app in each section" complaint — a user scrolling from General to Cache to Keybindings crosses three different visual grammars in a few hundred pixels.
- **Proposed fix**: Build one `SettingRow.svelte` (label, optional description, control slot) and route every boolean/text/select control in Preferences through it, including Cache's card content and Keybindings' non-table rows (preset selector, Enabled toggle).
- **Shared primitive it should use**: `SettingRow` (see contract below).

### A7-02 — Enum control is inconsistent: bespoke pill group duplicates the app's existing `.segmented` component
- **Severity**: major
- **Where**: `frontend/src/lib/views/preferences/AppearanceSection.svelte:36-46`, `frontend/src/style.css:5144-5168` (`.theme-mode-selector`) vs. `frontend/src/style.css:4306-4333` (`.segmented`), used correctly at `frontend/src/lib/modals/NotificationsModal.svelte:40-48`
- **What the user sees**: The theme-mode picker (System/Light/Dark) is a rounded pill group with a shadow on the selected item. The functionally identical All/Unread picker in the Notifications modal is a rounded pill group with a filled background on the selected item. They read as two different components because they are two different components solving the same "pick one of 2-4" problem.
- **Why it's wrong**: `.segmented` already exists in the design system and is used elsewhere (`.sandbox-mode-panel .segmented` at `style.css:4536`). `.theme-mode-selector` reimplements it with different padding (`--space-3` vs the same), different radius token, and a different selected-state treatment (box-shadow vs background+border-color) for no functional reason.
- **Proposed fix**: Replace `.theme-mode-selector` markup/CSS with `.segmented`, or promote `.segmented` to the canonical "SettingSegmented" control and delete the bespoke class.
- **Shared primitive it should use**: `.segmented` (already exists — this is a dedup, not a new build).

### A7-03 — MCP status severity is colorless while audit-log severity three lines below it is fully color-coded
- **Severity**: major
- **Where**: `frontend/src/lib/views/preferences/McpSection.svelte:183-185` (status line, `data-tone={summary.tone}`) vs. `frontend/src/lib/views/preferences/McpSection.svelte:370-384` (`.mcp-audit-outcome[data-outcome=...]` rules)
- **What the user sees**: The line telling you whether the MCP listener is actually running ("Running" / "Enabled but not listening" / "Off") is plain muted text no matter which of those three states it's in. Immediately below, the "denied"/"error"/"ok" badges in the activity list get warning-yellow, danger-red, and quiet-green treatment respectively for the same class of information.
- **Why it's wrong**: `grep -n "data-tone" frontend/src/style.css` returns nothing — the attribute is written for tests (`data-testid="mcp-status" data-tone={summary.tone}`) but has no visual selector at all. The "enabled but not running" middle state — which the code comment at `McpSection.svelte:6-12` explicitly calls out as the dangerous case (toggle looks on, nothing is listening) — is not visually distinguished from "Running."
- **Proposed fix**: Add `[data-tone='warning']`/`[data-tone='off']`/`[data-tone='running']` rules reusing the same `--warning-*`/`--muted`/`--success-*` tokens the audit badges already use.
- **Shared primitive it should use**: A `StatusTone` text/badge style shared between the status line and the audit outcome badges.

### A7-04 — Every section caps its content at a different, seemingly arbitrary max-width
- **Severity**: major
- **Where**: `frontend/src/style.css:2607` (`.field-grid` → 620px), `:5292` (`.font-preference-grid` → 600px), `:5305` (`.zoom-preference-row` → 440px), `:5316` (`.general-preferences-grid` → 780px), `:5357` (`.default-location-grid` → 780px), `:5377` (`.cache-preference-card` → 780px), `:5183` (`.theme-variants` → 920px), `:5472` (`.keybindings-table` → 680px min, unbounded/scrolls)
- **What the user sees**: On a wide window, Proxy's fields end their row at 620px, Display's font row ends at 600px, its own Zoom row two lines below ends at 440px, General's rows end at 780px, and Appearance's theme cards stretch to 920px. The right edge of the settings content zig-zags section to section instead of forming one column.
- **Why it's wrong**: These widths were set independently per component (each file has its own `<style>` block) with no shared token for "settings content column width," so the ragged edge is incidental, not designed.
- **Proposed fix**: Define one `--settings-content-max-width` token (or let `SettingRow`/`SettingSection` own the constraint) and have every section's content grid inherit it instead of hardcoding its own.
- **Shared primitive it should use**: `SettingSection` wrapper that fixes the content max-width once.

### A7-05 — Proxy's "Auth enabled" is a raw checkbox in a label/value grid, not `.inline-toggle` like every other boolean
- **Severity**: minor
- **Where**: `frontend/src/lib/views/preferences/ProxySection.svelte:44-45`
- **What the user sees**: Every other boolean setting in Preferences is `<label class="inline-toggle"><input type="checkbox">Label text</label>` — checkbox immediately left of its label, both inside one clickable label. Proxy's "Auth enabled" instead puts a plain `<span class="field-label">Auth enabled</span>` in the grid's label column and a bare `<input type="checkbox">` in the value column, matching the layout used for the text inputs beside it rather than the toggle convention used two lines below in the same file's sibling sections.
- **Why it's wrong**: It's the one boolean control in the whole surface that doesn't use the app's own boolean-control convention, and it sits inside a manual-proxy config block a user is likely to compare against General's SSL/cookie toggles.
- **Proposed fix**: Wrap it in `.inline-toggle` (or the new `SettingRow` with a checkbox control slot) to match every other boolean in Preferences.
- **Shared primitive it should use**: `SettingRow` boolean variant.

### A7-06 — Preferences panel has no navigation; opening it re-triggers 8 sequential dynamic-import waterfalls
- **Severity**: minor
- **Where**: `frontend/src/App.svelte:11746-11865`
- **What the user sees**: Preferences is one uninterrupted scroll from Appearance down through MCP with no anchor list, tabs, or sticky section jump — a user who wants Proxy has to scroll past five other sections. Each of the 8 `{#await import('./lib/views/preferences/XSection.svelte')}` blocks (e.g. `:11755`, `:11768`, `:11783`) re-runs its dynamic `import()` every time the panel mounts, so opening Preferences repeatedly re-triggers 8 module-resolution promises before anything but already-cached chunks render.
- **Why it's wrong**: Not an overflow bug (that part is fixed, see Summary), but a discoverability and perceived-performance issue: no way to jump directly to a section, and a panel that repopulates from blank on every open.
- **Proposed fix**: Add a lightweight in-panel section list/anchor nav (doesn't need to be a hard tab switch — even a sticky sidebar of section names with scroll-to behavior would do), and consider hoisting the imports so they resolve once per app session rather than once per panel-open.
- **Shared primitive it should use**: n/a (structural, not a control primitive).

### A7-07 — Dead CSS hook on OAuth2's toggle
- **Severity**: polish
- **Where**: `frontend/src/lib/views/preferences/OAuth2Section.svelte:16`
- **What the user sees**: No visible effect — the class does nothing.
- **Why it's wrong**: `class="inline-toggle oauth2-browser-toggle"` — `grep -rn "oauth2-browser-toggle" frontend/src` finds only this one usage and no matching rule in `style.css`, so it's leftover from a removed style or was never implemented.
- **Proposed fix**: Remove the dead class, or if OAuth2 was meant to get its own spacing, add the rule (though under the SettingRow proposal this becomes moot).
- **Shared primitive it should use**: n/a.

### A7-08 — Panel subtitle summarizes two arbitrary settings, ignoring the one most users check first
- **Severity**: polish
- **Where**: `frontend/src/App.svelte:11751`
- **What the user sees**: The Preferences header subtitle always reads `Theme {mode} · Proxy {label}` regardless of which sections exist or matter this session — e.g. it never reflects whether AI access (MCP) is on, which is arguably the more security-relevant piece of state to surface at a glance.
- **Why it's wrong**: It reads as an artifact of whichever two settings were wired up first rather than a deliberate "at a glance" summary.
- **Proposed fix**: Either drop the subtitle (the sections are one scroll away) or make it configurable/meaningful (e.g. surface MCP on/off state, which the McpSection code already treats as the one non-obvious state in the whole panel).
- **Shared primitive it should use**: n/a.

## Cross-cutting primitives this area needs
- **`SettingRow`** — one row component (label, optional description, control slot) used for every boolean/text/number/select setting across all 8 sections, replacing `.inline-toggle`, bare `.field-grid` rows, `.compact-preference-grid`, `.font-preference-grid`, `.zoom-preference-row`, `.default-location-grid`, and the settings content of `.cache-preference-card`.
- **`SettingSection`** — a wrapper owning the `<h3>` header (already close to consistent via `.settings-section-header`) plus one shared content max-width token, replacing each section's own ad hoc max-width.
- **Segmented control dedup** — delete `.theme-mode-selector` in favor of the existing `.segmented` (already correctly used in `NotificationsModal.svelte`).
- **`StatusTone`** — a shared text/badge style for "state severity" (running/warning/off, ok/denied/error) so the MCP status line and the audit-outcome badges speak the same visual language instead of one being colorless.
- **Toggle-switch question** — the app has no visual toggle-switch component anywhere (only native checkboxes labeled via `.inline-toggle`); this is at least *consistent*, so it's a design decision to ratify explicitly (keep checkboxes) rather than a bug, but it should be a documented decision so no section quietly introduces a switch later.

## Proposed SettingRow contract

```
SettingRow
  props:
    label: string                      // required, always visible, left-aligned
    description?: string               // optional help text, rendered under the label at reduced
                                        // size/opacity (reuse .settings-hint's existing type scale),
                                        // NEVER beside the control — keeps long descriptions from
                                        // fighting the control column's width
    id: string                         // forwarded to the control for label association
    disabled?: boolean                 // applies .settings-disabled treatment to the whole row
  slots:
    control                            // exactly one interactive element or tightly-coupled group
                                        // (e.g. input + browse/clear buttons for a path picker)

  control types allowed:
    - checkbox (boolean settings — always this, never a custom switch)
    - <select> (enum settings — always this; segmented pill group only for 2-3 options surfaced
      as primary navigation-like choices, e.g. theme mode, and must use the shared .segmented class)
    - text/number input (free values), with inline unit/suffix text allowed (e.g. "ms")
    - path-picker group (readonly input + Browse + Clear), matching the existing General pattern

  layout:
    - two-column grid: label column fixed width (one shared token, not per-section), control
      column flexible, min 0 so long content can't blow out the row
    - description spans both columns, full row width, directly under the label
    - row content width is capped by the SettingSection wrapper, not by the row itself

  width behavior:
    - control column has a sane max-width per control type (e.g. number inputs cap around 120px,
      text/path inputs flex up to the section's shared max-width) — but that cap is defined once,
      centrally, not redeclared per section

  save model (applies to every section, already true today — codify, don't change):
    - no Save/Cancel buttons anywhere in Preferences
    - every control commits on native change (checkbox/select: `change`; text/number: `input` is
      acceptable but should debounce writes >= ~300ms for free-text/number fields to avoid an IPC
      write per keystroke, which GeneralSection's timeout field and DisplaySection's font field
      currently do not do)
    - a control that depends on backend confirmation (MCP's enabled/port, which re-fetches status
      after writing) is allowed to show a brief pending/settling indicator in the row, but must not
      introduce a distinct "Apply" action
```
