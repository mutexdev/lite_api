<script lang="ts">
  // US-036 — the Rename Collection dialog, lifted out of App.svelte so its
  // markup is not in the initial chunk. Imported dynamically from inside the
  // {#if} that gates it.
  import Modal from '../Modal.svelte'

  // Bindable: the input writes the draft back on every keystroke.
  export let renameCollectionDraft: string
  export let busy: string
  export let confirmRenameCollection: () => void
  export let cancelRenameCollectionModal: () => void
</script>

<Modal labelledBy="rename-collection-title" onClose={cancelRenameCollectionModal} testId="rename-collection-modal">
      <form on:submit|preventDefault={confirmRenameCollection}>
        <header>
          <h2 id="rename-collection-title">Rename Collection</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelRenameCollectionModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Name</span>
            <input
              aria-label="Rename collection name"
              data-testid="rename-collection-name"
              value={renameCollectionDraft}
              on:input={(event) => (renameCollectionDraft = event.currentTarget.value)}
            />
          </label>
        </div>
        <div class="button-row">
          <button type="button" data-testid="rename-collection-cancel" on:click={cancelRenameCollectionModal}>Cancel</button>
          <button class="primary" type="submit" data-testid="rename-collection-confirm" disabled={busy !== '' || renameCollectionDraft === ''}>{busy === 'rename collection' ? 'Renaming...' : 'Rename'}</button>
        </div>
      </form>
</Modal>
