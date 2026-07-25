# US-002 — Wire frontend tests — Round 1

**Verdict: PASS**

Reviewers: R1, R5 (test coverage). Both discharged by the orchestrator directly — the change is one
line of configuration, and the substantive question is whether it actually gates.

## Change

`frontend/package.json` gains:

```json
"test": "node --test test/*.mts"
```

Nothing else. No test files were added or modified under this story.

## Acceptance criteria

| Criterion | Status | Evidence |
|---|---|---|
| `package.json` gains `"test": "node --test test/*.mts"` | PASS | verbatim, as specified in §7 |
| Existing 7 cases pass via `npm test` | PASS | `# tests 7  # pass 7  # fail 0` |
| Typecheck passes | PASS | svelte-check 151 files, 0 errors, 0 warnings |

## The check that actually matters

A test script that passes proves nothing on its own — a script that always exits 0 would also
"pass". The point of this story is to give CI something that **fails**. Verified by controlled
experiment:

```
npm test                          -> exit 0     (7/7)
+ test/__tmp_fail.test.mts        -> exit 1     (assert.equal(1, 2))
- test/__tmp_fail.test.mts        -> exit 0     (7/7, restored)
```

The temporary file was removed; `git status` confirms no stray artefact. Because a non-zero exit
fails the CI step, this also supplies the local half of US-001's "a deliberately broken PR fails the
workflow" criterion.

## Note on Node version

`node --test test/*.mts` runs the TypeScript `.mts` files with **no flags** because Node 22.18+
enables type stripping by default; the local toolchain is v22.23.0. CI pins Node 24 (`ci.yml`,
`NODE_VERSION: "24"`), where this is stable rather than experimental. So the script is correct on
both, and the version floor is Node 22.18 — worth knowing if anyone lowers `NODE_VERSION`.

## Coverage assessment (R5)

The 7 cases are genuine unit tests over extracted pure logic, not smoke tests:

| File | Cases | Covers |
|---|---|---|
| `gitWorkbench.test.mts` | 4 | push upstream handling, remote-branch tracking across branch transitions |
| `importPlanning.test.mts` | 2 | import row selection, replace-confirmation gating |
| `tabLifecycle.test.mts` | 1 | transient vs durable request deletion |

This is thin relative to a 13,165-line `App.svelte`, but that is **not this story's scope** — US-002
is wiring, and US-038 ("Test `response.ts`") is where real frontend coverage is specified. Recorded
here so the thinness is not mistaken for adequacy: passing `npm test` currently means very little
about frontend correctness, and CI's green tick should not be read as more than it is.

## Protected assets (§2.2)

None touched.
