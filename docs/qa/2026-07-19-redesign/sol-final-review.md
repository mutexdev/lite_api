# GPT-5.6 Sol final redesign review

Final release review: 2026-07-19 19:06 CDT  
P1 post-fix review: 2026-07-19 18:41 CDT  
Pre-fix review snapshot: 2026-07-19 18:30 CDT  
Reviewer role: independent final code/package reviewer; no application files edited  
Audit contract: `docs/ux/2026-07-19-pre-implementation-audit.md`  
Implementation report: `docs/qa/2026-07-19-redesign/implementation-report.md`  
Independent packaged QA: `docs/qa/2026-07-19-redesign/qa/independent-packaged-qa-report.md`

## Final verdict

**ACCEPT PACKAGE `10db82cbddc485683c8afc3447482fa17df0e821f58a5ef0de2c0c2b75521a19`.**

The package hash was independently recomputed and matches the updated implementation and independent QA reports. The four prior P1 repairs remain present in the packaged frontend/source, the UTF-8 exact-source benchmark and packaged response/modal evidence remain valid, and the final isolation delta is covered by code inspection, three focused passing regressions, and attached foreground-session R15–R17 evidence.

No P0 or open P1 remains. Same-directory restart persistence, three isolated clean-state runs, and the WebSocket/gRPC sweep are now evidenced. The sole retained limitation is that Computer Use could not manipulate the native compact splitter or resize the native window through its AX bridge. The slider remains named and visible, and the compact pointer axis is statically correct; no product failure was reproduced there.

### Final isolation and persistence delta

- `main.go:17-22` passes the real process arguments into `NewProductionApp`. `workspace_window_runtime.go:46-84` accepts one explicit main-window `--data-dir`, rejects missing/duplicate values, retains strict child-window launch intents, and initializes production state only from the selected directory.
- `storage_scope.go:9-20` issues a stable opaque SHA-256 namespace from the cleaned backend data directory. After `GetState`, `App.svelte:1872-1879` requests that scope; `App.svelte:5788-5817` reads/writes only `liteapi.workbench.v3.<scope>.*`. Browser-local geometry can no longer share global bundle keys across data directories; requests/tabs remain backend state.
- Independent focused rerun passed `TestWebStorageScopeIsStablePerDataDirectoryAndIsolatedAcrossDirectories`, `TestProductionDataDirectoriesDoNotShareSavedRequestsOrOpenTabs`, and `TestProductionMainWindowDataDirArgumentOverridesEnvironmentFallback` in 1.150 seconds.
- R15 used a foreground direct executable session with `--data-dir`, started clean, saved `R15 Persist`, exited `0`, and restored the saved request/open tab on same-directory relaunch. The retained directory contains its migration, registry, scoped workspace, recovery, session, shared state, and `R15 Persist.yml` artifacts.
- R16 and R17 used distinct foreground directories and PIDs, started with only the clean Sample API/request state, exited `0`, and produced their own distinct workspace-state/recovery/lock artifacts. Independent recursive inspection found `R15 Persist` only under R15 and nowhere under R16/R17. No LiteAPI process remained after the sequence according to the attached-session report.

Earlier detached R7–R13 results are retained in the QA report as historical non-evidence. Their shells exited/backgrounded the GUI and the resulting foreground app process did not retain the requested argument. R15–R17 use the documented long-lived foreground harness and supersede those attribution failures.

### Post-fix evidence by former blocker

| Former blocker | Post-fix result | Evidence |
|---|---|---|
| UTF-8 preview performance and boundary truth | **Resolved** | Imported the exact `sliceUtf8` from `frontend/src/lib/workbench/response.ts` under Node 22 type stripping. On `"é".repeat(262144)` (524,288 UTF-8 bytes), a 131,072-byte budget returned exactly 65,536 characters / 131,072 bytes, ended in `é`, contained no replacement character, and had a 0.783 ms median across seven runs (0.683–1.065 ms). Packaged `/xml` independently retains the complete 93-byte `héllo` response without false truncation. |
| Compact splitter axis | **Resolved, static plus packaged AX limitation** | `matchMedia('(max-width: 960px)')` drives `compactWorkbench` (`App.svelte:1117-1126`); the pointer handler selects `clientY / bounds.height` whenever compact (`App.svelte:5830-5843`); compact CSS makes the divider a horizontal row with `row-resize` (`style.css:5506-5511`). Persistence still uses the clamped response-split key. Packaged AX exposes the named slider/default, but this Computer Use runtime could not change native window dimensions or operate the slider. |
| New/palette keyboard containment | **Resolved** | Source provides Escape close, contained Tab/Shift+Tab, opener-focus restoration, inert background, palette active option, ArrowUp/ArrowDown, and Enter execution (`App.svelte:5846-5955`, `7578`, `11277-11330`). Packaged QA confirms New Escape returns focus to its invoker, modal AX hides background descendants, palette Escape closes, and filtered Enter executes Send. Focus movement during every Tab step was not separately observable through Computer Use. |
| Binary Base64/Hex truncation truth | **Resolved** | `ResponseInspector.svelte:43-71` now measures retained decoded bytes, clips Base64 by decoded-byte budget, uses full retained Base64 only after explicit Render full, and keeps truncation true if the source is incomplete. `response.ts:79-100` converts the complete supplied Base64 for Hex without the former hidden cap. Independent packaged `/binary-200k` QA confirms exact 204,800 bytes, truthful bounded Base64/Hex truncation, Download, Render full, and bounded-state reset across resend/view transitions; evidence: `qa/evidence/final-r3-binary-hex-truncated.jpeg`. |

### Final artifact and checks

| Check | Result |
|---|---|
| Executable SHA-256 | `10db82cbddc485683c8afc3447482fa17df0e821f58a5ef0de2c0c2b75521a19` |
| Executable timestamp | 2026-07-19 18:56:05 local; exact package exercised by R15–R17 |
| Packaged icon SHA-256 | `4eec839408f842cc042f98877d2fdd7851cfa561871c99518caf19d9bba81481` |
| `git diff --check` | Pass |
| `npm run check` | Pass: 0 errors, 0 warnings |
| Focused isolation regressions | Pass: opaque scope stability/separation, production directory request/tab separation, and main `--data-dir` override |
| Implementation/independent gates | Frontend build, full Go test, Go vet, Wails packaging, signing, hash checks, HTTP/WebSocket/gRPC, restart persistence, and attached R15–R17 runs reported pass |

## Pre-fix verdict (superseded by the post-fix acceptance above)

**REJECT PENDING REPAIR AND A NEW PACKAGED-QA PASS.**

The redesign has a coherent original LiteAPI identity and the intended workbench hierarchy is substantially present. The current implementation also passes the static frontend gates and the full Go suite. It is not ready for unconditional acceptance because the only independently exercised package is stale relative to current repairs, four P1 defects were reproducible or demonstrable at this review boundary, and the independent QA report explicitly left the required compact/resizer/persistence/clean-state matrix partial.

No P0 was found. The P1s and evidence gaps below are release blockers under the audit's explicit responsive, large-response, and fully keyboard-operable acceptance criteria. A code-only patch is insufficient; the final verdict must attach to one newly hashed package.

## Blocking findings

### P1 — Large multibyte text preview could freeze the response pane

**Confirmed against the implementation that produced packaged hash `ba5f53f9b34b944ff1c3970962e077336c896fbb2dfcbb95267f6941d7fada22`.** The prior `sliceUtf8` implementation in `frontend/src/lib/workbench/response.ts` decremented one UTF-16 code unit and re-encoded the entire prefix on every loop. An exact JavaScript equivalent took **4,599.6 ms** to bound a 512 KiB UTF-8 string (`"é".repeat(262144)`) to the shipped 128 KiB automatic-preview budget, performing 65,536 full-prefix re-encodes. Smaller doubling samples were 1.8, 6.9, 23.2, and 88.1 ms, which demonstrates quadratic growth.

The current worktree has a plausible linear repair at `frontend/src/lib/workbench/response.ts:7-30`: encode once, locate the UTF-8 boundary, and decode one subarray. That repair was not in the reviewed package and therefore remains **pending packaged verification**.

Required regression:

- Add a direct automated seam for ASCII plus 2-, 3-, and 4-byte UTF-8 boundaries, including a split surrogate/code-point boundary.
- Exercise at least 512 KiB of multibyte text through the packaged response inspector and show that first render/search/view switching remains responsive and the 128 KiB byte budget is truthful.
- Re-run the original 93-byte XML case to prove the repaired slicer does not restore the earlier false-truncation bug.

### P1 — Compact split-pointer math disagreed with the forced stacked layout

At the review checkpoint, `frontend/src/style.css:5499-5511` forced request and response panes into vertical rows below 960 px, while the pointer handler selected X/Y math from the stored wide-layout orientation. With the default/stored horizontal orientation, dragging the compact horizontal divider vertically computed an X fraction. Independent QA had explicitly left compact resizing unverified.

The current worktree has a plausible static repair: `compactWorkbench` follows `(max-width: 960px)` at `frontend/src/App.svelte:1113-1122`, and `startResponseSplitResize` uses it at `frontend/src/App.svelte:5814-5828`. This repair is not present in the reviewed package and has not been independently dragged at 800x600.

Required regression:

- In a newly built package, set the window to actual 800x600 and keep the stored wide preference horizontal.
- Drag the compact horizontal divider vertically to both bounds; verify it tracks the pointer, clamps to 30-70%, and does not resize from horizontal pointer-only movement.
- Verify keyboard adjustment, double-click reset to 52%, restart persistence, then widen past 960 px and verify the stored wide orientation remains usable.

### P1 — New and command-palette modals were not keyboard-contained

**Reproduced with Computer Use against packaged hash `ba5f53f9…ada22`.**

- `New` correctly focused the request-name field, but Escape caused no accessibility-tree change and left the modal open.
- The command palette correctly focused its filter, but Down Arrow did nothing. One Tab moved focus out of the `aria-modal` to the underlying WebView/background instead of traversing/containing modal controls.

The reviewed package's global shortcut path handled palette/action-menu/cancel Escape but not New dismissal; its modal markup had neither a focus trap nor palette active-option navigation. The current worktree now contains Escape handling, return-focus logic, tab containment, `inert` background state, and an `aria-activedescendant`/ArrowUp/ArrowDown palette model around `frontend/src/App.svelte:5833-5943`, `frontend/src/App.svelte:7565`, and `frontend/src/App.svelte:11264-11317`. This is a plausible repair, not packaged evidence.

Required regression:

- New: invoke by the compact control and keyboard shortcut; verify initial focus, Shift+Tab/Tab containment, Escape dismissal, and focus restoration to the invoker.
- Palette: invoke with Cmd+Shift+P; verify ArrowUp/ArrowDown wraps active selection, Enter runs that selection, Tab cannot reach background controls, Escape dismisses, and focus returns.
- Confirm Cmd+K still opens workspace search and does not alias the command palette.

### P1 — 128 KiB–1 MiB binary Base64/Hex previews can claim completeness after source clipping

Static data-flow finding in `frontend/src/lib/workbench/ResponseInspector.svelte:47-64`: `rawBase64` is clipped to `automaticPreviewLimit` before `display` and `bodyTruncated` are computed. For a binary response between 128 KiB and 1 MiB, Base64 `safeDisplay` can equal the already-clipped `display`, so `bodyTruncated` becomes false even though much of the response source is absent. “Render full” cannot recover bytes that were removed before display computation. Hex has the same source-clipping problem after expansion. The independent 5 MiB fixture did not expose it because `bytes > fullRenderLimit` forces the truncation branch true.

Required regression:

- Add or use an approximately 200 KiB binary fixture (strictly above 128 KiB and below 1 MiB).
- In Base64 and Hex views, verify the exact response byte count, truthful “preview truncated” state, no control that falsely implies the entire source is rendered, and exact Download output.
- Add a pure automated test separating source truncation from display truncation so future refactors cannot compare only two already-bounded strings.

## Architecture and acceptance assessment

### Accepted statically, pending final package

- `App.svelte` and `style.css` use one CSS variable for each resizer and clamp persisted values to sidebar 220-420 px and response 30-70%; corrupt/missing local storage falls back safely.
- The sidebar, activity rail, request command center, request tabs, response summary, response tabs, and response inspector remain one integrated workbench rather than a parallel redesign shell.
- Status, status text, duration, and size are response-local. The response inspector distinguishes empty/no-response content from successful response content and retains bounded download/render behavior.
- The compact `New` flow creates a local scratch request in the active collection and routes through the existing request creation path.
- Cmd+Shift+P and Cmd+K have distinct code paths and distinct packaged UI surfaces.
- Request/response tabs are horizontal, non-wrapping, and scrollable; responsive CSS replaces the old half-window activity rail with a 46 px horizontal rail and an overlay/collapsed collection sidebar.
- The app icon is supplied as original LiteAPI artwork (`build/appicon.svg` and packaged `iconfile.icns` evidence in independent QA), with no copied competitor branding or promotional surfaces.

### Partial QA that blocks unconditional acceptance

The independent report's `TARGETED PASS; broader acceptance remains PARTIAL` is an honest result, not a final acceptance substitute. Under the audit contract, these are blockers until rerun on the final hash:

- Actual 800x600 and exact 1024x768 window sizes.
- Pointer, keyboard, reset, bounds, and restart persistence for both resizers.
- Compact sidebar overlay/collapse behavior, with no obscured workbench after resize/restart.
- Three demonstrably isolated clean-state packaged runs with run-specific state and ownership/lock evidence.
- WebSocket and gRPC smoke checks in the final shell, because the audit explicitly included them in protocol-regression acceptance.

Theme screenshots, the repaired 93-byte XML case, 5 MiB guard, command/search separation, compact New creation, and wide sidebar collapse are useful supporting evidence already present. Lack of a console capture alone would be a follow-up if the final runs otherwise show no errors; it does not waive the explicit matrix above.

## Independent checks at this snapshot

| Check | Result |
|---|---|
| `git diff --check` | Pass |
| `npm run check` | Pass: 0 errors, 0 warnings |
| `npm run build` | Pass: Vite production build; existing >500 KiB chunk warning |
| `env GOCACHE=/tmp/liteapi-sol-final-gocache go test ./...` | Pass outside the restricted sandbox: root package plus platform and response fixtures |
| Packaged executable SHA-256 | `ba5f53f9b34b944ff1c3970962e077336c896fbb2dfcbb95267f6941d7fada22` |
| Package timestamp | 2026-07-19 18:20:32 local; predates the worktree repairs above |
| Computer Use | Packaged New/Escape and palette focus-containment failures reproduced; app restored afterward |

The first parallel Go attempt overlapped a frontend build and was also denied loopback binds by the sandbox. A clean sequential rerun after the frontend build, with the existing scoped test approval, passed all packages in 17.4 seconds. This was a verification artifact, not an application failure.

## Original promotion checklist

1. Land all four P1 repairs and their narrow automated tests.
2. Re-run `git diff --check`, `npm run check`, `npm run build`, and the full Go suite from the settled worktree.
3. Build `build/bin/LiteAPI.app` once from that settled worktree; record executable and icon SHA-256 values.
4. Run the P1 regressions above against that exact hash with Computer Use/accessibility evidence.
5. Complete the independent 800x600, 1024x768, resizer/restart, WebSocket/gRPC, and three-isolated-run gaps against the same hash.
6. Update this verdict only after code, package hash, and QA evidence all refer to the same build.

The four P1-specific items and the broader isolated-run/protocol matrix are now satisfied by the final package and evidence above. Only compact-slider manipulation through Computer Use remains a disclosed verification limitation; no product failure was reproduced.
