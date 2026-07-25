# Phase 0 — Computer-use QA (Reviewer 3) — Round 1

**Verdict: PASS**, with two defects logged to `.ralph/backlog.md`.

Method per `improvement_v2.md` §5.3. Performed against the Phase 0 tree (commit `2fe68f0`).

## Tooling — §5.3's primary path was unavailable

- `mcp__claude-in-chrome__*` could **not** be used: "Browser extension is not connected."
- Fell back to **Playwright MCP**, which worked. §5.3 should list it as an accepted driver.
- Native macOS capture is **not** an option for this app at all: `PARITY.md:12-19` records repeated
  `cgWindowNotFound` for bundle id `com.wails.LiteAPI`, which is why every existing smoke in that
  file used the Wails browser endpoint. Browser automation is the only viable route here, not a
  convenience.

Setup:

```sh
wails dev -nocolour          # app on http://localhost:34115, Vite on :5173
go run ./qa/responsefixture  # loopback fixture on 127.0.0.1:18487
```

Note `34115` serves the app **with live Go bindings**; `5173` is the bare Vite frontend and is not a
substitute — driving `:5173` would exercise no Go code at all.

## Test 1 — full stack after the Wails v2.12.0 bump (US-007)

The real question after a library bump and binding regeneration is whether the JS→Go bridge still
works end to end. It does.

| Step | Expected | Actual |
|---|---|---|
| Set URL to `http://127.0.0.1:18487/compare-a`, Send (auth = oauth2, no token URL) | rejected | **"OAuth2 access token URL is required"**, `Request failed 1 ms 0 B` |
| Auth → `none`, Send | 200 + body | `"fixture": "comparison", "value": "alpha", "items": ["one","two"], "needle": "NEEDLE-42"` — pretty-printed |
| URL → `/compare-b`, Send | body replaced | `alpha` **absent**; `"value": "beta"`, `"three"`, `"four"` present |

Row 1 is worth keeping: the app failed **loudly and correctly** on misconfigured auth rather than
silently sending an unauthenticated request.

Row 3 exercises the component US-004 fixed. It confirms the response inspector re-renders on a
second response. **Scope honestly stated:** this is not a reproduction of the original bug — the
specific broken paths were the JSON *tree* view and the WS/gRPC event logs, whereas the plain body
view was always reactive. It is corroborating evidence against the same component from the running
app, not proof of that exact path.

## Test 2 — layout probe at 1200px

Measured with `getBoundingClientRect` and `scrollWidth > clientWidth`, not read off a screenshot.
An initial naive probe reported 15 "overlaps"; most were **parent/child nesting artifacts** of the
probe itself and were discarded. What survives:

- `Development` (env selector) ends at x=666; `Cookies` starts at x=655 — **11px overlap between
  siblings**, visible as collided text.
- Clipped labels: `My Workspace`, `Sample API`, `R7 Persist`. The breadcrumb `Sample API / R7 Persist`
  is compressed into 80px (x=716..796).

Logged to `.ralph/backlog.md`. No existing story owns it; US-037 is closest but is a token migration,
not a responsive-overflow fix.

## Test 3 — console hygiene

One error in a clean session: `GET /favicon.ico → 404`. Cosmetic, but it blocks "zero console
errors" as a QA assertion until fixed. Logged.

## Artefacts

- `.ralph/baseline/phase0-main-window.png` — reference capture for Phase 2 screenshot diffing (§4.1).

## Cleanup

The sample request was mutated to run Test 1 (URL and auth mode). **Both restored** to `{{host}}/get`
and `oauth2`; the request stayed in UNSAVED/draft state throughout, so nothing was written to the
collection on disk. Stray `after-send.png` and `.playwright-mcp/` removed. `wails dev` and the
fixture server were stopped.

## Not covered — do not read this as broader assurance

Themes (12), virtualization, keyboard navigation, focus traps, modal `inert` behaviour, WS/gRPC
sessions, and the runner were **not** exercised. This round establishes that the QA path works and
that the core send/response loop survives Phase 0; it is not a UI regression suite. Phase 2 stories
need their own R3 rounds.
