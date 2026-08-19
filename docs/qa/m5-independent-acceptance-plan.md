# M5 independent black-box acceptance plan

Source contract: `docs/ui-redesign-milestone-ledger.md` M5 acceptance contract. This plan does not accept implementation by inspection.

## Readiness split

| State | What may be checked |
|---|---|
| Testable now | Existing deterministic response fixture: HTTP/GraphQL-over-HTTP media, 1/5 MiB bounds, HTTP cancellation, duplicate headers, TLS/proxy error surfaces, imports/exports where the packaged UI exposes them. These are regression evidence, not M5 acceptance. |
| Blocked until M5 package | Real second-process native window command; workspace owner/refusal/read-only/recovery; session migration; owner-safe filesystem/Git/import writes; native WebSocket/gRPC streaming and metadata/trailer proof; generated Wails binding surface gate. |

## Fixture and observation kit

- Two empty workspaces `A` and `B`, each with a uniquely named collection/request and marker files. A separate shared-workspace copy drives owner/refusal tests.
- Local deterministic HTTP fixture (`127.0.0.1:18487`) plus its published JSON/XML/text/image/PDF/binary, duplicate-header, comparison, timing, cancellation, and exact-hash payloads.
- Local gRPC fixture (`127.0.0.1:18488`): unary, server-streaming, client/bidi streaming if advertised, binary payload, request metadata, response header/trailer, delayed/cancelled and unavailable methods.
- Local WS fixture: text echo, binary echo, ping/close, delayed stream, forced disconnect. Local GraphQL query/error/subscription-equivalent fixture if exposed.
- Proxy fixture (direct, explicit proxy, unavailable proxy), self-signed/custom-CA HTTPS endpoint, mTLS endpoint with accepted/rejected client certificate.
- A controlled Git repo per workspace; Postman, Bruno, and OpenAPI import samples; export target directories.
- Permitted objective observations: native AX tree/screenshots, app/process identifiers, launched-process exit status, workspace files and timestamps/hashes/permissions, registry/session/recovery artifact names and byte scans for known secret sentinels. Never log real secrets.

## Matrix

| Area | Adversarial procedure | Pass evidence / failure condition |
|---|---|---|
| Two native windows, separate workspaces | From File/Window command open A, then B in New Window. Independently create/save/send in both; close/reopen either. | Two distinct native processes/windows and AX titles/workspace controls; A writes never change B markers/hashes or session; each restores its own tabs/pane/orientation. Any cross-workspace tab, write, response, or close effect fails. |
| Same-workspace ownership + stale recovery | Open A twice. Require refusal or conspicuous read-only state; kill first owner, then reopen second; repeat with delayed old process. | Live duplicate cannot writable-own A. Stale recovery succeeds. Old process cannot heartbeat/release/write over new owner; owner record changes are monotonic and logged without secrets. |
| Menu/session/close routing | With A and B active alternately, invoke File/New Window, Open Workspace in New Window, Save, Send, Close Window, Quit via menu + keyboard. Make dirty tab in one window. | Commands affect only active window/workspace; all menu actions have AX names and keyboard path. M3 Save/Discard/Cancel guard remains per-window; quit never discards another window's draft. |
| Legacy migration + workspace isolation | Seed legacy state fixture, launch M5 package, interrupt at migration boundaries, relaunch. Then mutate A/B sessions/recovery independently. | Migration is atomic or rolls back; legacy backup/reversibility remains until completion marker; secret-key identity unchanged. A child never changes B state/recovery; scratch is absent from durable shared registry. |
| Secret/cookie/OAuth non-duplication | Use sentinel values `M5_SECRET_SENTINEL`, cookie, OAuth token in scoped test fixture; save sessions, recover deletions, crash/reopen, export. Byte-scan registry/session/recovery/export paths. | No plaintext sentinel in registry/session/recovery artifacts or screenshots/logs. Functional secret use may remain encrypted/credential-store referenced. Any plaintext match fails. |
| HTTP/GraphQL | Send success/error/redirect/cancel and GraphQL query/error against local fixture under A/B. | Correct status/body/timing/cancel truth stays window/workspace scoped; no response bleed. |
| WebSocket/gRPC | Native package: text/binary WS, lifecycle/cancel/disconnect; gRPC unary + stream, metadata/header/trailer, binary payload, unavailable/cancel. | AX and response/timeline identify protocol, stream state, frames/messages, metadata/trailers, binary safe view, cancellation and offline errors. Stale streaming callback cannot update a closed/different workspace. |
| Proxy/TLS/cert/cookies | Direct/system/explicit/broken proxy; TLS verify on/off; custom CA; valid/invalid client cert; cookie persistence A vs B. | Every setting is visible/truthful; expected connection succeeds/fails with actionable reason; cookies/certs never cross workspace or leak plaintext artifacts. |
| Filesystem/external edits/Git | Externally change/delete/rename request while A owns; observe B/read-only. Run Git status/commit/remote operations in A then B. | Refresh preserves identities/tabs or gives recoverable conflict; only owner writes; Git cwd/remotes/status cannot cross workspace. Destructive change creates scoped recovery. |
| Import/export | Import Postman/Bruno/OpenAPI into A and B; export each; retry malformed/duplicate/cancelled import. | Imported data is scoped and authoritative files land only in selected workspace; export contains only selected workspace; failures are atomic/recoverable and no secret plaintext is exported. |
| Accessibility/compact | At ~1024x700 and large width, two windows active; tab through menus, owner/read-only banner, protocol controls, migration/recovery prompts. | Stable roles/names/focus, no clipped primary action, owner/refusal state spoken, dark/light contrast usable. |

## Acceptance evidence bundle

For every row: native screenshot + AX snapshot, command/process observation where relevant, before/after workspace hashes, and a short verdict. Capture artifacts under `docs/qa/m5-independent-*`; keep fixtures, tokens, and private paths redacted. A single cross-workspace write, owner bypass, session-secret plaintext match, stale callback update, or protocol-state lie is a release-blocking M5 failure.
