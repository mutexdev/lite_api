<script lang="ts">
  // US-036 — the Runner panel, lifted out of App.svelte so its markup is not in
  // the initial chunk. Imported dynamically from inside the
  // {:else if activeView === 'runner'} branch, so it loads the first time a
  // user opens the runner and never for anyone who does not.
  //
  // First of the four VIEW panels. DevTools cannot be done this way: its branch
  // is a single {@render devToolsPanel()} and the markup lives in a Svelte 5
  // snippet elsewhere in App.svelte.
  //
  // A8-03 — the results were a bare `<table>` with no colour, no filter, no
  // expansion and no export, which made the Runner the least legible of the
  // app's four run-results surfaces AND the one where it matters most: this is
  // where forty requests go out at once and the only question is which of them
  // failed and why. The Error column was a plain cell, so a stack of long
  // messages either wrapped the table into unreadability or was cut off.
  //
  // It is a list of RunResultRow now, the same anatomy History and the response
  // Timeline use. The table went because it was never really tabular — the last
  // column was prose, and prose does not belong in a grid cell that has to stay
  // the same width as forty of its neighbours.
  import FindBar from '../ui/FindBar.svelte'
  import PageHeader from '../ui/PageHeader.svelte'
  import PaneToolbar from '../ui/PaneToolbar.svelte'
  import RunResultRow from '../RunResultRow.svelte'
  import { formatDurationMs, formatStatusCode } from '../formatting'
  import { outcomeTone, statusTone } from '../statusTone'
  import { runResultMatches, runResultSearchText } from '../runResults'
  import type { types } from '../../../wailsjs/go/models'

  type Props = {
    // Bindable: the delay field writes back to App.svelte.
    runnerDelayMs: number
    runnerBailOnFailure: boolean
    runnerIterations: number
    runnerDataFile: string
    chooseRunnerDataFile: () => void
    normalizedRunnerIterations: (value: number) => number

    state: types.AppState
    busy: string
    activeCollectionRun: { collectionName: string } | undefined
    collectionRunCancellationRequested: boolean
    runnerCancelledCount: number
    runnerCompletedCancelled: boolean
    runnerConfigItems: types.RequestItem[]
    runnerSelectedCount: number
    runnerItemSelected: (id: string) => boolean
    setRunnerItemSelected: (id: string, selected: boolean) => void
    toggleRunnerSelectAll: () => void
    normalizedRunnerDelayMs: (value: number) => number
    resetRunnerConfiguration: () => void
    runCollection: () => void
    cancelCollectionRun: () => Promise<void> | void
  }

  let {
    runnerDelayMs = $bindable(),
    runnerBailOnFailure = $bindable(),
    runnerIterations = $bindable(),
    runnerDataFile = $bindable(),
    chooseRunnerDataFile,
    normalizedRunnerIterations,
    state: runnerState,
    busy,
    activeCollectionRun,
    collectionRunCancellationRequested,
    runnerCancelledCount,
    runnerCompletedCancelled,
    runnerConfigItems,
    runnerSelectedCount,
    runnerItemSelected,
    setRunnerItemSelected,
    toggleRunnerSelectAll,
    normalizedRunnerDelayMs,
    resetRunnerConfiguration,
    runCollection,
    cancelCollectionRun,
  }: Props = $props()

  let resultQuery = $state('')
  let onlyFailures = $state(false)
  let expandedKey = $state('')
  let copyStatus = $state('')

  // The index is part of the key because a data-file run repeats the same
  // request once per row, so itemId alone is not unique within one run and two
  // iterations of the same request would expand together.
  const rows = $derived(
    (runnerState.runner.results ?? []).map((result, index) => {
      const outcome = result.status === 'cancelled' ? 'Cancelled' : result.status
      return {
        key: `${index}:${result.itemId}`,
        result,
        // A8-01 — the status CELL is graded by the shared HTTP bucketing, the
        // badge by the run's own verdict, and they are allowed to disagree: a
        // 200 that failed an assertion is a green code on a failed row, which
        // is the true story and the one a single colour cannot tell.
        tone: statusTone(result.code),
        badge: { label: outcome, tone: outcomeTone(result.status) },
        searchText: runResultSearchText([result.name, outcome, result.code, result.error, result.iteration]),
      }
    })
  )

  const filter = $derived({ query: resultQuery, onlyFailures })
  const visibleRows = $derived(rows.filter((row) => runResultMatches({ tone: outcomeTone(row.result.status), searchText: row.searchText }, filter)))

  function toggle(key: string) {
    expandedKey = expandedKey === key ? '' : key
  }

  /**
   * Export, which the Runner had none of and the Timeline has had all along.
   *
   * The clipboard rather than a file dialog, deliberately: a save dialog needs a
   * backend binding routed through App.svelte, and the thing people actually do
   * with a failed run is paste it into a ticket or a chat. It exports what is on
   * SCREEN — the filtered rows — because a user who has just narrowed forty
   * results down to the three that failed wants those three, not all forty back
   * again.
   */
  async function copyResults() {
    try {
      await navigator.clipboard.writeText(JSON.stringify(visibleRows.map((row) => row.result), null, 2))
      copyStatus = 'Copied'
    } catch {
      copyStatus = 'Clipboard unavailable'
    }
  }
</script>

<section class="panel">
  <PageHeader title="Runner">
    {#snippet meta()}
      <!-- Which collection is running is a live fact about the view, so it
           reads in the meta slot that truncates first; the cancel it belongs
           to is an action and sits with the other one. The live region is
           still created with the run, as it was, so the announcement fires on
           the transition to "Cancelling run". -->
      {#if activeCollectionRun}
        <span class="runner-live-status" role="status" aria-live="polite" aria-atomic="true">
          {collectionRunCancellationRequested ? 'Cancelling run' : 'Running'}: {activeCollectionRun.collectionName}
        </span>
      {/if}
    {/snippet}
    {#snippet actions()}
      {#if activeCollectionRun}
        <button
          type="button"
          class="command-cancel"
          data-testid="runner-cancel-button"
          aria-label={collectionRunCancellationRequested
            ? `Cancelling collection run: ${activeCollectionRun.collectionName}`
            : `Cancel collection run: ${activeCollectionRun.collectionName}`}
          onclick={() => void cancelCollectionRun()}
          disabled={collectionRunCancellationRequested}
        >
          {collectionRunCancellationRequested ? 'Cancelling run…' : 'Cancel run'}
        </button>
      {/if}
      <button data-testid="runner-run-button" onclick={runCollection} disabled={runnerSelectedCount === 0 || busy !== '' || Boolean(activeCollectionRun)}>
        Run {runnerSelectedCount} Request{runnerSelectedCount === 1 ? '' : 's'}
      </button>
    {/snippet}
  </PageHeader>
  <div class="runner-workbench">
    <aside class="runner-config-panel" data-testid="runner-config-panel">
      <div class="runner-config-header">
        <strong data-testid="runner-config-counter">{runnerSelectedCount} of {runnerConfigItems.length} selected</strong>
        <div class="button-row compact">
          <button type="button" data-testid="runner-select-all" onclick={toggleRunnerSelectAll}>
            {runnerSelectedCount === runnerConfigItems.length ? 'Deselect All' : 'Select All'}
          </button>
          <button type="button" data-testid="runner-config-reset" onclick={resetRunnerConfiguration}>Reset</button>
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
          oninput={(event) => (runnerDelayMs = normalizedRunnerDelayMs(Number(event.currentTarget.value)))}
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
          oninput={(event) => (runnerIterations = normalizedRunnerIterations(Number(event.currentTarget.value)))}
        />
      </label>
      <div class="runner-delay-field">
        <span class="field-label">Data file (CSV or JSON)</span>
        <div class="button-row compact">
          <button type="button" data-testid="runner-data-file-choose" onclick={chooseRunnerDataFile}>Choose…</button>
          {#if runnerDataFile}
            <button type="button" data-testid="runner-data-file-clear" onclick={() => (runnerDataFile = '')}>Clear</button>
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
              <span class="checkbox-container"><input type="checkbox" checked={runnerItemSelected(item.id)} onchange={(event) => setRunnerItemSelected(item.id, event.currentTarget.checked)} /></span>
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
        <span>Total {runnerState.runner.total}</span>
        <span class="ok">Passed {runnerState.runner.passed}</span>
        <span class="bad">Failed {runnerState.runner.failed}</span>
        {#if activeCollectionRun}
          <span class="warning">{collectionRunCancellationRequested ? 'Cancellation requested' : 'Run active'}</span>
        {:else if runnerCompletedCancelled}
          <span class="warning">Cancelled {runnerCancelledCount || 'run'}</span>
        {/if}
        <span>Skipped {runnerState.runner.skipped}</span>
        {#if runnerState.runner.iterations}
          <span data-testid="runner-iteration-summary">Iterations {runnerState.runner.completedIterations ?? 0} of {runnerState.runner.iterations}</span>
        {/if}
      </div>

      <PaneToolbar ariaLabel="Run results">
        {#snippet left()}
          <label class="checkbox-line">
            <input type="checkbox" data-testid="runner-failures-filter" bind:checked={onlyFailures} />
            Failures only
          </label>
        {/snippet}
        {#snippet middle()}
          <FindBar
            testId="runner-result-search"
            ariaLabel="Filter run results"
            placeholder="Filter results"
            value={resultQuery}
            total={visibleRows.length}
            noun="results"
            onChange={(next) => (resultQuery = next)}
          />
        {/snippet}
        {#snippet right()}
          <span aria-live="polite" class="runner-copy-status">{copyStatus}</span>
          <button type="button" data-testid="runner-results-copy" onclick={() => void copyResults()} disabled={visibleRows.length === 0}>Copy</button>
        {/snippet}
      </PaneToolbar>

      {#if rows.length === 0}
        <div class="empty-state" data-testid="runner-results-empty">No results yet. Run the selected requests to see them here.</div>
      {:else if visibleRows.length === 0}
        <div class="empty-state" data-testid="runner-results-filtered-empty">No results match this filter.</div>
      {:else}
        <ul class="runner-result-list">
          {#each visibleRows as row (row.key)}
            <RunResultRow
              testId="runner-result-row"
              tone={row.tone}
              status={formatStatusCode(row.result.code, row.result.error)}
              badge={row.badge}
              title={row.result.name}
              subtitle={runnerState.runner.iterations && row.result.iteration ? `Iteration ${row.result.iteration}` : ''}
              metrics={[formatDurationMs(row.result.durationMs) || '0 ms']}
              emphasis={row.result.status === 'failed' ? 'danger' : 'none'}
              expanded={expandedKey === row.key}
              onToggle={row.result.error ? () => toggle(row.key) : undefined}
            >
              {#snippet detail()}
                <!-- The whole message, wrapped, with room for it. In the table
                     this replaced it was a single cell competing for width with
                     five others, so a real error was always the column that
                     lost. -->
                <p class="runner-result-error bad" data-testid="runner-result-error">{row.result.error}</p>
              {/snippet}
            </RunResultRow>
          {/each}
        </ul>
      {/if}
    </div>
  </div>
</section>

<style>
  /* style.css only gives this `min-width: 0` — enough for a single <table>
     child, not for the summary / toolbar / list stack that replaced it. */
  .runner-results {
    display: grid;
    gap: var(--space-10);
    align-content: start;
    min-width: 0;
  }

  .runner-result-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-8);
    overflow-y: auto;
    min-width: 0;
  }

  .runner-result-error {
    margin: 0;
    font-size: var(--font-size-12);
    font-weight: 400;
    overflow-wrap: anywhere;
  }

  .runner-copy-status {
    color: var(--muted);
    font-size: var(--font-size-11);
    white-space: nowrap;
  }

  /* Was `.runner-live-status > span` in style.css, styling a span inside a flex
     wrapper that no longer exists — the wrapper was only there to hold the
     cancel button beside the text, and the cancel button is an action now. */
  .runner-live-status {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 210px;
    color: var(--warning-text);
    font-size: var(--font-size-11);
    font-weight: 800;
  }
</style>
