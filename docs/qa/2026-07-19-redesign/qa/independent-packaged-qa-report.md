# Independent packaged QA — redesign

Date: 2026-07-19 (America/Chicago)  
Owner: independent QA (no application, build, icon, or generated-binding edits)  
Historical verdict: **NOT ACCEPTED** — the first package had a P1 response-inspector defect.  
Final verdict (attached R15–R17 launch harness): **ACCEPTED with one disclosed Computer Use compact-slider operation limitation.** The P1 repair, broad protocol sweep, restart persistence, and three isolated clean-state runs are now evidenced. Earlier R7–R13 isolation failures are superseded by the attached-session result below; they were launch-harness detachment artifacts rather than a reproduced product defect.

## Regression addendum — repaired package

Date/time: 2026-07-19 (America/Chicago), after the original package report.  
Repaired executable SHA-256: `ba5f53f9b34b944ff1c3970962e077336c896fbb2dfcbb95267f6941d7fada22`  
Fresh launch data directory: `/tmp/liteapi-qa-redesign-regression-r2-20260719`

The repaired packaged app was launched directly with that data directory and exercised through Computer Use. `cd frontend && npm run check` passed with 0 errors/0 warnings; `git diff --check` passed.

| Targeted regression | Result |
| --- | --- |
| 93-byte UTF-8 XML (`héllo`) → Raw | **Pass.** `200 OK`, `93 B`; Raw shows `93 bytes`, the complete one-line XML, no `preview truncated`, no truncation warning, and no Load more control. |
| UTF-8 response search/copy/pretty preview | **Pass.** Search for `héllo` returns `1 of 1`; Copy reports `Copied`; Pretty view reformats XML and preserves the non-ASCII text. No explicit wrap toggle is exposed by this response UI. |
| 5 MiB JSON guard | **Pass.** Shows `5,242,880 bytes · preview truncated`, bounded 128 KB warning, disabled Render full with an explanatory help string, Load more, and exact-body Download control. This is truthful truncation. |
| Launch/theme/compact shell quick regression | **Partial.** The repaired package launches in the dark workbench and named theme controls remain available. The Computer Use window resize action again could not change native dimensions, so compact-size acceptance remains unverified. |

Fresh evidence:

- `evidence/regression-r2-utf8-raw-pass.jpeg` — corrected 93-byte UTF-8 Raw response.
- `evidence/regression-r2-large-response-guard.jpeg` — truthful 5 MiB bounded-preview guard.

The original P1 is resolved. This addendum does not close the original broader-matrix partial scopes.

## Final P1-gate addendum — hash `6161ec37…93bcd`

Date/time: 2026-07-19 (America/Chicago).  
Final executable SHA-256: `6161ec3788d2cdbccd479674e8e8c9a9812b955fda40f882ad450272f6993bcd`  
Fresh launch data directory: `/tmp/liteapi-qa-final-r3-20260719`

`cd frontend && npm run check` passed with zero errors/warnings. `git diff --check` passed. The packaged app launched directly into the dark workbench with the packaged icon visible and no visible runtime error during the matrix.

| Sol P1 gate | Final result | Evidence |
| --- | --- | --- |
| Compact splitter vertical pointer + keyboard/persistence | **Partially verified; native interaction blocked.** Packaged AX exposes named sliders at sidebar `312` and response split `52`. A targeted Computer Use click/ArrowUp leaves split value `52`; its native window-corner resize attempt also did not change the window, so exact compact-pointer, keyboard, and relaunch persistence cannot be claimed from this runtime. Static review confirms the repaired implementation uses `clientY`/height when `compactWorkbench` is true and stores `liteapi.workbench.v2.response-split`; CSS assigns a `row-resize` horizontal splitter under the compact breakpoint. |
| 512 KiB multibyte preview and 93-byte UTF-8 truth | **Partially verified.** Fresh packaged `/xml` response gives `200`, `93 B`, full `héllo`, and Raw gives `93 bytes` with no false truncation. The exact repository fixture has no 512 KiB multibyte endpoint, so native interaction for that payload is unavailable. An independent Node execution of the shipped linear byte-boundary algorithm on `é`.repeat(262144) returned a valid `131072`-byte (`65536`-character) prefix ending in `é` in `0.53 ms`—no quadratic jank signal. |
| New dialog/palette Escape, inertness, focus and command execution | **Pass with Computer Use focus observability caveat.** Both modal AX trees hide the workbench descendants while open; Escape closes New and returns focus to its invoking `New` control. Escape closes the palette. Palette `send` filter produces the selected Send option and Enter sends the active XML request (`200`, `93 B`). Source-level modal Tab containment and invoker-focus restoration are present; Computer Use did not expose a reliable focused-element transition during Tab/Shift+Tab, so that sub-observation is not separately visualized. |
| 200 KiB binary Base64/Hex truth and full source | **Pass.** Loopback `/binary-200k` was independently confirmed as exact `204800` B. Packaged Base64 and fresh Hex both show `204,800 bytes · preview truncated`, Download remains available, and Render full is available. Rendering Base64 full removes only the truthful truncation state. Resending resets bounded Hex; switching between views retains the exact byte truth. |

Fresh final evidence:

- `evidence/final-r3-binary-hex-truncated.jpeg` — exact 200 KiB bounded Hex preview with truthful truncation, Render full, and Download.

**Contemporaneous P1 verdict: targeted P1 gates had no reproduced product failure.** The exact native 512 KiB multibyte fixture was not exposed by the repository fixture, while an independent byte-boundary check passed; the only retained acceptance caveat is the Computer Use inability to operate/resize the native compact splitter. The final attached-session result below closes the former persistence/isolation and three-clean-run gaps.

## Supplemental broad package sweep — hash `6161ec37…93bcd`

Date/time: 2026-07-19 (America/Chicago). This sweep used the same final executable hash and only repository-owned loopback fixtures.

| Scenario | Result |
| --- | --- |
| WebSocket primary flow | **Pass.** A temporary WebSocket request connected to `ws://127.0.0.1:18489/ws`; packaged UI reported `101 Switching Protocols`, `87 ms`, `290 B`, sent/received `{}` event rows, and named `Disconnect (Escape)`. |
| gRPC primary flow | **Pass.** Temporary gRPC request to `grpc://127.0.0.1:18490`, `grpc.testing.TestService/UnaryCall`, reported `200`, `OK`, `3 ms`, `184 B`; the response exposed Metadata `3`, Trailers `1`, and an intentional binary-response card. |
| Save/restart persistence | **Blocked / likely defect.** The gRPC request was explicitly saved (context changed to `SAVED`), then the package cleanly quit after discarding only a separate unsaved WebSocket draft. Relaunching the same requested data directory restored only Sample API/HTTP XML; the saved gRPC request was absent. |
| Three requested isolated launches | **Not accepted.** R4, R5, and R6 were launched by `LITEAPI_DATA_DIR=<path> build/bin/LiteAPI.app/Contents/MacOS/LiteAPI`. Process attribution confirmed R4 PID `80171` and R6 PID `55214` as the exact package executable. However, `find` before/after use found no files in any requested directory. This prevents proving that the package received the requested data directory or that launch state was isolated. Do not count these as clean-state passes. |
| Console/runtime errors | **No visible packaged runtime/console error** during HTTP, WebSocket, gRPC, save, close, or relaunch interaction. This is UI-level evidence, not a macOS unified-log audit. |

Launch paths and logs:

```text
/tmp/liteapi-qa-supplement-r4-20260719  -> /tmp/liteapi-qa-supplement-r4.log
/tmp/liteapi-qa-supplement-r5-20260719  -> /tmp/liteapi-qa-supplement-r5.log
/tmp/liteapi-qa-supplement-r6-20260719  -> /tmp/liteapi-qa-supplement-r6.log
```

All three directories were present but contained no regular files after the exercised runs. This is an acceptance blocker that must be resolved by proving `LITEAPI_DATA_DIR` reaches the packaged process, or by correcting the packaged launch/persistence path, then repeating the clean-state streak.

## Corrected R7–R9 isolation attribution — hash `6161ec37…93bcd`

Date/time: 2026-07-19 (America/Chicago). This section supersedes the R4–R6 clean-state attribution above; those older runs remain non-evidence because their process/data-directory relationship was not sufficiently controlled.

For each run, all existing LiteAPI package processes were first closed and a read-only process query showed none. The package was then launched directly from the exact executable with a unique `LITEAPI_DATA_DIR`; the reported PID below is the subsequently observed executable PID, not the transient shell-launch PID.

| Run | Direct process attribution | On-disk result | Fresh packaged UI result |
| --- | --- | --- | --- |
| R7 | `74220` with `LITEAPI_DATA_DIR=/tmp/liteapi-qa-isolation-r7-20260719` | Initial workspace directories plus the two files listed below; no request file or file containing `R7 Persist`. | Created `R7 Persist`, clicked Save, cleanly quit, and directly relaunched the *same* R7 directory. The saved request and its `SAVED` open tab returned. This is a same-directory persistence pass, but the storage is not represented by a readable request file in that data directory. |
| R8 | `75019` with `LITEAPI_DATA_DIR=/tmp/liteapi-qa-isolation-r8-20260719`, after R7 was confirmed gone | Only `My Workspace/` and `My Workspace/Sample API/`; zero regular files. | Before any R8 request interaction, the new process displayed `GET R7 Persist` in the collection and as the open `SAVED` tab. |
| R9 | `75395` with `LITEAPI_DATA_DIR=/tmp/liteapi-qa-isolation-r9-20260719`, after R8 was confirmed gone | Only `My Workspace/` and `My Workspace/Sample API/`; zero regular files. | Before any R9 request interaction, the new process again displayed `GET R7 Persist` in the collection and as the open `SAVED` tab. |

Exact final directory inventory:

```text
/tmp/liteapi-qa-isolation-r7-20260719
/tmp/liteapi-qa-isolation-r7-20260719/My Workspace
/tmp/liteapi-qa-isolation-r7-20260719/My Workspace/Sample API
/tmp/liteapi-qa-isolation-r7-20260719/transient
/tmp/liteapi-qa-isolation-r7-20260719/transient/bruno-scratch-465175176
/tmp/liteapi-qa-isolation-r7-20260719/transient/bruno-scratch-465175176/metadata.json
/tmp/liteapi-qa-isolation-r7-20260719/transient/bruno-scratch-465175176/opencollection.yml
/tmp/liteapi-qa-isolation-r8-20260719
/tmp/liteapi-qa-isolation-r8-20260719/My Workspace
/tmp/liteapi-qa-isolation-r8-20260719/My Workspace/Sample API
/tmp/liteapi-qa-isolation-r9-20260719
/tmp/liteapi-qa-isolation-r9-20260719/My Workspace
/tmp/liteapi-qa-isolation-r9-20260719/My Workspace/Sample API
```

R7's only regular files describe a transient `Scratch` collection and name the R7 workspace path; neither contains the request name. Recursive text search found `R7 Persist` in none of R7/R8/R9. Thus the request does **not** exist as a normal collection/request file in either new data directory. The direct same-directory R7 relaunch demonstrates persistence, while R8/R9 demonstrate that the visible request/tab state is also recovered across separate directories.

**QA classification: confirmed clean-workspace isolation failure.** The evidence is consistent with globally shared WebView/local storage or another unscoped persistence location, but that mechanism is an inference rather than a low-level storage proof. The app was cleanly closed after R9; final process inspection found no packaged LiteAPI executable. The required three isolated clean-state passes therefore fail and cannot be counted as accepted.

## R10 re-test — new package hash still fails isolation

Date/time: 2026-07-19 (America/Chicago). Re-test executable SHA-256: `ee55955a95e782509c0538f3bc8b09bc2c0547b277efc67172e77061997e90dd`.

Before launch, a read-only packaged-process query found no LiteAPI executable and `/tmp/liteapi-qa-isolation-r10-20260719` was absent. The exact direct launch was:

```text
env LITEAPI_DATA_DIR=/tmp/liteapi-qa-isolation-r10-20260719 build/bin/LiteAPI.app/Contents/MacOS/LiteAPI >/tmp/liteapi-qa-isolation-r10.log 2>&1 &
```

The observed exact package PID was `58966`. Before any R10 interaction, fresh Computer Use accessibility evidence showed `GET R7 Persist` as both a collection entry and the selected/open tab, with request context `HTTP Env: Development SAVED`. This is an immediate non-clean launch on a newly absent directory, so no `R10 Persist` request was created and R11/R12 were correctly not attempted.

The exact R10 directory inventory during and after the run was only:

```text
/tmp/liteapi-qa-isolation-r10-20260719
```

It contained zero regular files and no `R7 Persist` or `R10 Persist` text. The app was then closed through its native close control; a final process query found no packaged LiteAPI executable.

**Updated verdict: NOT ACCEPTED, pending backend persistence/isolation diagnosis.** This newer hash still fails before the requested same-directory persistence or three-clean-run sequence can begin. The compact splitter pointer/keyboard limitation remains independently unresolved, but it is not the release-blocking issue in this re-test.

## R13 explicit CLI-override re-test — final hash still fails isolation

Date/time: 2026-07-19 (America/Chicago). Re-test executable SHA-256: `10db82cbddc485683c8afc3447482fa17df0e821f58a5ef0de2c0c2b75521a19`.

Before launch, a read-only process query found no packaged LiteAPI executable and all three intended paths (`/tmp/liteapi-qa-isolation-r13-20260719`, `r14`, and `r15`) were absent. R13 was launched directly with the requested explicit override:

```text
build/bin/LiteAPI.app/Contents/MacOS/LiteAPI --data-dir /tmp/liteapi-qa-isolation-r13-20260719 >/tmp/liteapi-qa-isolation-r13.log 2>&1 &
```

The observed package PID was `41573`; its read-only `ps` command field showed only the package executable path (the passed CLI arguments were not retained in that process listing). Before any R13 interaction, fresh Computer Use accessibility evidence again showed `GET R7 Persist` in the collection and as the selected/open tab, with `HTTP Env: Development SAVED`.

At this point `/tmp/liteapi-qa-isolation-r13-20260719` did not exist at all—there were no startup artifacts, no request artifacts, and no text to search. The preserved `/tmp/liteapi-qa-isolation-r13.log` exists and is zero bytes. The package was closed through its native close control; final process inspection found no packaged LiteAPI executable and R13 remained absent. R14 and R15 were correctly not attempted because R13 failed the clean-start prerequisite.

**Historical R13 isolation verdict: NOT ACCEPTED.** The explicit CLI override did not produce an isolated workspace in that detached packaged execution. This was subsequently identified as a launch-harness artifact and is superseded by the attached-session R15–R17 result below.

## Final attached-session R15–R17 isolation and persistence acceptance — hash `10db82cb…21a19`

Date/time: 2026-07-19 (America/Chicago). This section **supersedes the R7–R13 isolation verdicts**. Root-cause investigation determined those launches were detached from their short-lived shell harnesses. Each run below instead used a foreground `exec_command` session with no `&`, redirection, or shell exit; the session stayed alive until the app was closed through the native UI and returned exit code `0`.

Final executable SHA-256: `10db82cbddc485683c8afc3447482fa17df0e821f58a5ef0de2c0c2b75521a19`.

| Run | Foreground command and observed PID | UI / persistence result | Scoped on-disk evidence |
| --- | --- | --- | --- |
| R15 initial | `build/bin/LiteAPI.app/Contents/MacOS/LiteAPI --data-dir /tmp/liteapi-qa-isolation-r15-20260719`; PID `91738` | Clean initial UI: Sample API + unsaved Request only; no legacy R7 state. Created `R15 Persist`, then explicitly saved it (`SAVED`). | Startup created `.liteapi-legacy-migration.lock`, `state.json`, `shared-state.json`, `workspace-registry.json`, workspace/recovery/window-session state, and `My Workspace/Sample API`. Saving produced `My Workspace/Sample API/R15 Persist.yml`; its content and shared state identify `R15 Persist`. |
| R15 same-dir relaunch | same foreground command/path; prior foreground session first exited `0` after UI close, then relaunch session also exited `0` | Saved `GET R15 Persist` returned in the collection and as the open saved tab. | Same R15 scoped request file remained present. |
| R16 | `build/bin/LiteAPI.app/Contents/MacOS/LiteAPI --data-dir /tmp/liteapi-qa-isolation-r16-20260719`; PID `92802`; session exited `0` after UI close | Clean initial UI: Sample API + unsaved Request only; neither collection nor tabs showed `R15 Persist`. | Its own migration lock, state, registry, recovery, window-session, and Sample API files were created under R16. Recursive search found no `R15 Persist`. |
| R17 | `build/bin/LiteAPI.app/Contents/MacOS/LiteAPI --data-dir /tmp/liteapi-qa-isolation-r17-20260719`; PID `93223`; session exited `0` after UI close | Clean initial UI: Sample API + unsaved Request only; neither collection nor tabs showed `R15 Persist`. | Its own migration lock, state, registry, recovery, window-session, and Sample API files were created under R17. Recursive search found no `R15 Persist`. |

Before R15, a process check found no LiteAPI process and all three paths were absent. After R17's native close and session completion, a final process check again found no LiteAPI process. No visible packaged runtime error occurred in any of the three attached launches.

**Final three-run result: PASS.** The direct CLI `--data-dir` override is now proven to create scoped artifacts, preserve same-directory saved work, and prevent R15 state leaking to fresh R16/R17 instances. This clears the former release blocker. The only retained acceptance caveat is the already disclosed Computer Use inability to operate the native compact splitter through its AX bridge; the slider remains named and visible with its default value, but its pointer/keyboard behavior was not machine-operable in that harness.

## Artifact identity

- App: `build/bin/LiteAPI.app`
- Executable: `build/bin/LiteAPI.app/Contents/MacOS/LiteAPI`
- Original SHA-256: `3f132bc08b0794fab2ec80c8ad8b08d8603c0659eb5bc04ba6e5b4f2dd2ca84d` (superseded by the repaired-package hash in the regression addendum)
- Bundle: `com.wails.LiteAPI`, arm64, ad-hoc signed.
- `Info.plist` declares `CFBundleIconFile=iconfile`; packaged `Resources/iconfile.icns` SHA-256 is `4eec839408f842cc042f98877d2fdd7851cfa561871c99518caf19d9bba81481`.
- Visible native window evidence shows the new purple/orange workbench icon at the app-window chrome. See `evidence/run1-default-1024.jpeg`.

## Execution and attribution

Computer Use interacted with the final packaged `.app`, not a browser/dev server. A read-only process check identified one active packaged executable:

```text
77537  /Users/mostafi/Developer/Workspace/lite_api/build/bin/LiteAPI.app/Contents/MacOS/LiteAPI
```

Run 1 was launched with `LITEAPI_DATA_DIR=/tmp/liteapi-qa-redesign-run1-20260719`. That directory remained empty during the pass, while the seeded Sample API rendered an Application Support path in search. This does not prove data leakage (the sample metadata may be static), but it means filesystem-level isolated-state attribution is **inconclusive**. Do not count this as one of the required three clean-state passes.

Loopback-only fixtures used during live interaction:

```text
go run ./qa/responsefixture -listen 127.0.0.1:18487 -grpc-listen 127.0.0.1:18488
go run docs/qa/fixtures/slow_server.go   # reported http://127.0.0.1:52899
```

`curl` confirmed `/xml` was `200`, 93 bytes, with the documented SHA-256 `e0c8c8170ddaec38d62ee566e31404b13247599bea95b231444f011546193b52` before it was sent through the packaged UI.

## Automated/package gates

| Check | Result |
| --- | --- |
| `shasum -a 256 build/bin/LiteAPI.app/Contents/MacOS/LiteAPI` | Pass; hash above |
| `codesign -dv --verbose=2 build/bin/LiteAPI.app` | Pass; ad-hoc arm64 bundle |
| `cd frontend && npm run check` | Pass; 0 errors, 0 warnings |
| `cd frontend && npm run build` | Pass; 1,118.16 kB chunk advisory only |
| `env GOCACHE=/tmp/liteapi-qa-gocache GOMODCACHE=/tmp/liteapi-qa-gomodcache go test ./...` | Pass with normal loopback/network access |
| same cache environment, `go vet ./...` | Pass |
| `git diff --check` | Pass in the same elevated repository-check chain |

The first Go attempt was sandbox-blocked from both dependency download and loopback fixture binding; rerunning with approved normal access passed. This is environment-only, not a test failure.

## Live packaged scenario matrix

| Scenario | Status | Evidence/observation |
| --- | --- | --- |
| Launch, native AX, icon | Verified | App window, native menu, readable names, icon evidence |
| HTTP method/URL and Send | Verified | POST `/xml` returned `200 OK`, `12–13 ms`, `93 B` |
| Params, body, headers, auth | Partially verified | Params and headers expose named editable tables; JSON body edit yielded correct invalid-JSON truth and disabled Format/Minify; auth mode selection exposes named secure Token field. Header text-field mutation was not reliable through the Computer Use AX bridge. |
| Malformed input | Verified | `{\"bad\": }` showed `Invalid JSON at line 1, column 9` and disabled Format/Minify without corrupting the UI. |
| Success/error/cancel | Verified | XML success; `127.0.0.1:1` showed connection-refused request failure; slow request exposed `Cancel request (Escape)` and Escape produced `Cancelled`, `Request cancelled`, `0 B`. |
| Response format/search/copy/raw/preview | Failed | Pretty XML and search (`1 of 1`) work. Raw exposes the P1 false-truncation defect below. Copy control is named; download intentionally not invoked because it opens a filesystem write dialog. |
| Tabs/data retention | Partially verified | Request/response tab groups have selected roles; editor state remained coherent while switching Body/Headers/Auth. Restart persistence not validly attributable. |
| Command palette vs workspace search | Verified | `Cmd+Shift+P` opens named command palette (New, Send, Save, Search, layout, sidebar, Preferences, Dev Tools); `Cmd+K` opens distinct Global Search. See `evidence/run1-command-palette.jpeg`. |
| Compact New flow | Verified | Named New dialog has editable name, HTTP protocol selector, Cancel/Create; no permanent create form. See `evidence/run1-new-request.jpeg`. |
| Sidebar collapse/overlay | Verified for wide state | Hide/Show sidebar works with named controls. Compact overlay requires an actual compact resize pass. |
| Both resize controls | Partially verified | Named AX sliders with values/sidebar `312` and split `52`, reset help text, and safe defaults were observed. Computer Use click/drag/key paths did not change values, so pointer, keyboard, reset, and persistence are not accepted. |
| Themes | Verified | Dark → Light → Dark settings transitions, selected states, and named controls. Light evidence: `evidence/run1-light-settings.jpeg`. |
| 800x600, 1024x768, wide | Partially verified | Wide native shell was observed at screenshot dimensions 1225x768. The Computer Use window resize drag did not change the native window, so required 800x600 and exact 1024x768 acceptance were blocked. |
| Environments, collections/history | Partially verified | Collection sidebar, Global/Active environment selectors, recovery summary, and response history/example controls were visible and named; destructive/recovery actions were not invoked. |
| WebSocket/gRPC | Not reverified | Fixture was launched but no protocol-specific fresh interaction pass was completed after the P1 blocker. |
| Restart/persistence, 3 clean-state passes | Not verified | Run 1 isolation attribution inconclusive; runs 2/3 were not performed after a blocking defect. |
| Console/runtime errors | Partially verified | No visible native console/runtime error occurred during exercised HTTP/UI scenarios; no system console capture was available in the Computer Use scope. |

## Defects

### P1 — UTF-8 response falsely labeled truncated in Raw view (**resolved in repaired hash `ba5f53f9…ada22`**)

**Reproduction**

1. Launch the package and send `http://127.0.0.1:18487/xml`.
2. The response is `200`, `93 bytes`, containing `héllo`.
3. Select `Response view → Raw`.

**Actual**: UI says `93 bytes · preview truncated`, renders `Preview is truncated.`, and shows `Load more`. `Load more` does not make the tiny fully-rendered response less truncated.

**Expected**: A complete 93-byte text/XML response should not be marked truncated or expose a no-op expansion action.

**Impact**: Response truth is misleading for all responses where UTF-8 byte count exceeds JavaScript character count, violating the requested raw/preview behavior.

**Cause confirmed by read-only source inspection**: `frontend/src/lib/workbench/ResponseInspector.svelte` computes `bodyTruncated` with `bytes > safeDisplay.length`. `bytes` is UTF-8 byte length while `safeDisplay.length` is JS UTF-16 code-unit length. The `é` in the fixture makes the byte count larger despite no omitted content.

**Original evidence**: `evidence/run1-default-1024.jpeg` captures the app/icon; AX capture recorded raw view with the false status and no-op Load more. The repaired-hash evidence in `evidence/regression-r2-utf8-raw-pass.jpeg` confirms the false status/warning/no-op control are absent.

## Evidence files

- `evidence/run1-default-1024.jpeg` — native default dark workbench and packaged icon.
- `evidence/run1-command-palette.jpeg` — dedicated command palette.
- `evidence/run1-light-settings.jpeg` — Light theme settings state.
- `evidence/run1-new-request.jpeg` — compact New request flow.

## Release recommendation

**Accept the final hash `10db82cb…21a19`.** The foreground-session R15–R17 gate now supplies three demonstrably isolated clean-state runs, scoped ownership artifacts, and same-directory restart persistence. Retain the compact-slider pointer/keyboard interaction as a known Computer Use verification limitation; no product failure was reproduced there.
