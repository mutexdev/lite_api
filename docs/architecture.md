# Layout

LiteAPI is a Wails v2 desktop app: Go behind a Svelte 5 frontend, one binary.

The repository root holds **one Go file**. Everything else is under `internal/`,
37 packages. This document says why the root looks like that, what decides
where a thing goes, and which constraints were verified rather than assumed —
so the next person changing the shape does not have to re-derive them.

```
lite_api/
  main.go                  24 lines — the only .go file in the root
  internal/
    core/                  the App: 188 bound methods, the state lock
    types/                 the domain structs everything speaks in
    ...35 more
  frontend/                Svelte 5 + the generated Wails bindings
  qa/                      the checks the ordinary build does not make
```

## Why main.go is alone, and why it cannot move

Two constraints, both checked against `wails/v2@v2.12.0` in the module cache
rather than inferred from the docs.

**`cmd/` is impossible.** `pkg/commands/build/base.go:293` sets
`cmd.Dir = b.projectData.Path` and builds the package *in the project
directory*. `internal/project/project.go` exposes `projectdir`, `build:dir`,
`build:tags` and `outputfilename` — nothing names a main package elsewhere.
Moving `main.go` into `cmd/liteapi/` would drag `wails.json`, `frontend/` and
`build/` with it, which is a rename of the root rather than a reorganisation.

**`//go:embed` pins the file itself.** `main.go` declares
`//go:embed all:frontend/dist`, and an embed path is resolved relative to the
declaring file and may not escape its directory. That single line is the whole
reason a Go file remains in the root.

## Why the App is NOT in package main

An earlier plan asserted that every bound method "must remain a method on
`*App` in package main". **That is false**, and acting on it is what kept 142
files in the root.

Wails derives the binding path from `reflect.Type.String()` on whatever pointer
is passed to `Bind`:

```go
// internal/binding/reflect.go:72
pkgPath := strings.TrimSuffix(structType.Elem().String(), fmt.Sprintf(".%s", structName))
// internal/binding/generate.go:43
packageDir := filepath.Join(baseDir, packageName)
```

So the bound struct may live in any package; its package name simply becomes
the generated directory. `App` lives in `internal/core`, and the frontend
imports from `wailsjs/go/core/App` instead of `wailsjs/go/main/App`.

Two consequences that bite if forgotten:

- **Every exported method of the bound struct is bound.** Exporting a lifecycle
  method so `main` can reach it silently adds it to the frontend's API. That is
  why `core.Run` is a package-level function, not a method: `main.go` hands it
  the embedded assets and nothing else.
- **Bound types are addressed by SHORT package name.** `reflect.Type.String()`
  yields `types`, never the import path, so two packages with the same short
  name collide silently in the generated TypeScript rather than failing to
  build. Check a new package name against the existing 37.

## What lives where

`internal/core` is the application: the `App` struct, the state lock, and the
188 bound methods. It is the largest package and is *supposed* to be — package
main being large is idiomatic Go (`go.dev/blog/organizing-go-code`; the Go
tool's own main package is 12,000+ lines across 34 files). What matters is that
what remains there genuinely needs the App.

Everything else was extracted on one test: **does this need `a.state`, `a.mu`,
or a bound method?** If not, it belongs in a package named for its domain.

| | |
|---|---|
| `types` | the structs every package speaks in, plus pure logic over them |
| `scalar` | leaf helpers with no dependencies — the bottom of the graph |
| `core` | the App, the lock, the bound surface |
| `store/bru`, `store/yamlstore` | the two on-disk collection formats, reader AND writer |
| `envsecrets`, `secretkey` | secrets at rest, and the key that protects them |
| `workspacestate`, `recovery` | workspace layout, migration, and the undo store |
| `transport`, `cookiejar`, `wsexec`, `grpcexec` | the wire |
| `export`, `codegen`, `importers`, `openapisync` | in and out |

### The heuristic has a limit

Zero App coupling means a file does not need the **App** — not that it is free
of the **application**. `workspace_migration.go` reads as fully decoupled by
that measure and could not move: it checksums state *as stored*, and "as
stored" means secrets scrubbed and cookies encrypted. Extracting
`internal/envsecrets` first is what unblocked it. A dependency on secret
handling is invisible to a grep for `a.state`.

## Two things that are load-bearing and look incidental

**Encryption of environment secrets is deterministic on purpose-by-accident.**
It uses an all-zero IV, which leaks equality — anyone reading the state file can
tell which entries share a value. Fixing that is not a swap of the cipher:
`secrets.json` is rewritten only when its bytes change, and the workspace
migration proves its artifacts idempotent by comparing two runs. Both break
under random IVs. See the note in `internal/envsecrets/envsecrets_test.go`,
which carries the evidence.

**Test helpers are duplicated across packages, deliberately.** `assertMode`,
`runConcurrently` and `writeDataFile` each exist in two places. A helper cannot
cross a package boundary without being exported into the *production* API, and
a permission check is not worth widening that surface. Each copy says so.

## Changing the shape

### Decide by reading the declaration list, never by the filename

```
go run ./tools/gosplit -src internal/store/bru/write.go -list
```

A file's name records what someone *intended* to put in it. Four times during
the restructure a file was called coherent on the strength of its name and its
size, and four times the full list showed otherwise — most recently
`store/bru/write.go`, which was carrying the rules for what a collection file
may be *named* and a ledger of Bruno/Postman feature support alongside the code
that writes `.bru` content. Nothing about "write.go, 939 lines" suggests any of
that. The list does, in one screen.

The reverse also holds: `store/bru/bru.go` is 975 lines and every declaration in
it parses `.bru`. Size decides nothing in either direction.

### Move declarations with the AST, not with text

`tools/gosplit` moves named top-level declarations between files:

```
go run ./tools/gosplit -src <from> -dst <to> -names A,B,C -header "<doc comment>"
```

It exists because text manipulation failed at this five separate times, each
time producing code that still compiled. An identifier appears in roles a
pattern cannot distinguish: a struct field whose name matches its type, a method
named for its own receiver type, a `var` block header whose body is left behind,
and — the one that decided it — the string `imp` matching *inside* a line, which
deleted `hash := ""` from an unrelated URL parser. `gosplit` asks `go/ast` where
a declaration begins and ends and copies those bytes.

Two properties are worth knowing:

- **`-verify` proves reassembly is byte-identical.** It passes on all 383 Go
  files in the repo. `tools/gosplit/main_test.go` pins each construct that
  broke an earlier version — including a signature returning
  `[]map[string]interface{}`, where the first `{` is in the return type and not
  the body. The tool written to fix a class of bug shipped with a fresh instance
  of that same bug; the test is there because of it.
- **Conservation is the check text could never offer.** Count declarations in
  the source before, count both files after, and the sum must match. Run it on
  every move.

Grouped `var` and `const` blocks move as one unit, because package-level
initialisation order follows **filename** sort order within a package — the one
class of error here that compiles and passes tests.

### The frontend: App.svelte is not a stalled extraction

`src/App.svelte` is ~10,700 lines and looks like the obvious next target. The
plan for this work assumed the lever was the one the repo had already used
successfully — move pure logic into plain `.ts` modules under `src/lib/` with
`node --test` coverage, leaving the `.svelte` file as a thin reactive shell.

**That lever is very nearly exhausted, and the number is worth knowing before
anyone budgets a project around it.** `frontend/scripts/scanScript.mjs` parses
the `<script>` block with the TypeScript compiler API and reports which
top-level functions are free of component state:

```
cd frontend && node scripts/scanScript.mjs          # summary
node scripts/scanScript.mjs --list                  # the pure ones, by size
node scripts/scanScript.mjs --why <name>            # what binds one function
```

Of 618 top-level functions, **585 are component-bound** — 461 touch `$state`
directly and 375 call a Wails binding. The analysis is deliberately
conservative (any identifier collision counts, including a local shadowing a
state name), but that is not what makes the number small: relaxing the strictest
rule would reclaim 15 functions. The ~40 `src/lib/` modules that already exist
took the extractable logic, and a scan of what is left mostly returns one-line
delegations to those modules — which is a component shell doing its job, not
debt.

So the remaining size is markup (~3,600 lines) and genuinely reactive code.
Reducing it means splitting out child components — props, bindings and event
flow — which is a different risk profile with no equivalent of the compiler-
verified safety net the Go moves had. `svelte-check` reports 0/0 and is the
main guard; treat that as a floor, not a proof.

### Then run the gates

`qa/` holds the checks the ordinary build does not make — see `qa/README.md`.
Three matter when moving code:

- `qa/bindings.sh` — the 188-method surface must not change by accident.
- `qa/mutation.sh` — path-anchored; a moved file makes entries report
  `NOT FOUND` rather than silently passing. Run the **whole** catalogue after a
  move, not just the entries you touched.
- `.golangci.yml` — its exclusions are anchored to exact paths and stop
  applying silently when code moves.

Cross-language contracts are anchored by path too, and there is no compiler to
catch them: `frontend/test/goMirrors.test.mts` and `nativeMenu.test.mts` read Go
source files directly. Repoint them, then prove the contract still binds by
breaking the Go side and watching the frontend suite fail.
