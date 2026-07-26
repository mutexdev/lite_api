<script lang="ts">
  // US-036 — the Delete Request confirmation, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { main } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  export let deleteRequestTarget: { request: main.RequestItem }
  export let busy: string
  export let confirmDeleteRequest: () => void
  export let cancelDeleteRequestModal: () => void
</script>

<Modal labelledBy="delete-request-title" onClose={cancelDeleteRequestModal} closeOnBackdrop={false}>
      <form on:submit|preventDefault={confirmDeleteRequest}>
        <header>
          <h2 id="delete-request-title">Delete Request</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelDeleteRequestModal}>x</button>
        </header>
        <div class="prompt-fields">
          <p>Delete <span class="font-medium">{deleteRequestTarget.request.name}</span>? {deleteRequestTarget.request.transient ? 'This unsaved request will be discarded without a recovery copy.' : 'A temporary recovery copy will be kept so it can be restored.'}</p>
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelDeleteRequestModal}>Cancel</button>
          <button
            class="danger-button"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== ''}
          >{busy === 'delete request' ? 'Deleting...' : 'Delete'}</button>
        </div>
      </form>
</Modal>
