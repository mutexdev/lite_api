# Deferred nits

Non-blocking findings that reviewers surfaced but that did not block a story's `PASS` verdict.
Synthesise appends here; nothing in this file gates a commit.

Format:

```
- [US-XXX] `file:line` — finding. (raised by R<n>, round <k>)
```

---

<!-- appended by Synthesise -->

## Phase 0

- [US-003] `app.go` — 60 `ST1005` fixes lowercased **user-facing GUI strings** to satisfy Go's
  error-string convention. Defensible as Go style, arguably a UI downgrade. Two judgement calls to
  re-examine: `"Yaak imports are not supported yet"` was rephrased rather than lowercasing a proper
  noun (same for Swagger 2), and ~20 `"Git ..."` became `"git ..."`. (raised by R1, round 1)

- [US-003] `collection_import.go` — the allowlist of "safe to display" messages was kept in sync by
  the same global replace that changed the strings. Mechanically consistent, but the pairing is
  implicit and a future string edit could silently desynchronise it. Worth an explicit test.
  (raised by R1, round 1)

- [US-003] Worker's excluded-count arithmetic (28 for staticcheck) differs by 5 from the 23
  measured by the orchestrator's unconfigured negative-control run. Immaterial to the verdict —
  configured run is 0 issues and the unconfigured 25 are fully accounted for — but unexplained.
  (raised by R1, round 1)

- [US-004] `App.svelte` — 21 unused bindings are evidence of **unwired features**, not dead
  cosmetics: `exportText`, `gitVersionText`, `gitCloneOutput`, `shareCollectionResult` and
  `generateDocsResult` are each assigned from a Wails call and never rendered, meaning Share
  Collection, Generate Docs, Export and Git Clone/Version compute results the user never sees.
  Never-called handlers with live bindings: `openCollection`, `connectGitRemote`,
  `openSelectedGitCollections`, `toggleGitCandidate`. **Owned by US-025** — decide per item whether
  to wire up or delete. (raised by R1, round 1)

- [US-004] `no-useless-assignment` is disabled repo-wide (21 violations). Re-enable and re-assess
  after **US-029** converts `App.svelte`'s 110 `$:` statements to runes — the rule's blind spot is
  specifically the pre-runes reactive model. (raised by R1, round 1)

- [US-005] `bench_test.go` uses `b.N` loops; gopls suggests `b.Loop()` (Go 1.24+), which also
  prevents dead-code elimination of the benchmark body. Kept `b.N` for now because it composes
  predictably with `-benchtime=Nx`. Reconsider when the baseline is regenerated — changing it later
  invalidates comparability with the committed baseline, so change it **before** Phase 1 starts or
  not at all. (raised by R1, round 1)

- [US-007] Wails **v2.13.0** exists. This program pins **v2.12.0** deliberately, because that is the
  version every QA result in `docs/` was produced on and jumping a minor would invalidate that
  evidence for no stated benefit. A v2.13.0 bump is a legitimate follow-up **with its own QA pass**.
  (raised by R1, round 1)

- [QA/computer-use] **Top toolbar does not lay out at 1200px viewport.** Found by driving the
  running app at `http://localhost:34115` (Playwright), measured via `getBoundingClientRect` and
  `scrollWidth > clientWidth`, not eyeballed:
  * `Development` (env selector) ends at x=666 and `Cookies` starts at x=655 — **11px overlap
    between sibling elements**, visible as collided text.
  * Three labels are clipped: `My Workspace`, `Sample API`, `R7 Persist`. The breadcrumb
    `Sample API / R7 Persist` is compressed into 80px (x=716..796).
  Reference capture: `.ralph/baseline/phase0-main-window.png`.
  Not owned by any existing story. Closest is **US-037** (spacing/type tokens), but this is a
  responsive-overflow defect rather than a token migration — likely wants its own story.
  (raised by R3, Phase 0)

- [QA/computer-use] `GET http://localhost:34115/favicon.ico` returns **404** on every load — the
  only console error in a clean session. Cosmetic, but it means "zero console errors" cannot be
  used as a QA assertion until it is fixed. (raised by R3, Phase 0)

- [general] `improvement_v2.md` §5.1 prescribes `du -b`, which is a GNU coreutils flag and fails on
  macOS. Use `stat -f "%z %N"` locally, or `wc -c`. (raised by R1, round 1)

- [general] §5.1's ">5% regression on any benchmark" gate is not sound across hosts. A baseline
  captured on a developer laptop compared against a CI runner will produce both false failures and
  false passes. `.ralph/baseline/bench.txt` must record host and Go version, and comparisons must be
  same-host. (raised by R1, round 1)

