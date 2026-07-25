# US-004 — Frontend linter — Round 1

**Verdict: PASS**

Reviewers: R1 (correctness/convention). R5 was not run separately; the story adds no new runtime
logic beyond one bug fix, and that fix was verified directly against source by the orchestrator.

## Gate output — re-run by the orchestrator, not taken from the worker's report

```
$ npm run lint
> eslint .
                                    (no output)
LINT_EXIT=0

$ npm run check
COMPLETED 151 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS

$ npm test
# tests 7
# pass 7
# fail 0

$ npm run build
✓ built in 4.00s
```

Bundle: 1,159,965 B JS (baseline 1,159,933, **+32 B**) + 113,999 B CSS (unchanged).
The +32 B was attributed by a controlled experiment — revert the two source edits, rebuild, get
exactly 1,159,933 B, restore. So it is entirely the ResponseInspector correctness fix, not config
drift.

## Acceptance criteria

| Criterion | Status | Evidence |
|---|---|---|
| `eslint.config.js` exists using `eslint-plugin-svelte` | PASS | flat config, ESLint 10; `svelte.configs.recommended` spread in |
| `npm run lint` passes | PASS | exit 0, verified independently |
| Typecheck passes | PASS | 151 files, 0 errors, 0 warnings |

## Findings

### 1. Real bug found and fixed — not a lint nit

`frontend/src/lib/workbench/ResponseInspector.svelte:76-78`

```js
$: wsEvents        = websocketEvents()   // before
$: grpcEventsParsed = grpcEvents()
$: jsonValue       = parsedJson()
```

Svelte's legacy `$:` determines dependencies from variables referenced **syntactically inside the
statement**. It does not look inside a called function. These three statements referenced only the
function bindings — which are immutable — so they had **no reactive dependencies and ran once at
mount**. There is no `{#key}` wrapper around the component (`App.svelte:9485`), so the JSON tree
view, the WebSocket event log and the gRPC stream log displayed the **first** response for the
lifetime of the component.

Fixed by threading state through as parameters. Verified in source — all three now read:

```
function websocketEvents(response: main.Response | undefined, size: number)
function grpcEvents(response: main.Response | undefined, size: number)
function parsedJson(response: main.Response | undefined, size: number)
```

`svelte/no-immutable-reactive-statements` remains **enabled**, so a regression is caught.

### 2. Spec correction — `eslint-plugin-svelte` v3 has no a11y rules

US-004's framing implies `eslint-plugin-svelte` provides accessibility coverage. It does not: all 85
plugin rules were enumerated and `svelte/a11y-*` matches = **0**. Those rules were removed upstream
in v3; a11y is now reported by the Svelte compiler.

Had this gone unnoticed, US-004 would have "passed" while protecting none of the accessibility work
§2.2 lists as non-regressable (239 `aria-label`, focus traps, `inert` blocking, roving tabindex).

Compensated by enabling `svelte/valid-compile` (not in the recommended preset), which surfaces
compiler warnings — including all a11y warnings — as lint errors. Confirmed non-vacuous by a
negative-control probe that made it fire `a11y_missing_attribute`,
`a11y_click_events_have_key_events` and `a11y_no_static_element_interactions`.

### 3. Disabled rules — accepted, each with count and owner

Raw census against full recommended presets (29 files): **200 findings**.

| Rule | n | Disposition |
|---|---|---|
| `svelte/require-each-key` | 116 | off → **US-030** |
| `@typescript-eslint/no-unused-vars` | 21 | off **for `src/App.svelte` only**, error elsewhere → **US-025** |
| `no-useless-assignment` | 21 | off — see below |
| `svelte/infinite-reactive-loop` | 20 | off → **US-029** |
| `svelte/prefer-svelte-reactivity` | 9 | off — wants runes-mode `SvelteMap`/`SvelteSet` → **US-027/028/029** |
| `no-control-regex` | 9 | off — deliberate `\x00-\x1F` filename sanitisers and `\x1B` ANSI stripping |
| `svelte/no-immutable-reactive-statements` | 3 | **fixed in code**, rule stays on |
| `no-useless-escape` | 1 | **fixed in code**, rule stays on |

`no-useless-assignment` is the one disable a reviewer should push on, and it is justified: the rule
cannot see that a `$:` block re-runs, so it flags the memo guards that make those blocks *terminate*
(`hydratedActiveTabID:959`, `lastPresetKey:1135`). Acting on it would introduce infinite reactive
loops. Reassess after US-029.

Everything not listed stays enabled at error level. No repo-wide `eslint-disable`, no
`--max-warnings` fudge.

## Non-blocking — appended to backlog

The 21 unused bindings in `App.svelte` are evidence of **unwired features**, not dead cosmetics:
`exportText`, `gitVersionText`, `gitCloneOutput`, `shareCollectionResult`, `generateDocsResult` are
each assigned from a Wails call and never rendered — meaning Share Collection, Generate Docs, Export
and Git Clone/Version compute results the user never sees. Handed to US-025.

## Protected assets (§2.2)

- `response.ts` — one edit, `[\[{]` → `[{[]` at `:57`. Provably identical (inside a character class
  `[` needs no escape). Tiered guarding untouched.
- `style.css` — not touched.
- Accessibility in `App.svelte` — not touched; net *improved* via `svelte/valid-compile`.

## Reviewability caveat

`frontend/src/lib/workbench/` is **untracked**, so `git diff` shows nothing for `ResponseInspector.svelte`
or `response.ts`. This review verified them by reading source directly. See the diff-attribution
issue in `progress.txt`.
