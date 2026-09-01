<script lang="ts">
  // US-036 — the Rename Collection dialog, lifted out of App.svelte so its
  // markup is not in the initial chunk. Imported dynamically from inside the
  // {#if} that gates it.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  // Bindable: the input writes the draft back on every keystroke.
  export let renameCollectionDraft: string
  export let busy: string
  export let confirmRenameCollection: () => void
  export let cancelRenameCollectionModal: () => void
</script>

<Modal labelledBy="rename-collection-title" onClose={cancelRenameCollectionModal} testId="rename-collection-modal" size="medium" busy={busy !== ''}>
      <form on:submit|preventDefault={confirmRenameCollection}>
        <header>
          <h2 id="rename-collection-title">Rename Collection</h2>
          <IconButton icon="close" label="Close" onclick={cancelRenameCollectionModal} />
        </header>
        <div class="prompt-fields">
          <!--
            data-modal-autofocus, because without it the first focusable element
            in this dialog is the header's close button: opening Rename
            Collection put the caret nowhere and the name you came here to type
            went into the void until you clicked the field. Its structural twins
            RenameFolder and RenameRequest already named their field; this one,
            CloneCollection and CloneFolder simply never adopted the shell's
            mechanism.
          -->
          <label>
            <span>Name</span>
            <input data-modal-autofocus
              aria-label="Rename collection name"
              data-testid="rename-collection-name"
              value={renameCollectionDraft}
              on:input={(event) => (renameCollectionDraft = event.currentTarget.value)}
            />
          </label>
        </div>
        <div class="button-row">
          <button type="button" data-testid="rename-collection-cancel" on:click={cancelRenameCollectionModal}>Cancel</button>
          <button class="primary" type="submit" data-testid="rename-collection-confirm" disabled={busy !== '' || renameCollectionDraft === ''}>{busy === 'rename collection' ? 'Renaming…' : 'Rename'}</button>
        </div>
      </form>
</Modal>
