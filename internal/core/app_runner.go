package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mutexdev/lite_api/internal/runner"
	"github.com/mutexdev/lite_api/internal/scripting"
)

func (a *App) RunCollection(collectionID, environmentID string) (AppState, error) {
	return a.RunCollectionWithOptions(collectionID, environmentID, RunnerOptions{})
}

// appendUnrunRunResults marks every request after a bail as unrun.
//
// A distinct status from "skipped" and "cancelled", which US-047 requires and
// which matters to a reader of the results: "skipped" means the runner decided
// not to run it (wrong protocol, unresolved prompt variables), "cancelled"
// means the user stopped the run, and "unrun" means an earlier failure ended
// the run before this request had a turn. Collapsing them would leave someone
// unable to tell a failing suite from an abandoned one.
// Only the rest of the CURRENT iteration is enumerated. Exploding every
// request of every remaining iteration into rows would bury the one failure
// that matters under thousands of identical placeholders; the snapshot's
// Iterations vs CompletedIterations pair is what reports the iterations that
// never started.
// firstFailedTestResult names the first failed assertion, or "" if none did.
//
// The first rather than a count: a run result has one error line, and the name
// of the assertion that broke is what someone acts on. The full list is still
// on the response.
func firstFailedTestResult(results []TestResult) string {
	for _, result := range results {
		if !result.Passed {
			if strings.TrimSpace(result.Message) == "" {
				return fmt.Sprintf("assertion failed: %s", result.Name)
			}
			return fmt.Sprintf("assertion failed: %s: %s", result.Name, result.Message)
		}
	}
	return ""
}

func appendUnrunRunResults(results []RunResult, items []RequestItem, from, iteration, totalIterations int) []RunResult {
	now := time.Now()
	for i := from; i < len(items); i++ {
		results = append(results, stampIteration(RunResult{
			ItemID: items[i].ID,
			Name:   items[i].Name,
			Status: "unrun",
			Error:  "not run: the run stopped at an earlier failure",
			At:     now,
		}, iteration, totalIterations))
	}
	return results
}

func (a *App) RunCollectionWithOptions(collectionID, environmentID string, options RunnerOptions) (AppState, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return AppState{}, err
	}
	ws, collection, err := a.findCollectionWithWorkspaceLocked(collectionID)
	if err != nil {
		a.mu.Unlock()
		return AppState{}, err
	}
	globalEnvs := append([]Environment(nil), scripting.ActiveGlobalEnvironmentsForWorkspace(*ws)...)
	collectionCopy := *collection
	items := filterRunnerItems(append([]RequestItem(nil), collection.Items...), options.SelectedItemIDs)
	delayMs := normalizeRunnerDelayMs(options.DelayMs)
	// US-024: one lookup hint for this run, discarded when it returns. See
	// runner_lookup_index.go for why it is scoped rather than cached on App.
	lookupIndex := newRunnerLookupIndex(&a.state)
	a.mu.Unlock()
	// items is this run's private, never-mutated copy, so a name→index map over
	// it cannot go stale and needs no verification.
	itemsByName := runnerItemNameIndex(items)
	runContext, finishRun := a.startCancellableCollectionRun(collectionID)
	defer finishRun()

	dataRows, err := runner.DataRows(options.DataFile)
	if err != nil {
		return AppState{}, err
	}
	// US-046. With a data file the row count leads: see runner.IterationPlan for
	// why asking for more iterations than rows is clamped rather than padded.
	totalIterations := runner.IterationPlan(dataRows, options.Iterations)
	results := make([]RunResult, 0, len(items)*totalIterations)
	completedIterations := 0
	// US-045. The labelled loop is what makes "stop the run" mean the RUN and
	// not just this iteration. Every break below is explicitly one or the
	// other, and the difference is behavioural: a cancelled or bailed run must
	// not quietly start iteration 3.
iterations:
	for iteration := 1; iteration <= totalIterations; iteration++ {
		iterationRow := runner.DataRowFor(dataRows, iteration)
		currentRequestIndex := 0
		// jumps resets per iteration because it guards against an infinite
		// setNextRequest cycle WITHIN one pass. Carrying it across iterations
		// would abort a legitimate long run for looping that never happened.
		jumps := 0
		for currentRequestIndex < len(items) {
			item := items[currentRequestIndex]
			if requestContextCancelled(runContext) {
				results = append(results, stampIteration(cancelledRunResult(item), iteration, totalIterations))
				break iterations
			}
			if item.Type != "http" && item.Type != "graphql" && item.Type != "grpc" {
				results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: "skipped", At: time.Now(), Error: "protocol runner is not implemented yet"}, iteration, totalIterations))
				if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
					currentRequestIndex++
					continue
				}
				currentRequestIndex++
				continue
			}
			if prompts := scripting.PromptVariablesForRequest(globalEnvs, &collectionCopy, environmentID, item); len(prompts) > 0 {
				results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: "skipped", At: time.Now(), Error: scripting.RunnerPromptVariableSkipMessage(prompts)}, iteration, totalIterations))
				if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
					currentRequestIndex++
					continue
				}
				currentRequestIndex++
				continue
			}
			state, controls, res, err := a.sendRequestWithControlsContext(runContext, collectionID, item.ID, environmentID, nil, lookupIndex, runner.Iteration{
				Index: iteration,
				Count: totalIterations,
				Data:  iterationRow,
			})
			if err != nil {
				if requestContextCancelled(runContext) {
					results = append(results, stampIteration(cancelledRunResult(item), iteration, totalIterations))
					break iterations
				}
				results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: "failed", Error: err.Error(), At: time.Now()}, iteration, totalIterations))
				if options.BailOnFailure {
					results = appendUnrunRunResults(results, items, currentRequestIndex+1, iteration, totalIterations)
					break iterations
				}
				if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
					currentRequestIndex++
					continue
				}
				currentRequestIndex++
				continue
			}
			// res is the *Response sendRequestWithControlsContext just stored on this
			// item, returned directly instead of being re-found by scanning the
			// state for the item that call had already resolved.
			status := "passed"
			errText := ""
			code := 0
			duration := int64(0)
			if res != nil {
				code = res.Status
				duration = res.DurationMs
				errText = res.Error
				if res.Cancelled {
					status = "cancelled"
				} else if res.Error != "" || res.Status >= 400 {
					status = "failed"
				}
			}
			// US-047. A failed assertion is a failed request. Without this the
			// runner only ever failed on a transport error or a >=400 status,
			// so a collection whose tests all failed against a 200 response
			// reported a fully green run — and BailOnFailure, whose criterion
			// names "a failed assertion", could never trigger on one.
			if status == "passed" && res != nil {
				if failure := firstFailedTestResult(res.TestResults); failure != "" {
					status = "failed"
					errText = failure
				}
			}
			if requestContextCancelled(runContext) {
				status = "cancelled"
				errText = "collection run cancelled"
			}
			if controls.SkipRequest && status != "cancelled" {
				status = "skipped"
			}
			results = append(results, stampIteration(RunResult{ItemID: item.ID, Name: item.Name, Status: status, Code: code, DurationMs: duration, Error: errText, At: time.Now()}, iteration, totalIterations))
			if status == "cancelled" {
				break iterations
			}
			// US-047. Deliberately AFTER the cancelled check: a cancelled run is not
			// a failure, and reporting the remaining requests as "unrun because
			// something failed" would misattribute the user's own cancellation.
			if options.BailOnFailure && status == "failed" {
				results = appendUnrunRunResults(results, items, currentRequestIndex+1, iteration, totalIterations)
				break iterations
			}
			// bru.runner.stopExecution() stops the RUN, not the iteration —
			// that is what the script author asked for.
			if controls.StopExecution {
				break iterations
			}
			if controls.NextRequestSet {
				jumps++
				if jumps > 10000 {
					return state, errors.New("too many jumps, possible infinite loop")
				}
				// setNextRequest(null) ends THIS iteration and lets the next
				// one start, matching Postman/newman. Escalating it to
				// break iterations would silently turn a per-iteration
				// early exit into a whole-run abort.
				if controls.NextRequestName == nil {
					break
				}
				nextRequestIndex := -1
				if index, ok := itemsByName[*controls.NextRequestName]; ok {
					nextRequestIndex = index
				}
				if nextRequestIndex >= 0 {
					if nextRequestIndex != currentRequestIndex {
						currentRequestIndex = nextRequestIndex
						if !sleepRunnerDelay(runContext, delayMs, 0, 2) {
							continue
						}
						continue
					}
					currentRequestIndex = nextRequestIndex
				} else {
					if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
						currentRequestIndex++
						continue
					}
					currentRequestIndex++
				}
				continue
			}
			if !sleepRunnerDelay(runContext, delayMs, currentRequestIndex, len(items)) {
				currentRequestIndex++
				continue
			}
			currentRequestIndex++
		}
		completedIterations = iteration
	}
	if finishRun() || requestContextCancelled(runContext) {
		if len(results) == 0 || results[len(results)-1].Status != "cancelled" {
			results = append(results, RunResult{Name: "Collection run", Status: "cancelled", Error: "collection run cancelled", At: time.Now()})
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot := RunnerSnapshot{Total: len(results), Results: results, Finished: time.Now()}
	if totalIterations > 1 {
		snapshot.Iterations = totalIterations
		snapshot.CompletedIterations = completedIterations
	}
	for _, result := range results {
		switch result.Status {
		case "passed":
			snapshot.Passed++
		case "skipped":
			snapshot.Skipped++
		case "cancelled":
			snapshot.Cancelled++
		case "unrun":
			// US-047. Deliberately counted nowhere. An unrun request did not
			// pass, fail, get skipped or get cancelled — it never ran. Letting
			// it fall through to the default arm would inflate Failed, so a
			// bailed run of 50 requests would report 49 failures when one
			// request failed.
		default:
			snapshot.Failed++
		}
	}
	a.state.Runner = snapshot
	return a.state, a.markDirty(persistScopeState)
}

func filterRunnerItems(items []RequestItem, selectedItemIDs []string) []RequestItem {
	if selectedItemIDs == nil {
		return items
	}
	selected := map[string]bool{}
	for _, id := range selectedItemIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			selected[id] = true
		}
	}
	out := make([]RequestItem, 0, len(items))
	for _, item := range items {
		if selected[item.ID] {
			out = append(out, item)
		}
	}
	return out
}

// runner.IterationLimit caps a run at a size that stays workable.
//
// Every request of every iteration becomes a row in RunnerSnapshot.Results,
// which is persisted in state.json and rendered in full — so the cap is a
// memory and file-size bound, not an arbitrary number. 200 iterations of a
// 100-request collection is already 20,000 rows.
// stampIteration records which iteration a row belongs to.
//
// Only for multi-iteration runs: a single-iteration run leaves Iteration at
// zero so the field stays omitted and the JSON shape is unchanged for every
// consumer that never asked for iterations.
func stampIteration(result RunResult, iteration, totalIterations int) RunResult {
	if totalIterations > 1 {
		result.Iteration = iteration
	}
	return result
}

func normalizeRunnerDelayMs(delayMs int) int {
	if delayMs < 0 {
		return 0
	}
	if delayMs > 600000 {
		return 600000
	}
	return delayMs
}

func cancelledRunResult(item RequestItem) RunResult {
	return RunResult{ItemID: item.ID, Name: item.Name, Status: "cancelled", Error: "collection run cancelled", At: time.Now()}
}

func sleepRunnerDelay(ctx context.Context, delayMs, currentIndex, total int) bool {
	if delayMs <= 0 || currentIndex+1 >= total {
		return !requestContextCancelled(ctx)
	}
	timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
