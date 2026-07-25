# LiteAPI final acceptance — 2026-07-22

Status: **ACCEPTED — automated gates and three consecutive final-hash packaged-native playthroughs passed**

## 1. Reference builds inspected

- Yaak source: `195f89337f64f023e5e02074b164cb50b7ab76df` (2026-07-21), plus the installed `app.yaak.desktop` application inspected through macOS accessibility.
- Bruno source: `bf40778b2d8a87ee810658699e9cdc849b11a3e0` (2026-07-21), release `v3.2.0`. Bruno was not installed locally, so source and official release evidence were used.
- Postman `12.20.1` was inspected only as a secondary discoverability reference.

## 2. Reviewed feature-ledger summary

The complete comparison inventory is in `docs/qa/2026-07-21-yaak-bruno-feature-ledger.md`. Yaak is the selected interaction and information-architecture reference; Bruno is the selected filesystem, portability, import, runner, and Git reference; native macOS conventions control menus, dialogs, focus, and windows. The bounded completion work closed the two priority defects and the missing safe Git workbench without replacing LiteAPI's local-first architecture or identity.

## 3. Decisions and explicit bounds

- Import is file/folder first. URL and paste remain secondary. Sensitive pasted content stays in memory and is not remembered.
- Supported final import paths are Postman v2/v2.1, Insomnia v4+, OpenAPI 3, Bruno JSON, `.bru`, Bruno/OpenCollection folders, and cURL. Unsupported Swagger 2, WSDL, ZIP, Yaak export, and bundled single-file OpenCollection inputs remain visible row-scoped errors rather than being silently mis-converted.
- Git exposes status, exact-file diff, stage, unstage, commit, branches, remote configuration, fetch, fast-forward-only pull, and upstream-aware push. Force/reset/clean and other destructive operations are intentionally not exposed.
- Core operation remains account-free and local. No competitor branding, cloud account, telemetry, or commercial surface was copied.

## 4. Architecture and UX changes

- Native application-menu installation now follows macOS order and keeps Settings out of File.
- Collection import uses a read-only preview followed by selected atomic apply, with staging, rollback, stale-plan hashing, path/symlink containment, per-row errors, manual format override, destination selection, conflict rename/skip/replace, and sensitive-input handling.
- Imported collections materialize authoritative local files and restore after relaunch.
- The Git workbench uses scoped command execution, exact selected paths, credential-safe remote validation and output redaction, fast-forward-only pull, conflict guards, and explicit upstream consent.
- Request GraphQL bodies now always use an `application/json` GraphQL envelope; the legacy no-mode fallback remains compatible.
- Deterministic local HTTP, GraphQL, WebSocket, SSE, gRPC, TLS, proxy, Git, and import fixtures replace public-service dependence.

## 5. Files changed

Primary product surfaces: `app.go`, `app_test.go`, `main.go`, `frontend/src/App.svelte`, `frontend/src/style.css`, generated Wails bindings, package manifests, and app icon assets.

Focused modules include collection import/recovery, request lifecycle, response timings/export, production isolation, workspace registry/session/state/locks/runtime, native menus and close handling, Git workbench, frontend import/Git helpers, deterministic fixtures, and their tests. Existing unrelated user work was preserved; nothing was reset, restored, or discarded.

## 6. Automated release gates

All commands passed on the final source and package:

- `env GOCACHE=/tmp/liteapi-gocache GOMODCACHE=/tmp/liteapi-gomodcache go test -race ./...`
- `go vet ./...`
- `cd frontend && npm run check` — 0 errors and 0 warnings
- `cd frontend && npm run build`
- `node --test test/*.mts` — 7/7 passing
- `$HOME/go/bin/wails build -nocolour`
- `git diff --check`
- `codesign --verify --deep --strict build/bin/LiteAPI.app`

The Go linker emitted its known malformed `LC_DYSYMTAB` warning during race linking; tests passed. Vite emitted its existing greater-than-500-KB chunk-size warning; the production build passed.

## 7. Native Computer Use results

Three consecutive clean-state playthroughs passed on signed executable hash `7f4da5836a6a5610d8306900382c194d424ed6838d68d839a97d31b698d5ad22`:

1. `/tmp/liteapi-delete-retest.1EUS8F`, PID 1466: transient gRPC draft deletion removed the sidebar item/tab/dialog without a missing-file error or recovery entry; a saved durable gRPC request still created a restorable recovery entry. Graceful quit removed the owner JSON.
2. `/tmp/liteapi-final7-r2b.JBRUti`, PIDs 5326 and 6202: strict fresh Sample API state, XML 200/93 B with `NEEDLE-42` and `héllo`, runner 1/1 pass, explicit Light appearance, same-data relaunch restoration, discarded request edits absent, and clean final owner release.
3. `/tmp/liteapi-final7-r3.Xmmb9L`, PIDs 7596 and 9413: conventional native menus, GraphQL success and `FIXTURE_ERROR`, WebSocket 101/echo/disconnect, ordered SSE completion, gRPC reflection/unary metadata/trailers plus unavailable-service error, System appearance, accessible request-tab/control labels, same-data relaunch with all protocol drafts absent, and clean final owner release.

The immediately preceding signed package independently passed the complete Git branch/upstream repair reproduction and a true mixed multi-file picker batch. The only source delta into the final package was focused transient-delete routing and its test; Git and import code did not change.

## 8. Real Git test results

Automated integration uses temporary repositories and local bare remotes for clean state, modification, exact diff, stage/unstage, commit, branch create/switch, push, peer fetch, fast-forward pull, divergence refusal, conflict creation and manual-resolution staging, clone/reopen, unavailable Git, URL credential rejection/redaction, and sibling-collection isolation. Independent native QA completed the full workflow on the signed Git milestone package. The later branch-target defect was reproduced against a disposable repository, repaired, and accepted by the same QA agent on signed hash `6a30c23b…22d67`: `Qa-r3` pushed and tracked `origin/Qa-r3`, switching to `main` followed `main`, and an explicit `release-preview` override survived refresh. The final package changed only transient-request deletion routing; final source tests retain the Git reconciliation coverage.

## 9. Import test matrix

Automated and prior exact-milestone native QA cover single and multiple files, native file and folder pickers, drag/drop, Postman, Insomnia, OpenAPI 3, Bruno JSON, `.bru`, cURL, Bruno folder, Unicode, nested hierarchy, environment/auth/script data, mixed valid/invalid input, selection/deselection, duplicate rename/skip/replace, cancel, stale plans, symlinks, partial independent success, batch rollback, durable materialization, relaunch restoration, URL restrictions, and unsupported-format reporting. Swagger 2, WSDL, ZIP, Yaak export, and bundled single-file OpenCollection are explicit unsupported rows.

## 10. Screenshot and accessibility evidence

Computer Use evidence includes native AX menu trees, the populated Services submenu, native About and Settings focus, import source/preview/conflict dialogs, selected-row state, response panes, Git status/diff/branch/conflict states, Light/Dark/System themes, responsive controls, and process/owner-lock attribution. Each final run used a fresh directory, exact PID-derived owner JSON, visible UI results, same-data relaunch where applicable, and graceful owner cleanup.

## 11. Packaged application

- App: `/Users/mostafi/Developer/Workspace/lite_api/build/bin/LiteAPI.app`
- Executable: `/Users/mostafi/Developer/Workspace/lite_api/build/bin/LiteAPI.app/Contents/MacOS/LiteAPI`
- Signed executable SHA-256: `7f4da5836a6a5610d8306900382c194d424ed6838d68d839a97d31b698d5ad22`
- Ad-hoc signature: valid on disk and satisfies its designated requirement.

## 12. Remaining limitations

- Unsupported import formats are reported explicitly as described in section 3.
- Git intentionally omits destructive reset/force/clean operations.
- Bruno hands-on inspection was unavailable because Bruno was not installed; its pinned source and official release were inspected instead.
- The frontend production bundle remains large enough to trigger Vite's advisory chunk warning.
- On the final package, Computer Use could not reliably preserve an `NSOpenPanel` valid-plus-invalid multi-selection because file-list focus and AX element IDs changed between snapshots. The same true mixed multi-file workflow passed on the immediately preceding signed package; no import code changed afterward. Single-file selection, row-scoped unsupported errors, apply, materialization, and relaunch persistence remained proven.

## 13. Independent QA verdict

**ACCEPT.** The established Terra Medium functional QA agent accepted the GraphQL transport repair, Git branch/upstream repair, transient-delete repair, three final-hash clean-state playthroughs, protocol results, relaunch behavior, and owner-lock cleanup. The separate native QA process-attribution audit found no product isolation defect; its own Computer Use kernel later became unusable and was not used as acceptance evidence.

## 14. Main-agent reviewer verdict

**ACCEPT.** Root reviewed each implementation diff and QA defect, enforced the same-agent retest loop, rejected harness-only attribution failures, and accepted only after the final signed package passed all automated gates and three consecutive clean native runs. No unresolved reproducible P0/P1/P2 product defect remains in the required scope.
