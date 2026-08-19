# M4 independent packaged-app QA

Candidate: `build/bin/LiteAPI.app` (fresh isolated data dir `/tmp/liteapi-m4-independent-oW7IA9`)

Result: **PASS — independent M4 acceptance complete.**

## Verified pass evidence

- `/json-1m` returned `200`, `1.0 MB`, and showed the explicit `1,048,576 bytes · preview truncated` safe-render protection. Full rendering was disabled with a download-first explanation.
- `/text-5m` returned `200`, `5.0 MB`, promptly and remained interactive. The UI bounded automatic rendering to `128 KB` and retained body search/copy/download controls.
- `/binary-5m` returned `200`, `5,242,880 exact bytes`, filename `binary-5m.bin`, and a bounded offset/byte/ASCII Hex renderer. Native download saved the stated filename; SHA-256 was `2e7cab6314e9614b6f2da12630661c3038e5592025f6534ba5823c3b340a1cb6` (expected match).
- Timeline fixture exposed accessible Timeline, search, phase filter, Copy, and Export controls; rows included upload, wait, download, connection-reused, status, URL, and duration. Header UI exposed accessible Search headers and Copy headers plus the fixture's Server-Timing and X-Timeline-Token headers.

## Repaired transition retest

Fresh isolated package: `/tmp/liteapi-m4-repair-bqDgoc`.

1. Fetched `/binary-5m` and selected `Hex` (visible offset/byte/ASCII dump).
2. Fetched `/xml`, opened Response view, and selected `Pretty`.
3. The selected AX control became `Response view, Value: Pretty`; formatted XML, `NEEDLE-42`, and Unicode `héllo` were visible.
4. Fetched `/html-safe`; its AX tree exposed `HTML content Description: Sandboxed HTML response preview, URL: about:srcdoc` plus heading `Sandbox fixture` and text `NEEDLE-42`.

The prior response-view blocker is repaired.

## Additional independent checks

- `/image` rendered an accessible `image Response preview`.
- `/pdf` rendered an accessible `HTML content PDF response preview`.
- A malformed JSON editor body produced `Invalid JSON at line 1, column 12: JSON Parse error: Unexpected token '}'`; Format and Minify were disabled rather than corrupting invalid input.
- Valid JSON with `{{name}}` and `{{missing}}` showed both named variable buttons and `Variables in this editor (2)` without exposing a secret. Format produced indented JSON; Minify returned one-line JSON.
- Saved `/compare-a` as an example, fetched `/compare-b`, and selected the example in the accessible comparison control. The comparison showed `Status: 201 → 200`, `Timing: 12 ms → 13 ms`, `X-Compare-Value beta → alpha`, JSON-structure summary, and changed body rows.

## Compact visual smoke

Native Computer Use did not expose a resize primitive. The observed packaged-window screenshots were 984×768 pixels, the closest available compact state. In both Light and Dark, the request editor, response tabs, response-view/search/copy/download controls, and Send/Save actions remained discoverable in the AX tree with no page-level horizontal overflow, clipped primary action, or overlapping text observed. Light and Dark toggles reported their selected state correctly.

WebSocket and gRPC remain explicitly deferred to M5: no M4-specific packaged controls were exposed during this response-pane pass.
