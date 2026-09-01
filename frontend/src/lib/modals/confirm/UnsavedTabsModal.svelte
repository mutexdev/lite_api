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
	    size="medium"
	  >
	      <header>
	        <h2 id="unsaved-tabs-title">Unsaved Changes</h2>
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
	      <!--
	        FOOTER ORDER: neutral first, destructive last.
	
	        This dialog opened with "Discard & Close" as the very first focusable
	        element — there is no header close button — so the destructive answer
	        was one Return away from a dialog the user had not read yet. Only an
	        imperative tabLifecycleCancelButton?.focus() in App.svelte held that
	        back, from outside the component, where the next refactor could drop
	        it. Cancel now leads the row and names itself the initial focus, which
	        is the shell's own mechanism and travels with the markup.
	      -->
	      <div class="button-row">
	        <button
	          type="button"
	          data-modal-autofocus
	          bind:this={tabLifecycleCancelButton}
	          on:click={dismissTabLifecycleDialog}
	          disabled={tabLifecycleDecisionBusy}
	        >Cancel</button>
	        <button
	          type="button"
	          class="danger-button"
	          on:click={discardAndCompleteTabLifecycle}
	          disabled={tabLifecycleDecisionBusy}
	        >Discard &amp; Close</button>
	        <button
	          type="button"
	          class="primary"
	          on:click={saveAndCompleteTabLifecycle}
	          disabled={tabLifecycleDecisionBusy}
	        >Save &amp; Close</button>
	      </div>
</Modal>
