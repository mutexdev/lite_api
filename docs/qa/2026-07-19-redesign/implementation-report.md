# LiteAPI redesign implementation report

Date: 2026-07-19 (America/Chicago)

## Delivered slice

- Repaired the compact shell: the activity navigation becomes a horizontal, scrollable rail below 960px and the collection sidebar becomes a reachable overlay drawer instead of creating a full-height dead row.
- Replaced permanent collection/request creation inputs with a compact **New** flow for scratch requests.
- Added a distinct `Cmd+Shift+P` command palette; `Cmd+K` remains workspace object search.
- Added bounded, persisted (WebView-local) sidebar width and request/response split preferences. Both controls are native range sliders with keyboard values, pointer resize, reset on double-click, and safe defaults (`312px`, `52%`).
- Converted document/request/response tab strips to non-wrapping horizontal scrollers.
- Moved response status, time, and size into the response header; the inspector now gives a truthful no-response state instead of disabled-looking response tools.
- Replaced the response-layout text glyph with an icon plus screen-reader label.
- Added the original API-workbench icon source at `build/appicon.svg`, regenerated `build/appicon.png`, and Wails packaged its macOS `iconfile.icns` from that source.

## Implementation files

- `frontend/src/App.svelte`
- `frontend/src/style.css`
- `frontend/src/lib/workbench/RequestCommandStrip.svelte`
- `frontend/src/lib/workbench/ResponseInspector.svelte`
- `build/appicon.svg`
- `build/appicon.png`
- `frontend/wailsjs/go/models.ts` (Wails-regenerated formatting cleanup only)

## Verification

| Command / check | Result |
| --- | --- |
| `cd frontend && npm run check` | Pass: 0 errors, 0 warnings |
| `cd frontend && npm run build` | Pass; standard Vite large-chunk advisory remains |
| `env GOCACHE=/tmp/liteapi-redesign-gocache GOMODCACHE=/tmp/liteapi-redesign-gomodcache go test ./...` | Pass |
| same cache environment, `go vet ./...` | Pass |
| `go run github.com/wailsapp/wails/v2/cmd/wails@v2.12.0 build -m -nosyncgomod` | Pass; packaged and self-signed |
| `git diff --check` | Pass after generated-binding whitespace cleanup |

Final package executable:

```text
build/bin/LiteAPI.app/Contents/MacOS/LiteAPI
SHA-256: 10db82cbddc485683c8afc3447482fa17df0e821f58a5ef0de2c0c2b75521a19
```

## UTF-8 preview repair (P1)

Independent QA found that the 93-byte `/xml` fixture (`héllo`) was falsely marked as truncated because the response byte count was compared to a JavaScript UTF-16 string length. The response workbench now measures and slices previews in UTF-8 bytes, clips at valid character boundaries, and determines truncation only from the preview budget/source bounds. Responses at or below the automatic 128 KiB preview budget cannot receive a truncation indicator solely because they contain multibyte text.

Focused native validation used `http://127.0.0.1:18489/xml` (verified `Content-Length: 93`). The accessibility tree showed `93 bytes`, the `héllo` payload, no truncation label, and no no-op Load more control. The byte clipper encodes the source once and finds only the final UTF-8 code-point boundary, rather than repeatedly encoding prefixes.

## Binary preview truth and modal keyboard repairs (P1)

- Base64 and Hex now derive their preview state from decoded source bytes. They retain a valid 128 KiB decoded-byte preview, show `preview truncated` when the source exceeds it, and expose Render full only when the complete retained source is at or below 1 MiB; otherwise Download remains the truthful path. Changing response views resets to that bounded preview so no prior full-render decision leaks into Base64/Hex.
- Added `/binary-200k` (exact 204,800-byte deterministic payload) to the local response fixture and Go test matrix. Native AX confirmed its exact size and truncated Pretty/binary state; the final freshly packaged view-transition assertion remains for independent QA because macOS kept the prior, unsaved app instance open rather than replacing it.
- New request and command palette are semantic `aria-modal` dialogs with Escape close, contained Tab/Shift+Tab, opener-focus return, and an inert background. The command palette retains filter focus while ArrowUp/ArrowDown select an action and Enter runs it.

Frontend check/build and Go test/vet pass. The final packaged executable is:

```text
SHA-256: 10db82cbddc485683c8afc3447482fa17df0e821f58a5ef0de2c0c2b75521a19
```

## Focused native pass

The final package was launched with `LITEAPI_DATA_DIR=/tmp/liteapi-redesign-native-final2-20260719` and inspected through macOS Computer Use.

- Default 1024x768 shell rendered coherently with no text fallback in the orientation control.
- Accessibility exposed the sidebar and split controls as named range sliders at their intended safe defaults: `312` and `52`.
- Command palette opened from its dedicated control and listed New, Send, Save, workspace search, layout, sidebar, Preferences, and Dev Tools commands.
- The compact New flow opened with an editable request name and protocol selection.
- Request and response section tabs remained single-line accessible tab groups; the response pane rendered the intentional empty state.

Evidence:

- `native-default-shell.png`
- `native-new-request-flow.png`

## Known limitations / follow-up QA

- This is an implementation-owner focused native pass, not independent acceptance. The independent QA matrix still needs the requested 800x600, theme, protocol, persistence, multi-window, and three-clean-run coverage.
- The Vite bundle remains approximately 1.12 MB minified; feature-level code splitting is deferred.
- The Windows ICO was not regenerated in this environment because `sips` was blocked from writing its protected temporary conversion output. macOS/Wails packaging uses the verified PNG source and generated `iconfile.icns`.
- The last Base64/Hex view-transition interaction needs independent fresh-instance confirmation: closing the current app opened an unsaved-changes prompt, so the existing workspace was preserved instead of discarding it to force macOS to replace the app process.

## Data-directory root cause and reproducible acceptance

The R7 request was not supplied by browser storage or a production-state merge. Disk inspection located `R7 Persist` in the default macOS store at `~/Library/Application Support/LiteAPI/My Workspace/Sample API/R7 Persist.yml`, with corresponding shared state in `~/Library/Application Support/LiteAPI/shared-state.json`. The claimed R10/R13 directories were present but had none of the initial migration marker, workspace registry, scoped workspace state, session, or collection artifacts. Process inspection identified the cause: the QA command backgrounded the GUI with `&` and allowed its shell to exit; the resulting macOS process had only the executable path in argv, so its requested directory argument was absent and startup selected the default store.

All production startup, workspace registry, migration, scoped-state, shared-state, recovery, and session paths were traced with an explicit `dataDir`; none resolves another data directory after startup or merges request/tab state across directories. The packaged app now accepts an explicit main-window `--data-dir` argument, so isolation does not rely on macOS preserving a shell environment variable during app activation. Child workspace windows retain their validated launch-intent directory.

For reproducible isolated acceptance, quit every LiteAPI window cleanly (resolve any unsaved-changes prompt), then invoke the Mach-O executable directly from a foreground, long-lived terminal session—do not use Finder, `open`, `&`, or a harness that exits its parent shell immediately:

```sh
/absolute/path/to/LiteAPI.app/Contents/MacOS/LiteAPI \
  --data-dir /tmp/liteapi-isolated-r7
```

Keep that terminal session alive while inspecting the process and data directory. A detached/backgrounded macOS launch can be reattached by the app runtime with only the executable path in its argv, dropping the requested data-directory argument. The foreground process must show both `LiteAPI.app/Contents/MacOS/LiteAPI` and `--data-dir /tmp/liteapi-isolated-r7` in `ps` before UI acceptance begins.

Before creating a request, verify that the selected directory receives the migration marker, workspace registry, `workspace-state/`, and `window-sessions/` artifacts. Save an R7 request/tab, fully quit, then launch a different explicit directory for R8. R8 must contain only its fresh Sample API/HTTP tab and none of R7's request or state files.

`TestProductionDataDirectoriesDoNotShareSavedRequestsOrOpenTabs` provides automated regression coverage: it saves a unique request/open tab in production directory A, confirms the same directory restores both after relaunch, then proves independent production directory B contains neither. `TestProductionMainWindowDataDirArgumentOverridesEnvironmentFallback` covers the direct-launch argument parser.

## WebView storage isolation repair (R7/R8)

WebKit storage is keyed to the application bundle, not `LITEAPI_DATA_DIR`. The frontend previously used two global localStorage keys for workbench geometry. It now requests an opaque backend-issued scope derived from the canonical backend data directory after `GetState()` hydrates, and reads/writes only `liteapi.workbench.v3.<scope>.*`. No request, workspace, or tab state is stored in browser storage: those values remain backend `AppState` data. The scope test proves it is stable across same-directory production relaunch and distinct for different directories. This is useful hardening, but the R7/R10 evidence above establishes it was not the source of the leaked request/tab.

Independent final acceptance should run R7 then R8 with explicit `--data-dir` direct executable launches and all prior LiteAPI processes stopped: R7 must restore its saved request/tab; R8 must contain only its newly initialized Sample API/HTTP tab and must not have R7 files in its own data directory.
