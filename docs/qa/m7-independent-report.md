# M7 Final-Package QA Report

Date: 2026-07-19 (America/Chicago)

Current package executable SHA-256: `b7a3598b3d43370c116f5c178df580ff3d6d3e2423d07ce2444061505a7d481c`

Result: PASS — no confirmed product P0/P1

## Repaired-package regression

After M8 found and repaired empty-collection materialization, independent QA repeated the final-package gate at `/tmp/liteapi-m7-repair.nzVe7v` on the new hash:

- Direct initial PID 38865 and direct relaunch PID 40636 each matched the isolated `main-window` owner lock; both normal closes ended their foreground exec sessions and left zero package processes/live locks.
- Explicit Light then Dark persisted through relaunch as AX `Theme dark`, Dark on.
- The saved XML request returned `200 OK`, 11 ms, 93 bytes, and `NEEDLE-42`; collection, tab, and URL restored.
- The empty `M7 Replay YAML` collection root and `opencollection.yml` existed before and after relaunch.
- The private session restored vertical response orientation and safe geometry x=516, y=123, width=1024, height=800.
- Compact workbench/fullscreen, Cmd+L/Cmd+S/Cmd+Enter, response-tab arrow navigation, More/Escape, named AX states, and File/View menus passed.

Evidence: `/tmp/liteapi-m7-repair.nzVe7v/m7-xml-response.png`, `m7-fullscreen.png`, and `m7-relaunch.png`.

## Trusted independent coverage

- Light and Dark workbench/Preferences appearances.
- Compact native capture near 984x768, larger capture near 1223x768, and fullscreen.
- No observed clipping, unreadable contrast, or inaccessible workbench/settings controls at those bounds.
- Cmd+L URL focus, Cmd+S save, Cmd+Enter send, response-tab Right Arrow navigation, and Escape dismissal of the in-app More disclosure.
- Native File menu commands and named AX roles/states for request target, response status, tabs, Settings/theme, search, copy, download, timeline, and export.
- Deterministic `http://127.0.0.1:18487/xml` response: `200 OK`, 93 bytes, duplicate headers, and `NEEDLE-42`.
- A replacement pass verified the immutable hash, live direct PID/owner-lock match, explicit Light selection, HTTP fixture truth, and Cmd+L before its controller session became untrusted.

Evidence directories: `/tmp/liteapi-m7-final.dDAoP2` and `/tmp/liteapi-m7-replacement-live.QA`.

## Rejected false Dark-persistence report

The first worker saw an apparent Dark selection after the isolated `shared-state.json` had stopped changing, then reported `theme: system` after relaunch. Root found an additional same-bundle process and terminated all LiteAPI processes. In a fresh single-process replay at `/tmp/liteapi-theme-repro.Kf8Y7a`:

1. The initial Settings AX state was `Theme system`, System on.
2. Clicking Dark changed AX to `Theme dark`, Dark on.
3. `shared-state.json` immediately contained `"theme": "dark"` with the current modification time.
4. Normal close exited the only process.
5. A direct isolated relaunch restored `Theme dark`, Dark on.

No code change was required because the alleged persistence defect was not reproducible under valid attribution.

## Rejected false process/owner report

The replacement worker closed its attributed process, then asked Computer Use for the app by bundle path. That operation auto-launched PID 64852 without `LITEAPI_DATA_DIR`, so it correctly had no lock in the isolated directory. This was a controller-created default-data process, not LiteAPI changing PID or losing ownership during relaunch. Root stopped it before M8.

## M8 controller rule

For every close/relaunch assertion: close through the current trusted AX tree, confirm the foreground exec session/process ended using the shell, do not query Computer Use while the app is closed, relaunch the binary directly with the same isolated environment, verify live PID/owner-lock attribution, and only then request a new AX state.
