<script lang="ts">
  // US-036 — the Clone Request dialog, lifted out of App.svelte so its markup
  // is not in the initial chunk. Imported dynamically from inside the {#if}
  // that gates it; a static import would leave it in the initial graph.
  //
  // Structurally identical to the folder dialogs and to its sibling: name draft,
  // filesystem-name toggle, filename draft, editing toggle, two validators.
  import type { main } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  // Bindable: the filename field and both toggles write back to App.svelte.
  export let cloneRequestFilenameDraft: string
  export let cloneRequestFilenameEditing: boolean
  export let cloneRequestShowFilesystemName: boolean

  export let cloneRequestNameDraft: string
  export let cloneRequestTarget: { collection: main.Collection }
  export let busy: string
  export let cloneRequestFilenameIsValid: () => boolean
  export let cloneRequestFilenameIsReserved: () => boolean
  export let collectionFolderNameIsValid: (name: string) => boolean
  export let sanitizeCollectionFolderName: (name: string) => string
  export let updateCloneRequestName: (name: string) => void
  export let confirmCloneRequest: () => void
  export let cancelCloneRequestModal: () => void
</script>

<Modal labelledBy="clone-request-title" onClose={cancelCloneRequestModal}>
      <form on:submit|preventDefault={confirmCloneRequest}>
        <header>
          <h2 id="clone-request-title">Clone Request</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelCloneRequestModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Request Name</span>
            <input
              id="collection-item-name"
              name="name"
              aria-label="Clone request name"
              data-testid="clone-request-name"
              value={cloneRequestNameDraft}
              on:input={(event) => updateCloneRequestName(event.currentTarget.value)}
            />
          </label>
          <div class="button-row compact">
            <button type="button" data-testid="clone-request-options-toggle" on:click={() => (cloneRequestShowFilesystemName = !cloneRequestShowFilesystemName)}>
              {cloneRequestShowFilesystemName ? 'Hide Filesystem Name' : 'Show Filesystem Name'}
            </button>
          </div>
          {#if cloneRequestShowFilesystemName}
            <label>
              <span>File Name (on filesystem)</span>
              <div class="inline-field-action">
                <div class="filename-with-extension">
                  {#if cloneRequestFilenameEditing}
                    <input
                      id="file-name"
                      name="filename"
                      aria-label="Clone request filesystem name"
                      data-testid="clone-request-file-name"
                      value={cloneRequestFilenameDraft}
                      on:input={(event) => (cloneRequestFilenameDraft = event.currentTarget.value)}
                    />
                  {:else}
                    <input
                      aria-label="Clone request filesystem name"
                      data-testid="clone-request-file-name"
                      readonly
                      value={cloneRequestFilenameDraft}
                    />
                  {/if}
                  <span>{cloneRequestTarget.collection.format === 'yml' || cloneRequestTarget.collection.format === 'yaml' ? '.yml' : '.bru'}</span>
                </div>
                <button
                  type="button"
                  data-testid="clone-request-filename-toggle"
                  on:click={() => {
                    cloneRequestFilenameEditing = !cloneRequestFilenameEditing
                    if (!cloneRequestFilenameEditing) cloneRequestFilenameDraft = sanitizeCollectionFolderName(cloneRequestNameDraft)
                  }}
                >{cloneRequestFilenameEditing ? 'Reset' : 'Edit'}</button>
              </div>
            </label>
          {/if}
          {#if cloneRequestFilenameDraft && !collectionFolderNameIsValid(cloneRequestFilenameDraft)}
            <p class="field-error">File name is not valid.</p>
          {/if}
          {#if cloneRequestFilenameIsReserved()}
            <p class="field-error">The file names "collection" and "folder" are reserved by the Bruno-compatible collection file format.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelCloneRequestModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="clone-item-button"
            disabled={busy !== '' || cloneRequestNameDraft.trim() === '' || !cloneRequestFilenameIsValid()}
          >{busy === 'clone request' ? 'Cloning...' : 'Clone'}</button>
        </div>
      </form>
</Modal>
