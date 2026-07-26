<script lang="ts">
  // US-036 — the item Info dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  // Mirrors App.svelte's local CollectionItemInfoTarget. Written out rather
  // than widened to `any`: eslint forbids `any`, and the discriminated union is
  // what makes `itemInfoTarget.kind === 'folder'` in the markup type-safe.
  type CollectionItemInfoTarget =
    | { kind: 'folder'; collection: types.Collection; folder: types.FolderConfig }
    | { kind: 'request'; collection: types.Collection; request: types.RequestItem }

  export let itemInfoTarget: CollectionItemInfoTarget
  export let itemInfoDisplayName: (target: CollectionItemInfoTarget) => string
  export let itemInfoFilesystemName: (target: CollectionItemInfoTarget) => string
  export let closeItemInfoModal: () => void
</script>

<Modal labelledBy="item-info-title" onClose={closeItemInfoModal} dialogClass="prompt-dialog item-info-dialog">
      <header>
        <h2 id="item-info-title">Info</h2>
        <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={closeItemInfoModal}>x</button>
      </header>
      <div class="prompt-fields">
        <table class="item-info-table">
          <tbody>
            <tr>
              <td class="item-info-label">{itemInfoTarget.kind === 'folder' ? 'Folder Name' : 'Request Name'}</td>
              <td class="item-info-value" title={itemInfoDisplayName(itemInfoTarget)}>
                <span class="item-info-colon">:</span>{itemInfoDisplayName(itemInfoTarget)}
              </td>
            </tr>
            <tr>
              <td class="item-info-label">
                {itemInfoTarget.kind === 'folder' ? 'Folder Name' : 'File Name'}
                <small>(on filesystem)</small>
              </td>
              <td class="item-info-value break-all" title={itemInfoFilesystemName(itemInfoTarget)}>
                <span class="item-info-colon">:</span>{itemInfoFilesystemName(itemInfoTarget)}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
</Modal>
