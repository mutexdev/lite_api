<script lang="ts">
  // US-036 — the Delete Folder confirmation, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  export let deleteFolderTarget: { folder: types.FolderConfig }
  export let busy: string
  export let slashPathBase: (path: string) => string
  export let confirmDeleteFolder: () => void
  export let cancelDeleteFolderModal: () => void
</script>

<Modal labelledBy="delete-folder-title" onClose={cancelDeleteFolderModal} closeOnBackdrop={false} size="medium" busy={busy !== ''}>
      <form on:submit|preventDefault={confirmDeleteFolder}>
        <header>
          <h2 id="delete-folder-title">Delete Folder</h2>
          <IconButton icon="close" label="Close" onclick={cancelDeleteFolderModal} testId="modal-close-button" />
        </header>
        <div class="prompt-fields">
          <p>Delete <span class="font-medium">{deleteFolderTarget.folder.name || slashPathBase(deleteFolderTarget.folder.displayPath || deleteFolderTarget.folder.path)}</span>? A temporary recovery copy will be kept so it can be restored.</p>
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelDeleteFolderModal}>Cancel</button>
          <button
            class="danger-button"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== ''}
          >{busy === 'delete folder' ? 'Deleting…' : 'Delete'}</button>
        </div>
      </form>
</Modal>
