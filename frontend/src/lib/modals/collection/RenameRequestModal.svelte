<script lang="ts">
  // US-036 — the Rename Request dialog, lifted out of App.svelte so its markup
  // is not in the initial chunk. Imported dynamically from inside the {#if}
  // that gates it; a static import would leave it in the initial graph.
  //
  // Structurally identical to the folder dialogs and to its sibling: name draft,
  // filesystem-name toggle, filename draft, editing toggle, two validators.
  import type { main } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  // Bindable: the filename field and both toggles write back to App.svelte.
  export let renameRequestFilenameDraft: string
  export let renameRequestFilenameEditing: boolean
  export let renameRequestShowFilesystemName: boolean

  export let renameRequestNameDraft: string
  export let renameRequestTarget: { collection: main.Collection }
  export let busy: string
  export let renameRequestFilenameIsValid: () => boolean
  export let renameRequestFilenameIsReserved: () => boolean
  export let collectionFolderNameIsValid: (name: string) => boolean
  export let sanitizeCollectionFolderName: (name: string) => string
  export let updateRenameRequestName: (name: string) => void
  export let confirmRenameRequest: () => void
  export let cancelRenameRequestModal: () => void
</script>

<Modal labelledBy="rename-request-title" onClose={cancelRenameRequestModal}>
      <form on:submit|preventDefault={confirmRenameRequest}>
        <header>
          <h2 id="rename-request-title">Rename Request</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelRenameRequestModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Request Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Rename request name"
              data-testid="rename-request-name"
              value={renameRequestNameDraft}
              on:input={(event) => updateRenameRequestName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="rename-request-options-toggle" on:click={() => (renameRequestShowFilesystemName = !renameRequestShowFilesystemName)}>
              {renameRequestShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if renameRequestShowFilesystemName}
            <label>
              <span>File Name (on filesystem)</span>
              <div class="inline-field-action">
                <div class="filename-with-extension">
                  {#if renameRequestFilenameEditing}
                    <input
                      id="file-name"
                      name="filename"
                      aria-label="Rename request filesystem name"
                      data-testid="rename-request-file-name"
                      value={renameRequestFilenameDraft}
                      on:input={(event) => (renameRequestFilenameDraft = event.currentTarget.value)}
                    />
                  {:else}
                    <input
                      aria-label="Rename request filesystem name"
                      data-testid="rename-request-file-name"
                      readonly
                      value={renameRequestFilenameDraft}
                    />
                  {/if}
                  <span>{renameRequestTarget.collection.format === 'yml' || renameRequestTarget.collection.format === 'yaml' ? '.yml' : '.bru'}</span>
                </div>
                <button
                  type="button"
                  data-testid="rename-request-edit-icon"
                  on:click={() => {
                    renameRequestFilenameEditing = !renameRequestFilenameEditing
                    if (!renameRequestFilenameEditing) renameRequestFilenameDraft = sanitizeCollectionFolderName(renameRequestNameDraft)
                  }}
                >{renameRequestFilenameEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if renameRequestFilenameDraft && !collectionFolderNameIsValid(renameRequestFilenameDraft)}
            <p class="field-error">File name is not valid.</p>
          {/if}
          {#if renameRequestFilenameIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelRenameRequestModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="rename-item-button"
            disabled={busy !== '' || renameRequestNameDraft.trim() === '' || !renameRequestFilenameIsValid()}
          >{busy === 'rename request' ? 'Renaming...' : 'Rename'}</button>
        </div>
      </form>
</Modal>
