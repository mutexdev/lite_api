# LiteAPI / Yaak / Bruno Feature Comparison Ledger

Root review: **FINAL ACCEPT — 2026-07-22**

This is the pre-implementation decision ledger for the current redesign goal. It records the exact reference revisions, distinguishes source evidence from hands-on evidence, and prevents an attractive mock-up from being mistaken for product parity. “Automated coverage” describes coverage found in the current worktree; it is not a fresh release-gate result unless the Evidence column says so.

## Reference baseline

| Reference | Exact version/revision | Evidence used |
|---|---|---|
| Yaak source | `195f89337f64f023e5e02074b164cb50b7ab76df` (2026-07-21) | Repository tree and pinned files: import dialog, Git dropdown, Git crate, request/auth/environment components |
| Yaak installed app | Bundle `app.yaak.desktop`; bundle version reports `0.0.0` | Hands-on macOS accessibility inspection of workbench, Main Menu, Import Data dialog, request/response layout |
| Bruno source | `bf40778b2d8a87ee810658699e9cdc849b11a3e0` (2026-07-21) | Repository tree and pinned files: FileTab, BulkImportCollectionLocation, clone/import/diff components |
| Bruno release | `v3.2.0` | Official GitHub release page; no local Bruno app was installed |
| Postman installed app | `12.20.1` | Hands-on macOS inspection used only as a secondary workflow reference |
| LiteAPI | `fe54084ec80e91bb8dbb23b7059968a5ec628696` plus the preserved dirty worktree; final accepted package hash `7f4da5836a6a5610d8306900382c194d424ed6838d68d839a97d31b698d5ad22` | Source/tests, packaged native app, independent functional and native QA |

Source keys: **YI** Yaak installed app; **YS** pinned Yaak source; **YD** official Yaak docs; **BS** pinned Bruno source; **BR** official Bruno release; **PI** Postman installed app; **LS** LiteAPI source/tests; **LN** LiteAPI native QA.

Status keys: **Pass** present at current baseline; **Partial** incomplete or lacking fresh proof; **Gap** absent; **Fail** present but acceptance-breaking; **Planned** selected for the current bounded implementation.

## Product shell, navigation, and macOS integration

| Feature / workflow | Yaak | Bruno | LiteAPI | Sources | Chosen standard | Status | Owner | Automated coverage | Native QA | Evidence | Risk |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Compact three-pane workbench | Sidebar + editor + response | Sidebar + editor + response | Implemented | YI, YS, BS, LS | Preserve LiteAPI’s denser native identity | Pass | Root | Svelte checks | Prior M8 only | Source + historical screenshots | Medium: current visual proof stale |
| Collapsible sidebar | Native control | Present | Present | YI, YS, LS | Keep persistent collapse affordance | Pass | Root | Component logic | Hands-on visible | Current package | Low |
| Collection/environment/request hierarchy | Resource tree | Collection tree | Tree present | YI, BS, LS | Retain local-first tree with keyboard access | Pass | Root | Go/Svelte tests | Visible | Current package | Medium |
| Command palette/command bar | Command surface | Search/commands | Implemented M9 | YI, LS | Keep action-led command bar | Pass | Root | Command tests | Prior M9 | Report + code | Medium |
| Keyboard-first navigation | Shortcuts menu | Shortcuts | Many shortcuts | YI, BS, LS | Preserve discoverable shortcuts | Partial | Root | Unit checks | Not fully retested | Source | Medium |
| Native macOS app menu | About, Settings, standard services | Native convention | Conventional application menu | YI, LS, LN | About, Settings `⌘,`, Services, Hide, Hide Others, Show All, Quit | **Pass** | Implementation / Root reviewed | Structural tests + package build | Passed exact signed package | AX order and two clean Settings-click runs at `7b6e2d…` | Low: repeat in final 3-run gate |
| Settings location | App menu | App menu | App menu only; absent from File/Help | YI, BS, LN | App menu only; remove from File | **Pass** | Implementation / native QA | Structural test | Click + `⌘,` each focus one Preferences view | Exact-hash screenshots and liveness checks | Low |
| About action | App menu | App menu | Native About panel | YI, BS, LS, LN | Native About panel | **Pass** | Implementation / native QA | Wails `Mac.About` build contract | Native alert opened/dismissed | AX alert + responsive post-dismiss state | Low |
| Services submenu | App menu | Native convention | Real populated NSApp services menu | LN; Wails 2.12 source | Standard NSApp services menu | **Pass** | Implementation / native QA | Build-tagged integration + structural tests | Populated submenu opened | Activity Monitor/Leaks/File Activity/System Trace/Time Profile visible | Low |
| File/Open Collection | Main/import flow | Open collection | Routes to Import view | YI, BS, LS | File picker first for existing collection | **Partial → Planned** | Implementation | Backend/picker tests | Current menu routes only | Source + native QA | High |
| Multiple windows | Supported | Desktop app | New/Open Workspace window actions | YI, BS, LS | Preserve native windows | Partial | Root | Backend tests | Not fresh | Source | Medium |
| Window menu | Standard | Standard | Standard Wails role | YI, LS, LN | Minimize/Zoom/Full Screen + app windows | Pass | Root | Menu test | Verified | Packaged app | Low |
| Help/resources | Docs/changelog/feedback | Docs/issues | Help menu | YI, BS, LS | Keep concise external help | Partial | Root | Menu test | Visible | Source | Low |
| Theme/system appearance | Theme support | Theme support | Light/dark/system | YS, BS, LS | System default with explicit overrides | Pass | Root | Preferences tests | Historical | Source | Low |
| Accessibility labels/focus | Semantic controls | Mixed | Substantial labels | YS, BS, LS | WCAG-oriented keyboard and AX semantics | Partial | QA | Svelte checks | Spot checked | Source/AX | Medium |

## Import, open, export, and durable local storage

| Feature / workflow | Yaak | Bruno | LiteAPI | Sources | Chosen standard | Status | Owner | Automated coverage | Native QA | Evidence | Risk |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Primary import entry | Select File | File drop/select | Manual path + textarea | YI, YS, BS, LS, LN | File-picker-first | **Fail → Planned** | Implementation | Add picker/service tests | Reproduced P1 | Yaak dialog + Bruno FileTab + LiteAPI | High |
| Multi-file import | Single file | Multiple files | Absent | YS, BS, LS | Multi-select with per-file preview | **Gap → Planned** | Implementation | Parser/import-plan tests | Pending | Bruno FileTab | High |
| Folder import | No | Collection folder conventions | Manual path only | YS, BS, LS | Folder picker beside file picker | **Gap → Planned** | Implementation | Picker/directory tests | Pending | Source | High |
| Drag and drop | No primary evidence | Multi-file drag/drop | Absent | BS, LS | Native/webview drop zone | **Gap → Planned** | Implementation | Svelte event tests if available | Pending | Bruno FileTab | Medium |
| Automatic format detection | Parser determines supported file | Detects OpenAPI/WSDL/Postman/Insomnia/OpenCollection/Bruno | Manual kind required | YS, BS, LS | Detect first; manual override available | **Partial → Planned** | Implementation | Table-driven detector tests | Pending | Pinned import sources | High |
| Manual format override | No visible override | Detection-led | Existing kind dropdown | YI, BS, LS | Keep as secondary advanced control | Partial | Implementation | Parser tests | Visible | Current package | Low |
| URL import | Not primary dialog | GitHub/URL tabs | No import URL | YS, BS, LS | Secondary URL tab/action | **Gap → Planned** | Implementation | Fetch/parser tests | Pending | Source | Medium: network/security |
| Paste/raw import | No primary dialog | Code/paste paths | Payload textarea | YS, BS, LS | Advanced paste panel, not primary | Pass/reshape | Implementation | Parser tests | Visible | Current package | Low |
| Postman collection v2/v2.1 | Supported | Supported | Supported parser | YI, YS, BS, LS | Preserve | Pass | Root | Import fixtures | Manual input only | Tests | Low |
| Insomnia v4+ | Supported | Supported | Supported parser | YI, YS, BS, LS | Preserve | Pass | Root | Import fixtures | Manual input only | Tests | Low |
| OpenAPI 3.0/3.1 | Supported | Supported | Supported + sync options | YI, YS, BS, LS | Preserve and expose grouping/sync after selection | Pass | Root | Import/sync tests | Manual input only | Tests | Medium |
| Swagger 2.0 | Supported | Supported through OpenAPI importer | Compatibility needs confirmation | YI, YS, BS, LS | Detect/convert or reject with specific explanation | Partial | Implementation | Add fixture | Pending | Source | Medium |
| Bruno JSON/BRU | Native | Native | Bruno JSON + BRU parser | BS, LS | Preserve | Pass | Root | Import fixtures | Manual input only | Tests | Low |
| OpenCollection | Not listed | Supported | Absent | BS, LS | Add only after priority import shell | Gap/backlog | Root | None | None | Bruno source | Low |
| WSDL | Not listed | Supported | Absent | BS, LS | Explicit unsupported explanation in this milestone | Gap/deferred | Root | None | None | Bruno source | Medium |
| ZIP import | No | Supported with no-mix rules | Absent | BS, LS | Defer until safe transactional multi-file path exists | Gap/deferred | Root | None | None | Bruno FileTab | Medium |
| Import preview | Minimal file confirmation | Selectable collection/environment preview | Direct mutation | YI, BS, LS | Preview detected items before mutation | **Gap → Planned** | Implementation | Import-plan tests | Pending | Bulk import source | High |
| Per-item selection | No visible | Collections/environments selectable | Absent | BS, LS | Checked-by-default item selection | **Gap → Planned** | Implementation | Plan selection tests | Pending | Bruno bulk import | Medium |
| Conflict handling/rename | No visible | Rename conflicts | Implicit generated name/path | BS, LS | Explicit overwrite/rename/skip per conflict | **Gap → Planned** | Implementation | Conflict matrix tests | Pending | Bruno bulk import | High |
| Per-file errors | Dialog error | Invalid files skipped/reported | Single global error | YS, BS, LS | Row-level errors; valid files remain previewable | **Gap → Planned** | Implementation | Mixed-validity tests | Pending | Bruno FileTab | High |
| Atomic final import | Unclear | Batch orchestration | State can mutate before durable write | YS, BS, LS | Preflight all outputs; commit transactionally or roll back | **Gap → Planned** | Implementation | Failure-injection tests | Pending | LiteAPI code review | Critical |
| Durable filesystem write | Local data | Filesystem collection | Non-OpenAPI import may be in-memory only | YD, BS, LS | Every accepted import materializes authoritative files | **Fail → Planned** | Implementation | Relaunch persistence test | Pending | LiteAPI `ImportCollection` review | Critical |
| Export active collection | Supported | Filesystem-native | Present | YD, BS, LS | Preserve JSON/export options | Pass | Root | Backend tests | Visible | Current package | Low |
| Import after relaunch | Local workspace | Filesystem-native | Not proven for generic imports | YD, BS, LS | Relaunch must restore imported collection | **Partial → Planned** | QA | Add integration test | Pending | Acceptance gap | High |
| Secret-safe import/logging | Local-first | Rejects credential-bearing remotes | Sanitizers present | BS, LS | Never log imported secret values or URL tokens | Partial | Root/QA | Sanitizer tests | Pending | Source review | Critical |

## Request authoring, execution, scripting, and protocols

| Feature / workflow | Yaak | Bruno | LiteAPI | Sources | Chosen standard | Status | Owner | Automated coverage | Native QA | Evidence | Risk |
|---|---|---|---|---|---|---|---|---|---|---|---|
| HTTP methods + URL editor | Full | Full | Full | YI, YS, BS, LS | Preserve dense method/URL/send row | Pass | Root | Request tests | Visible | Current package | Low |
| Headers/query/path parameters | Full | Full | Full | YS, BS, LS | Preserve tabular editors and enable toggles | Pass | Root | Backend tests | Historical | Source | Medium |
| Body modes | JSON/text/form/multipart/file | Full | Raw/JSON/form/multipart/file | YS, BS, LS | Preserve | Pass | Root | Request tests | Historical | Source | Medium |
| Authentication | Basic/Bearer/OAuth 2/AWS etc. | Broad auth set | Basic/Bearer/API key/OAuth2/AWS | YD, YS, BS, LS | Preserve and expose generated auth clearly | Pass | Root | Auth tests | Historical | Source | High |
| Cookie jar | Explicit | Cookie support | Cookie jar | YI, BS, LS | Preserve workspace-scoped jar controls | Pass | Root | Cookie tests | Visible | Current package | Medium |
| Environment variables | Hierarchical environments | Environments | Global/workspace/collection env | YI, YD, BS, LS | Preserve scope visibility and active selection | Pass | Root | Environment tests | Visible | Current package | High |
| Template resolution | Variables/functions | Variables/functions | Resolver + diagnostics | YD, BS, LS | Never silently send unresolved values | Pass | Root | Resolver tests | Historical | Source | High |
| Request chaining | Response templating/scripting | Script variables | Extraction/dependencies | YD, BS, LS | Preserve DAG and cycle diagnostics | Pass | Root | Chain tests | Historical | Source | High |
| Pre/post request scripts | JavaScript/plugin model | JavaScript | Restricted runtime | YS, BS, LS | Keep capability-restricted runtime | Pass | Root | Script tests | Historical | Source | Critical |
| Secret redaction | Sensitive values | Secret variables | Sanitized logs/history | BS, LS | Redact at persistence, logs, exports, UI diagnostics | Pass/verify | QA | Sanitizer tests | Pending fresh | Source | Critical |
| HTTP execution/cancel/timeout | Full | Full | Full | YS, BS, LS | Preserve | Pass | Root | Executor tests | Historical | Source | High |
| Redirect/TLS/proxy controls | Full | Full | Implemented | YS, BS, LS | Preserve per-request/collection overrides | Pass | Root | Transport tests | Historical | Source | High |
| Client certificate/custom CA | Full | Full | Pickers + persistence | YS, BS, LS | Preserve native pickers | Pass | Root | Picker/backend tests | Historical | Source | High |
| WebSocket | Full | Full | Implemented | YS, BS, LS | Preserve sessions/messages/history | Pass | Root | Protocol tests | Historical | Source | High |
| Server-sent events | Full | Present | Implemented | YS, BS, LS | Preserve streaming transcript | Pass | Root | Protocol tests | Historical | Source | Medium |
| GraphQL | Full | Full | Query/variables/schema support | YS, BS, LS | Preserve schema explorer | Pass | Root | GraphQL tests | Historical | Source | High |
| gRPC | Full | Full | Unary/server streaming/reflection | YS, BS, LS | Preserve; document unsupported streaming modes | Partial | Root | gRPC tests | Historical | Source | High |
| Socket.IO/MQTT | Not core import evidence | Dedicated client types | Absent | PI, BS, LS | Explicitly deferred; do not imply support | Gap/deferred | Root | None | None | Source | Medium |
| Request tabs | Present | Present | Present | YI, BS, LS | Preserve dirty/close/reopen behavior | Pass | Root | Session tests | Visible | Current package | Medium |
| Response status/time/size | Present | Present | Present | YI, BS, LS | Preserve compact summary | Pass | Root | Formatter tests | Visible | Current package | Low |
| Response body/headers/cookies/timeline | Present | Present | Present | YI, BS, LS | Preserve discoverable tabs | Pass | Root | Parser tests | Visible | Current package | Medium |
| Large response handling | Mature | Mature | Streaming/virtualization claims | YS, BS, LS | Prove bounded memory and cancellation | Partial | QA | Tests exist | Not fresh | Source | High |

## Persistence, history, collaboration, Git, and recovery

| Feature / workflow | Yaak | Bruno | LiteAPI | Sources | Chosen standard | Status | Owner | Automated coverage | Native QA | Evidence | Risk |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Local-first authoritative files | Local DB + export/sync | Filesystem first | Collection files + app metadata | YD, BS, LS | Collection directory is authoritative | Partial | Root | Persistence tests | Historical | Source | High |
| Request history | Present | Present | Present | YI, BS, LS | Preserve filtered, redacted history | Pass | Root | History tests | Visible | Current package | High |
| Undo/redo + crash recovery | Desktop behavior | Editor behavior | Journal/recovery modules | YS, BS, LS | Preserve deterministic recovery | Pass/verify | QA | Recovery tests | Pending fresh | Source | Critical |
| Backups | Export/local | Git/filesystem | Backup/restore modules | YD, BS, LS | Preserve explicit backup/restore | Pass/verify | QA | Backup tests | Pending fresh | Source | High |
| Git setup/init | Built-in | External Git/file workflow | Clone/remote only | YS, BS, LS | Built-in init for local collection | **Gap → Planned** | Implementation | Add temp-repo tests | Pending | Yaak Git source | High |
| Git status | Built-in | External Git | Absent | YS, BS, LS | Porcelain status with normalized paths | **Gap → Planned** | Implementation | Temp-repo integration | Pending | Yaak Git crate | High |
| Git diff | Built-in | Visual diff components | Absent | YS, BS, LS | Safe text diff; binary summary | **Gap → Planned** | Implementation | Temp-repo integration | Pending | Yaak/Bruno source | High |
| Git stage/unstage | Built-in | External Git | Absent | YS, BS, LS | Explicit path selection; no blanket surprises | **Gap → Planned** | Implementation | Temp-repo integration | Pending | Yaak Git crate | High |
| Git commit | Built-in | External Git | Absent | YS, BS, LS | Validated message + author errors | **Gap → Planned** | Implementation | Temp-repo integration | Pending | Yaak Git source | High |
| Git branch list/create/checkout | Built-in | External Git | Absent | YS, BS, LS | Local/remote branch model with dirty guard | **Gap → Planned** | Implementation | Temp-repo integration | Pending | Yaak GitDropdown | High |
| Git fetch | Built-in | External Git | Clone path only | YS, BS, LS | Credential-safe fetch with progress | **Gap → Planned** | Implementation | Local-remote integration | Pending | Yaak Git crate | High |
| Git pull | Built-in | External Git | Absent | YS, BS, LS | Fetch + merge/rebase policy surfaced | **Gap → Planned** | Implementation | Local-remote integration | Pending | Yaak Git source | Critical |
| Git push | Built-in | External Git | Absent | YS, BS, LS | Upstream-aware, credential-safe push | **Gap → Planned** | Implementation | Local-remote integration | Pending | Yaak Git source | Critical |
| Git conflict handling | Conflict-aware operations | External Git tools | Absent | YS, BS, LS | Detect conflicts; never overwrite; expose files/actions | **Gap → Planned** | Implementation | Conflict fixture | Pending | Yaak source + objective | Critical |
| Git destructive actions | Confirmed reset/delete | External Git | No full workflow | YS, BS, LS | Require explicit confirmation and exact scope | Planned | Implementation | Guard tests | Pending | Yaak GitDropdown | Critical |
| Git credential safety | Credential helpers | Reject embedded tokens | Remote sanitizer exists | YS, BS, LS | Reject/redact URL credentials; no command logging | Partial | Root/QA | Sanitizer tests | Pending | Pinned sources | Critical |
| Filesystem synchronization/watch | Built-in sync | Filesystem-native | Watcher/materialization modules | YS, BS, LS | Preserve external edit detection | Pass/verify | QA | Watcher tests | Pending fresh | Source | High |
| Multi-window state consistency | Workspaces | Collections | Event-driven state | YS, BS, LS | Reload or broadcast durable changes | Partial | Root | Some tests | Not fresh | Source | High |

## Search, runners, documentation, performance, and release quality

| Feature / workflow | Yaak | Bruno | LiteAPI | Sources | Chosen standard | Status | Owner | Automated coverage | Native QA | Evidence | Risk |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Search/filter collections | Search | Search | Search/filter | YI, BS, LS | Preserve keyboard-searchable hierarchy | Pass | Root | Search tests | Visible | Current package | Low |
| Global command search | Command palette | Command search | Command bar | YI, BS, LS | Preserve recent/fuzzy actions | Pass | Root | Command tests | Prior M9 | Report | Medium |
| Collection runner | Create Run Button | Runner | Collection runner | YI, BS, LS | Preserve selection/order/env/assertions | Pass/verify | QA | Runner tests | Pending fresh | Source | High |
| Test assertions | Scripts/assertions | Tests | Assertions/runner | YS, BS, LS | Clear per-request result and failure reason | Pass/verify | QA | Runner tests | Pending fresh | Source | High |
| OpenAPI documentation/schema | Import/docs | OpenAPI tools | Schema/explorer | YD, BS, LS | Preserve local docs and ref resolution | Pass/verify | QA | OpenAPI tests | Pending fresh | Source | High |
| Import issue reporting | Dialog errors | Copyable import issues | Global notification | YS, BS, LS | Persistent, copyable per-item report | **Gap → Planned** | Implementation | Error-report tests | Pending | Bruno bulk import | Medium |
| Empty-state onboarding | Setup Git/Add Resource | New/Open/Import | Quick actions | YI, BS, LS | Local-first three-action start | Pass | Root | Svelte check | Visible | Current package | Low |
| Offline operation | Local-first | Local-first | Local-first | YD, BS, LS | Core author/edit/send/history works offline | Pass/verify | QA | Backend tests | Pending fresh | Source | High |
| Launch/build packaging | Desktop app | Desktop app | Wails `.app` | LS, LN | `build/bin/LiteAPI.app` is acceptance artifact | Partial | Root | Build scripts | One current launch | Package attached | High |
| Three clean consecutive native runs | N/A | N/A | Historical M8 only | LS | Clean state, launch, scripted workflow, persistence, no diagnostics ×3 | Gap/current | Root/QA | Full gates required | Pending | Goal requirement | Critical |
| Full Go tests | N/A | N/A | Baseline pass reported | LS | `go test ./...` fresh at release hash | Partial | Root | Existing suite | N/A | Independent report | High |
| Race detector | N/A | N/A | Not yet fresh | LS | `go test -race ./...` | Gap/current | Root | Existing suite | N/A | Goal requirement | High |
| Go vet | N/A | N/A | Baseline pass reported | LS | `go vet ./...` fresh | Partial | Root | Existing suite | N/A | Independent report | Medium |
| Svelte check/build | N/A | N/A | Baseline pass reported | LS | `npm run check` and `npm run build` fresh | Partial | Root | Existing scripts | N/A | Independent report | Medium |
| Formatting/diff hygiene | N/A | N/A | Baseline diff-check pass | LS | `gofmt`, frontend formatter, `git diff --check` | Partial | Root | Tooling | N/A | Independent report | Medium |
| Performance evidence | Mature apps | Mature app | Historical claims | YS, BS, LS | Measure launch/large response/large collection where risk changes | Partial | QA | Some benchmarks | Pending | Acceptance gap | Medium |
| Release notes/known limits | Docs/changelog | Releases | Reports/PARITY | YD, BR, LS | Publish verified facts, explicit deferrals, no parity overclaim | Planned | Root | Ledger audit | Final | This ledger | High |

## Detailed feature-audit inventory

These rows close the objective’s minimum audit list beyond the higher-risk comparison tables above. “Partial” commonly means the source and automated surface exists but needs fresh final-package proof.

| Feature / workflow | Yaak | Bruno | LiteAPI | Sources | Chosen standard | Status | Owner | Automated coverage | Native QA | Evidence | Risk |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Persisted panel sizes/window state | Persisted | Persisted | Geometry/runtime persistence modules | YS, BS, LS | Native window + per-workspace layout restoration | Partial | QA | Geometry/state tests | Pending final | Source/tests | Medium |
| Resizable request/response/navigation regions | Resizable | Resizable | Splitters and orientation controls | YI, BS, LS | Preserve bounded, keyboard-reachable panes | Partial | QA | Svelte checks | Historical | Source | Medium |
| Customizable shortcuts | Shortcut settings | Shortcut settings | Keybinding preferences | YI, BS, LS | Preserve conflict-aware customization | Partial | QA | Preference tests | Pending final | Source | Medium |
| Interface density, editor font, zoom | Compact controls | Configurable | Preferences implement font/zoom; compact default | YI, BS, LS | Compact by default, user-adjustable | Partial | QA | Preference tests | Pending final | Source | Low |
| Reduced motion | System-aware | Theme/settings behavior | CSS/system handling needs fresh proof | YS, BS, LS | Honor `prefers-reduced-motion` | Partial | QA | Svelte/build only | Pending | Source review | Medium |
| Notifications/loading/error/empty states | Toasts/dialogs | Toasts/dialogs | Structured notifications and busy/error/empty UI | YI, BS, LS | Actionable, non-secret, recoverable states | Partial | QA | Backend/UI tests | Pending final | Source | High |
| Relaunch restoration/multi-window isolation | Workspace windows | Collection windows | Runtime/session/lock/recovery modules | YS, BS, LS | Per-window ownership with shared durable state | Partial | QA | Isolation/recovery tests | Pending final | Source | Critical |
| Native context menus | Present | Present | Sidebar/editor context actions | YS, BS, LS | Native-feeling action order, safe destructive confirms | Partial | QA | UI tests | Pending final | Source | Medium |
| XML/text/JSON request bodies | Present | Present | Raw body types | YS, BS, LS | Preserve content type and editor mode | Pass/verify | QA | HTTP tests | Pending final | Source | Medium |
| Form URL encoded/multipart/files/binary | Present | Present | Form, multipart, multi-file body; binary support bounded | YS, BS, LS | Bruno-compatible tables and file safety | Partial | QA | Extensive body tests | Historical | Feature manifest | High |
| URL encoding/redirects/timeouts | Present | Present | Per-request settings | YS, BS, LS | Explicit toggles and timeline evidence | Pass/verify | QA | Transport tests | Pending final | Feature manifest | High |
| Request documentation/examples | Present | Present | Docs + response examples | YS, BS, LS | Preserve editable docs and example snapshots | Pass/verify | QA | Round-trip tests | Pending final | Feature manifest | Medium |
| Generate cURL/fetch/grpcurl | cURL | cURL/fetch | cURL/fetch implemented; grpcurl needs confirmation | YS, BS, LS | Generate secret-aware snippets | Partial | QA | Generator tests | Pending final | Source/tests | Medium |
| Duplicate/move/rename/save/delete requests | Present | Present | CRUD, folders, tabs, filesystem save | YS, BS, LS | Preserve hierarchy and explicit deletes | Pass/verify | QA | CRUD/round-trip tests | Pending final | Feature manifest | High |
| Inherited authentication | Present | Present | Collection/folder/request inheritance | YS, BS, LS | Visible inheritance and override | Pass/verify | QA | Auth inheritance tests | Pending final | Source/tests | Critical |
| OAuth 1.0 | Present | Present | Implemented editor/execution | YS, BS, LS | Preserve all signature placements | Pass/verify | QA | OAuth1 tests | Pending final | Source/tests | Critical |
| JWT | Plugin/helper | Present | Auth/helper support needs explicit final proof | YS, BS, LS | Do not imply dedicated mode without proof | Partial | QA | Helper/auth tests | Pending | Source | High |
| NTLM | Supported | Supported | Challenge negotiation implemented | YS, BS, LS | Preserve platform-safe negotiation | Pass/verify | QA | NTLM fixtures | Pending final | Feature manifest | Critical |
| Digest/WSSE | Supported where protocol fits | Supported | Digest and WSSE paths present | YS, BS, LS | Preserve explicit per-request modes | Pass/verify | QA | Auth/protocol tests | Pending final | Source/tests | Critical |
| OS keychain/encrypted secrets | OS facilities | Safe storage/encrypted env | Encrypted stores + legacy hydration; OS integration varies | YS, BS, LS | Encrypt at rest; never silently downgrade | Partial | QA | Secret-store tests | Pending final | Feature manifest | Critical |
| Base/sub-environments and precedence | Hierarchical | Hierarchical | Global/workspace/collection/folder/request precedence | YD, BS, LS | Inspector must show winning scope | Pass/verify | QA | Resolver tests | Pending final | Feature manifest | Critical |
| Script helper/library surface | Plugins/functions | Broad JS helpers | Safe/developer runtimes with Bruno-compatible helpers | YS, BS, LS | Capability-gated useful union | Partial | QA | Extensive runtime tests | Pending final | Feature manifest | Critical |
| Script console/errors/stacks/timeouts/cancel | Present | Present | Console/timeline/error/timeout paths | YS, BS, LS | Source-aware bounded diagnostics | Partial | QA | Script/runtime tests | Pending final | Source/tests | Critical |
| Runner data files | Runner | Runner/CLI | Runner exists; data-file parity needs proof | YS, BS, LS | Local data file, selection, env and report | Partial | QA | Runner tests | Pending final | Source | High |
| Pretty/raw/preview/binary response | Present | Present | Pretty/raw; preview/binary behavior incomplete | YS, BS, LS | Safe rendering with download fallback | Partial | Root/QA | Response tests | Pending final | Source | High |
| JSONPath/XPath filtering | Filtering | Filtering | Search/filter support needs exact parity proof | YS, BS, LS | Add only evidence-backed filters; no false affordance | Partial | QA | Parser tests | Pending | Source | Medium |
| Response search/wrap/copy/download | Present | Present | Search/copy/export paths | YS, BS, LS | Preserve large-response-safe tools | Partial | QA | Export tests | Pending final | Source | Medium |
| Redirect chains/timeline | Timeline | Timeline | Detailed response timeline | YS, BS, LS | Preserve hop and scripted-child provenance | Pass/verify | QA | Timeline tests | Pending final | Feature manifest | High |
| Empty/malformed/truncated/unsupported responses | Defensive | Defensive | Error/render paths | YS, BS, LS | Never crash; offer raw/download | Partial | QA | Fixture tests | Pending final | Source/tests | High |
| Response persistence rules | Configurable/history | History/examples | Saved examples + redacted history | YS, BS, LS | Persist only intentional examples/history metadata | Partial | QA | Persistence tests | Pending final | Source/tests | Critical |
| Reveal in Finder/open terminal | Present | Present | Implemented sidebar actions | YS, BS, LS | Exact path, existence and containment checks | Pass/verify | QA | Path/action tests | Pending final | Feature manifest | High |
| Multiple collections in one repository | Git workspace | Filesystem repository | Scan/select multiple candidates | YS, BS, LS | Explicit candidate selection | Pass/verify | QA | Git scan tests | Pending final | Source/tests | High |
| Swagger import | Supported | Supported | OpenAPI sync currently rejects Swagger | YI, BS, LS | Format-specific conversion or explicit unsupported row | Gap/current | Implementation | Pending | Pending import QA | Source/tests | Medium |
| OpenCollection YAML import | Not primary | Supported | Folder open supported; bundled single-file import missing | BS, LS | Folder first; bundled preview/import when safe | Partial | Implementation | Folder tests | Pending import QA | Source | High |
| Yaak export import | Native Yaak | Not core | Missing | YS, LS | Feasible documented subset or explicit unsupported row | Gap/current | Root | None | Pending import QA | Source | Medium |
| cURL import | Supported | Supported | Generate exists; import missing | YI, BS, LS | Secondary paste/file parser | Gap/current | Root | None | Pending import QA | Source | Medium |
| ZIP/WSDL import | No ZIP/WSDL primary | Supported | Missing | BS, LS | Safe archive extraction; WSDL only with faithful SOAP model | Gap/current | Root | None | Pending import QA | Source | High |
| Export secret scrubbing | Local export | Scrubbed export | Scrubbing implemented | YD, BS, LS | Always scrub unless explicit secure path exists | Pass/verify | QA | Export tests | Pending final | Feature manifest | Critical |
| Import/export round-trip fidelity | Import/export | Filesystem portability | Strong existing parser/export tests; priority workflow incomplete | YS, BS, LS | Manifest-based loss reporting | Partial | QA | Round-trip tests | Pending import QA | Source/tests | High |
| CLI-compatible workflow | CLI/extensions | Bruno CLI | Desktop filesystem + runner; no separate CLI promise | YS, BS, LS | Plain files and Git remain CLI-compatible | Partial/intentionally bounded | Root | Filesystem tests | N/A | Architecture | Medium |
| Account-free portability | Local workspaces | Local-first | No cloud account required | YD, BS, LS | Preserve account-free core | Pass | Root | Architecture tests | Visible | Product behavior | Low |

## Root decisions and implementation gates

1. The first bounded slice is the P1 platform-and-entry contract: correct native macOS application menu plus a file/folder-first import launcher. It must land with structural/unit tests before further UI expansion.
2. Import implementation must use a backend plan/preview/apply boundary. Preview is read-only; final apply is all-or-nothing for selected outputs and must materialize authoritative collection files.
3. Full Git work follows the import contract as a separate bounded slice, backed by disposable local repositories and local bare remotes. Network credentials are not required for automated coverage.
4. `app.go` and `frontend/src/App.svelte` each have one active owner at a time. Independent QA never edits implementation files and never self-accepts.
5. Every bounded slice returns to Root for diff review, then to the same independent QA role for native or functional retest. P1/P0 findings repeat that loop until clear.
6. Existing M8/M9 reports are historical evidence only. Release acceptance requires fresh full gates and three consecutive clean packaged-native runs at the final package hash.

## Final release delta — 2026-07-22

This release delta supersedes the pre-implementation status/native-QA cells above. The detailed rows remain the audit trail for every discovered feature and the selected standard.

| Release area | Final status | Automated evidence | Packaged-native evidence | Remaining risk |
|---|---|---|---|---|
| Native macOS menus | Pass | Structural menu tests; final build/check gates | Final-hash app menu order, About, Settings, populated Services; File has no Settings | Low |
| File/folder-first import | Pass with documented unsupported formats | Detector/preview/apply, atomicity/rollback, stale-plan, symlink/path, conflict, URL, cURL, folder and persistence tests | Picker, preview, deselection, replace modal, single/mixed file, folder, Unicode/OpenAPI/Bruno/cURL, row errors, materialization and relaunch passed across signed milestone/final candidates | Low; final-hash `NSOpenPanel` mixed-range replay was blocked by CUA focus, with immediately preceding signed-package proof and no later import delta |
| Git workbench | Pass | Real temp repo/bare remote, status/diff/stage/commit/branch/fetch/pull/push/conflict/clone/unavailable/credential tests; frontend branch reconciliation test | Full native repository/remote workflow passed; later branch-target defect reproduced and same-agent retest accepted (`Qa-r3` tracking, main follow, explicit override preservation) | Low; no destructive force/reset/clean surface by design |
| HTTP/GraphQL | Pass | Full Go/race suites including GraphQL JSON-envelope regression | Final runs: XML marker/Unicode, GraphQL success and `FIXTURE_ERROR`, truthful failure recovery | Low |
| WebSocket/SSE/gRPC | Pass | Deterministic fixture tests | Final run: WS 101/echo/disconnect, ordered SSE completion, gRPC reflection/unary metadata/trailers and unavailable-service error | Low |
| Draft and recoverable deletion | Pass | `DiscardRequestDraft`, recovery, and frontend transient/durable routing tests | Final run: transient gRPC delete without file error/recovery entry; durable delete created and restored recovery entry | Low |
| Appearance/responsive/accessibility | Pass | Svelte check/build and preference tests | Light and System relaunch persistence; Dark on preceding signed candidate; named Auth/Vars/Script/Assert/Tests tabs and responsive controls visible in AX | Low |
| Persistence/window ownership | Pass | Workspace registry/session/lock/recovery/isolation tests | Fresh PID/data-dir attribution, same-data relaunch, duplicate-workspace guard, three final clean owner releases | Low |
| Release gates | Pass | Race, vet, Svelte check/build, 7/7 frontend tests, Wails build, diff check, codesign | Three consecutive clean runs on `7f4da583…5ad22` | Existing Vite chunk-size advisory only |

Final decisions and limits:

- Supported imports: Postman v2/v2.1, Insomnia v4+, OpenAPI 3, Bruno JSON, `.bru`, Bruno/OpenCollection folders, and cURL.
- Explicit unsupported rows: Swagger 2, WSDL, ZIP, Yaak export, and bundled single-file OpenCollection. They do not mutate state or create corrupt collections.
- Git intentionally omits destructive force/reset/clean operations.
- Socket.IO/MQTT and a separate Bruno-compatible CLI are outside LiteAPI's current architecture and are not claimed.
- The product remains local-first and account-free; no competitor branding, assets, telemetry, or commercial surfaces were copied.
