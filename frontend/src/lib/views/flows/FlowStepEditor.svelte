<script lang="ts">
  // One step of a flow, as a card.
  //
  // A STEP IS FOUR QUESTIONS AND THEY ARE ASKED IN RUN ORDER: which request,
  // what does it get told (vars), what do we keep from its answer (extract),
  // and what has to be true about that answer (assert). Rearranging them into,
  // say, alphabetical tabs would break the only reading that makes a chain
  // legible — you read a flow top to bottom because that is how it runs.
  //
  // The three lists are plain rows rather than the app's KeyValueTable: that
  // component is bound to types.KeyValue and carries enable/disable and bulk
  // edit, none of which a flow step has. The row shape below is the same
  // visual grammar without the machinery.
  import {
    blankFlowAssertDraft,
    flowRequestOptions,
    flowSelectedRequestLabel,
    type FlowRequestOption,
    type FlowStepDraft,
  } from '../../flowView'
  import type { types } from '../../../../wailsjs/go/models'

  type Props = {
    step: FlowStepDraft
    index: number
    stepCount: number
    requests: readonly types.RequestItem[]
    disabled: boolean
    onChange: (step: FlowStepDraft) => void
    onMove: (delta: number) => void
    onRemove: () => void
  }

  let { step, index, stepCount, requests, disabled, onChange, onMove, onRemove }: Props = $props()

  const options = $derived<FlowRequestOption[]>(flowRequestOptions(requests))
  // A step can name a request that has since been deleted. The select would
  // then render blank and read as "nothing chosen", which is the one thing it
  // is not — so the missing id gets an option of its own, and says so.
  const selectedIsMissing = $derived(
    Boolean(step.requestId) && !options.some((option) => option.id === step.requestId),
  )

  function patch(changes: Partial<FlowStepDraft>) {
    onChange({ ...step, ...changes })
  }
</script>

<article class="flow-step" data-testid="flow-step">
  <header class="flow-step-header">
    <span class="flow-step-position" aria-hidden="true">{index + 1}</span>
    <label class="flow-step-id">
      <span class="field-label">Step id</span>
      <input
        aria-label={`Step ${index + 1} id`}
        value={step.id}
        {disabled}
        oninput={(event) => patch({ id: event.currentTarget.value })}
      />
    </label>
    <label class="flow-step-request">
      <span class="field-label">Request</span>
      <select
        aria-label={`Step ${index + 1} request`}
        value={step.requestId}
        {disabled}
        onchange={(event) => patch({ requestId: event.currentTarget.value })}
      >
        <option value="">Choose a request…</option>
        {#if selectedIsMissing}
          <option value={step.requestId}>{flowSelectedRequestLabel(step.requestId, requests)}</option>
        {/if}
        {#each options as option (option.id)}
          <option value={option.id}>{option.label}</option>
        {/each}
      </select>
    </label>
    <div class="button-row compact flow-step-actions">
      <button
        type="button"
        title="Move step earlier"
        aria-label={`Move step ${index + 1} earlier`}
        disabled={disabled || index === 0}
        onclick={() => onMove(-1)}
      >↑</button>
      <button
        type="button"
        title="Move step later"
        aria-label={`Move step ${index + 1} later`}
        disabled={disabled || index === stepCount - 1}
        onclick={() => onMove(1)}
      >↓</button>
      <button
        type="button"
        class="danger-button"
        aria-label={`Remove step ${index + 1}`}
        {disabled}
        onclick={onRemove}
      >Remove</button>
    </div>
  </header>

  <section class="flow-step-section" aria-label={`Step ${index + 1} variables`}>
    <div class="flow-step-section-header">
      <strong>Vars</strong>
      <!-- Says what a var IS, because it is the one field on this card whose
           scope is not obvious: it resolves against the flow, never against the
           environment, which is what keeps a step var from standing in for a
           secret. -->
      <small class="muted">Values for this step, resolved from inputs and earlier extractions.</small>
      <button type="button" {disabled} onclick={() => patch({ vars: [...step.vars, { name: '', value: '' }] })}>Add var</button>
    </div>
    {#if step.vars.length > 0}
      <div class="flow-rows">
        {#each step.vars as variable, varIndex (varIndex)}
          <div class="flow-row flow-row-pair">
            <input
              aria-label={`Step ${index + 1} var ${varIndex + 1} name`}
              placeholder="name"
              value={variable.name}
              {disabled}
              oninput={(event) => {
                const vars = [...step.vars]
                vars[varIndex] = { ...vars[varIndex], name: event.currentTarget.value }
                patch({ vars })
              }}
            />
            <input
              aria-label={`Step ${index + 1} var ${varIndex + 1} value`}
              placeholder="{'{{storeId}}'}"
              value={variable.value}
              {disabled}
              oninput={(event) => {
                const vars = [...step.vars]
                vars[varIndex] = { ...vars[varIndex], value: event.currentTarget.value }
                patch({ vars })
              }}
            />
            <button
              type="button"
              class="icon-button"
              aria-label={`Remove step ${index + 1} var ${varIndex + 1}`}
              {disabled}
              onclick={() => patch({ vars: step.vars.filter((_, position) => position !== varIndex) })}
            >×</button>
          </div>
        {/each}
      </div>
    {/if}
  </section>

  <section class="flow-step-section" aria-label={`Step ${index + 1} extractions`}>
    <div class="flow-step-section-header">
      <strong>Extract</strong>
      <small class="muted">Names taken from this step's response into the flow.</small>
      <button
        type="button"
        {disabled}
        onclick={() => patch({ extract: [...step.extract, { name: '', from: 'body', path: '' }] })}
      >Add extraction</button>
    </div>
    {#if step.extract.length > 0}
      <div class="flow-rows">
        {#each step.extract as extract, extractIndex (extractIndex)}
          <div class="flow-row flow-row-extract">
            <input
              aria-label={`Step ${index + 1} extraction ${extractIndex + 1} name`}
              placeholder="name"
              value={extract.name}
              {disabled}
              oninput={(event) => {
                const next = [...step.extract]
                next[extractIndex] = { ...next[extractIndex], name: event.currentTarget.value }
                patch({ extract: next })
              }}
            />
            <select
              aria-label={`Step ${index + 1} extraction ${extractIndex + 1} source`}
              value={extract.from}
              {disabled}
              onchange={(event) => {
                const next = [...step.extract]
                next[extractIndex] = { ...next[extractIndex], from: event.currentTarget.value }
                patch({ extract: next })
              }}
            >
              <option value="body">body</option>
              <option value="header">header</option>
              <option value="status">status</option>
            </select>
            <!-- Status takes no path, so the box is disabled rather than
                 hidden: a field that vanishes when a neighbouring select
                 changes makes the row jump under the cursor. -->
            <input
              aria-label={`Step ${index + 1} extraction ${extractIndex + 1} path`}
              placeholder={extract.from === 'header' ? 'Header name' : '$.data.store.id'}
              value={extract.path}
              disabled={disabled || extract.from === 'status'}
              oninput={(event) => {
                const next = [...step.extract]
                next[extractIndex] = { ...next[extractIndex], path: event.currentTarget.value }
                patch({ extract: next })
              }}
            />
            <button
              type="button"
              class="icon-button"
              aria-label={`Remove step ${index + 1} extraction ${extractIndex + 1}`}
              {disabled}
              onclick={() => patch({ extract: step.extract.filter((_, position) => position !== extractIndex) })}
            >×</button>
          </div>
        {/each}
      </div>
    {/if}
  </section>

  <section class="flow-step-section" aria-label={`Step ${index + 1} assertions`}>
    <div class="flow-step-section-header">
      <strong>Assert</strong>
      <small class="muted">A failed assertion stops the flow here.</small>
      <button
        type="button"
        {disabled}
        onclick={() => patch({ assert: [...step.assert, blankFlowAssertDraft()] })}
      >Add assertion</button>
    </div>
    {#if step.assert.length > 0}
      <div class="flow-rows">
        {#each step.assert as assertion, assertIndex (assertIndex)}
          {@const update = (changes: Partial<typeof assertion>) => {
            const next = [...step.assert]
            next[assertIndex] = { ...next[assertIndex], ...changes }
            patch({ assert: next })
          }}
          <div class="flow-row flow-row-assert">
            <select
              aria-label={`Step ${index + 1} assertion ${assertIndex + 1} type`}
              value={assertion.kind}
              {disabled}
              onchange={(event) => update({ kind: event.currentTarget.value as 'status' | 'body' })}
            >
              <option value="status">status</option>
              <option value="body">body</option>
            </select>

            {#if assertion.kind === 'status'}
              <select
                aria-label={`Step ${index + 1} assertion ${assertIndex + 1} check`}
                value={assertion.statusCheck}
                {disabled}
                onchange={(event) => update({ statusCheck: event.currentTarget.value as 'equals' | 'in' })}
              >
                <option value="equals">equals</option>
                <option value="in">in</option>
              </select>
              {#if assertion.statusCheck === 'in'}
                <input
                  aria-label={`Step ${index + 1} assertion ${assertIndex + 1} status list`}
                  placeholder="200, 201"
                  value={assertion.statusIn}
                  {disabled}
                  oninput={(event) => update({ statusIn: event.currentTarget.value })}
                />
              {:else}
                <input
                  aria-label={`Step ${index + 1} assertion ${assertIndex + 1} status`}
                  placeholder="200"
                  inputmode="numeric"
                  value={assertion.statusEquals}
                  {disabled}
                  oninput={(event) => update({ statusEquals: event.currentTarget.value })}
                />
              {/if}
            {:else}
              <input
                aria-label={`Step ${index + 1} assertion ${assertIndex + 1} path`}
                placeholder="$.terminal.state"
                value={assertion.path}
                {disabled}
                oninput={(event) => update({ path: event.currentTarget.value })}
              />
              <select
                aria-label={`Step ${index + 1} assertion ${assertIndex + 1} check`}
                value={assertion.bodyCheck}
                {disabled}
                onchange={(event) => update({ bodyCheck: event.currentTarget.value as 'equals' | 'contains' | 'exists' })}
              >
                <option value="equals">equals</option>
                <option value="contains">contains</option>
                <option value="exists">exists</option>
              </select>
              <input
                aria-label={`Step ${index + 1} assertion ${assertIndex + 1} value`}
                placeholder={assertion.bodyCheck === 'exists' ? 'no value needed' : 'created'}
                value={assertion.bodyValue}
                disabled={disabled || assertion.bodyCheck === 'exists'}
                oninput={(event) => update({ bodyValue: event.currentTarget.value })}
              />
            {/if}

            <button
              type="button"
              class="icon-button"
              aria-label={`Remove step ${index + 1} assertion ${assertIndex + 1}`}
              {disabled}
              onclick={() => patch({ assert: step.assert.filter((_, position) => position !== assertIndex) })}
            >×</button>
          </div>
        {/each}
      </div>
    {/if}
  </section>
</article>

<style>
  /* Every value below is an existing token. A step card is a panel-inside-a-
     panel, so it borrows the surface the runner's config aside uses rather
     than introducing a third background. */
  .flow-step {
    display: grid;
    gap: var(--space-10);
    padding: var(--space-12);
    border: 1px solid var(--border);
    border-radius: var(--radius-8);
    background: var(--surface-soft);
  }

  .flow-step-header {
    display: grid;
    grid-template-columns: auto minmax(120px, 1fr) minmax(160px, 2fr) auto;
    gap: var(--space-8);
    align-items: end;
  }

  /* The ordinal is decoration for the eye, not information for a reader: the
     step's id is the label, and the position is already conveyed by the order
     of the cards. Hence aria-hidden on the element itself. */
  .flow-step-position {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 22px;
    height: 22px;
    margin-bottom: var(--space-4);
    border-radius: 999px;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: var(--font-size-11);
    font-weight: 700;
  }

  .flow-step-id,
  .flow-step-request {
    display: grid;
    gap: var(--space-4);
    min-width: 0;
  }

  .flow-step-actions {
    margin-bottom: var(--space-2);
  }

  .flow-step-section {
    display: grid;
    gap: var(--space-6);
    padding-top: var(--space-8);
    border-top: 1px solid var(--border-subtle);
  }

  .flow-step-section-header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-8);
  }

  /* The Add button is pushed to the trailing edge so the three sections'
     buttons line up in one column, which is what makes the card scannable. */
  .flow-step-section-header button {
    margin-left: auto;
  }

  .flow-rows {
    display: grid;
    gap: var(--space-6);
  }

  .flow-row {
    display: grid;
    gap: var(--space-6);
    align-items: center;
  }

  .flow-row-pair {
    grid-template-columns: minmax(0, 1fr) minmax(0, 2fr) auto;
  }

  .flow-row-extract {
    grid-template-columns: minmax(0, 1fr) 100px minmax(0, 2fr) auto;
  }

  .flow-row-assert {
    grid-template-columns: 100px minmax(0, 1fr) minmax(0, 1fr) auto;
  }

  @media (max-width: 1180px) {
    .flow-step-header,
    .flow-row-extract,
    .flow-row-assert,
    .flow-row-pair {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
