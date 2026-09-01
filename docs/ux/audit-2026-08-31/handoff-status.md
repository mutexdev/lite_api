# Handoff — W-D (A8: Flows, Runner, History, Import)

Scope worked: A8-01, A8-03, A8-04, A8-05, A8-06, A8-07, and the formatting audit.
A8-02 could not be applied here (it lives in `style.css`) — the exact replacement is in
[§3](#3-styleccss-edits-you-must-apply) below.

## Verification (from `frontend/`)

| gate | result |
|---|---|
| `npm run check` | **271 files, 0 errors, 0 warnings** |
| `npm test` | **1195 tests, 1195 pass, 0 fail** |
| `npm run lint` | **clean** |

All three gates are fully green. (Mid-session there were 5 failures in
`test/bodyHighlight.test.mts` / `test/bodyMode.test.mts` — untracked files belonging to the
response-inspector implementer working concurrently — and 2 pre-existing eslint `no-undef`
errors in `App.svelte`. Both were fixed by their owners before I finished; neither was mine
and neither is outstanding.)

**On the A8-04 compile break:** the brief said `npm run check` would fail at the App.svelte
Flow call site until you applied the change. **You have already applied it** — `App.svelte:11242`
now reads `{busy}` rather than `busy={busy !== ''}`. That is why `check` is fully clean rather
than showing the one flagged error. The edit is recorded in [§2](#2-appsvelte-edits) anyway,
in case it needs re-applying after a merge.

---

## 1. What changed, file by file

### New files

**`frontend/src/lib/statusTone.ts`** — the single answer to "what tone is this status".
`statusTone(status)`, `resultTone({status, error, cancelled})`, `outcomeTone(verdict)`,
`toneClass(tone)`, `toneLabel(tone)`.

The header comment argues the 3xx decision at length; the short version:
**a visible 3xx is amber.** LiteAPI follows redirects by default
(`internal/types/request_item.go:87` — `FollowRedirects: true, MaxRedirects: 5`), so a 3xx
only *reaches* a results row when the user turned following off, the chain ran past
MaxRedirects, or the Location was unusable. In every one of those the response on screen is
a *pointer to* the resource, not the resource. Nothing failed, so red would be a lie; but
the request did not deliver what was asked for, so green hides the commonest cause of
"my assertion passed but the body is empty". The bucketing is byte-for-byte the response
pane's existing 3-tier rule, so adopting it there is a no-op and a correction everywhere else.

`toneClass` maps `success→ok`, `warning→warn`, `danger→bad`, `idle→''`. Note **`.warning` is
not a global class** — `style.css` defines it only as `.runner-summary .warning`. Writing it
on a row paints nothing.

**`frontend/src/lib/formatting.ts`** — `formatDurationMs`, `formatDurationMsOrZero`,
`formatBytes`, `formatStatusCode`, `formatRelativeTime`, `formatWallClockTime`.
`formatBytes` reproduces `formatRuntimeBytes` exactly (same tests, both suites).
The two timestamp helpers are deliberately kept separate and both named — they answer
different questions ("how long ago" for a list row, "at what time" for a fact you correlate
with something outside the app), which is what the audit asked for.

**`frontend/src/lib/runResults.ts`** — `runResultSearchText(fields[])` and
`runResultMatches(row, filter)`. "Failures only" is pinned to `tone === 'danger'`, which is
*exactly* the backend's own rule at `internal/history/history.go:311`
(`Error != "" || Status >= 400`), so the checkbox means the same thing on all three surfaces
and the same thing the server already meant. Search fields join on `\n` so two adjacent
fields cannot match as one phrase.

**`frontend/src/lib/RunResultRow.svelte`** — the shared row. Anatomy copied from the response
Timeline (which I cannot edit, so it is the fixed point the other three move onto):
`[badge] [status] [method] [title …] [metrics]` as one full-width `aria-expanded` button,
with a detail region below. Flow's four-state chip was promoted into this component as the
optional `badge`, which is how the Runner's four verdict words got a pill at all. The status
cell carries a visually hidden `toneLabel()` word so colour is never the only signal.
`statusTestId`/`badgeTestId` props exist purely so surfaces keep their existing test hooks.

**Tests:** `frontend/test/statusTone.test.mts`, `frontend/test/formatting.test.mts`,
`frontend/test/runResults.test.mts`. The status table is driven over 0, ±1 of every class
boundary (199/200, 299/300, 399/400, 499/500, 599/600), plus `undefined`, negative, `NaN`
and `Infinity`, with a stated reason per row.

### Modified files

**`frontend/src/lib/views/HistoryPanel.svelte`** — rewritten in runes (`$props`/`$bindable`;
the four `bind:` props from App.svelte are unchanged and still bind). Toolbar is now
`PaneToolbar` + `FindBar`. Rows are `RunResultRow`. Gains: `resultTone` colour (A8-01),
**response size** via `formatBytes(entry.size)` (A8-07 — the data was on the wire all along),
row expansion showing request/response headers and the transport error, and all
`--space-*`/`--radius-*` tokens with the `var(--border, rgba(127,127,127,0.3))` fallback
gone (A8-05). Every `data-testid` preserved; `history-open-tab` and `history-save-collection`
now live inside the expanded row.

**`frontend/src/lib/views/RunnerPanel.svelte`** — rewritten in runes. The `<table>` became a
`RunResultRow` list. Gains all four capabilities it lacked (A8-03): per-row colour, a
`FindBar` + "Failures only" filter, expansion for the error (which was a plain cell competing
for width with five others), and a **Copy** button that exports the *filtered* rows as JSON
via `navigator.clipboard`. Clipboard rather than a save dialog because a file dialog needs a
binding routed through App.svelte, which I cannot edit.

*Note:* the `state` prop had to be destructured as `state: runnerState` — Svelte 5 errors on
a local binding named `state` beside the `$state` rune. **The prop name is unchanged**;
App.svelte still passes `state={appState}`.

**`frontend/src/lib/views/flows/FlowRunPanel.svelte`** — `busy: boolean` → `busy: string`
(A8-04). Step cards became `RunResultRow`s; assertions / extracted values / the error moved
behind the expander, **except the step that stopped the run, which opens by itself** so the
file's "a failed run has to name the step that stopped it" rule survives. Status code is now
toned by `statusTone` while the badge stays driven by pass/fail — the audit's own
recommendation, and they are allowed to disagree (a 200 that failed an assertion). Added the
same `PaneToolbar` + `FindBar` + "Failures only". The local `.flow-chip*`, `.flow-run-step*`
and stopper CSS was deleted — it moved into `RunResultRow`.

**`frontend/src/lib/views/flows/FlowTab.svelte`** — `busy: boolean` → `busy: string` (A8-04),
with a `const disabled = $derived(busy !== '')` for the ~18 `disabled=` sites. Save now says
**"Saving…"** on `busy === 'save flow'` and Delete says **"Deleting…"** on
`busy === 'delete flow'` — the thing the convention exists for and Flow structurally could
not do before.

**`frontend/src/lib/flowView.ts`** — `flowDurationLabel` is now a thin alias over
`formatDurationMs` (same name, same behaviour, same tests). `FlowRunRow` gained `status:
number` and `searchText: string`. New `flowStepBadgeTone(state)` maps Flow's four step states
onto the shared badge's tones.

**`frontend/src/lib/views/ImportPanel.svelte`** — added a scoped `<style>` block fixing A8-06
with `var(--warning-strong)`. **This is an override, not a fix at source** — see
[§3.2](#32-a8-06--imports-warning-colour); please delete the two `style.css` rules *and* this
scoped block together.

---

## 2. App.svelte edits

### 2.1 A8-04 — the Flow `busy` prop (**already applied by you**)

At the `<FlowTabComponent …>` call site (currently `src/App.svelte:11242`):

```diff
-                busy={busy !== ''}
+                {busy}
```

Nothing else at that call site changes. `FlowTab`/`FlowRunPanel` now take `busy: string`, and
the operation names they check for are your own `runAction` labels, `'save flow'` and
`'delete flow'`.

### 2.2 Formatting adoption (optional, but this is the point of centralising)

Add to App.svelte's imports:

```ts
import { formatDurationMs, formatWallClockTime } from './lib/formatting'
```

Then three inline duration copies:

```diff
@@ networkLogLines(), src/App.svelte:7821
-      `Duration: ${row.durationMs ?? 0} ms`,
+      `Duration: ${formatDurationMs(row.durationMs)}`,
```

```diff
@@ DevTools network table, src/App.svelte:9123
-                            <td>{row.durationMs} ms</td>
+                            <td>{formatDurationMs(row.durationMs)}</td>
```

```diff
@@ Network Log view, src/App.svelte:11755
-                <tr><td>{row.method}</td><td>{row.url}</td><td>{row.status}</td><td>{row.durationMs} ms</td><td>{row.error}</td></tr>
+                <tr><td>{row.method}</td><td>{row.url}</td><td>{row.status}</td><td>{formatDurationMs(row.durationMs)}</td><td>{row.error}</td></tr>
```

And `networkLogTime` becomes the shared wall-clock helper (`src/App.svelte:7793-7798`):

```diff
   function networkLogTime(row: types.NetworkLog) {
-    if (!row.at) return '-'
-    const value = new Date(row.at)
-    if (Number.isNaN(value.getTime())) return '-'
-    return value.toLocaleTimeString()
+    return formatWallClockTime(row.at) || '-'
   }
```

### 2.3 A FIFTH status→tone rule the audit did not find

`src/App.svelte:8496-8501`:

```ts
  function responseStatusClass(status?: number) {
    if (!status) return 'muted'
    if (status < 300) return 'ok'
    if (status < 400) return 'warn'
    return 'bad'
  }
```

The audit counted four rules for A8-01; this is a fifth. It happens to *agree* with the one I
made canonical (it is the only one of the five that already got 3xx right), so it is not a
live bug — but it is a fifth copy, and the whole point of `statusTone.ts` is that there is
one. Replace it:

```diff
-  function responseStatusClass(status?: number) {
-    if (!status) return 'muted'
-    if (status < 300) return 'ok'
-    if (status < 400) return 'warn'
-    return 'bad'
-  }
+  // A8-01 — the app's one bucketing. 'muted' rather than '' for no-status
+  // because this particular call site wants a visible grey, not nothing.
+  function responseStatusClass(status?: number) {
+    return toneClass(statusTone(status)) || 'muted'
+  }
```

with `import { statusTone, toneClass } from './lib/statusTone'`.

---

## 3. style.css edits you must apply

### 3.1 A8-02 — OpenAPI Spec Diff badges and cells (critical, light-mode-only hex)

Replace **`src/style.css:4790-4806`** (the three `.openapi-spec-diff-badge` variants) with:

```css
/*
  A8-02 — these three were the only status colours in the app with no
  theme-aware definition anywhere: raw pastel hex (#dcfce7 / #fef9c3 / #fee2e2
  with #166534 / #854d0e / #991b1b text) sitting inside a dialog that goes dark
  with html[data-theme="dark"]. In dark mode the Spec Diff badges were three
  light-mode pastels on a dark ground.

  They now use the same token family Flow's chips already used, so a future
  theme gets them right without knowing they exist. Borders are color-mix'd
  against transparent the way .run-result-badge.tone-success does it, so the
  edge stays proportionate in both themes rather than being a second solid.
*/
.openapi-spec-diff-badge.added {
  color: var(--accent-strong);
  border-color: color-mix(in srgb, var(--accent) 40%, transparent);
  background: var(--success-bg);
}

.openapi-spec-diff-badge.changed {
  color: var(--warning-strong);
  border-color: var(--warning-border);
  background: var(--warning-bg-soft);
}

.openapi-spec-diff-badge.removed {
  color: var(--danger-strong);
  border-color: var(--danger-border);
  background: var(--danger-bg);
}
```

Replace **`src/style.css:4874-4884`** (the three `.openapi-spec-diff-cell` variants) with:

```css
/*
  The diff LINES, not the badges: these are a wash behind code, so they are a
  low-alpha mix of the same three semantic colours rather than the solid
  backgrounds above. color-mix against transparent keeps them readable over
  --code-bg in dark and over --surface in light, which the fixed
  rgba(34,197,94,0.14) could not do — that green was mixed for a white ground.
*/
.openapi-spec-diff-cell.added {
  background: color-mix(in srgb, var(--success) 16%, transparent);
}

.openapi-spec-diff-cell.removed {
  background: color-mix(in srgb, var(--danger-strong) 16%, transparent);
}

.openapi-spec-diff-cell.changed {
  background: color-mix(in srgb, var(--warning) 18%, transparent);
}
```

Leave `.openapi-spec-diff-cell.active-change` alone — it already uses `var(--accent)`.

### 3.2 A8-06 — Import's warning colour

**Delete** the hardcoded halves of these two lines. `src/style.css:5776`:

```diff
-.import-preview-row.warning { border-color: color-mix(in srgb, #d99a26 52%, var(--border)); }
+.import-preview-row.warning { border-color: color-mix(in srgb, var(--warning-strong) 52%, var(--border)); }
```

`src/style.css:5782`:

```diff
-.import-row-warning { color: #b87913; margin: 7px 0 0; }
+.import-row-warning { color: var(--warning-strong); margin: 7px 0 0; }
```

**Then delete the `<style>` block at the bottom of
`frontend/src/lib/views/ImportPanel.svelte`.** It exists only because `style.css` was not
mine to edit; it wins on specificity today, and leaving both would mean two definitions of
one colour — the exact disease this campaign is treating. Its comment says so.

### 3.3 Dead rule after the Runner rewrite

`src/style.css:1701-1704`:

```css
.runner-result-cancelled td {
  color: var(--warning-strong);
  font-weight: 750;
}
```

The Runner no longer renders a `<table>` or a `.runner-result-cancelled` class — a cancelled
row is now an amber `Cancelled` badge via `outcomeTone`. Nothing in `src/` references either
name. Safe to delete.

### 3.4 Promote `.sr-only` (the audit's own cross-cutting recommendation)

`RunResultRow.svelte` and `FlowRunPanel.svelte` each carry a local copy, because `style.css`
has none. Add once to `style.css` and delete both local blocks:

```css
/*
  A visually hidden label. Two components defined this privately because the
  stylesheet had no copy, and every icon-only or colour-only status cue in the
  app needs one — colour and a tick mark are invisible to a screen reader.
*/
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
```

---

## 4. Adopt-the-formatters list, outside my files

| file:line | today | should be |
|---|---|---|
| `src/lib/workbench/commandState.ts:16-27` | `formatRuntimeBytes` (the original) | `export { formatBytes as formatRuntimeBytes } from '../formatting'` — behaviour is identical and `test/commandState.test.mts` passes unchanged |
| `src/lib/workbench/commandState.ts:87` | the 3-tier `tone` ternary | `const tone = resultTone({ status: response?.status, cancelled: response?.cancelled })` — **this is A8-01's canonical site and the one I could not route**; behaviour is unchanged, the point is that it stops being a second copy |
| `src/lib/workbench/commandState.ts:118` | `` `${response?.durationMs ?? 0} ms` `` | `formatDurationMsOrZero(response?.durationMs)` |
| `src/lib/workbench/ResponseInspector.svelte:515` | two inline `${…} ms` in the compare summary | `formatDurationMsOrZero(...)` |
| `src/lib/workbench/ResponseInspector.svelte:537` | `{entry.duration \|\| 0} ms` in the Timeline | `formatDurationMsOrZero(entry.duration)` |
| `src/App.svelte:7821`, `9123`, `11755` | three inline `${…} ms` | `formatDurationMs(...)` — see §2.2 |
| `src/App.svelte:7793` | inline `toLocaleTimeString()` | `formatWallClockTime(row.at) \|\| '-'` |
| `src/App.svelte:8496` | a fifth status→tone rule | `toneClass(statusTone(status))` — see §2.3 |
| `src/lib/openApiSync.ts:77` | `date.toLocaleTimeString()` | `formatWallClockTime(...)` — same style, now named |
| `src/lib/notificationView.ts:59`, `src/lib/cookieView.ts:142`, `src/App.svelte:2329` | `toLocaleString()` (date **and** time) | a **third** style the audit did not name. Either add `formatWallClockDateTime` to `formatting.ts` or fold these into `formatWallClockTime`; do not leave three ad hoc copies |

---

## 5. What the audit missed

1. **A fifth status→tone rule.** `App.svelte:8496` (`responseStatusClass`). A8-01 says four.
   It agrees with the canonical rule, so it is latent rather than live — but the finding's
   count is wrong and the fix list was one site short. See §2.3.

2. **A third timestamp style.** The formatting audit named two ("how long ago" vs
   "at what wall-clock time") and three call sites. There is a third *style*:
   `toLocaleString()` — date **and** time — at `notificationView.ts:59`, `cookieView.ts:142`
   and `App.svelte:2329`, plus a conditional same-day-or-not variant at
   `mcpSettings.ts:561`. That is four call sites answering "when, precisely" four ways.

3. **`ResponseInspector.svelte:429/463/497` measure bytes in a fourth way** —
   `bytes.toLocaleString()` + the literal word `bytes` ("1,024 bytes"), while
   `formatRuntimeBytes` one screen over says "1.0 KB". The audit's byte table lists only the
   one formatter and History's gap; it does not mention that the response body strip and the
   response *summary* disagree with each other **on the same screen**. This is a live,
   visible inconsistency and arguably belongs in A8's findings rather than mine.
   (It is `ResponseInspector`, so out of my scope.)

4. **"Failures only" had no definition anywhere in the frontend.** History's checkbox is
   server-side (`history.go:311`); the audit records the checkbox exists but not what it
   means, so a client-side reimplementation for the Runner had a real chance of picking a
   different rule. `runResults.ts` now names it and cites the Go line.

5. **`RunResult` carries no `size`, and `FlowStepResult` carries neither `size` nor a
   per-step timestamp.** The audit notes this as "backend limitation, not a UI
   inconsistency", which is fair — but the consequence is that the Runner and Flow can show
   only two of the four core response metrics while History and the response pane show four.
   Worth a backend ticket if the goal is uniform surfaces.

6. **A8-08, A8-09 and A8-10 are in `modals/`** and are not mine. Still open unless another
   implementer took them: `GrpcurlCommandModal`'s unguarded Copy, the two different
   "no environment colour" defaults, and `ShareCollectionModal`'s `'Exporting...'` ASCII
   ellipsis.

7. **The response Timeline is now the odd one out.** Runner, Flow and History all draw
   `RunResultRow`. The Timeline (`ResponseInspector.svelte:537`) still hand-rolls a nearly
   identical `<article>`/`<button aria-expanded>` with its own column template and its own
   `{entry.duration || 0} ms`. Its anatomy is what I copied, so switching it over should be
   close to mechanical — and until it happens, four surfaces means three sharing and one
   twin. Recommend it as the follow-up.
