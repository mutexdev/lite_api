<script lang="ts">
  // The Flow tab: the definition on the left, the run on the right.
  //
  // NO GRAPH CANVAS, and that is a decision rather than an omission. A flow is
  // a LIST — steps run in order, each one reading what the last produced — and
  // a canvas would spend the whole pane drawing the one edge that a vertical
  // list already draws for free, while making "what runs third" a question you
  // answer by following arrows. The shape that fits is the shape the runner
  // already uses: configuration on one side, results on the other.
  //
  // THE DRAFT IS LOCAL AND IS NOT RE-SYNCED FROM THE PROP. App.svelte wraps
  // this component in a {#key} on the flow id, so switching flows remounts it
  // and picks the saved definition up fresh; within one flow, state refreshes
  // arrive constantly (every send, every watcher tick) and re-deriving the
  // draft from any of them would delete whatever the user had half-typed.
  //
  // VALIDATION IS THE BACKEND'S. Nothing here refuses a save: the flow goes as
  // typed, and internal/core/flows.go answers with a sentence that names the
  // step and the field. Those sentences are the error message, verbatim — see
  // flowSaveErrorMessage.
  import { untrack } from 'svelte'
  import FlowStepEditor from './FlowStepEditor.svelte'
  import FlowRunPanel from './FlowRunPanel.svelte'
  import {
    blankFlowStepDraft,
    flowDraftFrom,
    flowFromDraft,
    moveFlowStep,
    type FlowDraft,
    type FlowRunProgress,
    type FlowStepDraft,
  } from '../../flowView'
  import type { types } from '../../../../wailsjs/go/models'

  type Props = {
    collection: types.Collection
    flow: types.Flow
    busy: boolean
    /** The backend's refusal of the last save, verbatim. */
    saveError: string
    running: boolean
    result: types.FlowRunResult | undefined
    progress: FlowRunProgress | undefined
    runError: string
    onSave: (flow: types.Flow) => void | Promise<void>
    onDelete: (flow: types.Flow) => void | Promise<void>
    onRun: (inputs: Record<string, string>) => void | Promise<void>
  }

  let {
    collection,
    flow,
    busy,
    saveError,
    running,
    result,
    progress,
    runError,
    onSave,
    onDelete,
    onRun,
  }: Props = $props()

  // untrack() states the intent the compiler otherwise warns about: this reads
  // the prop ONCE, at mount, and the draft is the user's from then on. The
  // warning ("did you mean a derived?") is asking for exactly the behaviour
  // this component must not have — see the note above about half-typed edits.
  let draft = $state<FlowDraft>(untrack(() => flowDraftFrom(flow)))

  const requests = $derived(collection.items ?? [])
  const saved = $derived(JSON.stringify(flowFromDraft(flowDraftFrom(flow))))
  const dirty = $derived(JSON.stringify(flowFromDraft(draft)) !== saved)

  function updateStep(index: number, next: FlowStepDraft) {
    const steps = [...draft.steps]
    steps[index] = next
    draft = { ...draft, steps }
  }
</script>

<section class="panel flow-panel" aria-label={`Flow ${draft.name || flow.id}`}>
  <header class="panel-header">
    <div>
      <h2>{draft.name || 'Untitled flow'}</h2>
      <p class="panel-subtitle">{collection.name} · {draft.steps.length} step{draft.steps.length === 1 ? '' : 's'}</p>
    </div>
    <div class="button-row compact">
      <button
        type="button"
        class="primary"
        data-testid="flow-save-button"
        disabled={busy || !dirty}
        onclick={() => void onSave(flowFromDraft(draft))}
      >{dirty ? 'Save' : 'Saved'}</button>
      <button
        type="button"
        class="danger-button"
        data-testid="flow-delete-button"
        disabled={busy}
        onclick={() => void onDelete(flow)}
      >Delete flow</button>
    </div>
  </header>

  {#if saveError}
    <!-- The backend's own sentence. It names the step, the field and what to
         set it to; a house-styled "Could not save this flow" would throw away
         the only part worth reading. -->
    <div class="error-banner" role="alert" data-testid="flow-save-error">{saveError}</div>
  {/if}

  <div class="flow-workbench">
    <div class="flow-definition">
      <div class="field-grid flow-meta">
        <span class="field-label">Name</span>
        <input
          aria-label="Flow name"
          data-testid="flow-name-input"
          value={draft.name}
          disabled={busy}
          oninput={(event) => (draft = { ...draft, name: event.currentTarget.value })}
        />
        <span class="field-label">Description</span>
        <input
          aria-label="Flow description"
          placeholder="What this chain does"
          value={draft.description}
          disabled={busy}
          oninput={(event) => (draft = { ...draft, description: event.currentTarget.value })}
        />
      </div>

      <section class="flow-section" aria-label="Flow inputs">
        <div class="flow-section-header">
          <h3>Inputs</h3>
          <small class="muted">What the caller supplies when the flow runs.</small>
          <button
            type="button"
            data-testid="flow-add-input"
            disabled={busy}
            onclick={() => (draft = { ...draft, inputs: [...draft.inputs, { name: '', required: false, description: '' }] })}
          >Add input</button>
        </div>
        {#if draft.inputs.length === 0}
          <div class="empty-state compact">No inputs</div>
        {:else}
          <div class="flow-rows">
            {#each draft.inputs as input, index (index)}
              <div class="flow-row flow-row-input">
                <input
                  aria-label={`Input ${index + 1} name`}
                  placeholder="storeCode"
                  value={input.name}
                  disabled={busy}
                  oninput={(event) => {
                    const inputs = [...draft.inputs]
                    inputs[index] = { ...inputs[index], name: event.currentTarget.value }
                    draft = { ...draft, inputs }
                  }}
                />
                <label class="checkbox-line">
                  <input
                    type="checkbox"
                    aria-label={`Input ${index + 1} required`}
                    checked={input.required}
                    disabled={busy}
                    onchange={(event) => {
                      const inputs = [...draft.inputs]
                      inputs[index] = { ...inputs[index], required: event.currentTarget.checked }
                      draft = { ...draft, inputs }
                    }}
                  />
                  Required
                </label>
                <input
                  aria-label={`Input ${index + 1} description`}
                  placeholder="Store short code, e.g. DHK-04"
                  value={input.description}
                  disabled={busy}
                  oninput={(event) => {
                    const inputs = [...draft.inputs]
                    inputs[index] = { ...inputs[index], description: event.currentTarget.value }
                    draft = { ...draft, inputs }
                  }}
                />
                <button
                  type="button"
                  class="icon-button"
                  aria-label={`Remove input ${index + 1}`}
                  disabled={busy}
                  onclick={() => (draft = { ...draft, inputs: draft.inputs.filter((_, position) => position !== index) })}
                >×</button>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <section class="flow-section" aria-label="Flow steps">
        <div class="flow-section-header">
          <h3>Steps</h3>
          <small class="muted">Run in order; each step can use what earlier ones extracted.</small>
          <button
            type="button"
            data-testid="flow-add-step"
            disabled={busy}
            onclick={() => (draft = { ...draft, steps: [...draft.steps, blankFlowStepDraft(draft.steps.map((step) => step.id))] })}
          >Add step</button>
        </div>
        {#if draft.steps.length === 0}
          <div class="empty-state compact">A flow runs at least one request. Add a step.</div>
        {:else}
          <div class="flow-step-list">
            {#each draft.steps as step, index (index)}
              <FlowStepEditor
                {step}
                {index}
                stepCount={draft.steps.length}
                {requests}
                disabled={busy}
                onChange={(next) => updateStep(index, next)}
                onMove={(delta) => (draft = { ...draft, steps: [...moveFlowStep(draft.steps, index, delta)] })}
                onRemove={() => (draft = { ...draft, steps: draft.steps.filter((_, position) => position !== index) })}
              />
            {/each}
          </div>
        {/if}
      </section>

      <section class="flow-section" aria-label="Flow outputs">
        <div class="flow-section-header">
          <h3>Outputs</h3>
          <small class="muted">What the flow hands back when it finishes.</small>
          <button
            type="button"
            data-testid="flow-add-output"
            disabled={busy}
            onclick={() => (draft = { ...draft, outputs: [...draft.outputs, { name: '', value: '' }] })}
          >Add output</button>
        </div>
        {#if draft.outputs.length === 0}
          <div class="empty-state compact">No outputs</div>
        {:else}
          <div class="flow-rows">
            {#each draft.outputs as output, index (index)}
              <div class="flow-row flow-row-output">
                <input
                  aria-label={`Output ${index + 1} name`}
                  placeholder="terminalId"
                  value={output.name}
                  disabled={busy}
                  oninput={(event) => {
                    const outputs = [...draft.outputs]
                    outputs[index] = { ...outputs[index], name: event.currentTarget.value }
                    draft = { ...draft, outputs }
                  }}
                />
                <input
                  aria-label={`Output ${index + 1} value`}
                  placeholder="{'{{terminalId}}'}"
                  value={output.value}
                  disabled={busy}
                  oninput={(event) => {
                    const outputs = [...draft.outputs]
                    outputs[index] = { ...outputs[index], value: event.currentTarget.value }
                    draft = { ...draft, outputs }
                  }}
                />
                <button
                  type="button"
                  class="icon-button"
                  aria-label={`Remove output ${index + 1}`}
                  disabled={busy}
                  onclick={() => (draft = { ...draft, outputs: draft.outputs.filter((_, position) => position !== index) })}
                >×</button>
              </div>
            {/each}
          </div>
        {/if}
      </section>
    </div>

    <aside class="flow-run-column">
      <FlowRunPanel
        flow={flow}
        {requests}
        {dirty}
        {running}
        {busy}
        {result}
        {progress}
        {runError}
        {onRun}
      />
    </aside>
  </div>
</section>

<style>
  .flow-panel {
    display: grid;
    gap: var(--space-12);
    align-content: start;
  }

  /* The same two-column split the Runner uses, and for the same reason: the
     thing you change and the thing it produces have to be on screen together.
     Definition first because it is the wider of the two. */
  .flow-workbench {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(320px, 420px);
    gap: var(--space-16);
    align-items: start;
  }

  .flow-definition {
    display: grid;
    gap: var(--space-14);
    min-width: 0;
  }

  .flow-run-column {
    padding: var(--space-12);
    border: 1px solid var(--border);
    border-radius: var(--radius-8);
    background: var(--surface-soft);
    min-width: 0;
  }

  .flow-meta {
    max-width: 620px;
    margin-bottom: 0;
  }

  .flow-section {
    display: grid;
    gap: var(--space-8);
  }

  .flow-section-header {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-8);
  }

  .flow-section-header h3 {
    margin: 0;
  }

  .flow-section-header button {
    margin-left: auto;
  }

  .flow-step-list,
  .flow-rows {
    display: grid;
    gap: var(--space-8);
  }

  .flow-row {
    display: grid;
    gap: var(--space-6);
    align-items: center;
  }

  .flow-row-input {
    grid-template-columns: minmax(0, 1fr) auto minmax(0, 2fr) auto;
  }

  .flow-row-output {
    grid-template-columns: minmax(0, 1fr) minmax(0, 2fr) auto;
  }

  @media (max-width: 1180px) {
    .flow-workbench {
      grid-template-columns: minmax(0, 1fr);
    }

    .flow-row-input,
    .flow-row-output {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
