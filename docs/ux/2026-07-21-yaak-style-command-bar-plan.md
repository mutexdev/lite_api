# LiteAPI Yaak-style command bar implementation plan

Date: 2026-07-21 (America/Chicago)

Status: implemented and root-validated on 2026-07-21. The final package and Computer Use evidence are recorded in `docs/qa/2026-07-21-command-bar-implementation-report.md`. This document is based on direct Computer Use inspection of installed Yaak, Postman 12.20.1, and the packaged LiteAPI application, plus source inspection of the existing dirty worktree. Existing work was preserved.

## Context

The requested “menubar” spans two different surfaces on macOS:

1. The native macOS menu bar (`File`, `Edit`, `View`, and so on).
2. The in-window command bar directly beneath the native title bar.

The desired Yaak quality comes primarily from the second surface. Yaak keeps a fixed, compact command strip with workspace and environment context on the left, the active resource in the center, and layout, command, and overflow actions on the right. Its native `File` and `View` menus are intentionally sparse. Postman uses a heavier in-window header but a more complete native `View` menu for sidebar, pane, layout, console, and reset commands.

LiteAPI already implements most required commands and state. The problem is their current composition: the top toolbar exposes workspace, search, command palette, sidebar, notifications, Git/local state, recovery, global environment, collection environment, and runner cancellation in one wrapping row. At normal sizes it is visually busy; at narrower sizes controls either wrap, horizontally scroll, or disappear.

## Goals

- Replace the wrapping toolbar with one stable, Yaak-inspired command strip while preserving LiteAPI's own visual identity.
- Consolidate the current 48 px activity rail into the command strip, contextual controls, overflow menu, and native menus so the workbench has one clear navigation model.
- Make workspace, environment, active-resource, and local/Git state understandable at a glance.
- Keep frequent actions directly visible and move secondary actions into an accessible overflow menu.
- Preserve the existing request tabs immediately below the command strip.
- Keep the native macOS menu bar as the keyboard/discoverability safety net.
- Reuse existing state and action paths rather than create parallel behavior.
- Preserve light/dark themes, compact layouts, sidebar collapse/resize, multi-window isolation, notifications, recovery, and local-first behavior.

## Non-goals

- Do not copy Yaak or Postman icons, assets, colors, branding, or exact geometry.
- Do not adopt a frameless/custom macOS title bar or move the traffic-light controls into the WebView.
- Do not add account, cloud, collaboration, promotional, or plugin-store surfaces.
- Do not change request execution, persistence formats, protocol behavior, Git semantics, or security boundaries.
- Do not remove the native `Request`, `Collection`, `Environment`, or `Git` menus merely to match Yaak's sparse native menu.
- Do not redesign the collection tree, request editor, or response inspector in this slice.

## Direct comparative observations

### Yaak

- The in-window command strip remains one line and does not compete with request or response content.
- The leading cluster contains sidebar visibility, Add Resource, cookie jar, workspace, and environment controls.
- Add Resource is intentionally narrow: HTTP, GraphQL, gRPC, WebSocket, and Folder. It is a creation menu, not a general navigation drawer.
- The middle communicates the active request and provides quick resource switching.
- The trailing cluster contains response orientation, command execution, and a single Main Menu disclosure.
- Main Menu groups Settings, Keyboard shortcuts, Plugins, Import Data, Export Data, Create Run Button, Feedback, and Changelog with separators and shortcut hints.
- Sidebar collections are compact, expandable, and communicate method plus latest status without turning the command strip into a status dashboard.
- The native `File` menu contains only window-close commands; the native `View` menu contains fullscreen and zoom commands.

### Postman

- The in-window header is feature-rich but visually heavier: global navigation, search, account actions, banners, tabs, history, console, and promotional state all compete for attention.
- Its native `File` menu provides New, New Tab, Runner, Window, Import, and close commands.
- Its in-window New surface groups resource types—HTTP, WebSocket, Socket.IO, GraphQL, gRPC, MQTT, Collection, Environment, API, Flows, and Workspace—rather than keeping a separate permanent navigation button for each creation path.
- Its native `View` menu is useful precedent for panel and layout commands: left/right sidebar, two-pane view, workbench, swap sidebars, reset layout, console, and developer tools.
- Adopt the layout-command completeness, not the header density or account/cloud clutter.

### Current LiteAPI

- The packaged app exposes a comprehensive native menu: `LiteAPI`, `File`, `Edit`, `View`, `Request`, `Collection`, `Environment`, `Git`, `Window`, and `Help`.
- The current in-window toolbar already connects to workspace-window selection, workspace search, command palette, sidebar collapse, notifications, collection settings/Git, recovery, runner cancellation, global environment, and collection environment.
- A separate activity rail currently adds Request, Collection, Environments, Import, Network, Cookies, Dev Tools, Capabilities, Runner, and Settings. Several of those are destinations or utilities rather than peer workspaces, so the rail makes the app look more complex than the underlying workflow.
- The current toolbar uses `flex-wrap: wrap` and hides some important controls below 680 px. That makes the command surface change shape instead of remaining predictable.
- The request tabs already form a separate non-wrapping row below the toolbar and should remain there.
- There is one routing defect to repair in this slice: the native command ID `command-palette` currently calls `openGlobalSearch()` instead of `openCommandPalette()`.
- The packaged app and source already expose named accessibility controls for all major actions; the new bar must retain or improve those names.

## Selected approach

Use a hybrid model:

- Keep the native macOS title/menu bar and existing domain menus.
- Replace the current in-window toolbar markup with an extracted `WorkspaceCommandBar` component.
- Use progressive disclosure modeled on Yaak: a small stable set of primary controls plus one grouped overflow menu.
- Borrow Postman's useful native `View`-menu idea only for layout and panel commands.

### Target command-strip layout

Wide and medium layouts use one 36-40 px row:

```text
[Sidebar] [New v] [Workspace v] [Environment v] [Cookies] | [Collection / active request] | [Run] [Git/local] [alerts] [layout] [commands] [more]
```

The existing request-tab strip remains directly below it:

```text
[GET Health x] [POST Login x] [WS Stream x] ...
```

Primary-control rules:

- **Sidebar:** always visible, icon plus tooltip and current-state `aria-label`.
- **New:** one disclosure containing New Request, New Collection, Open/Import Collection, and Open Workspace in New Window. Existing flows remain authoritative.
- **Workspace:** shows the active workspace and opens the existing workspace-window picker or workspace selector.
- **Environment context:** presents Global and collection environment as explicitly labeled scopes. At compact width, combine them behind one context button whose accessible name contains both selected values.
- **Cookies:** use a direct cookie-jar button beside environment context, matching the importance and placement observed in Yaak. This replaces the Cookies activity-rail destination.
- **Active context:** a flexible, truncated `Collection / Request` breadcrumb. It is informational in the first slice; tabs remain the switching mechanism.
- **Git/local state:** compact status button that opens the current collection settings. Hide the text label at medium width but preserve the state in the accessible name.
- **Alerts:** notifications use an icon with unread badge; recovery appears only when nonzero and remains visually prominent. Recovery must never be hidden only inside More.
- **Layout:** direct response-orientation button using an original split-layout SVG, not the current glyph stand-in.
- **Run:** a compact play button opens the existing collection Runner. While a collection run is active, it becomes truthful running/cancelling status with a directly reachable cancel action.
- **Commands:** direct command-palette button with `Cmd+Shift+P` hint at wide width and icon-only presentation at medium/compact width.
- **More:** grouped overflow for lower-frequency destinations and settings.

### Overflow-menu contents

Only expose commands that already have working LiteAPI routes. The menu is also the new home for low-frequency activity-rail destinations:

- Workspace: Search Workspace, Manage Environments, Collection Settings.
- Execution: Runner, cancel active request/run when applicable.
- Tools: Network Log, Dev Tools.
- App: Import, Capabilities, Preferences, Keyboard Shortcuts.

Menu behavior:

- Button exposes `aria-haspopup="menu"` and `aria-expanded`.
- Opening moves focus to the first enabled item.
- Up/Down, Home/End, Enter/Space, Escape, Tab, click-outside, and focus restoration are deterministic.
- Disabled or unavailable actions remain truthfully disabled rather than silently doing nothing.
- The menu closes after an action and when the active workspace/window changes.
- Menu sections use separators and visible shortcut hints.

### Responsive behavior

- **>=1180 px:** labels and shortcut hints are visible; the active-context breadcrumb consumes remaining space.
- **960-1179 px:** secondary text collapses to icons/tooltips; environment context becomes one compact control; the row never wraps.
- **800-959 px:** sidebar is already an overlay/collapsed surface; the bar keeps Sidebar, New, Environment, Alerts when present, Commands, and More. Active context truncates before controls disappear.
- **Below 800x600:** retain the project's existing minimum-size decision. Do not solve extreme sizes by making the command strip multi-row.
- Long workspace, collection, request, and environment names must ellipsize without pushing action buttons off-screen.

### Activity-rail consolidation map

The activity rail should be removed only after every route below is proven reachable through its new home. This is a navigation simplification, not feature removal.

| Current activity-rail item | New primary home | Secondary/native route |
|---|---|---|
| Request | Active request tab and `Collection / Request` context | Command palette and native `Request` menu |
| Collection | Clickable collection segment in the context breadcrumb | More > Collection Settings and native `Collection` menu |
| Environments | Environment context button | More > Manage Environments and native `Environment` menu |
| Import | New menu and More > Import | Native `File > Import…` |
| Network | More > Tools > Network Log | Native `View > Network Log` |
| Cookies | Direct Cookie Jar button | Native `Request > Cookie Jar…` |
| Dev Tools | More > Tools > Dev Tools | Native `View > Toggle Developer Tools` |
| Capabilities | More > Capabilities | Native `Help > Capabilities` |
| Runner | Direct Run button | More > Runner and native `Collection > Open Runner` |
| Settings | More > Preferences | `Cmd+,` and the existing native Preferences route |

After consolidation, the app shell becomes collection sidebar plus main workbench instead of activity rail plus collection sidebar plus main workbench. The sidebar visibility button remains in the command strip. The LiteAPI brand stays in the collection sidebar header; it does not need a second persistent rail.

## Codebase analysis and file-level changes

### Frontend command surface

`frontend/src/App.svelte`

- Remains the state/action integration owner.
- Replace only the current `<header class="topbar"><div class="toolbar">...</div>` command row with the new component; retain the current tab strip and application behavior.
- Pass derived display state and callbacks into the component. Do not duplicate Wails calls inside the presentation component.
- Replace ad hoc action calls with one frontend `runWorkbenchCommand(id)` dispatcher used by the command bar, overflow menu, command palette, keyboard shortcuts, and native-menu event handler.
- Correct `command-palette` to call `openCommandPalette()`; preserve `Cmd+K` exclusively for workspace search.
- Keep runner-cancellation and recovery state live and truthfully named.
- Remove the `.activity-bar` markup only after the new command routes pass a reachability audit. Preserve each `activeView` surface and route to it through the mapping above.

`frontend/src/lib/workbench/WorkspaceCommandBar.svelte` (new)

- Owns command-strip layout and responsive presentation.
- Receives values, availability flags, option lists, and callbacks as props.
- Contains no persistence, Wails imports, protocol state mutation, or workspace loading logic.
- Emits no generic stringly typed DOM events; use typed callbacks or one typed command ID.

`frontend/src/lib/workbench/CommandOverflowMenu.svelte` (new)

- Owns disclosure state, grouped item rendering, focus movement, outside dismissal, and Escape behavior.
- Accepts a typed item model with ID, label, shortcut, disabled reason, and group.
- Exposes stable test IDs and accessible names.

`frontend/src/lib/workbench/workbenchCommands.ts` (new)

- Defines the frontend command-ID union and display metadata shared by the command bar, overflow menu, and command palette.
- Does not own stateful callbacks; `App.svelte` binds command IDs to current state/actions.
- Keeps search (`workspace-search`) and command palette (`command-palette`) as distinct IDs.

`frontend/src/style.css`

- Replace `.topbar .toolbar` wrapping rules with a non-wrapping `.workspace-command-bar` grid/flex layout.
- Add explicit leading, context, status, and trailing clusters with `min-width: 0` at every truncation boundary.
- Add original SVG-button, badge, breadcrumb, popover, focus, high-contrast, reduced-motion, and responsive styles.
- Remove the current rule that simply hides command, notification, and Git controls below 680 px; replace it with priority-based label collapse.
- Change `.app-shell` from the current three-column activity-rail/sidebar/workbench grid to a two-column sidebar/workbench grid after route parity is proven. Delete compact horizontal-activity-bar rules that become obsolete; retain the compact overlay sidebar behavior.
- Preserve existing theme tokens and avoid hard-coded competitor colors.

### Native menu

`native_menu.go`

- Keep standard app, edit, and window roles and the existing LiteAPI domain menus.
- Keep current command IDs stable unless a migration is unavoidable.
- Add `Reset Layout` to `View` only if the implementation exposes a real safe-default reset for sidebar width, response split, response orientation, and Dev Tools drawer geometry.
- Add existing destinations where they make semantic sense rather than creating another crowded top-level menu: Network Log and Dev Tools under `View`, Cookie Jar under `Request`, Runner under `Collection`, and Capabilities/Keyboard Shortcuts under `Help`.
- Do not add native items that have no functioning frontend route.

`native_menu_test.go`

- Update the stable-command inventory only for intentionally added commands.
- Assert accelerators remain unique and conventional.
- Assert `View` contains sidebar, response orientation, Dev Tools, and optional Reset Layout commands.
- Retain standard-role and pre-start safety tests.

`main.go`

- No expected change. Preserve the standard framed Wails window and current native menu attachment.

### Documentation and QA

`docs/ui-redesign-milestone-ledger.md`

- Add a focused command-bar milestone and record the exact package hash used for native acceptance.

`docs/qa/` (new focused report/evidence directory)

- Record wide/medium/compact, light/dark, keyboard, accessibility, native menu, multi-window, and recovery/notification states.

## Rejected alternatives

### Custom frameless title bar

Rejected. It would make the WebView responsible for traffic lights, drag regions, fullscreen transitions, double-click zoom, safe areas, and accessibility. The visual gain is small because the desired Yaak behavior is the command strip, not ownership of the macOS chrome.

### Copy Yaak's minimal native menu

Rejected. LiteAPI already has useful, accepted domain menus and keyboard routes. Removing them would reduce discoverability and regress macOS conventions without improving the in-window bar.

### Keep every current control visible

Rejected. The current toolbar demonstrates the failure mode: wrapping, scrolling, and breakpoint-based disappearance. Progressive disclosure is the central design improvement.

### Move everything into the native menu

Rejected. Workspace and environment state, alerts, layout, and active context need to remain visible inside the working window. Native menus are a secondary route, not the entire interaction model.

### Rewrite all shell state during the visual change

Rejected. Existing state, persistence, multi-window ownership, and protocol behavior already passed extensive native QA. The first implementation should change composition and routing only.

## Implementation sequence

1. **Protect the baseline.** Record `git status`, current package hash, and existing generated bindings. Treat every current modification and untracked file as user/prior-agent work. Give `App.svelte`, `style.css`, and `native_menu.go` one integration owner each; do not parallel-edit the two frontend monolith surfaces.
2. **Add a behavior-preserving component seam.** Extract the current toolbar into `WorkspaceCommandBar.svelte` with the same controls and callbacks. Run frontend check/build and compare packaged AX names before changing layout.
3. **Centralize command routing.** Add the typed frontend command registry and `runWorkbenchCommand`. Route palette, keyboard, toolbar, overflow, and native events through it. Fix the native command-palette/search mismatch and verify each command's enabled state.
4. **Implement the stable command strip.** Apply the selected cluster layout, original icons, truncation rules, explicit environment scopes, and the unchanged tab row.
5. **Implement progressive disclosure.** Add New and More menus, move secondary destinations, preserve visible alerts/recovery, and complete keyboard/focus behavior.
6. **Prove route parity and remove the activity rail.** Exercise every row in the consolidation map, then switch the app shell to the two-column layout. Do not remove the rail first and discover unreachable features later.
7. **Implement responsive priority rules.** Validate long labels, zero/nonzero badges, connected/local Git state, running/cancelling states, one/many tabs, and sidebar expanded/collapsed at every target size.
8. **Align native menus.** Keep existing domain menus, add only working destinations in semantically correct submenus, and update focused Go tests.
9. **Package and independently validate.** Build one production `LiteAPI.app`, hash it, launch it against isolated temporary data, and perform the full native matrix below. Repair failures, rebuild, and restart acceptance against the new hash.

## Test and validation plan

### Automated gates

- `git diff --check`
- `cd frontend && npm run check`
- `cd frontend && npm run build`
- `env GOCACHE=/tmp/liteapi-menubar-gocache GOMODCACHE=/tmp/liteapi-menubar-gomodcache go test ./...`
- `env GOCACHE=/tmp/liteapi-menubar-gocache GOMODCACHE=/tmp/liteapi-menubar-gomodcache go test -race ./...`
- `env GOCACHE=/tmp/liteapi-menubar-gocache GOMODCACHE=/tmp/liteapi-menubar-gomodcache go vet ./...`
- Pinned Wails production build and executable SHA-256 recording.
- Generated-binding comparison; no binding churn is expected for this frontend/native-menu slice.

### Focused behavior matrix

- Sidebar control: click and `Cmd+\` both collapse/restore the same sidebar.
- New menu: mouse and keyboard open; each item routes to its existing flow; Escape and outside click close it.
- Workspace: current name, picker/selector, second-window launch, and per-window state remain correct.
- Environment: Global and collection scopes remain distinguishable; changes update the existing request context.
- Workspace search: `Cmd+K` opens search and never the command palette.
- Command palette: `Cmd+Shift+P`, toolbar button, native menu, filtering, Arrow keys, Enter, Escape, and focus restoration all open the same palette.
- Alerts: notification badge at 0 and nonzero; recovery absent at 0 and visible/actionable when nonzero.
- Git/local: accessible state and collection-settings route in both states.
- Layout: direct button, `Cmd+J`, and native View item change the same persisted response orientation.
- More menu: full keyboard traversal, disabled states, shortcuts, close-on-action, Escape, and click-outside.
- Activity-rail parity: Request, Collection, Environments, Import, Network, Cookies, Dev Tools, Capabilities, Runner, and Settings are each reachable from the documented new primary and secondary routes before the rail is removed.
- Runner: running and cancelling state does not cause wrapping or cover environment/actions.
- Tabs: zero, one, and many tabs remain a distinct scrollable second row.
- Native File/View/Request/Collection/Environment/Git commands still dispatch to the active LiteAPI window.

### Visual and accessibility matrix

- Light and dark themes.
- 1225x768 wide, 1024x768 default, and 800x600 compact geometry.
- Short and deliberately long workspace, collection, request, and environment names.
- Sidebar expanded, collapsed, and compact overlay.
- No toolbar wrapping, clipped primary action, page-level horizontal scroll, popover clipping, or overlap with the tab strip.
- 200% zoom and increased system text contrast where available.
- VoiceOver/AX names, roles, expanded/selected/disabled states, focus order, focus-visible treatment, and no background focus while a menu is active.
- Reduced-motion mode has no essential animation dependency.

### Native acceptance boundary

Browser/Vite evidence is useful during implementation but is not final acceptance. The final result must be the packaged `build/bin/LiteAPI.app`, launched with process-attributed isolated data. If Computer Use cannot resize the window directly, seed isolated window-session geometry or use the existing native session mechanism, then verify the resulting size and screenshot without touching the user's normal data.

## Risks and mitigations

- **Dirty monolith overlap:** `App.svelte` and `style.css` already contain extensive uncommitted work. Use one owner, small patches, and diff inspection after every phase.
- **Hidden-command regression:** moving controls can make important state hard to find. Keep recovery visible when nonzero, retain shortcuts/native menus, and validate discoverability from a clean launch.
- **Command drift:** native menu, keyboard, palette, and toolbar can route differently. Centralize frontend dispatch and test each entry path against one behavior.
- **Popover accessibility:** custom menus can leak focus or fail Escape behavior. Make keyboard/focus acceptance a release gate, not a polish item.
- **Responsive state explosion:** long labels, runner state, alerts, and two environment scopes can collide. Use explicit priority rules and a fixed target matrix rather than ad hoc hiding.
- **Framed-window temptation:** visual iteration may suggest custom title-bar integration. Keep the framed-window non-goal unless a separate macOS platform RFC proves the interaction and accessibility cost is justified.

## Rollout and rollback

- Deliver as one frontend/native-menu slice behind no data migration.
- Keep backend state and persistence schemas unchanged.
- If native QA finds a severe usability regression, rollback is limited to the new components, command routing seam, styles, and optional native View command; no user data conversion is required.
- Do not accept a package until the exact final hash passes the focused matrix and an independent reviewer confirms no P0/P1 issue.

## Done criteria

The slice is complete when a clean packaged LiteAPI launch visibly has a single-line Yaak-inspired command strip, no redundant activity rail, full reachability for every relocated destination, the preserved tab/workbench hierarchy, discoverable primary context and alerts, a fully keyboard-operable secondary menu, distinct search and command-palette behavior, passing automated gates, and independent native light/dark and wide/default/compact validation without state or multi-window regressions.
