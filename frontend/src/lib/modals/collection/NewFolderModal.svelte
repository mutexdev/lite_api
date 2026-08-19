<script lang="ts">
  // US-036 — the New Folder dialog, lifted out of App.svelte so its markup is
  // not in the initial chunk. Imported dynamically from inside the {#if} that
  // gates it; a static import would leave it in the initial graph.
  import Modal from '../Modal.svelte'

  // Bindable: the name/directory fields and both toggles write back.
  export let newFolderDirectoryDraft: string
  export let newFolderDirectoryEditing: boolean
  export let newFolderShowFilesystemName: boolean

  export let newFolderNameDraft: string
  export let busy: string
  export let newFolderDirectoryNameIsValid: () => boolean
  export let collectionFolderNameIsValid: (name: string) => boolean
  export let newFolderDirectoryIsReservedRoot: () => boolean
  export let sanitizeCollectionFolderName: (name: string) => string
  export let updateNewFolderName: (name: string) => void
  export let confirmNewFolder: () => void
  export let cancelNewFolderModal: () => void
</script>

<Modal labelledBy="new-folder-title" onClose={cancelNewFolderModal} testId="new-folder-modal">
      <form on:submit|preventDefault={confirmNewFolder}>
        <header>
          <h2 id="new-folder-title">New Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelNewFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Folder Name</span>
            <input
              id="folder-name"
              name="folderName"
              aria-label="Folder Name"
              data-testid="new-folder-input"
              value={newFolderNameDraft}
              on:input={(event) => updateNewFolderName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="new-folder-options-toggle" on:click={() => (newFolderShowFilesystemName = !newFolderShowFilesystemName)}>
              {newFolderShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if newFolderShowFilesystemName}
            <label>
              <span>Folder Name (on filesystem)</span>
              <div class="inline-field-action">
                {#if newFolderDirectoryEditing}
                  <input
                    id="file-name"
                    name="directoryName"
                    aria-label="Folder Name on filesystem"
                    data-testid="new-folder-directory-name"
                    value={newFolderDirectoryDraft}
                    on:input={(event) => (newFolderDirectoryDraft = event.currentTarget.value)}
                  />
                {:else}
                  <input
                    aria-label="Folder Name on filesystem"
                    data-testid="new-folder-directory-name"
                    readonly
                    value={newFolderDirectoryDraft}
                  />
                {/if}
                <button
                  type="button"
                  data-testid="new-folder-directory-toggle"
                  on:click={() => {
                    newFolderDirectoryEditing = !newFolderDirectoryEditing
                    if (!newFolderDirectoryEditing) newFolderDirectoryDraft = sanitizeCollectionFolderName(newFolderNameDraft)
                  }}
                >{newFolderDirectoryEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if newFolderDirectoryDraft && !collectionFolderNameIsValid(newFolderDirectoryDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
          {#if newFolderDirectoryIsReservedRoot()}
            <p class="field-error">The folder name "environments" at the root is reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" data-testid="new-folder-cancel" on:click={cancelNewFolderModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== '' || newFolderNameDraft.trim() === '' || !newFolderDirectoryNameIsValid()}
          >{busy === 'new folder' ? 'Creating...' : 'Create'}</button>
        </div>
      </form>
</Modal>
