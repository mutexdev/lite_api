# LiteAPI pre-implementation UX audit and delivery plan

Date: 2026-07-19 (America/Chicago)

Status: implementation-ready planning baseline. This document records the incoming dirty worktree, a fresh packaged-app review, competitor patterns, ownership boundaries, and acceptance gates. It does not authorize replacing or reverting any existing work.

## Executive decision

Do not begin with a visual reskin. The next implementation should first repair the responsive shell and then make the request workflow progressively disclose complexity. LiteAPI already has substantial protocol, persistence, accessibility, and response-inspection capability; the usability problem is that too much of it competes for space and the current sub-960px breakpoint makes the app nearly unusable.

The first release slice should deliver:

1. A usable 800x600 minimum layout and stable 1024x768 layout.
2. A resizable/collapsible collection sidebar and request-response split, with persisted safe bounds.
3. One compact create action instead of always-visible collection/request creation forms.
4. Single-line, horizontally scrollable request and response section tabs with keyboard and overflow access.
5. A clearer request target/status/response hierarchy and an informative empty-response state.
6. A true command palette in addition to workspace search.

The visual direction is an original LiteAPI developer workbench: quiet macOS chrome, compact hierarchy, local-first/Git truth, orange only for primary action and active context, and no copied Postman/Bruno/Yaak assets or promotional surfaces.

## Audit scope and evidence boundary

- Repository and current implementation inspected directly.
- Fresh package built with pinned Wails CLI v2.12.0.
- Fresh native app launched with `LITEAPI_DATA_DIR=/tmp/liteapi-ux-audit-20260719`; normal application data was not used.
- Computer Use verified the current workbench, menus, accessibility tree, settings, search, sidebar collapse, request actions, and resizing.
- The installed Postman app requested for comparison is not present. `/Applications/Postman.app`, `/Applications/Bruno.app`, and `/Applications/Insomnia.app` are absent; Computer Use returned `Invalid app: Postman`. No hands-on Postman claim is made.
- Competitor findings therefore use current official product documentation. This is enough for interaction-pattern planning, but a later visual-polish gate should repeat hands-on comparison if the apps become available.
- Existing native evidence remains useful but belongs to an older accepted package hash. Current historical evidence is in `docs/qa/`; fresh Computer Use observations in this document are the source of truth for this audit.

## Exact incoming git baseline

Branch: `main...origin/main` at `fe54084` (`Merge remote main`). No repository `AGENTS.md` applies.

The baseline was captured before this plan file was added:

```text
## main...origin/main
 M app.go
 M app_test.go
 M frontend/package-lock.json
 M frontend/package.json
 M frontend/package.json.md5
 M frontend/src/App.svelte
 M frontend/src/style.css
 M frontend/wailsjs/go/main/App.d.ts
 M frontend/wailsjs/go/main/App.js
 M frontend/wailsjs/go/models.ts
 M frontend/wailsjs/runtime/runtime.d.ts
 M frontend/wailsjs/runtime/runtime.js
 M go.mod
 M go.sum
 M main.go
?? collection_recovery.go
?? collection_recovery_test.go
?? docs/
?? draft_guard.go
?? draft_guard_test.go
?? frontend/src/lib/workbench/
?? native_close.go
?? native_close_test.go
?? native_menu.go
?? native_menu_test.go
?? qa/
?? recovery_store.go
?? recovery_store_security_test.go
?? recovery_types.go
?? request_lifecycle.go
?? request_lifecycle_test.go
?? response_export.go
?? response_export_test.go
?? response_timeline_export.go
?? response_timeline_export_test.go
?? response_timings.go
?? response_timings_test.go
?? shared_state.go
?? shared_state_test.go
?? window_session.go
?? window_session_test.go
?? workspace_migration.go
?? workspace_migration_test.go
?? workspace_registry.go
?? workspace_runtime_security_test.go
?? workspace_service.go
?? workspace_service_test.go
?? workspace_shared_merge_test.go
?? workspace_state_store.go
?? workspace_state_store_test.go
?? workspace_window_lock.go
?? workspace_window_lock_test.go
?? workspace_window_runtime.go
?? workspace_window_runtime_test.go
```

There were 15 tracked modified files and 39 untracked status entries. All are presumed user/Claude/prior-agent work and must be preserved. In particular, `app.go`, `app_test.go`, `App.svelte`, and `style.css` are large, overlapping integration surfaces; they must not have simultaneous implementation owners.

## Technology and architecture

- Go 1.25.1 on arm64; module declares Go 1.25.0.
- Wails v2 application. `go.mod` currently requires Wails v2.10.2; the accepted/reproducible packaging path uses CLI v2.12.0. This mismatch must be resolved or explicitly documented before release.
- Svelte 5.56.4, TypeScript 6.0.3, Vite 8.1.0.
- CodeMirror 6 powers the editor surfaces.
- `app.go` is 42,486 lines and `app_test.go` is 24,205 lines.
- `frontend/src/App.svelte` is 12,255 lines and `frontend/src/style.css` is 5,416 lines.
- Existing extracted workbench modules are `CodeEditor.svelte`, `ProtocolRequestLine.svelte`, `RequestCommandStrip.svelte`, `RequestSettingsPanel.svelte`, `ResponseInspector.svelte`, `WorkspaceWindowPicker.svelte`, `response.ts`, `tabLifecycle.ts`, and `types.ts`.
- Backend capabilities are already split partially into recovery, request lifecycle, response export/timing, native menu/close, shared state, and workspace/window modules.
- The native window defaults to 1024x768. Window/session geometry and response orientation already persist.

The application is not greenfield. Preserve the backend and protocol truth unless a UI contract genuinely requires a small persistence extension.

## Fresh native observations

### What already works

- The primary workbench is recognizable: activity rail, collection tree, request tabs, method/URL/send line, request sections, response sections, environment context, and native menu bar.
- `Cmd+L` selects the request URL.
- `Cmd+K` opens/focuses workspace search.
- `Cmd+\` collapses and restores the collection sidebar.
- Request/response section tabs expose selected states and arrow-key semantics through accessible tab roles.
- Request actions open as an accessible disclosure and Escape closes it.
- Method, URL, Save, Run, Send, response search/copy/download, theme controls, menu commands, and window controls have readable accessibility names.
- Light/Dark/System themes, environment selectors, local/Git context, draft state, TLS/proxy state, response status/time/size, and variable chips are visible.
- Existing response tools and protocol-specific functionality are considerably deeper than the first screen suggests.

### P0 acceptance blocker: sub-960px layout collapse

At approximately 836x647, the `@media (max-width: 960px)` rule changes `.app-shell` from three columns to `grid-template-columns: 1fr`. The vertical `.activity-bar` remains a full-height flex column with `.activity-bar-spacer { flex: 1; }`. It becomes the first full-width grid row and consumes roughly half the window. The collection sidebar is hidden, the toolbar begins near mid-window, and the request/response editor is pushed largely below the fold.

This is a deterministic implementation defect, not a subjective visual critique. Existing M7 evidence covered about 984x768 and larger, so it did not exercise the broken breakpoint.

Required repair: either support an adaptive shell down to 800x600 or set and enforce a larger native minimum. The better product choice is adaptive 800x600: horizontal/compact activity navigation, sidebar drawer/collapsed default, stacked request-response panes, and no page-level dead area.

### P1 workflow and hierarchy findings

1. Sidebar creation controls are permanently expanded. Collection name, request type, request name, and two `+` buttons occupy valuable vertical space before the tree. Replace them with a compact New button/menu and contextual creation dialog or inline quick-create.
2. Request section tabs wrap to as many as three rows at the default window; response tabs wrap as well. The controls consume editor height and their spatial positions move as the window changes. Use one non-wrapping strip, horizontal wheel/trackpad scroll, keyboard roving tab index, and a trailing overflow menu.
3. The toolbar wraps workspace/environment controls into multiple rows at 1024px. Global and collection environments are two visually similar native selects with weak scope explanation. Consolidate them into a compact context cluster with explicit scope labels and keep the primary row stable.
4. The response’s status/time/size summary lives inside the request command strip while response actions live in the response pane. Move the summary to a sticky response header so all response truth is scanned in one place.
5. The empty response pane displays disabled/meaningless operational controls (`0 bytes`, Copy, Download, Search, Previous, Next). Show an intentional empty state; reveal response tools after a response or saved example exists.
6. The response-orientation control is visually rendered as an ellipsis-like square although its accessibility name is correct. Use a recognizable split-layout icon with tooltip and shortcut.
7. The collection tree’s visible `More` label consumes request-row width. Use an accessible ellipsis/menu button shown on hover/focus/selection, with the same full text inside the menu.
8. Folder rows can expose up to seven 32px glyph actions. This overwhelms hierarchy and can starve long folder names. Put destructive/infrequent actions in one contextual menu; retain only expand/select and one primary add action.
9. The activity rail is icon-only. Accessibility names are good, but first-use discoverability is weak. Add reliable tooltips and optional labels at wide widths; keep the original icons.
10. Preferences is one long scrolling page with large theme grids and settings below. Use a settings category sidebar or segmented section index, sticky heading, and narrower readable content width.

### P2 refinement findings

- Native select styling and dense forms are inconsistent with the more polished command strip.
- Very wide Preferences windows leave an oversized empty right region because content does not establish a deliberate readable max width or two-column grouping.
- The visual system uses several textual/glyph stand-ins (`LA`, `DT`, `<<`, `>>`, `x`, `F`, `T`, `C`) beside polished SVG activity icons. Replace them with one coherent icon system while preserving names and shortcuts.
- The current Vite bundle is a single 1,112.12 kB minified JS chunk (338.55 kB gzip). Route/feature-level lazy loading should be considered after the interaction refactor, especially for Dev Tools, Runner, import, cookies, and Preferences.
- Workspace search finds collections/requests but is not a command palette. Keep `Cmd+K` search and add `Cmd+Shift+P` for commands; do not overload one modal with two unclear modes.

## Competitor patterns to adopt selectively

### Postman

Current official documentation describes a stable header/sidebar/workbench/footer model, a configurable sidebar, hover-based item actions, preview tabs, tab overflow arrows/search, `Cmd+K` search, `Cmd+Shift+P` command palette, customizable shortcuts, and response code/time/size/network details in the response context.

Adopt: stable workbench hierarchy, tab overflow/search, command palette, response-local truth, hover/context actions, configurable density.

Do not adopt: cloud/account/promotional surfaces, collaboration clutter, copied icons/branding, or right-side panels that reduce request space without a LiteAPI-specific need.

Sources: [Navigating Postman](https://learning.postman.com/docs/getting-started/basics/navigating-postman/), [Postman response structure](https://learning.postman.com/docs/use/send-requests/response-data/responses/), [Postman shortcuts](https://learning.postman.com/docs/getting-started/installation/settings/shortcut-settings).

### Insomnia

Official documentation emphasizes quick search, sidebar filtering/toggling, URL focus, response focus, environment switching, customizable shortcuts, and protocol-specific request editors with inherited folder settings.

Adopt: direct keyboard focus targets, compact request construction, protocol-aware progressive disclosure, and explicit inheritance.

Source: [Insomnia keyboard shortcuts](https://developer.konghq.com/insomnia/keyboard-shortcuts/), [Requests in Insomnia](https://developer.konghq.com/insomnia/requests/).

### Bruno

Official documentation describes collection-context creation, collection-free scratch requests, inline tab `+` creation, inherited collection settings, an unsaved-until-save lifecycle, filesystem-friendly names, and runner access from the collection menu or top bar.

Adopt: an immediate scratch request path and one contextual New flow. LiteAPI already has scratch/draft lifecycle support; surface it instead of keeping three permanent creation fields.

Sources: [Create a request](https://docs.usebruno.com/get-started/bruno-basics/create-a-request), [Run a collection](https://docs.usebruno.com/get-started/bruno-basics/run-a-collection), [Bruno local-first model](https://docs.usebruno.com/introduction/getting-started).

### Yaak

Yaak’s current product principle is “simple by default, exposing features only when needed,” paired with local-first storage, Git, multi-window, request debugging, command palette, rich previews, and customizable hotkeys.

Adopt: progressive disclosure, calm density, command-driven operation, and explicit local/Git state. Do not imitate its visual assets.

Source: [Yaak product overview](https://yaak.app/), [Yaak documentation](https://yaak.app/docs).

## Target interaction model

### App shell

- Wide (>=1180): 48px activity rail + resizable 260-380px collection sidebar + workbench.
- Medium (960-1179): 44px activity rail + resizable 240-320px sidebar + workbench; compact toolbar with overflow.
- Compact (800-959): horizontal or overlay activity navigation; sidebar becomes a drawer/collapsible overlay; request and response stack vertically; toolbar stays usable in at most two bounded rows.
- Below 800x600: enforce a native minimum rather than render a broken layout.
- Every hidden panel must remain reachable by keyboard and named controls.

### Collection sidebar

- Header: workspace/collection context and one New menu.
- Search stays sticky and shortcut-driven.
- Tree owns independent vertical scroll; menus render above the scroller and are never clipped.
- Drag/reorder is optional later; selection, expansion, context menu, rename, clone, reveal, code, and delete remain keyboard reachable.
- Sidebar width persists per window with min/max validation.

### Tabs and command access

- One non-wrapping document tab strip with active/dirty/protocol state, close button, horizontal scroll, overflow arrows, and tab search/recently closed menu.
- One non-wrapping request section strip and one response section strip; preserve the existing arrow-key behavior.
- `Cmd+K`: search workspace objects.
- `Cmd+Shift+P`: command palette for New, Send, Save, Run, change layout, open tools/settings, and navigation.
- Existing shortcut customization remains authoritative and must reject collisions.

### Request and response workbench

- First scan line: method/protocol + target + primary Send/Connect/Call action.
- Second scan line: inherited environment/auth/TLS/proxy/draft context and secondary Save/Run actions.
- Request editor and response inspector separated by a keyboard-operable splitter in horizontal mode; vertical mode uses a horizontal splitter.
- Response header contains status, status text, time, size, latest/history context, save-example, copy/download, and layout control.
- Response body/search controls appear only when applicable; empty, loading, cancelled, unavailable, streaming, and complete states are visually distinct and truthfully named.

## Implementation milestones and ownership

The root/integration agent owns sequencing, patch review, and acceptance. No implementation worker self-accepts.

### M0 — Baseline protection and component seams

Owner: one frontend integration owner only.

Files: `frontend/src/App.svelte`, `frontend/src/style.css`, `frontend/src/lib/workbench/*`; documentation only elsewhere.

Work:

- Freeze the exact incoming diff and generated binding surface.
- Extract shell, activity navigation, collection sidebar, document tabs, section tabs, and settings navigation into new components without behavioral change.
- Move only the selectors those components own into component-scoped styles or clearly named shell/workbench CSS sections.
- Add component-level tests if the chosen Svelte test harness is introduced; do not add a second UI framework.

Acceptance:

- Pixel/AX parity at 1024x768 before the behavior redesign.
- `svelte-check`, frontend build, Go tests/vet, and generated-binding comparison pass.
- `App.svelte` and `style.css` each have exactly one active owner during extraction.

### M1 — Responsive shell and resizing (first product slice)

Owner: frontend shell owner. If persistence fields are needed, a separate backend owner works only after the frontend contract is frozen.

Frontend files: new shell/sidebar/splitter components, `App.svelte` integration seam, `style.css` or component styles.

Backend files only if required: `main.go` for native minimum size; layout preference/session types in `app.go`, `window_session.go`, `workspace_window_runtime.go`, their focused tests, and regenerated Wails bindings.

Work:

- Repair the <=960px layout defect.
- Implement compact navigation/sidebar behavior.
- Add sidebar and request-response splitters with pointer, touchpad, and keyboard operation.
- Persist clamped widths/ratios; reset invalid or off-screen values.
- Define independent scroll ownership for tree, request editor, response inspector, Dev Tools drawer, and settings content.

Acceptance:

- Native packaged app is usable at 800x600, 984x768, 1024x768, 1223x768, and fullscreen in Light and Dark.
- No dead area, clipped primary action, page-level horizontal scroll, inaccessible hidden panel, or menu clipped by a scroller.
- Splitters have names, values, min/max, arrow-key adjustment, and reset behavior.
- Resize/relaunch restores safe geometry and panel ratios.

### M2 — Creation, sidebar hierarchy, and document tabs

Owner: one collection/tabs frontend owner; no concurrent editor of the same extracted components.

Files: extracted collection sidebar/document tab components, `tabLifecycle.ts`, narrow `App.svelte` wiring, existing Go lifecycle methods only if a missing contract is proven.

Work:

- Replace permanent create fields with one contextual New menu and fast scratch-request flow.
- Consolidate folder/request row actions into accessible menus.
- Add non-wrapping tabs, overflow arrows, tab search/recently closed, dirty state, and drag/reorder only if it does not destabilize keyboard behavior.
- Keep collection/file truth and draft guards unchanged.

Acceptance:

- New HTTP/GraphQL/WebSocket/gRPC requests are reachable from keyboard and UI without pre-filling three sidebar fields.
- 20 open tabs remain navigable at 800, 1024, and wide widths without wrapping.
- Rename/clone/reveal/code/delete and recovery flows remain reachable and tested.
- Closing dirty tabs and quitting retain the current save/discard/cancel guarantees.

### M3 — Request/response hierarchy and progressive disclosure

Owner: workbench owner.

Files: `RequestCommandStrip.svelte`, `ProtocolRequestLine.svelte`, `RequestSettingsPanel.svelte`, `ResponseInspector.svelte`, `response.ts`, narrow `App.svelte` wiring and workbench styles.

Work:

- Stabilize method/target/Send hierarchy.
- Move response status/time/size into the response header.
- Replace wrapped section tabs with a single-line accessible strip and overflow.
- Add truthful empty/loading/cancelled/error/streaming states.
- Replace glyph stand-ins with the approved original icon set and consistent tooltips.

Acceptance:

- HTTP, GraphQL, WebSocket, and gRPC each expose the correct primary action and only relevant settings.
- Response truth, history/example actions, exact download, search, timeline, metadata/trailers, and streaming events remain intact.
- One MiB text and five MiB binary safeguards remain bounded and interactive.
- `Cmd+L`, Save, Send/Connect/Call, cancel/Escape, and response-tab arrows pass native QA.

### M4 — Search, command palette, Preferences, and polish

Owner: command/settings frontend owner after M1-M3 components are stable.

Files: new command/search and settings components, shortcut utilities in `App.svelte` only through a reviewed seam, settings/layout styles.

Work:

- Split search from command execution.
- Add `Cmd+Shift+P` command palette with discoverable shortcuts.
- Add tab search/recently closed integration.
- Reframe Preferences with category navigation and readable content width.
- Consider feature-level lazy loading to reduce the 1.11 MB main JS chunk.

Acceptance:

- Search results distinguish collection/folder/request/example and show path/context.
- Command palette is fully keyboard operable and never executes destructive actions without the existing confirmation/recovery contracts.
- Preferences is usable at 800x600 and 1024x768 without page-level horizontal overflow; keybindings retain bounded two-axis scrolling.

### M5 — Independent packaged acceptance

Owner: independent QA agent with no application-file ownership.

Files: new report/evidence only under `docs/qa/`; implementation owners may repair failures but cannot mark the milestone accepted.

Required matrix:

- Clean isolated data directory, package hash, PID/session/data-dir/owner-lock attribution.
- Light/Dark/System, 800x600, 984x768, 1024x768, wide, fullscreen.
- Mouse/trackpad resize, keyboard splitters, independent scroll regions, overflow menus, 20 tabs.
- Search, command palette, URL focus, sidebar toggle, create/save/send/cancel/close/reopen.
- HTTP, WebSocket, gRPC, and unavailable/error response truth.
- Draft/recovery/relaunch, multi-window isolation/refusal, native menus, accessibility names/states.
- Three consecutive clean-state packaged passes after the last repair.

## Integration and agent boundaries

1. One owner at a time for `frontend/src/App.svelte`.
2. One owner at a time for `frontend/src/style.css`; prefer extracted component styles to create future parallel seams.
3. One backend owner at a time for `app.go`/`app_test.go`; new focused modules are preferred over further growth.
4. Generated Wails bindings have one generator owner and are regenerated only after backend contracts freeze.
5. QA owns evidence/reports only and does not accept its own implementation.
6. Root reviews every milestone, runs `git diff --check`, and compares the Wails exported surface before integration.
7. Workers must preserve the incoming dirty state and must not use reset/restore/revert on files they do not own.

## Commands and current baseline results

Available project commands:

```bash
cd frontend && npm run dev
cd frontend && npm run check
cd frontend && npm run build
env GOCACHE=/tmp/liteapi-audit-gocache go test ./...
env GOCACHE=/tmp/liteapi-audit-gocache go vet ./...
env GOCACHE=/tmp/liteapi-audit-gocache go test -race ./...
env GOCACHE=/tmp/liteapi-audit-gocache go run github.com/wailsapp/wails/v2/cmd/wails@v2.12.0 build -m -nosyncgomod
git diff --check
```

Fresh audit results:

- `npm run check`: pass, 0 errors and 0 warnings.
- `npm run build`: pass; Vite large-chunk advisory for 1,112.12 kB JS.
- `go test ./...`: pass when loopback binding is allowed. The sandboxed attempt failed only because local fixture listeners could not bind ports.
- `go vet ./...`: pass.
- Pinned Wails v2.12.0 package: pass.
- Current executable: `build/bin/LiteAPI.app/Contents/MacOS/LiteAPI`.
- Current executable SHA-256: `c4436eac3b9d646170b9311895c1c2597fa8c1926622c49d2f924a573f22e840`.
- `git diff --check`: fail on pre-existing generated `frontend/wailsjs/go/models.ts` trailing whitespace at lines 1006, 1010, 2812, 2816, 2828, 2850, 2854, 2860, 2879, 2962, 2966, 2976-2978, 3052, 3056, 3066-3068. Fix/regenerate this as the first integration gate, not as an undocumented planning edit.
- Full race testing was not repeated during this UX-only audit; it remains a release gate.

Historical accepted package hash `b7a3598b...481c` in M7/M8 reports is not the current fresh-build hash and must not be presented as current evidence.

## Existing evidence worth retaining

- `docs/ui-redesign-milestone-ledger.md`: historical milestone contract and repairs.
- `docs/qa/m7-independent-report.md`: prior packaged Light/Dark/keyboard/AX evidence and Computer Use attribution caveats.
- `docs/qa/m8-clean-state-playthroughs.md`: prior three-run protocol, persistence, and multi-window acceptance.
- `docs/qa/m2-root-pass-shell-dark-1024.jpeg`: prior 1024 shell evidence.
- `docs/qa/m2-root-pass-request-actions-dark.jpeg`: prior request-menu evidence.
- `docs/qa/m2-root-pass-preferences-light-1024.jpeg`: prior Preferences evidence.
- `docs/qa/m4-independent-*.jpeg`: prior response inspector and compact-view evidence.

Use these as regression references, not a substitute for fresh final-package QA after implementation.

## Risks and explicit non-goals

- Risk: simultaneous monolith edits will create subtle lost behavior. Mitigation: extract seams first and serialize ownership.
- Risk: responsive changes can hide advanced controls. Mitigation: every hidden action must remain in overflow/command access and the AX tree.
- Risk: persisted width/ratio values can strand panes. Mitigation: clamp, version, and reset invalid values.
- Risk: Wails CLI/runtime version skew can repeatedly rewrite generated files. Mitigation: pin one supported version in `go.mod` and build instructions, then regenerate once.
- Risk: polished empty states may accidentally mask errors. Mitigation: model idle/loading/cancelled/unavailable/complete explicitly from existing truth.
- Non-goal: copy Postman, Bruno, Insomnia, or Yaak branding/assets.
- Non-goal: redesign backend protocol behavior already covered by tests unless a UI contract proves a defect.
- Non-goal: add cloud accounts, telemetry, AI promotion, licensing promotion, or collaboration clutter.

## Definition of done

The redesign is done only when the current dirty work has been intentionally integrated, automated gates pass, a freshly hashed production package is launched directly against isolated state, the 800x600 breakpoint no longer collapses, core workflows remain visible and keyboard-efficient at 1024x768, all four protocol paths remain truthful, and independent QA completes three consecutive clean-state native passes after the last repair.
