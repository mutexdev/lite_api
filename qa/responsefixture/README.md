# LiteAPI response fixture service

This local-only service supplies deterministic M4 response, performance, download, WebSocket, and gRPC fixtures without an internet dependency.

Run it from the repository root:

```sh
go run ./qa/responsefixture -listen 127.0.0.1:18487 -grpc-listen 127.0.0.1:18488
```

HTTP endpoints:

| Endpoint | Purpose | SHA-256 |
| --- | --- | --- |
| `/json-1m` | Exact 1 MiB valid JSON with `NEEDLE-42` | `1ffbe5d1269f04ce03b3bc8c8ff62f2f151ef7f862361fafae2e9fe49df75f43` |
| `/json-5m` | Exact 5 MiB valid JSON with `NEEDLE-42` | `aa4e83c20bd96c98da75071dad0771d4a96712397eec07508551ee085c7592a9` |
| `/text-1m` | Exact 1 MiB text | `bad85c94cdbfe462be89c686d736d867e8dad16bba2cd5cc43692d29d903cbb1` |
| `/text-5m` | Exact 5 MiB text | `501c258e43c8d799e7308cee26bc01dbd86d806d0c44da2f42947a01ec2ce361` |
| `/binary-200k` | Exact 200 KiB binary payload for Base64/Hex preview and full-render checks | deterministic |
| `/binary-5m` | Bytes `00..ff` repeated, exact 5 MiB, attachment filename | `2e7cab6314e9614b6f2da12630661c3038e5592025f6534ba5823c3b340a1cb6` |
| `/image` | Deterministic PNG | `e3e2905354caea3ca3df90206b2eb5e3c37a4c72437d92a0c31e181b05886585` |
| `/pdf` | Minimal valid PDF | `be70fd796cea639c2196ee5cb6594dddb670c37ebc8aac4f96b545474d1e2e1d` |
| `/html-safe` | HTML containing a script/network escape attempt for sandbox verification | `a9946767be3d5d332264fd5e9f580c1ad571ad4001f0af627c8b9d4dee9ffc52` |
| `/xml` | UTF-8 XML with `NEEDLE-42` | `e0c8c8170ddaec38d62ee566e31404b13247599bea95b231444f011546193b52` |
| `/compare-a`, `/compare-b` | Known status/header/JSON differences | response-specific |
| `/timeline` | Controlled server delay and `Server-Timing` phases | response-specific |
| `/sse` | Ordered, flushing Server-Sent Events stream with `SSE-NEEDLE-42`, then completion | streaming |
| `/ws` | Text and binary echo | streaming |
| `/grpc` | JSON discovery document for the gRPC fixture | response-specific |

The reflected gRPC target is `grpc://127.0.0.1:18488`, service `grpc.testing.TestService`. It supports `UnaryCall`, server/client streaming, and bidirectional streaming, and emits deterministic initial metadata, trailers, text, and binary payloads.

Each fixed HTTP payload publishes `Content-Length`, `Content-Disposition`, `X-Fixture-SHA256`, and repeated `X-LiteAPI-Duplicate` headers. `/sse` uses `text/event-stream`, emits and flushes three fixed events in order, and closes promptly. Automated tests assert sizes, digests, JSON validity, media signatures, and SSE ordering/completion.
