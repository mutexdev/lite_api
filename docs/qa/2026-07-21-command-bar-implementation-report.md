# LiteAPI command-bar implementation report

Date: 2026-07-21 (America/Chicago)

Status: implemented; automated gates and packaged macOS Computer Use acceptance pass.

## Outcome

LiteAPI now uses one compact, Yaak-inspired workspace command bar instead of a permanent activity rail plus a wrapping toolbar. The design keeps LiteAPI's own colors, typography, icons, local-first identity, native macOS chrome, request tabs, and domain menus.

The command bar keeps frequent context and actions visible: sidebar, Add Resource, workspace, scoped environment, Cookie Jar, active collection/request, Runner, Git/local state, recovery, notifications, response orientation, command palette, and Main Menu. Lower-frequency destinations moved into grouped menus and semantically matching native macOS menus.

The prior native routing defect is repaired: `Cmd+K` and `View > Search Workspace` open workspace search, while `Cmd+Shift+P` and `View > Command Palette` open the distinct command palette.

## Implementation surfaces

- `frontend/src/lib/workbench/WorkspaceCommandBar.svelte`: stable single-line command bar and responsive priority rules.
- `frontend/src/lib/workbench/CommandOverflowMenu.svelte`: grouped Add/Main menus, first-item focus, arrow/Home/End navigation, Escape dismissal, outside dismissal, and focus restoration.
- `frontend/src/lib/workbench/EnvironmentContextMenu.svelte`: explicit Global and collection environment scopes in one compact control.
- `frontend/src/lib/workbench/workbenchCommands.ts`: typed shared command inventory and metadata.
- `frontend/src/App.svelte`: centralized `runWorkbenchCommand`, relocated routes, removed activity-rail markup, and distinct search/palette dispatch.
- `frontend/src/style.css`: two-column shell, compact overlay sidebar, non-wrapping command surface, and obsolete toolbar/activity-rail rule removal.
- `native_menu.go` and `native_menu_test.go`: View, Request, Collection, and Help destinations plus focused inventory/accelerator/reachability coverage.

## Automated gates

| Gate | Result |
| --- | --- |
| `npm run check` | Pass: 0 errors, 0 warnings |
| `npm run build` | Pass; only the existing large-chunk advisory remains |
| `go test ./...` | Pass across the app and QA fixture packages |
| `go test -race ./...` | Pass across the app and QA fixture packages |
| `go vet ./...` | Pass |
| Focused native-menu tests | Pass |
| `git diff --check` | Pass after normalizing generated Wails whitespace |
| Pinned Wails package | Pass with Wails CLI v2.12.0; packaged and self-signed |

Final executable:

- Path: `build/bin/LiteAPI.app/Contents/MacOS/LiteAPI`
- SHA-256: `4f49d7d73383f98116db9860ccca07d72c9c1e9266a4d149471948e74f78a6e6`

## Packaged Computer Use acceptance

The final package was launched directly as PID 67425 with isolated data at `/tmp/liteapi-menubar-escape-final.YQutjy`. The process arguments and isolated owner state were checked before acceptance; the user's normal LiteAPI data was not used for the final run.

Computer Use verified on the final executable hash:

- AX exposes `Workspace command bar`; no `Primary navigation` activity rail remains.
- Add Resource exposes HTTP, GraphQL, gRPC, WebSocket, Folder, Collection, Import, and Open Workspace.
- Mouse-opened Add Resource focuses HTTP; Escape closes the menu and restores focus to Add Resource.
- Main Menu exposes Search Workspace, Manage Environments, Collection Settings, Network Log, Dev Tools, Capabilities, Import, Keyboard Shortcuts, and Preferences.
- Mouse-opened Main Menu focuses Search Workspace; Escape closes it and restores focus to Main Menu.
- Environment context exposes distinct Global and collection scopes; Escape closes it and restores focus.
- Cookie Jar opens the Cookies view; Runner opens the Runner view; Main Menu opens Network Log.
- The active collection/request breadcrumb returns the workbench to the request view.
- Sidebar collapse/restore preserves the command bar and changes the AX name truthfully between Show and Hide.
- Native View exposes Search Workspace, Command Palette, Toggle Sidebar, Change Response Orientation, Network Log, and Toggle Developer Tools.
- Native Request exposes Cookie Jar; Collection exposes Runner; Help exposes Capabilities and Keyboard Shortcuts.
- Native Command Palette opens the command palette, not workspace search.

Computer Use discovered one focus defect before final acceptance: a pointer-opened Add/Main menu could miss Escape in macOS WebKit. The disclosure now focuses its first enabled item on open. The repaired final package passed Add, Main, and environment Escape checks with trigger-focus restoration.

## Evidence

- Screenshot: `docs/qa/2026-07-21-command-bar-isolated-pass.jpeg`
- Screenshot SHA-256: `ec46a97e43c59d3840e525bb32b4cf7c0be7fe74f22428be79a02eeb17319491`
- Planning/audit: `docs/ux/2026-07-21-yaak-style-command-bar-plan.md`

## Remaining advisory

Vite reports that the existing main JavaScript chunk exceeds 500 kB. This implementation does not increase runtime correctness risk, but future performance work should split lower-frequency workbench views with dynamic imports. No blocking product, keyboard, routing, build, vet, race, or packaged native issue remains in this slice.
