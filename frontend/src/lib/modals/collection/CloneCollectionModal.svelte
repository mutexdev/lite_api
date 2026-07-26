<script lang="ts">
  // US-036 — the Clone Collection dialog, lifted out of App.svelte so its
  // markup is not in the initial chunk. Imported dynamically from inside the
  // {#if} that gates it; a static import would leave it in the initial graph.
  import Modal from '../Modal.svelte'

  // Bindable: the folder field and its edit toggle write back to App.svelte.
  export let cloneCollectionFolderDraft: string
  export let cloneCollectionFolderEditing: boolean

  export let cloneCollectionNameDraft: string
  export let cloneCollectionLocationDraft: string
  export let busy: string
  export let collectionFolderNameIsValid: (name: string) => boolean
  export let updateCloneCollectionName: (name: string) => void
  export let browseCloneCollectionLocation: () => void
  export let confirmCloneCollection: () => void
  export let cancelCloneCollectionModal: () => void
</script>

<Modal labelledBy="clone-collection-title" onClose={cancelCloneCollectionModal} testId="clone-collection-modal">
      <form on:submit|preventDefault={confirmCloneCollection}>
        <header>
          <h2 id="clone-collection-title">Clone Collection</h2>
          <button type="button" class="icon-button" title="Cancel" data-testid="modal-close-button" on:click={cancelCloneCollectionModal}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Name</span>
            <input
              id="collection-name"
              name="collectionName"
              aria-label="Clone collection name"
              data-testid="clone-collection-name"
              value={cloneCollectionNameDraft}
              on:input={(event) => updateCloneCollectionName(event.currentTarget.value)}
            />
          </label>
          <label>
            <span>Location</span>
            <div class="inline-field-action">
              <input
                id="collection-location"
                name="collectionLocation"
                aria-label="Clone collection location"
                data-testid="clone-collection-location"
                readonly
                value={cloneCollectionLocationDraft}
                on:click={browseCloneCollectionLocation}
              />
              <button type="button" data-testid="clone-collection-browse" on:click={browseCloneCollectionLocation}>Browse</button>
            </div>
          </label>
          <label>
            <span>Folder Name</span>
            <div class="inline-field-action">
              {#if cloneCollectionFolderEditing}
                <input
                  id="collection-folder-name"
                  name="collectionFolderName"
                  aria-label="Clone collection folder name"
                  data-testid="clone-collection-folder-name"
                  value={cloneCollectionFolderDraft}
                  on:input={(event) => (cloneCollectionFolderDraft = event.currentTarget.value)}
                />
              {:else}
                <input
                  aria-label="Clone collection folder name"
                  data-testid="clone-collection-folder-name"
                  readonly
                  value={cloneCollectionFolderDraft}
                />
              {/if}
              <button type="button" data-testid="clone-collection-folder-toggle" on:click={() => (cloneCollectionFolderEditing = !cloneCollectionFolderEditing)}>{cloneCollectionFolderEditing ? 'Reset' : 'Edit'}</button>
            </div>
          </label>
          {#if cloneCollectionFolderDraft && !collectionFolderNameIsValid(cloneCollectionFolderDraft)}
            <p class="field-error">Folder name is not valid.</p>
          {/if}
        </div>
        <div class="button-row">
          <button type="button" data-testid="clone-collection-cancel" on:click={cancelCloneCollectionModal}>Cancel</button>
          <button
            class="primary"
            type="submit"
            data-testid="modal-submit-btn"
            disabled={busy !== '' || cloneCollectionNameDraft === '' || !cloneCollectionLocationDraft || !collectionFolderNameIsValid(cloneCollectionFolderDraft)}
          >{busy === 'clone collection' ? 'Creating...' : 'Create'}</button>
        </div>
      </form>
</Modal>
