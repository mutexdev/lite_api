<script lang="ts">
  // US-036 — the Remove Collection confirmation, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  export let removeCollectionTarget: types.Collection
  export let busy: string
  export let confirmRemoveCollection: () => void
  export let cancelRemoveCollectionModal: () => void
</script>

<Modal labelledBy="remove-collection-title" onClose={cancelRemoveCollectionModal} testId="remove-collection-modal" closeOnBackdrop={false}>
      <header>
        <h2 id="remove-collection-title">Remove Collection</h2>
        <button type="button" class="icon-button" title="Cancel" on:click={cancelRemoveCollectionModal}>x</button>
      </header>
      <p>Remove {removeCollectionTarget.name} from this workspace. A temporary recovery copy will be kept so it can be restored.</p>
      <code>{removeCollectionTarget.path}</code>
      <div class="button-row">
        <button type="button" data-testid="remove-collection-cancel" on:click={cancelRemoveCollectionModal}>Cancel</button>
        <button class="primary" type="button" data-testid="remove-collection-confirm" on:click={confirmRemoveCollection} disabled={busy !== ''}>{busy === 'remove collection' ? 'Removing...' : 'Remove'}</button>
      </div>
</Modal>
