# LiteAPI UI Redesign Milestone Ledger

Updated: 2026-07-19 (America/Chicago)

## Acceptance authority

- Product/UI contract: `/Users/mostafi/.codex/attachments/97e28965-2d58-4bb6-ab83-bb19c93ad155/goal-objective.md`
- Implementation agents do not accept their own work.
- A slice is accepted only after root review, automated checks, packaged-app evidence, and independent QA.
- `App.svelte` and `app.go` each remain single-owner files until module boundaries are established.

## Baseline

| Surface | Evidence | Result | Risk |
| --- | --- | --- | --- |
| Git | `git status --short --branch` | Clean `main` at `fe54084` | Preserve user work if state changes |
| Go | `go test ./...` | Pass in 6.0 s | `app.go` is 42,132 lines |
| Frontend | `npm run check` | 0 errors, 0 warnings | `App.svelte` is 12,083 lines |
| Frontend | `npm run build` | Pass; JS 431.71 kB, CSS 69.27 kB | No frontend test runner exists |
| Native app | Computer Use against `build/bin/LiteAPI.app` | Launches; AX tree readable | Current hierarchy is cramped and exposes Bruno support/licensing surfaces |

## Milestones

| ID | Owner | Files / responsibility | Dependencies | Status | Required verification | QA result | Remaining risk |
| --- | --- | --- | --- | --- | --- | --- | --- |
| M0 | Root + all workers | Read-only architecture and native baseline | None | Complete | Baseline checks and native capture | QA matrix complete; root native AX/screenshot captured | Existing package predates redesign and is ad-hoc signed |
| M1 | Frontend/UI | `frontend/src/App.svelte`, `frontend/src/style.css`, new `frontend/src/lib/workbench/*` | M0 | Complete | check, build, Go tests/vet, diff-check, packaged root + independent QA | Pass at 1024x768 after two native repair loops | HTTP Cancel awaits backend M3 contract; broader shell remains M2 |
| M2 | Frontend/UI + platform | Shell, sidebar, tabs, compact responsive split views, native menus | M1 | Complete | check/build, Go tests/vet, pinned packaged build, root + independent native QA | Pass after four native repair loops | Folder-row glyph actions remain for a later tree refinement; multi-window is M5 |
| M3 | Frontend/UI + platform | Commands, restore, cancel/save/undo, accessibility | M1/M2 + platform seams | Complete | automated regressions + keyboard/AX QA | Cancellation, draft guards, recoverable deletion/restore, native quit, watcher identity, and restart persistence pass root + independent packaged QA | Long-running JavaScript remains checkpoint-cancellable rather than preemptible |
| M4 | Frontend/UI + platform | Editor/response viewers, comparison, timeline, large payloads | M1/M3 | Complete | fixtures, performance thresholds, native QA | Root + independent packaged QA pass after one renderer-state repair | WebSocket/gRPC protocol-session acceptance remains M5 |
| M5 | Backend/platform | Go modules, Wails bindings, native menus/windows, filesystem/Git/network/security | M0 boundary plan | Complete | Go tests/race/vet, bindings, packaged app | Root and independent QA pass; see `docs/qa/m5-independent-report.md` | Final integration package is M6 |
| M6 | Root | Integrated build and regression suite | M1-M5 | Complete | diff-check, frontend, Go, `wails build` | Pass; pinned production package launched from clean state | Final-package experience is M7 |
| M7 | Independent QA | Light/dark, 3 sizes, keyboard, accessibility, defects | Packaged M6 app | Complete | Evidence bundle and regression reports | Repaired hash passes independent replay | Keep strict controller attribution |
| M8 | Independent QA + Root | Three clean-state native playthroughs | All repaired M7 defects | Complete | 3 consecutive complete passes | Pass, 3/3 | Evidence directories retained |

## M0 findings

- Existing app bundle: arm64, 27.6 MB, bundle ID `com.wails.LiteAPI`, version `1.0.0`, ad-hoc signed, with `liteapi` and legacy `bruno` URL schemes.
- Installed Wails CLI is `v2.10.2`; `go.mod` resolves Wails `v2.12.0`. Treat package-tooling differences as a release risk until a fresh build passes.
- Root Computer Use captured the current AX tree and native screenshot. The first-glance workflow is cramped at the default window, and Support/Golden/Bruno-branded surfaces violate the product contract.
- Independent QA defined isolated-state, fixture, keyboard, accessibility, window-size, security, protocol, and performance matrices. Its own native capture attempt was interrupted, so later slice acceptance still requires independent packaged-app evidence.

## M1 evidence

- Added a typed, Wails-independent request command component and kept `App.svelte` as the mutation bridge.
- Native root QA found and repaired two failures: the header first inherited 880px fixed pane minimums and clipped actions; then legacy tab bars left partial/offscreen controls.
- Final packaged app at about 1024x768 visibly exposes method, URL, environment, saved state, TLS, proxy, Save, Run, Send, status text, time, size, orientation, all top views, all request tabs, and all response tabs.
- Cmd+L selected the URL without changing it. The orientation control changed to a usable stacked layout and restored horizontal layout.
- Independent QA scoped M1 pass screenshot: `/var/folders/xh/v5cp9wp16nnd19nq7bxxpc3h0000gn/T/com.openai.sky.CUAService/LiteAPI Screenshot 2026-07-18 at 6.30.13 PM.jpeg`.

## M3 cancellation evidence

- Added single-winner request and collection-run lifecycle registries, parent cancellation contexts, context-aware HTTP/GraphQL/gRPC execution, cancellation-aware runner delays, and explicit cancelled response/runner models.
- Root review found and repaired two acknowledgement races plus frontend busy-state/background-navigation defects before packaging.
- The first independent Runner replay failed because collection runs exposed no cancel target and timed out generically. Durable failure captures: `docs/qa/m3-qa-fail-runner-background.jpeg` and `docs/qa/m3-qa-fail-runner-timeout.jpeg`.
- The repair adds named cancellation controls in the request command strip, Runner panel, and global toolbar; modal-safe Escape; explicit `Cancelled` response state; and Runner `Cancelled N` plus cancelled result rows.
- Root packaged-app evidence: `docs/qa/m3-native-cancel-button.jpeg`, `docs/qa/m3-native-cancel-escape.jpeg`, `docs/qa/m3-native-cancel-background.jpeg`, `docs/qa/m3-native-runner-active.jpeg`, `docs/qa/m3-native-runner-cancelled.jpeg`, and `docs/qa/m3-native-runner-escape.jpeg`.
- Independent repaired-package QA passed direct Send, Runner button, cross-view global cancellation, Escape, duplicate prevention, and cancellation AX names. Remaining visual debt at 1024px is duplicated critical runner controls and clipped sidebar/status metadata; route those to M2 rather than weakening the cancellation contract.
- Automated gates passed: full frontend check/build, full Go tests/vet, focused Go race coverage, generated bindings from pinned Wails v2.12.0, and `git diff --check`.

## M3 recovery and draft evidence

- Added private durable seven-day recovery snapshots for request, folder, and collection removal, with staged/committed truth, semantic conflict detection, targeted workspace restore, collision protection, file permissions, and rollback on persistence failure.
- Added sequential save/discard contracts for unsaved drafts and native Save and Quit / Discard Changes / Cancel handling. Browser Cmd+W exposes Save & Close / Discard & Close / Cancel with a focus trap, initial Cancel focus, and focus return.
- Root and independent QA exposed an internal-write watcher bug that reloaded requests with different IDs, stale tabs, cross-request edits, and false recovery conflicts. Internal writes now seed the final watch fingerprint, first process observations establish a baseline, and new file-backed requests receive stable identities before opening.
- A true clean-state independent replay then exposed never-saved normal requests claiming a saved lifecycle. New requests are now explicit transient drafts; rename/save commits them, discard removes them, and storage snapshot filtering no longer aliases or mutates live tabs.
- Final root evidence: `docs/qa/m3-final-cmdw-guard.jpeg`, `docs/qa/m3-final-recovery-restored.jpeg`, and `docs/qa/m3-root-final-never-saved-rename-pass.jpeg`.
- Final independent evidence: `docs/qa/m3-independent-final2-save-isolation.jpeg`, `docs/qa/m3-independent-final2-cmdw-modal.jpeg`, `docs/qa/m3-independent-final2-recovery-restored.jpeg`, `docs/qa/m3-independent-final2-relaunch-persisted.jpeg`, and `docs/qa/m3-independent-final2-native-quit-modal.jpeg`.
- Final independent result: PASS at 1024x768 with no clipping/overflow and named AX controls. Automated gates passed: full Go suite, vet, focused race, Svelte check/build, pinned Wails v2.12.0 package, and `git diff --check`.

## M4 response-inspector acceptance contract

- The packaged app must expose keyboard-operable response tabs and named controls for search, copy, exact-byte download, preview, comparison, and timeline diagnostics without clipping at 1024x768.
- JSON tree, formatted text, raw, Base64, hex, image, sandboxed HTML, PDF, XML, streaming-event, and downloadable-file paths must be content-aware. Unsafe HTML execution and unbounded binary/PDF embedding are failures.
- Bodies above the automatic render budget must start bounded and disclose truncation. Incremental reveal may extend the visible window, but the default DOM must not contain the full large response; exact bytes remain downloadable.
- A 1 MiB textual fixture must open, switch view, search, and advance a match without a visible multi-second stall. A 5 MiB fixture must remain bounded by default and keep response controls interactive; full rendering requires an explicit user action and a documented ceiling.
- Comparison must cover status, timing, headers, JSON structure, and bounded body text. Timeline diagnostics must be searchable/exportable and preserve gRPC metadata/trailers plus WebSocket/gRPC event logs.
- Root review, automated checks, a pinned Wails v2.12.0 package, clean-state native fixture runs, and independent QA are required. Component owners cannot accept this milestone.
- Deterministic offline fixtures live in `qa/responsefixture`; `README.md` publishes exact hashes, endpoints, reflected gRPC methods, and the local launch command. The fixture tests verify lengths, digests, JSON/media validity, and gRPC metadata/trailer fidelity.

## M4 acceptance evidence

- Added CodeMirror request editors with line numbers, folding/search, JSON/XML language support, validation with line/column diagnostics, format/minify, bounded large-document behavior, preserved editor selection/scroll state, and inspectable resolved/missing variable spans.
- Added content-aware response modes for JSON, XML, text, Base64, bounded hex, image, sandboxed HTML, PDF, binary/download-only fallback, WebSocket/gRPC event payloads, exact native downloads, duplicate headers, searchable timelines, export, saved-example comparison, and bounded JSON/body diffs.
- Backend response truth now preserves duplicate/Unicode header entries, response timings, saved-example duration, unique timeline row identities, system-proxy connection reuse, cancellation, and exact-byte body/timeline export. The deterministic local fixture publishes lengths and SHA-256 values.
- Root native QA repaired five defects before acceptance: oversized JSON media fallback, lossy binary byte counts, irrelevant binary load controls, duplicate timeline keys, and missing JSON diagnostic locations. Representative evidence includes `docs/qa/m4-root-json-1m-bounded-pass.jpeg`, `docs/qa/m4-root-binary-5m-hex-pass.jpeg`, `docs/qa/m4-root-response-comparison-pass.jpeg`, `docs/qa/m4-root-timeline-search-pass.jpeg`, and `docs/qa/m4-root-editor-json-diagnostic-pass.jpeg`.
- Independent QA found one additional blocker: a native response-view pop-up could remain on Hex after Pretty was activated. The repaired component now normalizes and commits the selection locally and to parent state; independent replay proved Binary -> Hex -> XML -> Pretty plus sandboxed HTML.
- Final independent evidence covers 1 MiB JSON, 5 MiB text/binary, exact binary SHA-256 `2e7cab6314e9614b6f2da12630661c3038e5592025f6534ba5823c3b340a1cb6`, headers, timeline/export, XML/image/HTML/PDF, comparison, editor diagnostics/format/minify/variables, and compact Light/Dark smoke at the closest available native 984x768 bounds. See `docs/qa/m4-independent-report.md` and `docs/qa/m4-independent-*.jpeg`.
- Automated gates passed: `svelte-check`, production frontend build, full Go tests, vet, race suite, `git diff --check`, and a pinned Wails v2.12.0 production package. WebSocket/gRPC end-to-end protocol sessions remain explicitly assigned to M5.

## M5 backend/platform acceptance contract

- `app.go` must shed behavior into tested internal modules without changing or silently dropping any Wails-exported method signature. Pinned binding generation and a generated-surface comparison are release gates.
- Because Wails v2.12 exposes one native window per process, LiteAPI uses a reviewed multi-process window model. A real File/Window command must open a second native workspace window, each process must load only its selected workspace, two different workspaces must persist independently, and a live second owner of the same workspace must be refused or explicitly read-only.
- Window sessions persist private versioned records for selected workspace, tabs, active pane, orientation, and safe geometry. A crash/stale owner is recoverable; an old owner cannot overwrite, heartbeat, or release a newer owner. Legacy `state.json` migration is atomic, reversible until marked complete, and never changes secret-key identity.
- Shared registry/preferences/secrets and workspace-scoped state/recovery have explicit ownership. Child persistence cannot overwrite another workspace, collection request files remain authoritative, scratch data is not promoted to durable registry state, and secret/cookie/OAuth plaintext is never duplicated into registry/session/recovery artifacts.
- Native menus route to the active process/window. Close and quit retain the M3 draft guard. New Window/Open Workspace in New Window, workspace activation, and restored sessions are keyboard and accessibility reachable.
- HTTP, GraphQL, WebSocket, and gRPC surfaces receive protocol-appropriate settings and native packaged replay. Proxy/system proxy, TLS verification/custom CA/client certificate, cookies, cancellation, metadata/trailers, binary frames, streaming lifecycle, and offline/error states must be observable and truthful.
- Filesystem/Git/import-export acceptance includes external-change refresh without identity loss, workspace-owner write locking, recoverable destructive actions, Postman/Bruno/OpenAPI paths, and Git operations that cannot cross workspace ownership boundaries.
- Root review, full Go tests/vet/race, frontend check/build, `git diff --check`, pinned Wails v2.12.0 bindings/package, two-window clean-state native proof, and independent security/protocol/accessibility QA are required. Implementation owners cannot accept M5.

## M5 acceptance evidence

- The Wails v2 multi-process workspace model now has private versioned window sessions, scoped owner locks and heartbeats, stale-owner recovery, owner-token-safe release, atomic migration, shared/scoped state merges, and child-process launch arguments that identify one workspace and data directory.
- Process-attributed clean-state native replay created a second workspace window, matched both process arguments to isolated owner records, proved independent persistence, kept the child alive when the primary closed, and refused a duplicate live owner. Closing and relaunching restored the request tab, URL, vertical response orientation, and 1024x800 geometry from the private session record.
- Packaged native HTTP replay returned `200`, 93 exact bytes, and `NEEDLE-42`. WebSocket replay connected with `101 Switching Protocols`, exposed sent/received event rows, and recorded a truthful disconnected system row. gRPC unary replay returned `200`, 178 exact binary bytes, three initial metadata rows including `x-liteapi-fixture: initial`, and trailer `x-liteapi-fixture-trailer: complete`.
- Native gRPC QA exposed and repaired two boundary defects: an empty recovery list crossed Wails as `null` and left the production shell at Loading, and Metadata/Trailers selections rendered correctly but were rejected by the persisted-pane validator. Regression coverage now requires a non-nil empty recovery slice and persistence of both gRPC response tabs.
- Independent automated QA passed full race/vet/frontend gates plus focused TLS/custom-CA/mTLS, cookies, proxy/PAC, import formats, WebSocket lifecycle/binary/ping, gRPC unary/stream/trailers/bidi/cancel, ownership/recovery, migration/session/concurrency, encryption/traversal/tamper, secrets, and stale-owner suites. Independent process-attributed native QA passed the two-window, refusal, HTTP, close/replay, and session-orientation contracts.
- One earlier native report alleged cross-data leakage, but its AX tree contained default Application Support recovery/notification state absent from the isolated registry. A fresh PID/owner-lock-attributed replay showed only the requested temporary data directory; the contaminated report is not acceptance evidence.
- The generated Wails surface comparison removed no exported bindings and added the M3/M5 cancellation, recovery, draft, save, workspace-window, and active-workspace contracts. Full M5 evidence and the independent split of duties are recorded in `docs/qa/m5-independent-report.md`.

## M6 integrated build evidence

- `svelte-check` completed with 0 errors and 0 warnings; the Vite production build passed with the documented large-chunk advisory.
- `go test -race ./...` passed for the application and both platform/response fixture packages, and `go vet ./...` passed with the isolated Go cache.
- `git diff --check` passed after regenerating the Wails bindings. `App.js` and `App.d.ts` contain additions only, so no previously exported frontend binding was removed.
- Pinned Wails CLI v2.12.0 regenerated bindings, rebuilt the frontend, compiled the arm64 application, packaged it, and self-signed it successfully. The installed v2.10.2 CLI mismatch is therefore removed from the release path.
- The bundle is an arm64 Mach-O with identifier `com.wails.LiteAPI`, ad-hoc SHA-256 signature, version 1.0.0, and `liteapi` plus compatibility `bruno` URL schemes. The first M7 package hash was `66ddddc1726ee039e743fe653d5a843f5b6bbc06920efd30d4496d902f61c5d6`; an M8 defect forced a rebuild, and the current executable SHA-256 is `b7a3598b3d43370c116f5c178df580ff3d6d3e2423d07ce2444061505a7d481c`.
- A true clean-state launch using `/tmp/liteapi-m6-final.6WkSQI` reached the complete native workbench (not Loading), exposed the expected menu bar and named AX controls, and exited normally through the native close button.

## M7 final-package QA evidence

- Independent QA exercised explicit Light and Dark appearances, compact native bounds near 984x768, a larger 1223x768 workbench, and fullscreen. Workbench and Preferences remained readable without observed clipping, overflow, or contrast failure.
- Independent keyboard/AX coverage passed Cmd+L, Cmd+S, Cmd+Enter, response-tab arrow navigation, in-app Escape dismissal, native File menu commands, selected states, named request/response/settings/theme controls, focus behavior, and response search/copy/download/timeline surfaces. A deterministic XML request returned `200 OK`, 93 bytes, duplicate headers, and `NEEDLE-42`.
- A first report labeled Dark persistence P1, but file timestamps proved the isolated shared state had not been written after that worker's apparent click. Root terminated every same-bundle process and replayed with one PID/owner lock: selecting Dark immediately wrote `"theme":"dark"`, normal close exited the process, and direct isolated relaunch restored AX `Dark, Value:on`.
- A replacement worker independently passed immutable-hash verification, live PID/owner-lock attribution, explicit Light selection, HTTP fixture truth, and Cmd+L. After closing the app, querying Computer Use by bundle path auto-launched a new no-environment process; that controller-created default-data PID was correctly rejected as untrusted, not classified as a LiteAPI defect.
- The combined independent matrix plus root's attribution-corrected persistence replay has no confirmed product P0/P1. The exact controller caveats and trusted/untrusted boundaries are recorded in `docs/qa/m7-independent-report.md`; M8 forbids querying a closed app before direct isolated relaunch.

## M8 reset and empty-collection repair

- Independent Run 1 used valid foreground-session attribution at `/tmp/liteapi-m8-run1.jxDivf`. Before failure it passed explicit Dark, HTTP create/save/send (`200 OK`, 93 bytes, `NEEDLE-42`), timeline, keyboard/AX, response-tab arrows, More/Escape, orientation, and a real second workspace process/owner lock.
- Relaunch then failed truthfully because workspace state referenced the newly created empty collection path, but `CreateCollection` had persisted no directory/root metadata. The saved request correctly lived under Sample API; the unrelated empty collection's missing path caused hydration to abort.
- `CreateCollection` now refuses an existing target and materializes the collection directory plus format-appropriate root files before publishing it into workspace state. `TestProductionRelaunchHydratesNewEmptyCollections` covers both OpenCollection YAML and Bruno formats through a production migration/relaunch.
- The repaired source passed focused relaunch tests, complete `go test -race ./...`, `go vet ./...`, Svelte check/build, `git diff --check`, and a pinned Wails v2.12.0 production rebuild. The M8 streak is zero; M7 and all three M8 runs restart on hash `b7a3598b3d43370c116f5c178df580ff3d6d3e2423d07ce2444061505a7d481c`.
- Independent repaired-package M7 replay passed at `/tmp/liteapi-m7-repair.nzVe7v`: first PID 38865 and relaunch PID 40636 each matched `main-window` ownership; Light then Dark persisted as AX `Theme dark`; the XML request returned `200 OK`, 11 ms, 93 bytes, and `NEEDLE-42`; the empty YAML collection and `opencollection.yml` existed before and after relaunch; tab/URL, vertical orientation, and safe 1024x800 geometry restored; compact/fullscreen, keyboard, Escape, AX, and File/View menus passed. Both closes ended their foreground sessions with zero PIDs and no live lock.
- Official M8 Run 1 passes at `/tmp/liteapi-m8-official-r1.CLQQp8`. Independent QA proved primary/child PIDs, arguments, locks, duplicate refusal, collection-owned XML fixture truth, keyboard/AX, orientation, and primary relaunch. Its post-primary-close Computer Use capture was stale; registry/scoped/session files showed correct child isolation, and root directly launched the exact child session as the sole process and obtained `LiteAPI — M8 Official Child`, no Sample API, and `No environment`. See `docs/qa/m8-clean-state-playthroughs.md` for the attribution-corrected evidence. Streak: 1/3.
- Official M8 Run 2 passes at `/tmp/liteapi-m8-official-r2.gzwfZp`: explicit Light, collection-owned WebSocket 101 with sent/received/system-disconnect rows, response keyboard/AX, vertical orientation, concurrent child args/locks/session, duplicate refusal, exact solo-child AX isolation, primary relaunch persistence, and final zero processes/no live owner. Streak: 2/3.
- Official M8 Run 3 passes at `/tmp/liteapi-m8-official-r3.uZE7J7`: collection-owned gRPC unary returned `200 OK`, application/grpc binary-safe 184 exact bytes, fixture initial/duplicate metadata and completion trailer; an unavailable endpoint produced truthful connection-refused/0-byte state; concurrent and solo-child ownership/isolation passed; primary relaunch restored collection/request/tab/URL/method; final state had zero processes and zero owner locks. Streak: 3/3, complete.

## M2 evidence

- Replaced copied promotional/support/licensing identity with original LiteAPI “Local-first API workbench” language; remaining Bruno references are limited to genuine import/export/file-format compatibility.
- Added native File, Edit, View, Request, Collection, Environment, Git, Window, and Help menus. Root and independent packaged QA verified the menu bar and File → New Request callback.
- Added a persisted `SetActiveWorkspace` backend contract. Invalid IDs do not mutate state; success persists across restart and the toolbar adopts the returned state instead of faking a local selection.
- Request actions now use one accessible More disclosure with visible Reveal, Code, Info, Rename, Clone, and Delete labels. Root native QA found and repaired article stacking, collection-list clipping, Escape/outside dismissal, and dark-rail contrast failures.
- Preferences theme cards wrap at the 1024 target without page-level horizontal overflow. The large Keybindings catalog is closed by default and, when opened, owns a bounded two-axis scroll region.
- The first independent Keybindings default-state replay was contaminated because root had opened the shared disclosure before handoff. A genuinely untouched isolated relaunch then passed with `Value: off`; the corrected evidence supersedes that false failure.
- Representative passes: `docs/qa/m2-root-pass-shell-dark-1024.jpeg`, `docs/qa/m2-root-pass-request-actions-dark.jpeg`, `docs/qa/m2-root-pass-preferences-light-1024.jpeg`, `docs/qa/m2-independent-request-actions.jpeg`, and `docs/qa/m2-independent-keybindings-clean-pass.jpeg`.
- Durable repaired defects remain recorded in `docs/qa/m2-root-fail-clipped-actions.jpeg`, `docs/qa/m2-root-fail-layered-actions.jpeg`, `docs/qa/m2-root-fail-preferences-overflow.jpeg`, and `docs/qa/m2-independent-fail-keybindings-scroll.jpeg`.

## Current behavioral references

- Postman workbench: request tabs, environment selector, sidebar hierarchy, command/search behavior, and explicit response status/time/size/network details.
- Yaak: compact chrome, configurable shortcuts, and full request/response debugging flow.
- These are behavioral references only. LiteAPI keeps its own branding, code, assets, and visual identity.

## Integration gates

1. Review the complete patch and confirm file ownership.
2. Add a regression test for every discovered bug where practical.
3. Run `git diff --check` and the focused suite.
4. Run full frontend and Go suites before packaging.
5. Build and launch the packaged `LiteAPI.app`.
6. Independent QA exercises the accepted slice; the owner repairs failures; QA retests.

## M9 command-bar consolidation evidence

- Replaced the permanent activity rail and wrapping toolbar with a single-line, Yaak-inspired LiteAPI workspace command bar while preserving native macOS chrome, request tabs, local-first identity, and every relocated destination.
- Added typed command routing shared by the command bar, Add/Main menus, command palette, and native events. `Cmd+K` now opens workspace search and `Cmd+Shift+P` opens the distinct command palette.
- Relocated Cookie Jar, Runner, environments, Network Log, Dev Tools, Capabilities, Import, Preferences, and Keyboard Shortcuts into direct contextual controls, grouped overflow menus, and semantically matching native menus.
- Computer Use found and drove repair of a pointer-opened menu Escape defect. Add and Main now focus their first enabled item; Escape closes and restores trigger focus. The combined environment control also passed scoped labels and Escape restoration.
- Full Go tests, race suite, vet, native-menu tests, Svelte diagnostics, Vite production build, and `git diff --check` pass. The package was built and self-signed with pinned Wails v2.12.0.
- Final process-attributed native acceptance ran PID 67425 against `/tmp/liteapi-menubar-escape-final.YQutjy`. Executable SHA-256: `4f49d7d73383f98116db9860ccca07d72c9c1e9266a4d149471948e74f78a6e6`. Evidence and the complete matrix are in `docs/qa/2026-07-21-command-bar-implementation-report.md` and `docs/qa/2026-07-21-command-bar-isolated-pass.jpeg`.
