<script lang="ts">
  // US-036 — the Runner panel, lifted out of App.svelte so its markup is not in
  // the initial chunk. Imported dynamically from inside the
  // {:else if activeView === 'runner'} branch, so it loads the first time a
  // user opens the runner and never for anyone who does not.
  //
  // First of the four VIEW panels. DevTools cannot be done this way: its branch
  // is a single {@render devToolsPanel()} and the markup lives in a Svelte 5
  // snippet elsewhere in App.svelte.
  import type { types } from '../../../wailsjs/go/models'

  // Bindable: the delay field writes back to App.svelte.
  export let runnerDelayMs: number
  export let runnerBailOnFailure: boolean
  export let runnerIterations: number
  export let runnerDataFile: string
  export let chooseRunnerDataFile: () => void
  export let normalizedRunnerIterations: (value: number) => number

  export let state: types.AppState
  export let busy: string
  export let activeCollectionRun: { collectionName: string } | undefined
  export let collectionRunCancellationRequested: boolean
  export let runnerCancelledCount: number
  export let runnerCompletedCancelled: boolean
  export let runnerConfigItems: types.RequestItem[]
  export let runnerSelectedCount: number
  export let runnerItemSelected: (id: string) => boolean
  export let setRunnerItemSelected: (id: string, selected: boolean) => void
  export let toggleRunnerSelectAll: () => void
  export let normalizedRunnerDelayMs: (value: number) => number
  export let resetRunnerConfiguration: () => void
  export let runCollection: () => void
  export let cancelCollectionRun: () => Promise<void> | void
</script>

        <section class="panel">
          <header class="panel-header">
            <h2>Runner</h2>
            <div class="runner-header-actions">
              {#if activeCollectionRun}
                <div class="runner-live-status" role="status" aria-live="polite" aria-atomic="true">
                  <span>{collectionRunCancellationRequested ? 'Cancelling run' : 'Running'}: {activeCollectionRun.collectionName}</span>
                  <button
                    type="button"
                    class="command-cancel"
                    data-testid="runner-cancel-button"
                    aria-label={collectionRunCancellationRequested
                      ? `Cancelling collection run: ${activeCollectionRun.collectionName}`
                      : `Cancel collection run: ${activeCollectionRun.collectionName}`}
                    on:click={() => void cancelCollectionRun()}
                    disabled={collectionRunCancellationRequested}
                  >
                    {collectionRunCancellationRequested ? 'Cancelling run…' : 'Cancel run'}
                  </button>
                </div>
              {/if}
              <button data-testid="runner-run-button" on:click={runCollection} disabled={runnerSelectedCount === 0 || busy !== '' || Boolean(activeCollectionRun)}>
              Run {runnerSelectedCount} Request{runnerSelectedCount === 1 ? '' : 's'}
              </button>
            </div>
          </header>
          <div class="runner-workbench">
            <aside class="runner-config-panel" data-testid="runner-config-panel">
              <div class="runner-config-header">
                <strong data-testid="runner-config-counter">{runnerSelectedCount} of {runnerConfigItems.length} selected</strong>
                <div class="button-row compact">
                  <button type="button" data-testid="runner-select-all" on:click={toggleRunnerSelectAll}>
                    {runnerSelectedCount === runnerConfigItems.length ? 'Deselect All' : 'Select All'}
                  </button>
                  <button type="button" data-testid="runner-config-reset" on:click={resetRunnerConfiguration}>Reset</button>
                </div>
              </div>
              <label class="runner-delay-field">
                <span class="field-label">Delay between requests (ms)</span>
                <input
                  data-testid="runner-delay-input"
                  type="number"
                  min="0"
                  max="600000"
                  value={runnerDelayMs}
                  on:input={(event) => (runnerDelayMs = normalizedRunnerDelayMs(Number(event.currentTarget.value)))}
                />
              </label>
              <label class="runner-delay-field">
                <span class="field-label">Iterations</span>
                <input
                  data-testid="runner-iterations-input"
                  type="number"
                  min="1"
                  max="200"
                  value={runnerIterations}
                  on:input={(event) => (runnerIterations = normalizedRunnerIterations(Number(event.currentTarget.value)))}
                />
              </label>
              <div class="runner-delay-field">
                <span class="field-label">Data file (CSV or JSON)</span>
                <div class="button-row compact">
                  <button type="button" data-testid="runner-data-file-choose" on:click={chooseRunnerDataFile}>Choose…</button>
                  {#if runnerDataFile}
                    <button type="button" data-testid="runner-data-file-clear" on:click={() => (runnerDataFile = '')}>Clear</button>
                  {/if}
                </div>
                {#if runnerDataFile}
                  <small data-testid="runner-data-file-name">{runnerDataFile}</small>
                  <small class="muted">One iteration per row; the row count sets the iteration count.</small>
                {/if}
              </div>
              <label class="checkbox-line">
                <input type="checkbox" data-testid="runner-bail-input" bind:checked={runnerBailOnFailure} />
                Stop at the first failure
              </label>
              <div class="runner-request-list">
                {#if runnerConfigItems.length === 0}
                  <div class="empty-state compact">No runnable requests</div>
                {:else}
                  {#each runnerConfigItems as item (item.id)}
                    <label class="runner-request-item" data-testid="runner-request-item">
                      <span class="checkbox-container"><input type="checkbox" checked={runnerItemSelected(item.id)} on:change={(event) => setRunnerItemSelected(item.id, event.currentTarget.checked)} /></span>
                      <span>
                        <strong>{item.name}</strong>
                        <small>{item.method || 'GET'} {item.folderPath || 'Collection'}</small>
                      </span>
                    </label>
                  {/each}
                {/if}
              </div>
            </aside>
            <div class="runner-results">
              <div class="runner-summary">
                <span>Total {state.runner.total}</span>
                <span class="ok">Passed {state.runner.passed}</span>
                <span class="bad">Failed {state.runner.failed}</span>
                {#if activeCollectionRun}
                  <span class="warning">{collectionRunCancellationRequested ? 'Cancellation requested' : 'Run active'}</span>
                {:else if runnerCompletedCancelled}
                  <span class="warning">Cancelled {runnerCancelledCount || 'run'}</span>
                {/if}
                <span>Skipped {state.runner.skipped}</span>
                {#if state.runner.iterations}
                  <span data-testid="runner-iteration-summary">Iterations {state.runner.completedIterations ?? 0} of {state.runner.iterations}</span>
                {/if}
              </div>
              <table>
                <thead><tr>{#if state.runner.iterations}<th>Iter</th>{/if}<th>Name</th><th>Status</th><th>Code</th><th>Time</th><th>Error</th></tr></thead>
                <tbody>
                  {#each state.runner.results ?? [] as result, index (index)}
                    <tr class:runner-result-cancelled={result.status === 'cancelled'}>{#if state.runner.iterations}<td>{result.iteration ?? ''}</td>{/if}<td>{result.name}</td><td>{result.status === 'cancelled' ? 'Cancelled' : result.status}</td><td>{result.code}</td><td>{result.durationMs} ms</td><td>{result.error}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </div>
        </section>
