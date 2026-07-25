# M8 Clean-State Native Playthroughs

Date: 2026-07-19 (America/Chicago)

Package: `/Users/mostafi/Developer/Workspace/lite_api/build/bin/LiteAPI.app`

Current executable SHA-256: `b7a3598b3d43370c116f5c178df580ff3d6d3e2423d07ce2444061505a7d481c`

Rule: acceptance requires three consecutive complete passes against the final unchanged production package. Any product defect resets the consecutive-pass count after repair and rebuild. Each run must use a new temporary data directory and process-attributed Computer Use window.

## Required evidence per run

- Fresh production launch reaches the complete workbench with no Loading stall, crash, or accessibility error announcement.
- Window PID, launch arguments, data directory, workspace ID, session ID, and owner-lock record agree.
- A workspace/collection/request can be created, saved, and sent to a deterministic local fixture.
- The response reports truthful status, duration, byte count, and fixture content.
- Across the three runs, packaged WebSocket and gRPC behavior, metadata/trailers, disconnect/cancel, and an unavailable/error state are represented.
- New Window/Open Workspace in New Window creates a real isolated child process; duplicate ownership is refused.
- Light/Dark, compact/medium/large sizing, keyboard focus/shortcuts, named accessibility controls, and responsive layout receive representative coverage across the sequence.
- Close/relaunch restores the correct workspace, open tab, request content, response orientation, and safe geometry without leaking another workspace or secrets.
- The app exits cleanly, leaving no stale live process. Temporary directories are retained as evidence rather than destructively removed.

## Run 1 — PASS (1/3)

- Owner: Independent QA, with root attribution adjudication.
- Temp data directory: `/tmp/liteapi-m8-official-r1.CLQQp8`.
- Process/session attribution: primary PID 50241/session `main-window`; child PID 51001/session `workspace-a793e6e31f64e81e771d`; both matched registry, process arguments, and separate owner locks. Duplicate child ownership was refused with the live owner session named.
- Theme and size: explicit Dark at compact bounds; vertical response orientation and safe 1024x800 geometry persisted.
- Request/protocol evidence: collection `M8 Official R1` and its `opencollection.yml` existed; `M8 XML.yml` was saved inside it; Cmd+Enter returned `200 OK`, 12 ms, 93 bytes, `NEEDLE-42`, and five timeline rows.
- Multi-window/owner evidence: two live PIDs/locks and child launch arguments were correct. After primary close, Computer Use returned a stale primary-window capture. Disk registry/scoped state/session all showed the child correctly isolated. Root then launched that exact child session alone and obtained AX title `LiteAPI — M8 Official Child`, no Sample API, and `No environment`, disproving product leakage.
- Keyboard/accessibility evidence: Cmd+L, Cmd+S, Cmd+Enter, response-tab arrows, More/Escape, named AX controls, and native owner-refusal error passed.
- Relaunch evidence: direct primary relaunch restored Dark, collection/request/tab/URL, vertical orientation, and geometry. Root's direct child-session replay proved the child state. All verification processes ended normally with zero package PIDs.
- Result: PASS. The independent FAIL classification was a stale closed-window controller attachment; root's same-directory solo-session replay corrected the attribution without a code or package change.

Prior attempt (not part of the streak): FAIL at `/tmp/liteapi-m8-run1.jxDivf`. It exposed a real empty-collection materialization/relaunch defect. The defect was repaired, regression-tested for YAML and Bruno collections, and the package was rebuilt; the streak reset to zero.

## Run 2 — PASS (2/3)

- Owner: Independent QA.
- Temp data directory: `/tmp/liteapi-m8-official-r2.gzwfZp`.
- Process/session attribution: primary PID 62335, direct relaunch PID 64929, and solo child PID 64029 each matched the intended session/owner lock. Final state had zero package PIDs and no owner-lock JSON.
- Theme and size: explicit Light at medium/large coverage; Light persisted. Vertical orientation and safe geometry x=516, y=123, 1024x800 restored.
- Request/protocol evidence: `M8 WebSocket R2/M8 WS Echo.yml` was collection-owned. `ws://127.0.0.1:18487/ws` connected with `101 Switching Protocols` (88 ms/290 B), rendered sent and received `{}` rows, and Escape produced a truthful `disconnected` system row (final 101/6019 ms/420 B).
- Multi-window/owner evidence: concurrent child PID 62667 had exact child arguments/lock and scoped registry/session; duplicate ownership was refused with its session named. Exact child session then launched alone as PID 64029 with AX title `LiteAPI — M8 R2 Child`, no primary collection, and no matching requests; it closed normally.
- Keyboard/accessibility evidence: Escape disconnect, response-tab arrow, named AX status/events/controls, and owner refusal passed.
- Relaunch evidence: direct primary relaunch restored Light, the collection, WebSocket tab/URL, vertical orientation, and safe geometry without child data.
- Result: PASS.

## Run 3 — PASS (3/3)

- Owner: Independent QA with root acceptance review.
- Temp data directory: `/tmp/liteapi-m8-official-r3.uZE7J7`.
- Process/session attribution: initial primary PID 74512, primary relaunch PID 76396, concurrent child PID 74844, and solo child PID 75782 each matched their intended arguments/session/owner lock. Final state had zero package PIDs and zero owner-lock JSON.
- Theme and size: explicit Dark AX state; orientation exercised. Earlier consecutive runs supply compact/medium/large and Light coverage.
- Request/protocol evidence: native YML collection `M8 GRPC Bruno` contained the saved gRPC request. `grpc://127.0.0.1:18488` + `grpc.testing.TestService/UnaryCall` returned `200 OK`, an application/grpc binary-safe view with 184 exact bytes, `x-liteapi-fixture: initial`, duplicate metadata `one, two`, and trailer `x-liteapi-fixture-trailer: complete`. `grpc://127.0.0.1:1` produced truthful `Unavailable`, connection-refused, 0-byte state; the valid target was restored and saved.
- Multi-window/owner evidence: concurrent child arguments/locks/session were scoped. Exact child session then launched alone as `LiteAPI — M8 R3 Child`, with no primary collections/requests, and closed normally.
- Keyboard/accessibility evidence: named gRPC response, Metadata, Trailers, binary-safe and error-state controls/values were exposed; the preceding consecutive runs cover response arrows and Escape.
- Relaunch evidence: direct primary relaunch restored the primary collection, gRPC request/tab, URL, method, and no child data.
- Result: PASS. Consecutive sequence complete: 3/3.

## Final disposition

PASS. Three consecutive complete clean-state native playthroughs passed on executable SHA-256 `b7a3598b3d43370c116f5c178df580ff3d6d3e2423d07ce2444061505a7d481c` after the last product repair. Evidence directories are retained and were not destructively removed.
