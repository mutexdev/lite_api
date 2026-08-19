<script lang="ts">
  // US-036 — the New Request dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  // Bindable: both form fields write back to App.svelte.
  export let requestName: string
  export let requestType: string

  export let activeCollection: types.Collection | undefined
  export let submitCreationFlow: () => void
  export let closeCreationFlow: () => Promise<void> | void
</script>

<Modal labelledBy="new-request-title" onClose={() => void closeCreationFlow()} dialogClass="prompt-dialog compact-create-dialog">
    <form on:submit|preventDefault={submitCreationFlow}>
      <header>
        <div>
          <h2 id="new-request-title">New request</h2>
          <p>Create a local scratch request in {activeCollection?.name ?? 'the active collection'}.</p>
        </div>
        <button type="button" class="icon-button" aria-label="Close new request" title="Close" on:click={() => void closeCreationFlow()}>×</button>
      </header>
      <label>
        <span>Name</span>
        <input data-new-request-name aria-label="New request name" bind:value={requestName} placeholder="Untitled request" />
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
        <button class="primary" type="submit" disabled={!activeCollection}>Create request</button>
      </footer>
    </form>
</Modal>
