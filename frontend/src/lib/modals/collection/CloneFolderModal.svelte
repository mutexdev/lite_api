<script lang="ts">
  // US-036 — the Clone Folder dialog, lifted out of App.svelte so its markup is
  // not in the initial chunk. Imported dynamically from inside the {#if} that
  // gates it; a static import would leave it in the initial graph.
  import Modal from '../Modal.svelte'

  // Bindable: the directory field and both toggles write back to App.svelte.
  export let cloneFolderDirectoryDraft: string
  export let cloneFolderDirectoryEditing: boolean
  export let cloneFolderShowFilesystemName: boolean

  export let cloneFolderNameDraft: string
  export let busy: string
  export let cloneFolderDirectoryNameIsValid: () => boolean
  export let cloneFolderDirectoryIsReserved: () => boolean
  export let collectionFolderNameIsValid: (name: string) => boolean
  export let sanitizeCollectionFolderName: (name: string) => string
  export let updateCloneFolderName: (name: string) => void
  export let confirmCloneFolder: () => void
  export let cancelCloneFolderModal: () => void
</script>

<Modal labelledBy="clone-folder-title" onClose={cancelCloneFolderModal}>
      <form on:submit|preventDefault={confirmCloneFolder}>
        <header>
          <h2 id="clone-folder-title">Clone Folder</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelCloneFolderModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Folder Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Clone folder name"
              data-testid="clone-folder-name"
              value={cloneFolderNameDraft}
              on:input={(event) => updateCloneFolderName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="clone-folder-options-toggle" on:click={() => (cloneFolderShowFilesystemName = !cloneFolderShowFilesystemName)}>
              {cloneFolderShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if cloneFolderShowFilesystemName}
            <label>
              <span>Folder Name (on filesystem)</span>
              <div class="inline-field-action">
                {#if cloneFolderDirectoryEditing}
                  <input
                    id="file-name"
                    name="filename"
                    aria-label="Clone folder filesystem name"
                    data-testid="clone-folder-directory-name"
                    value={cloneFolderDirectoryDraft}
                    on:input={(event) => (cloneFolderDirectoryDraft = event.currentTarget.value)}
                  />
                {:else}
                  <input
                    aria-label="Clone folder filesystem name"
                    data-testid="clone-folder-directory-name"
                    readonly
                    value={cloneFolderDirectoryDraft}
                  />
                {/if}
                <button
                  type="button"
                  data-testid="clone-folder-directory-toggle"
                  on:click={() => {
                    cloneFolderDirectoryEditing = !cloneFolderDirectoryEditing
                    if (!cloneFolderDirectoryEditing) cloneFolderDirectoryDraft = sanitizeCollectionFolderName(cloneFolderNameDraft)
                  }}
                >{cloneFolderDirectoryEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if cloneFolderDirectoryDraft && !collectionFolderNameIsValid(cloneFolderDirectoryDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
          {#if cloneFolderDirectoryIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelCloneFolderModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="clone-item-button"
            disabled={busy !== '' || cloneFolderNameDraft.trim() === '' || !cloneFolderDirectoryNameIsValid()}
          >{busy === 'clone folder' ? 'Cloning...' : 'Clone'}</button>
        </div>
      </form>
</Modal>
