# qa/

Checks that the ordinary build does not make.

`go build`, `go test`, `npm run check` and the rest cover whether the code
works. These cover the things that stay green while quietly meaning nothing —
a binding renamed on one side of the Wails bridge, a lint exclusion left
pointing at a file the code has moved out of, a test suite that runs zero tests.

Every one of these was written after finding the gap it now guards.

| script | what it fails on | cost | in CI |
|---|---|---|---|
| `bindings.sh` | the bound-method surface changed, or Go/`App.d.ts`/`App.js` disagree | <1s | yes |
| `test-presence.sh` | a package lost its tests, or gained them without leaving the exemption list | ~45s | yes |
| `lint-exclusions.sh` | a `.golangci.yml` exclusion has stopped suppressing anything | ~5s | yes |
| `skip-audit.sh` | a test skips that did not before, or a baseline entry no longer skips | ~50s | yes |
| `layout.sh` | the repository root gains a Go file, or bound methods appear outside `internal/core` | <1s | yes |
| `mutation.sh` | a catalogued break stops failing the tests | ~170s | no |
| `coverage.sh` | nothing — it reports two figures | ~90s | no |
| `selftest.sh` | one of the gates above can no longer fail | ~60s | no |

`frontend/scripts/verify-inputs.mjs` is the frontend counterpart, wired to
`pretest`, `precheck` and `prelint` so it runs automatically.

**A note on paths.** The application lives in `internal/core`; the repository
root holds only `main.go`. Several gates are anchored to file paths, and those
anchors are the first thing to update when code moves — an exclusion or a
mutation entry pointing at a path that no longer exists does not error, it just
stops applying. `lint-exclusions.sh` and `mutation.sh` exist because that has
already happened here.

## Why each exists

**`bindings.sh`** — the 188 bound methods are the contract between `package
main` and the frontend, and the generated bindings are committed. A rename in
Go that is not followed by `wails generate module` leaves the frontend calling a
method that no longer exists, and **nothing in either build says so**, because
the `.d.ts` still type-checks against itself. Checking the count alone is not
enough: a changed parameter type, or one method unexported while another is
added, both keep it at 188. The gate diffs the full signature list.

To change the surface deliberately: `wails generate module`, then
`qa/bindings.sh --update`, and review that diff as part of the change.

**`test-presence.sh`** — `go test ./...` prints `[no test files]` and **exits
0**, so a package losing its tests leaves CI green. Four packages legitimately
have none; they are listed in `baseline/untested-packages.txt` with a reason
each. The comparison runs both ways, so the list cannot rot into a blanket
exemption.

**`lint-exclusions.sh`** — an exclusion is anchored to an exact path. When code
moves the anchor stops matching, silently: nothing errors, the exclusion just
stops applying. This repo reached 45 live issues that way once, with five
exclusions still pointing at `app.go` after the code had moved to `internal/`.

**`coverage.sh`** — reports, it does not gate. ONE scope, `./internal/...`.
It used to report two, `internal/` alone and `internal/` plus root `package
main`, because those measured genuinely different things while the application
lived in package main at the repository root. Now that root holds only
`main.go`, the two differ by rounding, and offering both would only invite
quoting whichever is higher.

**`skip-audit.sh`** — a skipped test reports success while checking nothing,
and `go test` prints `ok` either way. Written after a test here skipped on every
run: it asserted a sidebar placement that only holds in a workspace containing
nothing but the Scratch collection, took the DEFAULT workspace, which ships with
a sample collection, and skipped silently. It was green and it verified nothing.

Platform guards are the other reason. Several tests skip when
`http.DefaultTransport` is not an `*http.Transport`, or when the system
certificate pool is unavailable — false here today, but a toolchain or CI image
change could make one true, and the affected test would stop checking anything
without saying so. `baseline/skipped-tests.txt` lists the one legitimate skip
with its reason, and the comparison runs both ways.

**`mutation.sh`** — replays a catalogue of deliberate breaks and requires the
tests to catch every one. Coverage says which lines RAN; this says which
behaviours are actually CHECKED, and in this repo the two came apart badly (see
the note below). Every entry in `baseline/mutations.txt` corresponds to a real
defect or a real blind spot found here, so the same gap cannot reopen quietly.

It distinguishes three ways a control lies, all seen for real, none counted as
a pass: `NOT FOUND` when the pattern no longer matches, `AMBIGUOUS(n)` when it
matches more than once — two controls here reported a clean result after
editing a different function than the one intended — and `NO COMPILE`, where a
build error otherwise reads exactly like a pass. `BLIND` means the break
landed, compiled, and nothing failed.

Run `qa/mutation.sh --list` to see the catalogue, `--only <text>` for one entry.
Add an entry whenever a test is written for something that would be expensive
to get wrong.

**`layout.sh`** — the repository root holds exactly `main.go`, and the bound
struct lives in exactly one package. Both are the outcome of a restructure that
moved ~70,000 lines, and neither is enforced by anything the compiler does: a
new file in the root builds perfectly well, and so does a second bound struct.
The layout would drift back one file at a time with every other gate green.

It also checks that `main.go` still declares the `//go:embed` that pins it
there, and that no two `internal/` packages share a short name — Wails resolves
bound types by short name, so duplicates collide silently in the generated
TypeScript. `docs/architecture.md` has the reasoning.

**`selftest.sh`** — breaks each gate's premise on purpose and requires the gate
to notice. A gate that has lost its ability to fail is a green tick that means
nothing.

## Things learned the hard way, so they are not re-learned

**Coverage needs `tools/coverage/mergeprofile.go`.** With `-coverpkg` over a
multi-package build, every test binary writes a full profile and `go test
-coverprofile` concatenates them, so one source block appears once per binary —
here that turns roughly ten thousand blocks into nearly three hundred thousand
lines. `go tool cover -func` counts each occurrence separately, so **the
reported percentage falls as test binaries are added**, which is exactly
backwards. `coverage.sh` merges first.

To see the ratio for yourself:

```
go test -count=1 -coverpkg=./internal/... -coverprofile=raw.out ./...
wc -l raw.out
go run tools/coverage/mergeprofile.go raw.out | wc -l
```

`-count=1` matters for an unrelated reason: a cached package contributes no
profile data at all, so a cached run silently under-reports.

**`-cover` hides packages that have no tests.** With that flag a package without
tests prints `coverage: 0.0% of statements` instead of `[no test files]`, so
`grep -c "no test files"` returns zero. A claim that every package here had
tests rested on exactly that measurement; four do not. `test-presence.sh` runs
without `-cover` deliberately.

**Count findings from JSON, not from prose.** Two of the mistakes above came
from grepping a tool's human-readable output for a string. `lint-exclusions.sh`
counts from `--output.json.path`; a machine format does not move under a flag,
and one that does change is reported rather than silently counted as zero.

**Every gate is asked what it does when its input is absent.** Not one of them
answered "fail" before being asked — `node --test` over a glob matching nothing
exits 0; `svelte-check` with `src/` gone still finds 103 files in `node_modules`
and reports success; `golangci-lint` missing from `PATH` made every exclusion
look dead. The frontend floors are floors, not targets: they catch a collapse,
not growth.

**100% line coverage is not evidence of a tested behaviour.**
`countRegularCollections` and `firstScratchCollectionIndex` both reported 100%.
Changing `countRegularCollections` to count scratch collections as regular —
inverting the one thing it exists to decide — left the entire `internal/core`
suite green (then `package main`, before the restructure). Every line ran on the way to somewhere else and nothing asserted
what any of it meant.

The same held one level up: `restoreCollectionRemovalLocked` was 60% covered,
and replacing its whole insertion index with "always append" was invisible to
the suite, so undoing a collection delete could silently reorder the user's
sidebar. Both were found by mutating the code and watching the tests not care,
which is the only measurement that answers the question directly. Prefer it to
reading `coverage.sh`, and treat a covered-but-unverified function as untested.

**`selftest.sh` cannot verify itself.** Breaking its `expect_failure` helper
makes it pass a blind gate. Something must be trusted at the bottom; the
mitigation is that the helper is six lines with two visibly opposite branches.
