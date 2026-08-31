<script lang="ts">
  // US-036 — the New Request dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  // Bindable: both form fields write back to App.svelte.
  export let requestName: string
  export let requestType: string

  export let activeCollection: types.Collection | undefined
  export let submitCreationFlow: () => void
  export let closeCreationFlow: () => Promise<void> | void

  /** The folder the request will be created in; '' means the collection root. */
  export let destinationFolder = ''

  // Naming the destination matters now that this dialog can be opened from a
  // folder's own menu. Without it the two cases — root and folder — look
  // identical, and the only way to find out where the request went is to go and
  // look for it.
  $: destination = destinationFolder
    ? `${activeCollection?.name ?? 'the active collection'} / ${destinationFolder}`
    : (activeCollection?.name ?? 'the active collection')
</script>

<Modal labelledBy="new-request-title" onClose={() => void closeCreationFlow()} dialogClass="prompt-dialog compact-create-dialog" size="small">
    <form on:submit|preventDefault={submitCreationFlow}>
      <header>
        <div>
          <h2 id="new-request-title">New Request</h2>
          <p>Create a local scratch request in {destination}.</p>
        </div>
        <IconButton icon="close" label="Close" onclick={() => void closeCreationFlow()} />
      </header>
      <label>
        <span>Name</span>
        <input data-modal-autofocus data-new-request-name aria-label="New request name" bind:value={requestName} placeholder="Untitled request" />
      </label>
      <label>
        <span>Protocol</span>
        <select aria-label="New request protocol" bind:value={requestType}>
          <option value="http">HTTP</option>
          <option value="graphql">GraphQL</option>
          <option value="websocket">WebSocket</option>
          <option value="grpc">gRPC</option>
        </select>
      </label>
      <footer class="button-row">
        <button type="button" on:click={() => void closeCreationFlow()}>Cancel</button>
        <button class="primary" type="submit" disabled={!activeCollection}>Create</button>
      </footer>
    </form>
</Modal>
