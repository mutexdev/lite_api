<script lang="ts">
  // US-036 — the variable-prompt dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  export let promptDialog: { prompts: string[]; values: Record<string, string> }
  export let updatePromptValue: (prompt: string, value: string) => void
  export let submitPromptDialog: () => void
  export let cancelPromptDialog: () => void
</script>

<Modal labelledBy="prompt-dialog-title" onClose={cancelPromptDialog}>
      <form on:submit|preventDefault={submitPromptDialog}>
        <header>
          <h2 id="prompt-dialog-title">Input Required</h2>
          <button type="button" class="icon-button" title="Cancel" on:click={cancelPromptDialog}>x</button>
        </header>
        <div class="prompt-fields">
          {#each promptDialog.prompts as prompt, index (index)}
            <label>
              <span>{prompt}</span>
              <input value={promptDialog.values[prompt] ?? ''} on:input={(event) => updatePromptValue(prompt, event.currentTarget.value)} />
            </label>
          {/each}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelPromptDialog}>Cancel</button>
          <button class="primary" type="submit">Continue</button>
        </div>
      </form>
</Modal>
