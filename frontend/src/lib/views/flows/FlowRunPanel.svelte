<script lang="ts">
  // Running a flow, and what came back.
  //
  // THIS READS LIKE THE RESPONSE PANE, NOT LIKE A NEW SCREEN. A flow run is the
  // same event as a Send — a request went out, something came back, some checks
  // held or did not — so it borrows the response area's vocabulary: .ok and
  // .bad for the verdict, a status code and a duration on the summary line, the
  // panel's own table for the rows. The only new idiom is the per-step chip,
  // and it exists because a flow has something a single request does not: steps
  // that have not happened yet.
  //
  // A FAILED RUN HAS TO NAME THE STEP THAT STOPPED IT, and it says so three
  // times over, on purpose. The banner carries the backend's sentence (which
  // names the step); the failing card is the only one with the failed chip; and
  // that card alone is marked "stopped the run", with every card below it
  // sitting at Pending. Fail-fast means the steps after the failure produced
  // nothing at all, and a report that simply omitted them would read as a flow
  // that is shorter than the one the user wrote.
  import {
    flowInputFields,
    flowInputPayload,
    flowOutputRows,
    flowRunRows,
    flowRunSummary,
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
    /** Disabled for any reason the whole tab is busy. */
    busy: boolean
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

  const fields = $derived(flowInputFields(flow, inputValues))
  const missingRequired = $derived(missingRequiredFlowInputs(fields))
  const rows = $derived(flowRunRows(flow, requests, result, progress))
  const summary = $derived(flowRunSummary(result))
  const outputs = $derived(flowOutputRows(flow, result))
  // A flow with no id has never been saved, so there is nothing on disk for the
  // backend to run. Everything else — a missing required input, a step naming a
  // deleted request — is left to the backend, whose refusal names the field.
  const unsaved = $derived(!flow?.id)
</script>

<section class="flow-run" aria-label="Run flow">
  <header class="flow-run-header">
    <h3>Run</h3>
    <button
      type="button"
      class="primary"
      data-testid="flow-run-button"
      disabled={busy || running || unsaved}
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
            disabled={busy || running}
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

  {#if rows.length === 0}
    <div class="empty-state">This flow has no steps yet.</div>
  {:else}
    <ol class="flow-run-steps" aria-label="Flow steps">
      {#each rows as row (row.stepId)}
        <li class="flow-run-step" class:flow-run-step-stopper={row.stoppedRun} data-testid="flow-run-step">
          <div class="flow-run-step-head">
            <span class={`flow-chip flow-chip-${row.state}`} data-testid="flow-step-chip">{flowStepStateLabel(row.state)}</span>
            <strong>{row.position}. {row.stepId}</strong>
            <span class="muted flow-run-step-request">
              {#if row.method}<span class="method" data-method={row.method}>{row.method}</span>{/if}
              {row.requestLabel}
            </span>
            <span class="flow-run-step-metrics">
              {#if row.statusLabel}<span data-testid="flow-step-status">{row.statusLabel}</span>{/if}
              {#if row.durationLabel}<span>{row.durationLabel}</span>{/if}
            </span>
          </div>

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
        </li>
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

  .flow-run-steps {
    display: grid;
    gap: var(--space-8);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .flow-run-step {
    display: grid;
    gap: var(--space-6);
    padding: var(--space-10);
    border: 1px solid var(--border);
    border-radius: var(--radius-6);
    background: var(--surface);
  }

  /* The one card the eye should land on in a failed run. A left rule rather
     than a red fill: the card still has to be readable, and a tinted block of
     assertion detail is not. */
  .flow-run-step-stopper {
    border-color: var(--danger-border);
    border-left: 3px solid var(--danger-strong);
    background: var(--danger-bg-soft);
  }

  .flow-run-step-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-8);
  }

  .flow-run-step-request {
    display: inline-flex;
    align-items: center;
    gap: var(--space-6);
    font-size: var(--font-size-12);
    min-width: 0;
  }

  .flow-run-step-metrics {
    display: inline-flex;
    gap: var(--space-8);
    margin-left: auto;
    color: var(--muted-strong);
    font-family: var(--code-font-family);
    font-size: var(--font-size-11);
  }

  /* Four chips, one grammar. Pending is deliberately the quietest: in a long
     flow most chips are pending most of the time, and a tray of loud grey
     badges would drown the one chip that is moving. */
  .flow-chip {
    display: inline-flex;
    align-items: center;
    padding: 1px var(--space-8);
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface-alt);
    color: var(--muted-strong);
    font-size: var(--font-size-11);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.02em;
  }

  .flow-chip-running {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--accent-strong);
  }

  .flow-chip-passed {
    border-color: color-mix(in srgb, var(--accent) 40%, transparent);
    background: var(--success-bg);
    color: var(--accent-strong);
  }

  .flow-chip-failed {
    border-color: var(--danger-border);
    background: var(--danger-bg);
    color: var(--danger-strong);
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
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

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
