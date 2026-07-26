<script lang="ts">
  // US-036 — the Git Required dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  export let gitNotFoundMessage: string
</script>

<Modal labelledBy="git-not-found-title" onClose={() => (gitNotFoundMessage = '')} dialogClass="prompt-dialog git-not-found-dialog">
      <header>
        <h2 id="git-not-found-title">Git Required</h2>
        <button type="button" class="icon-button" title="Close" on:click={() => (gitNotFoundMessage = '')}>x</button>
      </header>
      <p>{gitNotFoundMessage}</p>
      <div class="button-row">
        <button class="primary" type="button" on:click={() => (gitNotFoundMessage = '')}>Close</button>
      </div>
</Modal>
