# .ralph — orchestration working directory

Scratch and evidence state for the improvement program defined in `../improvement_v2.md`.
See §4.1 of that document for the file contract.

| Path | Written by | Contents |
|---|---|---|
| `plans/US-XXX.md` | Planner | One implementation plan per story. Written once, not per round. |
| `reviews/US-XXX-round-N.md` | Synthesise | Verdict (`PASS`/`FAIL`) + ranked blocking findings with `file:line`, plus pasted gate output. |
| `reviews/US-XXX-bench.txt` | Worker | `go test -bench` output for Phase 1 stories. |
| `reviews/US-XXX-bundle.txt` | Worker | `dist` sizes for stories that change the bundle. |
| `blocked/US-XXX.md` | Orchestrator | Escalation after 3 failed review rounds. Story stays `passes: false`. |
| `backlog.md` | Synthesise | Non-blocking nits. Never gates a commit. |
| `baseline/bench.txt` | US-005 | Benchmark baseline. Phase 1 stories compare against it (>5% regression fails). |
| `baseline/bundle.txt` | US-036 | Bundle-size baseline (start: 1.16 MB JS + 114 KB CSS). |
| `baseline/*.png` | US-005 / QA | Reference screenshots for visual diffing. |

`prd.json` and `progress.txt` live at the repo root, where Ralph expects them.
