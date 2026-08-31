<script lang="ts">
  // US-036 — the Remove Collection confirmation, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  export let removeCollectionTarget: types.Collection
  export let busy: string
  export let confirmRemoveCollection: () => void
  export let cancelRemoveCollectionModal: () => void
</script>

<Modal labelledBy="remove-collection-title" onClose={cancelRemoveCollectionModal} testId="remove-collection-modal" closeOnBackdrop={false} size="medium" busy={busy !== ''}>
      <header>
        <h2 id="remove-collection-title">Remove Collection</h2>
        <IconButton icon="close" label="Close" onclick={cancelRemoveCollectionModal} />
      </header>
      <p>Remove {removeCollectionTarget.name} from this workspace. A temporary recovery copy will be kept so it can be restored.</p>
      <code>{removeCollectionTarget.path}</code>
      <div class="button-row">
        <button type="button" data-testid="remove-collection-cancel" on:click={cancelRemoveCollectionModal}>Cancel</button>
        <!--
          .danger-button, not .primary. Remove was the one member of the
          delete/remove family painted in the filled accent — the same weight and
          colour as every Save and Create button in the app — so the button that
          takes a collection out of the workspace looked exactly like the button
          that keeps your work. Its four siblings (DeleteRequest, DeleteFolder,
          DeleteFlow, ImportReplace) were already outlined in --danger.
        -->
        <button class="danger-button" type="button" data-testid="remove-collection-confirm" on:click={confirmRemoveCollection} disabled={busy !== ''}>{busy === 'remove collection' ? 'Removing…' : 'Remove'}</button>
      </div>
</Modal>
