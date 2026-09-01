<script lang="ts">
  // Running a flow, and what came back.
  //
  // THIS READS LIKE THE RESPONSE PANE, NOT LIKE A NEW SCREEN. A flow run is the
  // same event as a Send — a request went out, something came back, some checks
  // held or did not — so it borrows the response area's vocabulary: .ok and
  // .bad for the verdict, a status code and a duration on the summary line, the
  // panel's own table for the rows. The one idiom a single response does not
  // need is the per-step chip, because a flow has something a request does not:
  // steps that have not happened yet. That chip is no longer Flow's alone — see
  // A8-03 below.
  //
  // A FAILED RUN HAS TO NAME THE STEP THAT STOPPED IT, and it says so three
  // times over, on purpose. The banner carries the backend's sentence (which
  // names the step); the failing card is the only one with the failed chip; and
  // that card alone is marked "stopped the run", with every card below it
  // sitting at Pending. Fail-fast means the steps after the failure produced
  // nothing at all, and a report that simply omitted them would read as a flow
  // that is shorter than the one the user wrote.
  //
  // A8-03 — the step cards used to be always-open: assertions, extracted values
  // and the error all on screen for every step at once, so a ten-step flow
  // produced a page you had to scroll to find the one step that broke. They are
  // behind the same expander History and the response Timeline use now, via the
  // shared RunResultRow, with ONE exception that keeps the rule above intact:
  // the step that stopped the run opens by itself. A report that hides the
  // failure behind a click would be a report that makes you hunt for it.
  import FindBar from '../../ui/FindBar.svelte'
  import PaneToolbar from '../../ui/PaneToolbar.svelte'
  import RunResultRow from '../../RunResultRow.svelte'
  import { formatStatusCode } from '../../formatting'
  import { statusTone } from '../../statusTone'
  import { runResultMatches } from '../../runResults'
  import {
    flowInputFields,
    flowInputPayload,
    flowOutputRows,
    flowRunRows,
    flowRunSummary,
    flowStepBadgeTone,
    flowStepStateLabel,
    missingRequiredFlowInputs,
    type FlowRunProgress,
  } from '../../flowView'
  import type { types } from '../../../../wailsjs/go/models'

  type Props = {
    flow: types.Flow | undefined
    requests: readonly types.RequestItem[]
    /** The definition on disk differs from what is in the editor. */
    dirty: boolean
    running: boolean
    /**
     * A8-04 — the app-wide convention: the NAME of the operation in flight, or
     * '' for none. This was a boolean here and a string in every sibling panel,
     * and App.svelte collapsed the string at the call site just to feed it.
     */
    busy: string
    result: types.FlowRunResult | undefined
    progress: FlowRunProgress | undefined
    /** A rejected RunFlow call — the backend refused before any step ran. */
    runError: string
    onRun: (inputs: Record<string, string>) => void | Promise<void>
  }

  let { flow, requests, dirty, running, busy, result, progress, runError, onRun }: Props = $props()

  // Keyed by input NAME rather than by position, so renaming an input in the
  // editor does not move a typed value onto a different field.
  let inputValues = $state<Record<string, string>>({})

  let stepQuery = $state('')
  let onlyFailures = $state(false)
  // null means "the user has not chosen yet", which is NOT the same as '' —
  // see stepOpen below, where the step that stopped the run opens on its own
  // until the user picks a different one or closes it.
  let expandedStepId = $state<string | null>(null)

  const disabled = $derived(busy !== '')
  const fields = $derived(flowInputFields(flow, inputValues))
  const missingRequired = $derived(missingRequiredFlowInputs(fields))
  const rows = $derived(flowRunRows(flow, requests, result, progress))
  const summary = $derived(flowRunSummary(result))
  const outputs = $derived(flowOutputRows(flow, result))
  // A flow with no id has never been saved, so there is nothing on disk for the
  // backend to run. Everything else — a missing required input, a step naming a
  // deleted request — is left to the backend, whose refusal names the field.
  const unsaved = $derived(!flow?.id)

  const filter = $derived({ query: stepQuery, onlyFailures })
  const visibleRows = $derived(
    rows.filter((row) => runResultMatches({ tone: row.state === 'failed' ? 'danger' : 'idle', searchText: row.searchText }, filter))
  )

  function stepOpen(row: (typeof rows)[number]) {
    // Once the user has touched a row their choice wins outright: clicking a
    // different step is a decision, and a report that kept the failure pinned
    // open underneath would be arguing with it.
    return expandedStepId === null ? row.stoppedRun : expandedStepId === row.stepId
  }

  function toggleStep(row: (typeof rows)[number]) {
    expandedStepId = stepOpen(row) ? '' : row.stepId
  }
</script>

<section class="flow-run" aria-label="Run flow">
  <header class="flow-run-header">
    <h3>Run</h3>
    <button
      type="button"
      class="primary"
      data-testid="flow-run-button"
      disabled={disabled || running || unsaved}
      onclick={() => void onRun(flowInputPayload(fields))}
    >{running ? 'Running…' : 'Run flow'}</button>
  </header>

  {#if unsaved}
    <p class="muted flow-run-note">Save the flow before running it.</p>
  {:else if dirty}
    <!-- Not a blocker. The run executes what is SAVED, and saying so is more
         useful than refusing: the user may well want to run the last good
         version while editing the next one. -->
    <p class="muted flow-run-note" data-testid="flow-run-dirty">Unsaved edits are not part of this run.</p>
  {/if}

  {#if fields.length > 0}
    <div class="field-grid flow-run-inputs">
      {#each fields as field (field.name)}
        <span class="field-label">
          {field.name}{#if field.required}<em class="flow-required" title="Required">*</em>{/if}
        </span>
        <div class="flow-run-input">
          <input
            aria-label={`Flow input ${field.name}`}
            aria-required={field.required}
            data-testid="flow-input"
            placeholder={field.description || ''}
            value={field.value}
            disabled={disabled || running}
            oninput={(event) => (inputValues = { ...inputValues, [field.name]: event.currentTarget.value })}
          />
          {#if field.description}<small class="muted">{field.description}</small>{/if}
        </div>
      {/each}
    </div>
    {#if missingRequired.length > 0}
      <p class="muted flow-run-note" data-testid="flow-run-missing">
        Required: {missingRequired.join(', ')}
      </p>
    {/if}
  {/if}

  {#if runError}
    <!-- The backend's sentence, unaltered. It is the only description of a
         refusal that happened before any step ran. -->
    <div class="error-banner" role="alert" data-testid="flow-run-error">{runError}</div>
  {/if}

  {#if summary}
    <div class="flow-run-summary" data-testid="flow-run-summary" role="status" aria-live="polite">
      <strong class={summary.tone}>{summary.headline}</strong>
      {#if summary.detail}<span data-testid="flow-run-summary-detail">{summary.detail}</span>{/if}
    </div>
  {/if}

  {#if rows.length > 0}
    <PaneToolbar ariaLabel="Flow step results">
      {#snippet left()}
        <label class="checkbox-line">
          <input type="checkbox" data-testid="flow-failures-filter" bind:checked={onlyFailures} />
          Failures only
        </label>
      {/snippet}
      {#snippet middle()}
        <FindBar
          testId="flow-step-search"
          ariaLabel="Filter flow steps"
          placeholder="Filter steps"
          value={stepQuery}
          total={visibleRows.length}
          noun="steps"
          onChange={(next) => (stepQuery = next)}
        />
      {/snippet}
    </PaneToolbar>
  {/if}

  {#if rows.length === 0}
    <div class="empty-state">This flow has no steps yet.</div>
  {:else if visibleRows.length === 0}
    <div class="empty-state" data-testid="flow-steps-filtered-empty">No steps match this filter.</div>
  {:else}
    <ol class="flow-run-steps" aria-label="Flow steps">
      {#each visibleRows as row (row.stepId)}
        <RunResultRow
          testId="flow-run-step"
          statusTestId="flow-step-status"
          badgeTestId="flow-step-chip"
          tone={statusTone(row.status)}
          status={formatStatusCode(row.status, row.error)}
          badge={{ label: flowStepStateLabel(row.state), tone: flowStepBadgeTone(row.state) }}
          method={row.method}
          title={`${row.position}. ${row.stepId}`}
          subtitle={row.requestLabel}
          metrics={row.durationLabel ? [row.durationLabel] : []}
          emphasis={row.stoppedRun ? 'danger' : 'none'}
          expanded={stepOpen(row)}
          onToggle={row.error || row.stoppedRun || row.assertions.length > 0 || row.extracted.length > 0
            ? () => toggleStep(row)
            : undefined}
        >
          {#snippet detail()}
            {#if row.stoppedRun}
              <p class="flow-run-stopper-note" data-testid="flow-run-stopper">This step stopped the run.</p>
            {/if}

            {#if row.error}
              <p class="flow-run-step-error bad" data-testid="flow-step-error">{row.error}</p>
            {/if}

            {#if row.assertions.length > 0}
              <ul class="flow-assertions" aria-label={`Assertions for step ${row.stepId}`}>
                {#each row.assertions as assertion, assertionIndex (assertionIndex)}
                  <li data-testid="flow-assertion">
                    <span class={assertion.ok ? 'ok' : 'bad'} aria-hidden="true">{assertion.ok ? '✓' : '✗'}</span>
                    <span class="sr-only">{assertion.ok ? 'passed' : 'failed'}</span>
                    <span>{assertion.detail}</span>
                  </li>
                {/each}
              </ul>
            {/if}

            {#if row.extracted.length > 0}
              <dl class="flow-extracted" aria-label={`Values extracted by step ${row.stepId}`}>
                {#each row.extracted as extracted (extracted.name)}
                  <dt>{extracted.name}</dt>
                  <dd>{extracted.value}</dd>
                {/each}
              </dl>
            {/if}
          {/snippet}
        </RunResultRow>
      {/each}
    </ol>
  {/if}

  {#if outputs.length > 0}
    <section class="flow-run-outputs" aria-label="Flow outputs">
      <h4>Outputs</h4>
      <dl class="flow-extracted">
        {#each outputs as output (output.name)}
          <dt>{output.name}</dt>
          <dd data-testid="flow-output">{output.value}</dd>
        {/each}
      </dl>
    </section>
  {/if}
</section>

<style>
  .flow-run {
    display: grid;
    gap: var(--space-12);
    align-content: start;
  }

  .flow-run-header {
    display: flex;
    align-items: center;
    gap: var(--space-8);
  }

  .flow-run-header h3 {
    margin: 0;
    margin-right: auto;
  }

  .flow-run-note {
    margin: 0;
    font-size: var(--font-size-12);
  }

  .flow-run-inputs {
    max-width: none;
    margin-bottom: 0;
  }

  .flow-run-input {
    display: grid;
    gap: var(--space-4);
    min-width: 0;
  }

  /* The asterisk is the only red on the form. Marking required fields with a
     full danger-coloured label would make an untouched form look like a form
     with errors in it. */
  .flow-required {
    color: var(--danger-strong);
    font-style: normal;
    margin-left: 2px;
  }

  .flow-run-summary {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-8);
    padding: var(--space-8) var(--space-10);
    border: 1px solid var(--border);
    border-radius: var(--radius-6);
    background: var(--surface-soft);
    font-size: var(--font-size-12);
  }

  /* The card, the stopper's left rule and the four chips all moved into
     RunResultRow (A8-03) — they were the shape History and the Runner needed
     and could not reach. What is left here is only what is genuinely Flow's:
     the assertion list and the extracted-value table inside a row's detail. */
  .flow-run-steps {
    display: grid;
    gap: var(--space-8);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .flow-run-stopper-note {
    margin: 0;
    color: var(--danger-strong);
    font-size: var(--font-size-11);
    font-weight: 700;
  }

  .flow-run-step-error {
    margin: 0;
    font-size: var(--font-size-12);
    font-weight: 400;
    overflow-wrap: anywhere;
  }

  .flow-assertions {
    display: grid;
    gap: var(--space-4);
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: var(--font-size-12);
  }

  .flow-assertions li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--space-6);
    overflow-wrap: anywhere;
  }

  /* The tick is aria-hidden and paired with a visually hidden word, so a
     screen reader hears "passed"/"failed" rather than a punctuation mark. */

  .flow-extracted {
    display: grid;
    grid-template-columns: minmax(0, auto) minmax(0, 1fr);
    gap: var(--space-4) var(--space-10);
    margin: 0;
    font-family: var(--code-font-family);
    font-size: var(--font-size-11);
  }

  .flow-extracted dt {
    color: var(--muted-strong);
    font-weight: 700;
  }

  .flow-extracted dd {
    margin: 0;
    overflow-wrap: anywhere;
  }

  .flow-run-outputs {
    display: grid;
    gap: var(--space-6);
    padding-top: var(--space-10);
    border-top: 1px solid var(--border-subtle);
  }

  .flow-run-outputs h4 {
    margin: 0;
  }
</style>
