# US-003 — Go linter — Round 1

**Verdict: PASS**

Reviewers: R1. All gate output below was **re-run by the orchestrator**, independently of the
worker's report.

## Acceptance criteria

| Criterion | Status | Evidence |
|---|---|---|
| `.golangci.yml` with govet, staticcheck, errcheck, ineffassign, unused | PASS | v2 schema, `default: none`, all five enabled |
| CI step added | PASS | `go-lint` job in `.github/workflows/ci.yml`, action pinned `v2.12.2` |
| Repo passes, or violations excluded with a reason | PASS | 0 issues configured; 25 excluded, each with a written reason |
| Typecheck passes | PASS | `go build` + `go vet` exit 0 |

## Gates — orchestrator-verified

```
golangci-lint run --timeout 15m ./...     0 issues.        EXIT=0
go build ./...                                             EXIT=0
go vet ./...                                               EXIT=0
go test -count=1 ./...        ok LiteAPI 22.676s           EXIT=0
go test -race -count=1 ./...  ok LiteAPI 47.415s           EXIT=0
```

Test functions removed from tracked test files: **0** (`git diff HEAD -- '*_test.go' | grep '^-func Test'`).
Test functions added: 4. Total collected: 461.

## Census correction — the orchestrator's brief was wrong

The brief given to the worker stated **107** findings. That figure was truncated by golangci-lint's
defaults (`max-issues-per-linter: 50`, `max-same-issues: 3`). Re-measured with both set to `0`:

| Linter | Briefed | Actual | Fixed | Excluded |
|---|---|---|---|---|
| errcheck | 49 | **81** | 81 | 0 |
| staticcheck | 35 | **99** | 71 | 28 |
| unused | 17 | 17 | 17 (deleted) | 0 |
| ineffassign | 6 | 6 | 6 | 0 |
| govet | 0 | 0 (+2 in vendored `node_modules`) | 0 | 2 |
| **total** | **107** | **203** | **178** | **25** |

ST1005 alone was **66**, not 4. Any future census in this program must set both caps to `0` or it
will silently under-report.

## Exclusions are honest — verified by negative control

Running the linter **unconfigured** against the post-fix tree returns exactly **25** issues, and every
one falls inside a documented exclusion:

| Remaining unconfigured | n |
|---|---|
| SA1019 — `grpc/reflection/v1alpha` | 17 |
| SA1019 — `x509.IsEncryptedPEMBlock` / `DecryptPEMBlock` | 2 |
| ST1005 — `"Cannot find module"` | 3 |
| SA1019 — `pkcs12.Encode` (test fixture) | 1 |
| govet — `frontend/node_modules/flatted/golang` | 2 |

Zero errcheck, zero ineffassign, zero unused, and **zero SA4004** remain. So the exclusions hide
nothing beyond what they name, and the other 178 findings were genuinely repaired rather than
suppressed.

## SA4004 — investigated, not a bug

`openAPIExampleRawValue`. It sorted the example names then looped, but every path through the body
returned on iteration 1 — a "take the lowest-sorting key" idiom written as a loop, with an
unreachable trailing `return nil, false`. Rewritten as a direct `examples[names[0]]` index, keeping
the sort (and a comment) because it is what makes selection deterministic over Go's randomised map
order. Behaviour-identical.

## Real bug found and fixed

`copyCollectionFile` (collection clone path) held `defer target.Close()` on the **write** handle.
`io.Copy` succeeding meant `return nil`, so a close-time failure — ENOSPC, over-quota, a failing
network filesystem — was silently discarded and a **truncated collection copy reported success**.
Converted to a named return that propagates the close error.

This is exactly the class §2.1 warns about and the reason R4 (silent-failure-hunter) is mandatory
for US-012, where 83 synchronous persist calls become asynchronous.

## Worker correctly refused an orchestrator instruction

The brief said to exclude `workspace_window_runtime.go:363` `screen.Width` if Wails v2.10.2 still
required it. The worker verified the claim and found it **false**: all three backends
(`darwin/screen.go:106`, `linux/screen.go:75`, `windows/screen.go`) populate `Size`. The code already
preferred `Size` and only fell back to the deprecated field when `Size` was zero — dead weight. It
removed the fallback instead of excluding the rule, and updated one test fixture accordingly.

Independently corroborated: the US-007 module diff shows `Screen` is byte-identical between v2.10.2
and v2.12.0, with `Size`/`PhysicalSize` present in both.

## Deletions — 17 `unused`, all grep-verified

Includes a dead 3-function chain (`evaluateRuntimeTests` / `WithJar` / `WithJarState`) that only
called each other, and `newScriptRuntime`, a 3-line wrapper. **`newScriptRuntimeWithMeta` is
untouched** — verified present with 5 references. That distinction matters: it is the US-005
benchmark target that US-018's ">=5x" acceptance criterion is graded on, and the two names differ
only by suffix.

Re-ran `unused` after deleting: 0 cascade. None appear in the `Feature` ledger (§2.2), which
references test names and prose rather than Go identifiers.

## Non-blocking — for `.ralph/backlog.md`

- 60 ST1005 fixes lowercased **user-facing GUI strings**. Defensible as Go convention, arguably a UI
  downgrade. Two judgement calls to re-examine: `"Yaak imports are not supported yet"` rephrased to
  avoid lowercasing a proper noun (same for Swagger 2), and ~20 `"Git ..."` → `"git ..."`.
  `collection_import.go` keeps an allowlist of displayable messages that a global replace kept in
  sync — worth a second pair of eyes.
- The worker's own excluded-count arithmetic (28 for staticcheck) differs by 5 from the 23 measured
  here. Immaterial to the verdict — the configured run is 0 and the unconfigured run is fully
  accounted for above — but the discrepancy is recorded rather than smoothed over.
