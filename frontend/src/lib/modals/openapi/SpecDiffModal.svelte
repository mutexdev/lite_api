<script lang="ts">
  // US-036 / US-025 — the Spec Diff dialog, extracted from App.svelte so its
  // markup is not in the initial chunk.
  //
  // App.svelte imports this with a dynamic import() inside the {#if} that gates
  // the dialog, so the code loads the first time a user opens Spec Diff and
  // never for anyone who does not. A static import would have kept it in the
  // initial bundle no matter where the component file lived — moving markup to
  // a new file is not, by itself, code splitting.
  import type { main } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  export let openAPISpecDiffResult: main.OpenAPISyncSpecDiffResult
  export let openAPISpecDiffChangeCount: number
  export let openAPISpecDiffActiveChangeIndex: number
  export let openAPISyncSpecDiffSummary: (result: main.OpenAPISyncSpecDiffResult) => string
  export let openAPISpecDiffLineIsActive: (lineIndex: number, line: main.OpenAPISyncSpecDiffLine) => boolean
  export let goOpenAPISpecDiffChange: (direction: number) => void
  export let closeOpenAPISyncSpecDiff: () => void
</script>

<Modal labelledBy="openapi-spec-diff-title" onClose={closeOpenAPISyncSpecDiff} dialogClass="prompt-dialog openapi-spec-diff-dialog" testId="openapi-spec-diff-modal">
      <header>
        <h2 id="openapi-spec-diff-title">Spec Diff</h2>
        <button type="button" class="icon-button" title="Close" on:click={closeOpenAPISyncSpecDiff}>x</button>
      </header>
      <div class="openapi-spec-meta">
        {#if openAPISpecDiffResult.sourceUrl}
          <span data-testid="openapi-spec-diff-source">{openAPISpecDiffResult.sourceUrl}</span>
        {/if}
        <small data-testid="openapi-spec-diff-summary">{openAPISyncSpecDiffSummary(openAPISpecDiffResult)}</small>
        {#if openAPISpecDiffResult.noStoredSpec}
          <small>No stored spec file found. Comparing against an empty current spec.</small>
        {/if}
      </div>
      <div class="openapi-spec-diff-badges" aria-label="Endpoint change summary">
        <span class="openapi-spec-diff-badge added">{openAPISpecDiffResult.added ?? 0} New in Spec</span>
        <span class="openapi-spec-diff-badge changed">{openAPISpecDiffResult.updated ?? 0} Updated in Spec</span>
        <span class="openapi-spec-diff-badge removed">{openAPISpecDiffResult.removed ?? 0} Removed from Spec</span>
      </div>
      <div class="openapi-spec-diff-toolbar" data-testid="openapi-spec-diff-navigation">
        <button type="button" data-testid="openapi-spec-diff-previous" on:click={() => goOpenAPISpecDiffChange(-1)} disabled={openAPISpecDiffChangeCount === 0 || openAPISpecDiffActiveChangeIndex === 0}>Previous</button>
        <span data-testid="openapi-spec-diff-change-counter">
          {openAPISpecDiffChangeCount > 0 ? `${openAPISpecDiffActiveChangeIndex + 1} / ${openAPISpecDiffChangeCount} changes` : '0 changes'}
        </span>
        <button type="button" data-testid="openapi-spec-diff-next" on:click={() => goOpenAPISpecDiffChange(1)} disabled={openAPISpecDiffChangeCount === 0 || openAPISpecDiffActiveChangeIndex >= openAPISpecDiffChangeCount - 1}>Next</button>
      </div>
      <div class="openapi-spec-diff-grid" data-testid="openapi-spec-diff-content">
        <div class="openapi-spec-diff-heading">Current Spec</div>
        <div class="openapi-spec-diff-heading">Updated Spec</div>
        {#each openAPISpecDiffResult.lines ?? [] as line, lineIndex (lineIndex)}
          <div class={`openapi-spec-diff-cell ${line.kind}`} class:active-change={openAPISpecDiffLineIsActive(lineIndex, line)} data-testid="openapi-spec-diff-current-line" data-openapi-spec-diff-line-index={lineIndex}>
            <span class="openapi-spec-diff-line-number">{line.oldNumber || ''}</span>
            <code>{line.oldText ?? ''}</code>
          </div>
          <div class={`openapi-spec-diff-cell ${line.kind}`} class:active-change={openAPISpecDiffLineIsActive(lineIndex, line)} data-testid="openapi-spec-diff-updated-line" data-openapi-spec-diff-line-index={lineIndex}>
            <span class="openapi-spec-diff-line-number">{line.newNumber || ''}</span>
            <code>{line.newText ?? ''}</code>
          </div>
        {/each}
      </div>
      <div class="button-row">
        <button type="button" data-testid="openapi-spec-diff-close" on:click={closeOpenAPISyncSpecDiff}>Close</button>
      </div>
</Modal>
