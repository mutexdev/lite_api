<script lang="ts">
  // US-036 — the variable-prompt dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  export let promptDialog: { prompts: string[]; values: Record<string, string> }
  export let updatePromptValue: (prompt: string, value: string) => void
  export let submitPromptDialog: () => void
  export let cancelPromptDialog: () => void
</script>

<Modal labelledBy="prompt-dialog-title" onClose={cancelPromptDialog} size="medium">
      <form on:submit|preventDefault={submitPromptDialog}>
        <header>
          <h2 id="prompt-dialog-title">Input Required</h2>
          <IconButton icon="close" label="Close" onclick={cancelPromptDialog} />
        </header>
        <div class="prompt-fields">
          <!--
            Only the first prompt is marked: Modal.svelte takes the first match,
            and a dialog asking for three variables should land on the first of
            them. This replaces App.svelte's
            setTimeout(() => document.querySelector('.prompt-dialog input')?.focus())
            — a global CSS query fired on a timer, which would have found the
            wrong input the moment two dialogs were ever open at once.
          -->
          {#each promptDialog.prompts as prompt, index (index)}
            <label>
              <span>{prompt}</span>
              <input data-modal-autofocus={index === 0 ? '' : undefined} value={promptDialog.values[prompt] ?? ''} on:input={(event) => updatePromptValue(prompt, event.currentTarget.value)} />
            </label>
          {/each}
        </div>
        <div class="button-row">
          <button type="button" on:click={cancelPromptDialog}>Cancel</button>
          <button class="primary" type="submit">Continue</button>
        </div>
      </form>
</Modal>
