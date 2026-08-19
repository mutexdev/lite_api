<script lang="ts">
  // US-036 — the Save Response Example dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  // Bindable: both fields and the input element reference write back.
  export let createResponseExampleName: string
  export let createResponseExampleDescription: string
  export let createResponseExampleInput: HTMLInputElement | null = null

  export let busy: string
  export let createResponseExample: () => void
  export let cancelCreateResponseExample: () => void
</script>

<Modal labelledBy="create-example-title" onClose={cancelCreateResponseExample} dialogClass="prompt-dialog create-example-dialog">
      <form on:submit|preventDefault={createResponseExample}>
        <header>
          <h2 id="create-example-title">Create Response Example</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelCreateResponseExample}>x</button>
        </header>
        <div class="prompt-fields">
          <label>
            <span>Example Name</span>
            <input
              bind:this={createResponseExampleInput}
              aria-label="Create example name"
              value={createResponseExampleName}
              on:input={(event) => (createResponseExampleName = event.currentTarget.value)}
            />
          </label>
          <label>
            <span>Description</span>
            <textarea
              aria-label="Create example description"
              rows="3"
              value={createResponseExampleDescription}
              on:input={(event) => (createResponseExampleDescription = event.currentTarget.value)}
            ></textarea>
          </label>
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelCreateResponseExample}>Cancel</button>
          <button class="primary" type="submit" disabled={busy !== '' || !createResponseExampleName.trim()}>Create Example</button>
        </div>
      </form>
</Modal>
