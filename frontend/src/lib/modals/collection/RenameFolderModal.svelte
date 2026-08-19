<script lang="ts">
  // US-036 — the Rename Folder dialog, lifted out of App.svelte so its markup
  // is not in the initial chunk. Imported dynamically from inside the {#if}
  // that gates it; a static import would leave it in the initial graph.
  import Modal from '../Modal.svelte'

  // Bindable: the directory field and both toggles write back to App.svelte.
  export let renameFolderDirectoryDraft: string
  export let renameFolderDirectoryEditing: boolean
  export let renameFolderShowFilesystemName: boolean

  export let renameFolderNameDraft: string
  export let busy: string
  export let renameFolderDirectoryNameIsValid: () => boolean
  export let renameFolderDirectoryIsReserved: () => boolean
  export let collectionFolderNameIsValid: (name: string) => boolean
  export let sanitizeCollectionFolderName: (name: string) => string
  export let updateRenameFolderName: (name: string) => void
  export let confirmRenameFolder: () => void
  export let cancelRenameFolderModal: () => void
</script>

<Modal labelledBy="rename-folder-title" onClose={cancelRenameFolderModal} testId="rename-folder-modal">
      <form on:submit|preventDefault={confirmRenameFolder}>
        <header>
          <h2 id="rename-folder-title">Rename Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelRenameFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Folder Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Rename folder name"
              data-testid="rename-folder-name"
              value={renameFolderNameDraft}
              on:input={(event) => updateRenameFolderName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="rename-folder-options-toggle" on:click={() => (renameFolderShowFilesystemName = !renameFolderShowFilesystemName)}>
              {renameFolderShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if renameFolderShowFilesystemName}
            <label>
              <span>Folder Name (on filesystem)</span>
              <div class="inline-field-action">
                {#if renameFolderDirectoryEditing}
                  <input
                    id="file-name"
                    name="filename"
                    aria-label="Rename folder filesystem name"
                    data-testid="rename-folder-directory-name"
                    value={renameFolderDirectoryDraft}
                    on:input={(event) => (renameFolderDirectoryDraft = event.currentTarget.value)}
                  />
                {:else}
                  <input
                    aria-label="Rename folder filesystem name"
                    data-testid="rename-folder-directory-name"
                    readonly
                    value={renameFolderDirectoryDraft}
                  />
                {/if}
                <button
                  type="button"
                  data-testid="rename-folder-directory-toggle"
                  on:click={() => {
                    renameFolderDirectoryEditing = !renameFolderDirectoryEditing
                    if (!renameFolderDirectoryEditing) renameFolderDirectoryDraft = sanitizeCollectionFolderName(renameFolderNameDraft)
                  }}
                >{renameFolderDirectoryEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if renameFolderDirectoryDraft && !collectionFolderNameIsValid(renameFolderDirectoryDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
          {#if renameFolderDirectoryIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" data-testid="rename-folder-cancel" on:click={cancelRenameFolderModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="rename-item-button"
            disabled={busy !== '' || renameFolderNameDraft.trim() === '' || !renameFolderDirectoryNameIsValid()}
          >{busy === 'rename folder' ? 'Renaming...' : 'Rename'}</button>
        </div>
      </form>
</Modal>
