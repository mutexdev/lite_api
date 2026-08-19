# M5 Backend and Platform Acceptance Report

Date: 2026-07-19 (America/Chicago)

Result: PASS

## Independent automated review

An independent Terra/medium QA worker performed a read-only backend/security/protocol review. It passed the complete Go race suite, `go vet`, Svelte check and production build, plus focused coverage for:

- HTTP TLS verification, custom CAs, mTLS, cookies, proxy and PAC behavior.
- Postman, Bruno, and OpenAPI imports.
- WebSocket persistent sessions, ping/keepalive, text/binary frames, disconnect, and cancellation.
- gRPC unary and streaming calls, metadata, trailers, bidirectional lifecycle, and cancellation.
- Workspace ownership, stale owners, recovery, session migration, concurrent locks, scoped state, and secrets.
- Encryption, traversal rejection, tamper handling, and owner-safe filesystem writes.

## Independent native review

A replacement Terra/medium QA worker used an isolated temporary data directory and attributed every window to its process arguments and owner-lock record. It verified:

- The primary process created Workspace B in a real child process whose `--workspace-id` and `--data-dir` matched the isolated registry.
- Closing the primary did not terminate the child; its heartbeat continued advancing.
- A duplicate process for the same workspace was refused while the live owner held the lock.
- A saved loopback XML request returned `200`, 93 bytes, and `NEEDLE-42`.
- Closing and relaunching restored the workspace title, collection, request/tab, URL, vertical response orientation, and 1024x800 geometry.

The worker did not accept WebSocket/gRPC based on a request-type selector alone. Root subsequently completed those protocol sessions in the same process-attributed package.

## Root protocol completion

- WebSocket: `ws://127.0.0.1:18487/ws` connected with `101 Switching Protocols`, rendered sent and received event rows, exposed `Disconnect (Escape)`, and recorded the disconnected system state.
- gRPC: `grpc://127.0.0.1:18488` with `grpc.testing.TestService/UnaryCall` returned `200 OK`, 178 exact binary bytes, metadata `content-type: application/grpc`, `x-liteapi-fixture: initial`, duplicate metadata values `one, two`, and trailer `x-liteapi-fixture-trailer: complete`.

## Defects repaired during acceptance

1. An empty recovery list serialized through Wails as `null`, causing the production shell to remain at Loading. The backend now returns a non-nil empty slice, the frontend also normalizes `null`, and the Go regression asserts the boundary.
2. Metadata and Trailers rendered but `UpdateOpenTabPanes` rejected their IDs, producing an accessibility-visible error. Both are now valid persisted response tabs with regression coverage.

## Attribution note

An earlier native attempt reported cross-workspace data leakage. Its screenshot and AX tree contained default Application Support recovery and notification state that was absent from the isolated registry, proving that Computer Use had attached to the wrong same-bundle process. A fresh replay launched directly, matched the PID to the temporary owner lock, and showed only the isolated workspace. The earlier report is retained as a tooling-attribution lesson, not as a product defect.

## M5 disposition

M5 is accepted. M6 owns the final clean-source production rebuild and integrated regression rerun; M7 and M8 still require final-package visual/accessibility QA and three consecutive clean-state native playthroughs.
