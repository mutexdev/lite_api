<script lang="ts">
  // US-036 — the OpenAPI spec viewer, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  export let openAPISpecViewerResult: { sourceUrl?: string; fetched?: boolean; content?: string }
  export let formattedOpenAPISpecContent: (content: string | undefined) => string
  export let copyOpenAPISyncSpec: () => void
  export let closeOpenAPISyncSpecViewer: () => void
</script>

<Modal labelledBy="openapi-spec-title" onClose={closeOpenAPISyncSpecViewer} testId="openapi-spec-viewer-modal" size="large">
		      <header>
		        <h2 id="openapi-spec-title">API Spec</h2>
		        <IconButton icon="close" label="Close" onclick={closeOpenAPISyncSpecViewer} />
		      </header>
		      <div class="openapi-spec-meta">
		        {#if openAPISpecViewerResult.sourceUrl}
		          <span data-testid="openapi-spec-viewer-source">{openAPISpecViewerResult.sourceUrl}</span>
		        {/if}
		        {#if openAPISpecViewerResult.fetched}
		          <small data-testid="openapi-spec-viewer-origin">Showing spec file from source.</small>
		        {:else}
		          <small data-testid="openapi-spec-viewer-origin">Stored spec from last sync.</small>
		        {/if}
		      </div>
		      <pre class="openapi-spec-viewer" aria-label="OpenAPI spec content" data-testid="openapi-spec-viewer-content">{formattedOpenAPISpecContent(openAPISpecViewerResult.content)}</pre>
		      <div class="button-row">
		        <button type="button" data-testid="openapi-spec-viewer-close" on:click={closeOpenAPISyncSpecViewer}>Close</button>
		        <button class="primary" type="button" data-testid="openapi-spec-viewer-copy" on:click={copyOpenAPISyncSpec}>Copy</button>
		      </div>
</Modal>
