<script lang="ts">
  // US-036 — the unsaved-changes dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  // Bindable: the cancel button reference is held by App.svelte so it can
  // focus it when the dialog opens.
  export let tabLifecycleCancelButton: HTMLButtonElement | null = null

  export let tabLifecycleDialog: {
    action: string
    affected: Array<{ collectionId: string; requestId: string; requestName: string; transient?: boolean }>
  }
  export let tabLifecycleDecisionBusy: boolean
  export let discardAndCompleteTabLifecycle: () => void
  export let saveAndCompleteTabLifecycle: () => void
  export let dismissTabLifecycleDialog: () => void
</script>

<Modal
	    labelledBy="unsaved-tabs-title"
	    describedBy="unsaved-tabs-description"
	    busy={tabLifecycleDecisionBusy}
	    onClose={dismissTabLifecycleDialog}
	    dialogClass="prompt-dialog unsaved-tabs-dialog"
	    closeOnBackdrop={false}
	  >
	      <header>
	        <h2 id="unsaved-tabs-title">Unsaved changes</h2>
	      </header>
	      <p id="unsaved-tabs-description">
	        {#if tabLifecycleDialog.action === 'quit'}
	          Save or discard these drafts before quitting LiteAPI.
	        {:else if tabLifecycleDialog.action === 'close-all'}
	          Save or discard these drafts before closing all tabs.
	        {:else}
	          Save or discard this draft before closing the tab.
	        {/if}
	      </p>
	      <ul class="unsaved-tabs-list" aria-label="Affected unsaved requests">
	        {#each tabLifecycleDialog.affected as request (request.collectionId + request.requestId)}
	          <li>
	            <strong>{request.requestName}</strong>
	            <span>{request.transient ? 'Scratch draft' : 'Unsaved changes'}</span>
	          </li>
	        {/each}
	      </ul>
	      <div class="button-row modal-footer">
	        <button
	          type="button"
	          class="danger-button"
	          on:click={discardAndCompleteTabLifecycle}
	          disabled={tabLifecycleDecisionBusy}
	        >Discard &amp; Close</button>
	        <button
	          type="button"
	          bind:this={tabLifecycleCancelButton}
	          on:click={dismissTabLifecycleDialog}
	          disabled={tabLifecycleDecisionBusy}
	        >Cancel</button>
	        <button
	          type="button"
	          class="primary"
	          on:click={saveAndCompleteTabLifecycle}
	          disabled={tabLifecycleDecisionBusy}
	        >Save &amp; Close</button>
	      </div>
</Modal>
