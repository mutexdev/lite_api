# A8 — Flows, Runner, History, Import/Export/Codegen

## Summary

- The app has **four independent implementations of "status is good/bad"**, and they disagree with each other on the same data: History colors any 2xx/3xx as green, the main response pane treats 3xx as amber, Runner colors nothing, and Flow ignores the status code entirely (color comes from assertion pass/fail). A 302 response reads as "ok" in History and "warning" in the response pane. See A8-01.
- The app has **four structurally different widgets for "a list of executed requests + results"** (Runner = `<table>`, Flow run = `<ol>` of always-expanded cards, History = `<ul>` of cards, response Timeline = expandable `<article>` list), each with a different subset of filtering / row-expansion / export. Runner — the oldest of the four — is the poorest: no filter, no expansion, no export, no per-row color. See comparison table below and A8-03.
- Flow's `busy` prop is a `boolean` (`FlowTab.svelte:38`, `FlowRunPanel.svelte:38`); every sibling panel (`RunnerPanel`, `HistoryPanel`, `ImportPanel`, `GenerateDocsModal`, `ShareCollectionModal`, `SyncSettingsModal`) takes `busy: string`, the app-wide convention that names *which* operation is running so a button can say "Exporting…" instead of just disabling. App.svelte collapses the string to a boolean at the call site (`App.svelte:11118`) specifically to feed Flow, discarding the information other panels use.
- Two dialogs hardcode colors that never adapt to dark mode: the OpenAPI Spec Diff badges/cells (`style.css:4639-4656`, `4723-4734`) use raw Tailwind-style hex, and Import's warning text (`style.css:5625`, `5631`) uses a hardcoded amber unrelated to the `--warning-strong` token. Both will look wrong or low-contrast in dark mode while every other warning/success color in the app (Flow's chips, the response pane, Runner's cancelled-row styling) correctly uses tokens that flip per theme.
- `HistoryPanel.svelte` is the one file in this area still written in hardcoded `rem`/`px` values (`gap: 0.75rem`, `border-radius: 4px`, `border: 1px solid var(--border, rgba(127, 127, 127, 0.3))`) instead of the `--space-*`/`--radius-*` tokens that `FlowTab`, `FlowStepEditor`, and `FlowRunPanel` use consistently throughout.
- `HistoryEntry.size` exists on the wire type and the app already has a shared byte formatter (`formatRuntimeBytes`, `lib/workbench/commandState.ts:16`) used in the main response strip — but History never renders it, silently dropping a metric it already has.
- Where Flows *did* reuse the app's vocabulary, it did it well: the tab strip repurposes the request tab's method-badge slot for a "FLOW" badge (`App.svelte:8869-8871`) rather than inventing a new tab shape, and `FlowTab`/`FlowStepEditor`/`FlowRunPanel` are the most token-disciplined files in this whole area — every spacing/radius value is a `var(--space-*)`/`var(--radius-*)`. The drift is concentrated in the *other* surfaces (History, Import, OpenAPI diff), not in Flow's own new UI.
- Duration formatting is, by luck rather than design, visually consistent everywhere (`"{n} ms"` — Runner, History, Flow, Timeline, the main response strip all agree) but is implemented as five-plus separate inline string templates rather than one shared helper; `flowDurationLabel` in `flowView.ts` is the only place this was factored out, and only Flow uses it.
- Codegen modals (`RequestCodeModal`, `ResponseExampleCodeModal`, `GrpcurlCommandModal`) are the most internally consistent family in this area — same `Modal` + header + `field-grid` + `pre.generated-code` + `button-row` shape — with one small miss: `GrpcurlCommandModal`'s Copy button isn't disabled when there's nothing to copy, unlike its two siblings.

## Run-results comparison table

| | **Runner** (`RunnerPanel.svelte`) | **Flow run** (`FlowRunPanel.svelte`) | **History** (`HistoryPanel.svelte`) | **Response Timeline** (`ResponseInspector.svelte`) |
|---|---|---|---|---|
| Structure | `<table>` (144-151) | `<ol>` of cards (127-173) | `<ul>` of cards (81-120) | `<div>` of expandable `<article>` (439) |
| Columns shown | Iter*, Name, Status, Code, Time, Error (145) | chip, position+id, method+request, status, duration, plus always-open assertions & extracted values | method, url, status, duration + relative time, name, redacted note, actions (84-117) | status, method/kind, url/message, phase + duration (439) |
| Status representation | plain text word, **no color** (148) | 4-state pill chip (pending/running/passed/failed), colored (131, 297-327); status code itself is plain text (138) | 2-tier color: `>=400` bad, `>=200` ok — **3xx counts as "ok"** (26-31, 87) | plain text, **no color** (439) |
| Timing format | `{result.durationMs} ms` inline (148) | `flowDurationLabel()` → `${Math.round(v)} ms` (flowView.ts:153-157) | `{entry.durationMs ?? 0} ms · {relativeTime(entry.at)}` (88) — only surface with a relative-time humanizer (33-42) | `{entry.duration \|\| 0} ms` inline (439) |
| Pass/fail badges | none per-row; only the aggregate summary line above has `.ok`/`.bad` text (132-133) | yes — chip + ✓/✗ per assertion (154-157) | color text only, no chip | none (these are protocol phases, not pass/fail) |
| Row expansion | none — Error is a plain cell | none — everything is always inline | none — no way to see headers/body, only "Open in tab" | **yes** — click to expand source/kind/payload/error, gRPC metadata & trailer tables |
| Filtering | none | none | **yes** — text query + method dropdown + failures-only checkbox (47-63) | **yes** — text search + phase dropdown with live per-filter counts (435) |
| Export | none | none | none (Clear only; Save-to-collection is a mutation) | **yes** — Copy (JSON) + Export, with its own `exportBusy` loading state (435) |
| Cancellation | yes — live status region + Cancel button (41-57) | no — a flow run cannot be cancelled | n/a | n/a |

Runner — the surface a user meets first, running a whole collection — is the least capable and the least legible of the four: no color signal for pass/fail on the rows themselves, no way to filter to just the failures, no way to expand a failed row to see what happened, nothing to export. History and the Timeline each independently built filtering; Flow and the Timeline each independently built a richer per-row model; none of that got shared back into Runner.

## Formatting audit

| Value | Where | Exact format | Notes |
|---|---|---|---|
| Duration | `flowView.ts:156` (`flowDurationLabel`) | `` `${Math.round(value)} ms` `` | Only shared helper for duration in this area |
| Duration | `RunnerPanel.svelte:148` | `` `{result.durationMs} ms` `` | Inline, no rounding guard |
| Duration | `HistoryPanel.svelte:88` | `` `{entry.durationMs ?? 0} ms` `` | Inline |
| Duration | `lib/workbench/ResponseInspector.svelte:439` (Timeline) | `` `{entry.duration \|\| 0} ms` `` | Inline |
| Duration | `lib/workbench/ResponseInspector.svelte:417` (compare view) | `` `{response?.durationMs ?? 0} ms` `` / `` `${compareTarget.duration} ms` `` | Two more inline copies in the same file |
| Duration | `lib/workbench/commandState.ts:118` | `` `${response?.durationMs ?? 0} ms` `` | Main response command strip — the "canonical" surface |
| Duration | `App.svelte:7754`, `9050`, `11622` | `` `${row.durationMs ?? 0} ms` `` / `{row.durationMs} ms` | Three more inline copies |
| **→ Recommended** | | `formatDurationMs(ms)` → `"{Math.round(ms)} ms"`, exported once, imported everywhere above | The visible output already agrees everywhere by coincidence; the seven-plus separate inline copies are a latent-drift risk, not a live bug |
| Byte size | `lib/workbench/commandState.ts:16-25` (`formatRuntimeBytes`) | `"0 B"`, else `amount.toFixed(precision) + " " + unit` (B/KB/MB/GB), precision 0 above 10 units or for bytes, else 1 | The only byte formatter in the app |
| Byte size | `HistoryPanel.svelte` | **not rendered** — `HistoryEntry.size` exists on the wire type (`wailsjs/go/models.ts:813`) and is never shown | Gap, not a format mismatch — see A8-07 |
| Byte size | Flow / Runner | not applicable — `FlowStepResult`/`RunResult` carry no size field from the backend | Backend limitation, not a UI inconsistency |
| **→ Recommended** | | Reuse `formatRuntimeBytes` in History once size is rendered | |
| Status color (2xx/3xx/4xx/5xx) | `lib/workbench/commandState.ts:87` (main response pane) | 3-tier: `<300` success, `<400` warning, else danger (plus idle/cancelled) | The most correct bucketing of the four |
| Status color | `HistoryPanel.svelte:26-31` (`statusClass`) | 2-tier: `error` or `>=400` → bad, `>=200` → ok | **3xx reads as "ok" here but "warning" in the response pane for the identical status code** |
| Status color | `RunnerPanel.svelte:148` | none | Status/Code cells are always default text color |
| Status color | `FlowRunPanel.svelte:138` | none | Status code text is plain; color lives entirely on the separate state chip, driven by assertion/error, not by the code |
| **→ Recommended** | | One shared `statusTone(status, cancelled?)` returning `success \| warning \| danger \| idle`, matching `commandState.ts:87`'s 3-tier bucketing, used by History and (where a code is shown) Runner and Flow | |
| Timestamp ("when") | `HistoryPanel.svelte:33-42` (`relativeTime`) | `"{n}s ago"` / `"{n}m ago"` / `"{n}h ago"` / `"{n}d ago"` | Only relative-time humanizer in the app |
| Timestamp ("when") | `lib/openApiSync.ts:73-78` (`formatOpenAPISyncCheckedAt`) | `date.toLocaleTimeString()` (locale time-of-day, e.g. "3:45:12 PM") | |
| Timestamp ("when") | `App.svelte:2309` (OAuth token expiry) | `` `expires ${value.toLocaleString()}` `` (locale date+time) | |
| **→ Recommended** | | Not necessarily one function (these answer different questions — "how long ago" vs "at what wall-clock time" — and both are legitimate), but the three should be named and documented as the app's two sanctioned timestamp styles rather than left as three ad hoc call sites | |

## Findings

### A8-01 — Four different rules for "is this status good", disagreeing on the same code
- **Severity**: critical
- **Where**: `frontend/src/lib/workbench/commandState.ts:87` (response pane, 3-tier); `frontend/src/lib/views/HistoryPanel.svelte:26-31` (History, 2-tier); `frontend/src/lib/views/RunnerPanel.svelte:148` (Runner, no color); `frontend/src/lib/views/flows/FlowRunPanel.svelte:138` (Flow, code ignored)
- **What the user sees**: Send a request that 3xx-redirects. In the response pane it shows amber/"warning" styling (`commandState.ts:87`: `status < 400` → `'warning'`). The same request, viewed later in History, shows green/"ok" text (`HistoryPanel.svelte:28-29`: `status >= 200` → `'ok'`, and 3xx never hits the `>=400` branch). Run the same request from the Runner and its Status/Code cells carry no color at all. Run it as a Flow step and the status code renders as plain text — Flow only colors the pass/fail chip, and that chip is driven by whether an assertion failed, not by the status code.
- **Why it's wrong**: This is the single clearest, most checkable instance of "looks like a different app": the same HTTP status code is graded three different ways (good / not-good / ungraded) depending only on which panel you're looking at it in. A user who learns "green means fine" in History will be surprised when a nearly-identical response is amber in the response pane.
- **Proposed fix**: Extract the response pane's 3-tier bucketing (`commandState.ts:87`) into a shared `statusTone(status, cancelled?)` helper and use it for History's status color and for Runner's Status/Code cells. For Flow, keep the chip driven by pass/fail (that's the right signal for a flow step) but color the status-code text itself with the same shared bucketing so it doesn't read as inert.
- **Shared primitive it should use**: a single `statusTone()` in a shared lib module, imported by `commandState.ts`, `HistoryPanel.svelte`, `RunnerPanel.svelte`, and `FlowRunPanel.svelte`.

### A8-02 — OpenAPI Spec Diff badges and cells are hardcoded light-mode-only colors
- **Severity**: critical
- **Where**: `frontend/src/style.css:4639-4656` (`.openapi-spec-diff-badge.added/.changed/.removed`), `frontend/src/style.css:4723-4734` (`.openapi-spec-diff-cell.added/.removed/.changed`)
- **What the user sees**: In dark mode, opening Spec Diff (`SpecDiffModal.svelte`) shows "New in Spec" / "Updated in Spec" / "Removed from Spec" badges and diff-line highlighting in pale, light-mode pastel colors (`#dcfce7`, `#fef9c3`, `#fee2e2` backgrounds with `#166534`/`#854d0e`/`#991b1b` text) sitting inside an otherwise dark dialog.
- **Why it's wrong**: The app themes via `html[data-theme="dark"]` (`style.css:151`) and every other status color in this area — Flow's `.flow-chip-passed`/`.flow-chip-failed` (`FlowRunPanel.svelte:317-327`), the response pane's success/warning/danger, Runner's cancelled-row color — is expressed with `var(--success-bg)`, `var(--danger-bg)`, `var(--danger-border)`, `var(--warning-strong)`, all of which are redefined for dark mode (`style.css:79-183`). The diff badges/cells are the only status-color usage in this area with no theme-aware definition anywhere in the file — grepping the whole stylesheet, `.openapi-spec-diff-badge.added` etc. each appear exactly once.
- **Proposed fix**: Replace the hardcoded hex with the existing tokens: `added` → `var(--success-bg)`/`var(--accent-strong)` (matching Flow's "passed" chip), `removed` → `var(--danger-bg)`/`var(--danger-strong)`, `changed` → `var(--warning-*)` family, with a `color-mix` border the way `.flow-run-step-stopper` does it (`FlowRunPanel.svelte:264-268`).
- **Shared primitive it should use**: the same `--success-bg`/`--danger-bg`/`--warning-*` token family Flow's chips already use.

### A8-03 — Runner is the least capable of the four "run results" surfaces
- **Severity**: major
- **Where**: `frontend/src/lib/views/RunnerPanel.svelte:144-151`
- **What the user sees**: Runner's result table has no per-row color for pass/fail, no way to filter to just failures, no way to expand a row to see the request/response that produced an error (the `Error` column is a raw text cell), and no export. History (filter), the response Timeline (filter + expand + export), and Flow (color-coded chip + inline assertion/extraction detail) each solved one or more of these independently; Runner — arguably the surface where "which of my 40 requests failed and why" matters most — solved none of them.
- **Why it's wrong**: this is the practical cost of the app having four separate "run results" implementations instead of one shared one: capability built for one surface doesn't propagate to the others, and the oldest surface (Runner) has stagnated the furthest behind.
- **Proposed fix**: at minimum, give Runner's Status/Code cells the same color treatment as History (via the shared `statusTone()` from A8-01), and add a results-only-failures filter mirroring History's checkbox.
- **Shared primitive it should use**: see "Cross-cutting primitives" below — a shared run-results row component/pattern.

### A8-04 — Flow's `busy` prop is a boolean; every sibling panel uses the app's `busy: string` convention
- **Severity**: major
- **Where**: `frontend/src/lib/views/flows/FlowTab.svelte:38`, `frontend/src/lib/views/flows/FlowRunPanel.svelte:38`, vs. `frontend/src/lib/views/RunnerPanel.svelte:21`, `frontend/src/lib/views/HistoryPanel.svelte:15`, `frontend/src/lib/views/ImportPanel.svelte:46`, `frontend/src/lib/modals/collection/GenerateDocsModal.svelte:15`, `frontend/src/lib/modals/collection/ShareCollectionModal.svelte:13`, `frontend/src/lib/modals/openapi/SyncSettingsModal.svelte:12`; conversion at `frontend/src/App.svelte:11118` (`busy={busy !== ''}`)
- **What the user sees**: nothing directly broken today (Flow tracks its own `running` boolean for the "Running…" label), but the app-wide `busy: string` convention exists specifically so a button can say what's happening — `ShareCollectionModal.svelte:129` shows "Exporting..." only because it can check `busy === 'share collection'`. Flow structurally cannot do this: App.svelte throws the operation name away before it reaches `FlowTab`.
- **Why it's wrong**: it's evidence Flow was built as its own subsystem rather than integrated into the existing prop contract every other panel in this audit shares. It also forecloses giving Flow's Save/Delete buttons the same "what's happening" labels the rest of the app can show for free.
- **Proposed fix**: change `FlowTab`/`FlowRunPanel`'s `busy` prop to `string` and pass the real value through from `App.svelte` instead of collapsing it at the call site.
- **Shared primitive it should use**: the app's existing `busy: string` prop convention.

### A8-05 — `HistoryPanel.svelte` hardcodes spacing, radius, and border color instead of using tokens
- **Severity**: major
- **Where**: `frontend/src/lib/views/HistoryPanel.svelte:125-176`
- **What the user sees**: no visible defect today, but the file is an outlier: `gap: 0.75rem` / `0.5rem` / `0.25rem` (lines 128, 136, 146, 153, 162) where the token system defines `--space-12`/`--space-8`/`--space-4` for exactly those pixel values (`style.css:11-18`); `border-radius: 4px` (line 156) where `--radius-4` exists (`style.css:39`); `border: 1px solid var(--border, rgba(127, 127, 127, 0.3))` (line 155) — a hardcoded rgba fallback not used anywhere else in this area (every other file in this audit writes `var(--border)` with no fallback, e.g. `FlowRunPanel.svelte:238`, `FlowStepEditor.svelte:324`).
- **Why it's wrong**: this is the one file in the Flows/Runner/History group that isn't token-disciplined — everything else audited here (`FlowTab`, `FlowStepEditor`, `FlowRunPanel`) uses `var(--space-*)`/`var(--radius-*)` exclusively. It's a straightforward, low-risk fix that removes one more small source of "this corner feels different."
- **Proposed fix**: replace the `rem` literals with the matching `--space-*` tokens, `4px` with `var(--radius-4)`, and drop the rgba fallback on `var(--border)`.
- **Shared primitive it should use**: `--space-*` / `--radius-*` tokens, as used by every other file in this area.

### A8-06 — Import's warning color is a bespoke hex, unrelated to `--warning-strong`, and doesn't adapt to dark mode
- **Severity**: major
- **Where**: `frontend/src/style.css:5625` (`.import-preview-row.warning { border-color: color-mix(in srgb, #d99a26 52%, var(--border)); }`), `frontend/src/style.css:5631` (`.import-row-warning { color: #b87913; ... }`)
- **What the user sees**: an import row with warnings (lossy conversion notes, skipped items) is bordered/colored in a hardcoded amber that is neither the light-theme `--warning-strong` (`#9b6b16`, `style.css:81`) nor the dark-theme one (`#f2ce84`, `style.css:182`) — it's a third, unrelated amber that never changes with theme.
- **Why it's wrong**: in dark mode, every other warning in the app (Runner's live-run status, the response pane's 3xx warning, notification badges) correctly brightens to the dark-theme `--warning-strong`; Import's warning text stays the same low-contrast light-mode amber, so import warnings will look visually muted/off compared to warnings anywhere else in a dark-themed session.
- **Proposed fix**: replace both hardcoded hex values with `var(--warning-strong)` (and a `color-mix(in srgb, var(--warning-strong) 52%, var(--border))` border, matching the pattern already used for `.import-preview-row.error { border-color: var(--danger); }` one line above it at `style.css:5624`, which *does* use a token).
- **Shared primitive it should use**: `--warning-strong`.

### A8-07 — History has response size data and a shared formatter available, but never shows size
- **Severity**: minor
- **Where**: `frontend/src/lib/views/HistoryPanel.svelte` (no reference to `entry.size` anywhere in the file); wire type at `frontend/wailsjs/go/models.ts:813` (`HistoryEntry.size?: number`); formatter at `frontend/src/lib/workbench/commandState.ts:16-25` (`formatRuntimeBytes`)
- **What the user sees**: a history row shows method, URL, status, duration, and relative time, but never the response size — even though the backend already records it and the app already has a byte formatter used one screen over, in the response command strip (`commandState.ts:119`).
- **Why it's wrong**: size is one of the four core response metrics the task brief calls out (status, duration, size, timestamp) and History is missing exactly one of the four, silently, despite having the data.
- **Proposed fix**: add a size cell to `.history-summary` (`HistoryPanel.svelte:84-89`) using `formatRuntimeBytes(entry.size)`.
- **Shared primitive it should use**: `formatRuntimeBytes` from `lib/workbench/commandState.ts`.

### A8-08 — `GrpcurlCommandModal`'s Copy button isn't disabled when there's nothing to copy
- **Severity**: minor
- **Where**: `frontend/src/lib/modals/codegen/GrpcurlCommandModal.svelte:19`, vs. `frontend/src/lib/modals/codegen/RequestCodeModal.svelte:39` and `frontend/src/lib/modals/codegen/ResponseExampleCodeModal.svelte:34`
- **What the user sees**: `RequestCodeModal` and `ResponseExampleCodeModal` both disable their Copy button with `disabled={!requestGeneratedCode}` / `disabled={!responseExampleGeneratedCode}`. `GrpcurlCommandModal`'s Copy button (`<button class="primary" type="button" on:click={copyGrpcurlCommand}>Copy</button>`) has no such guard — if the dialog is ever shown before generation completes, or generation produced an empty string, Copy is silently clickable and copies nothing.
- **Why it's wrong**: this is otherwise the most internally-consistent modal family in this area (same `Modal` + header + `field-grid` + `pre.generated-code` + `button-row` shape across all three); this is the one place the pattern isn't followed through.
- **Proposed fix**: add `disabled={!generatedGrpcurlCommand}` to match its two siblings.
- **Shared primitive it should use**: the same disabled-guard pattern already used in `RequestCodeModal.svelte:39` / `ResponseExampleCodeModal.svelte:34`.

### A8-09 — Environment "no color set" fallback differs between the docs modal and the rest of the app
- **Severity**: polish
- **Where**: `frontend/src/lib/modals/collection/GenerateDocsModal.svelte:77` (`style={`background: ${env.color || '#64748b'}`}`), vs. `frontend/src/App.svelte:11193` (`value={selectedGlobalEnvironment.color || '#2f8cff'}`)
- **What the user sees**: an environment with no color set shows as gray (`#64748b`) in the Generate Docs modal's environment checklist, but the environment editor itself defaults an uncolored environment's color input to blue (`#2f8cff`) — two different "this environment has no color yet" defaults for the same underlying concept, neither of which is a design token.
- **Proposed fix**: reuse the same default hex (or a shared `--muted`-based token) in both places.
- **Shared primitive it should use**: a single constant for the "no environment color set" default, ideally token-based.

### A8-10 — Ellipsis character inconsistency in `ShareCollectionModal`
- **Severity**: polish
- **Where**: `frontend/src/lib/modals/collection/ShareCollectionModal.svelte:129` (`'Exporting...'` — three ASCII periods), vs. `frontend/src/lib/views/flows/FlowRunPanel.svelte:72` (`'Running…'`) and `frontend/src/lib/workbench/ResponseInspector.svelte:435` (`'Saving…'`) — real ellipsis character
- **What the user sees**: a small typographic inconsistency between busy-state button labels in this area — most use the single "…" character, `ShareCollectionModal`'s Proceed button uses three periods.
- **Proposed fix**: change `'Exporting...'` to `'Exporting…'`.
- **Shared primitive it should use**: none needed beyond a consistent character choice; worth a lint rule if this recurs elsewhere in the codebase (it does, e.g. `CloneFolderModal.svelte:92`, `CloneRequestModal.svelte:100`, outside this area's scope).

## Cross-cutting primitives this area needs

- **`statusTone(status, cancelled?)`** — one shared bucketing function (2xx/3xx success-ish/warning, 4xx/5xx danger, matching the response pane's existing 3-tier logic at `commandState.ts:87`), imported by History, Runner, and Flow instead of each inventing its own rule (A8-01).
- **`formatDurationMs(ms)`** — one shared `"{n} ms"` formatter to replace the ~7 inline copies across `App.svelte`, `RunnerPanel.svelte`, `HistoryPanel.svelte`, `ResponseInspector.svelte`, and `commandState.ts`. The output already agrees everywhere; centralizing it removes the risk of that agreement drifting.
- **A shared "run results" row/list pattern** — something Runner, Flow, and History could all consume so that a capability (color, filter, expand, export) built for one of them stops requiring three more PRs to reach the others (A8-03).
- **A promoted `.sr-only` utility** — `FlowRunPanel.svelte:361-371` defines a local screen-reader-only class because `style.css` has none; worth moving into the shared stylesheet so the next feature that needs it doesn't reinvent it too, and so it's available to Runner/History if they ever add icon-only status indicators.
- **`busy: string` as the enforced convention** — Flow's panels should take the same `busy: string` prop every other panel in this audit does (A8-04), so button labels can name the in-flight operation instead of just disabling.
- **Token-only styling discipline for the remaining outliers** — `HistoryPanel.svelte` (A8-05) and the OpenAPI/Import hardcoded colors (A8-02, A8-06) are the only places in this area that don't exclusively use `--space-*`/`--radius-*`/color tokens; `FlowTab`/`FlowStepEditor`/`FlowRunPanel` already demonstrate the standard the rest of the area should be brought up to.
