<script lang="ts">
  // US-036 — the Generate Documentation dialog, lifted out of App.svelte so its
  // markup is not in the initial chunk. App.svelte imports this dynamically
  // from inside the {#if} that gates it; a static import would leave it in the
  // initial graph and save nothing.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  export let activeCollection: types.Collection
  export let generateDocsFolderCount: number
  export let generateDocsRequestCount: number
  export let generateDocsEnvironments: Array<{ id: string; name: string; color?: string }>
  export let generateDocsSelectedEnvIds: string[]
  export let generateDocsSelectedCount: number
  export let busy: string
  export let formattedCollectionVersion: (version: string | undefined) => string
  export let toggleGenerateDocsSelectAll: (checked: boolean) => void
  export let toggleGenerateDocsEnvironment: (id: string, checked: boolean) => void
  export let generateCollectionDocs: () => void
  export let cancelGenerateDocsModal: () => void
  // bind:this cannot cross a component boundary directly, so the element is
  // bound here and surfaced as a bindable prop. App.svelte keeps its existing
  // `generateDocsSelectAllInput` reference via bind:selectAllInput, so the
  // indeterminate-state logic there is untouched.
  export let selectAllInput: HTMLInputElement | null = null

  // What an environment with no colour set is drawn as.
  //
  // This list used to answer #64748b (grey) and the environment editor answers
  // #2f8cff (blue), for the identical state — so the same uncoloured
  // environment had two different "no colour yet" appearances one screen apart.
  // The editor's answer wins because it is the consequential one: its value is
  // pre-filled into a <input type="color">, so blue is what actually gets saved
  // the moment anyone touches that control. The swatch here now predicts that
  // rather than contradicting it.
  //
  // Still a literal rather than a token because the other site is that colour
  // input, which takes a hex attribute and cannot take a var(). Extracting the
  // pair into one shared constant needs an App.svelte edit; see the handoff.
  const defaultEnvironmentColor = '#2f8cff'
</script>

<Modal labelledBy="generate-docs-title" onClose={cancelGenerateDocsModal} testId="generate-docs-modal" size="medium" busy={busy !== ''}>
      <form on:submit|preventDefault={generateCollectionDocs}>
        <header>
          <h2 id="generate-docs-title">Generate Documentation</h2>
          <IconButton icon="close" label="Close" onclick={cancelGenerateDocsModal} />
        </header>
        <div class="generate-docs-content">
          <h3 data-testid="generate-docs-heading">Interactive API Documentation</h3>
          <p>Generate a standalone HTML file that can be hosted anywhere or shared with your team.</p>
          <ul class="generate-docs-features">
            <li>Standalone HTML file - no server required</li>
            <li>Interactive API playground</li>
            <li>Host on any static file server</li>
          </ul>
          <div class="generate-docs-card">
            <div class="version-info" data-testid="version-info">
              <div class="version-line">
                <span class="version-label">Collection Version:</span>
                <span class="version-value" data-testid="version-value">{formattedCollectionVersion(activeCollection.version)}</span>
              </div>
              <p class="version-summary" data-testid="version-summary">{generateDocsFolderCount} {generateDocsFolderCount === 1 ? 'Folder' : 'Folders'} • {generateDocsRequestCount} {generateDocsRequestCount === 1 ? 'request' : 'requests'}</p>
            </div>
            {#if generateDocsEnvironments.length > 0}
              <div class="card-divider"></div>
              <div class="env-section-header">
                <div class="env-section-heading">
                  <h4 class="env-section-title" data-testid="env-section-title">Environments to Include</h4>
                  <span class="env-section-count" data-testid="env-selected-count">({generateDocsSelectedCount}/{generateDocsEnvironments.length} selected)</span>
                </div>
                <label class="env-select-all">
                  <input
                    bind:this={selectAllInput}
                    type="checkbox"
                    data-testid="env-select-all"
                    checked={generateDocsSelectedCount === generateDocsEnvironments.length}
                    on:change={(event) => toggleGenerateDocsSelectAll(event.currentTarget.checked)}
                  />
                  <span data-testid="env-select-all-label">Select All</span>
                </label>
              </div>
              <div class="env-list">
                {#each generateDocsEnvironments as env (env.id)}
                  <label class="env-row" data-testid="env-row">
                    <input
                      type="checkbox"
                      data-testid={`env-select-${env.id}`}
                      checked={generateDocsSelectedEnvIds.includes(env.id)}
                      on:change={(event) => toggleGenerateDocsEnvironment(env.id, event.currentTarget.checked)}
                    />
                    <span class="env-color" style={`background: ${env.color || defaultEnvironmentColor}`}></span>
                    <span>{env.name}</span>
                  </label>
                {/each}
              </div>
            {/if}
          </div>
          <p class="generate-docs-note">The generated file loads OpenCollection's JavaScript and CSS files from a CDN, which requires an internet connection.</p>
        </div>
        <div class="button-row">
          <button type="button" data-testid="generate-docs-cancel" on:click={cancelGenerateDocsModal}>Cancel</button>
          <button class="primary" type="submit" data-testid="generate-docs-generate" disabled={busy !== ''}>Generate</button>
        </div>
      </form>
</Modal>
