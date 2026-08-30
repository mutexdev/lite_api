<script lang="ts">
  // US-036 — the Share Collection dialog, lifted out of App.svelte so its
  // markup is not in the initial chunk. Imported dynamically from inside the
  // {#if} that gates it.
  import Modal from '../Modal.svelte'
  import type { types } from '../../../../wailsjs/go/models'

  // Bindable: the format cards assign to this directly, so it has to write back
  // to App.svelte rather than be a one-way prop. App.svelte binds it, which
  // keeps its own reset-to-'zip' logic working unchanged.
  export let shareCollectionFormat: string
  export let shareCollectionUnsupportedTypes: string[]
  export let busy: string
  export let shareCollectionProceed: () => void
  export let cancelShareCollectionModal: () => void
  // The export result, once there is one.
  //
  // The backend has been filling in `warning` and `skippedTypes` — dropped
  // collection and folder headers, assertions Postman cannot express, request
  // types it has no concept of — and the modal threw the whole result away and
  // closed. The user got a file that was quietly missing things, and the only
  // hint was the pre-export guess below, which walks top-level items only and
  // so misses a gRPC request inside a folder entirely.
  export let shareCollectionResult: types.CollectionExportResult | undefined = undefined
</script>

<Modal labelledBy="share-collection-title" onClose={cancelShareCollectionModal} dialogClass="prompt-dialog share-collection-dialog" testId="share-collection-modal">
      <form on:submit|preventDefault={shareCollectionProceed}>
        <header>
          <h2 id="share-collection-title">Share Collection</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelShareCollectionModal}>x</button>
        </header>
        <div class="share-collection-content">
          <p>Bruno-compatible collection exports use <a href="https://opencollection.com" target="_blank" rel="noreferrer">OpenCollection</a>, an open format for API collections.</p>

          <div class="share-section-title">Bruno-compatible format</div>
          <div class="share-format-grid">
            <button
              type="button"
              class:selected={shareCollectionFormat === 'zip'}
              class="share-format-card"
              data-testid="share-format-zip"
              aria-pressed={shareCollectionFormat === 'zip'}
              on:click={() => (shareCollectionFormat = 'zip')}
            >
              <div class="share-card-header">
                <span>Bruno Collection (ZIP)</span>
                <em>Recommended</em>
              </div>
              <p>OpenCollection format organized as folders and files</p>
              <ul>
                <li>Folder structure with individual .yml files</li>
                <li>Collaborate with your team via pull requests</li>
                <li>Extract and open directly in Bruno</li>
              </ul>
              <small>Best for: Team collaboration, version control, publishing</small>
            </button>

            <button
              type="button"
              class:selected={shareCollectionFormat === 'yaml'}
              class="share-format-card"
              data-testid="share-format-yaml"
              aria-pressed={shareCollectionFormat === 'yaml'}
              on:click={() => (shareCollectionFormat = 'yaml')}
            >
              <div class="share-card-header">
                <span>Single File (YAML)</span>
              </div>
              <p>OpenCollection format bundled into one .yml file</p>
              <ul>
                <li>Everything in a single YAML file</li>
                <li>Paste in a gist or attach to an issue</li>
              </ul>
              <small>Best for: Quick sharing as a single file</small>
            </button>
          </div>

          <div class="share-section-title">Other Format</div>
          <button
            type="button"
            class:selected={shareCollectionFormat === 'postman'}
            class="share-other-format"
            data-testid="share-format-postman"
            aria-pressed={shareCollectionFormat === 'postman'}
            on:click={() => (shareCollectionFormat = 'postman')}
          >
            <strong>Postman</strong>
            <span>Export for Postman</span>
          </button>

          {#if shareCollectionFormat === 'postman' && shareCollectionUnsupportedTypes.length > 0}
            <div class="share-warning" data-testid="share-postman-warning">
              Note: {shareCollectionUnsupportedTypes.join(', ')} requests in this collection will not be exported
            </div>
          {/if}

          {#if shareCollectionResult}
            <!--
              Kept on screen instead of closing the dialog. A confirmation the
              user never sees is the same as no confirmation, and the warnings
              below are the only place several of these losses are reported at
              all.
            -->
            <div class="share-result" data-testid="share-collection-result">
              <strong>Exported {shareCollectionResult.filename}</strong>
              <span>
                {shareCollectionResult.requestCount} request{shareCollectionResult.requestCount === 1 ? '' : 's'},
                {shareCollectionResult.folderCount} folder{shareCollectionResult.folderCount === 1 ? '' : 's'},
                {shareCollectionResult.environmentCount} environment{shareCollectionResult.environmentCount === 1 ? '' : 's'}
              </span>
              {#if shareCollectionResult.skippedTypes && shareCollectionResult.skippedTypes.length > 0}
                <!--
                  The authoritative list: the backend walks the full nested tree,
                  so this names types the pre-export guess above cannot see.
                -->
                <div class="share-warning" data-testid="share-result-skipped">
                  Not exported: {shareCollectionResult.skippedTypes.join(', ')} requests
                </div>
              {/if}
              {#if shareCollectionResult.warning}
                <div class="share-warning" data-testid="share-result-warning">{shareCollectionResult.warning}</div>
              {/if}
            </div>
          {/if}
        </div>
        <div class="button-row modal-footer">
          <button type="button" on:click={cancelShareCollectionModal}>{shareCollectionResult ? 'Close' : 'Cancel'}</button>
          <button class="primary" type="submit" data-testid="share-collection-proceed" disabled={busy !== ''}>{busy === 'share collection' ? 'Exporting...' : 'Proceed'}</button>
        </div>
      </form>
</Modal>
