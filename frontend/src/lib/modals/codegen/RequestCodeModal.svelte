<script lang="ts">
  // US-036 — the request Generate Code dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  export let requestCodeLanguage: string
  export let requestGeneratedCode: string
  export let changeRequestCodeLanguage: (language: string) => void
  export let copyRequestCode: () => void
  export let closeRequestCode: () => void
</script>

<Modal labelledBy="request-code-title" onClose={closeRequestCode} dialogClass="prompt-dialog code-generator-dialog">
      <header>
        <h2 id="request-code-title">Generate Code</h2>
        <button type="button" class="icon-button" title="Close" on:click={closeRequestCode}>x</button>
      </header>
      <div class="field-grid code-generator-controls">
        <span class="field-label">Language</span>
        <select
          aria-label="Request code language"
          data-testid="request-code-language"
          value={requestCodeLanguage}
          on:change={(event) => changeRequestCodeLanguage(event.currentTarget.value)}
        >
          <option value="curl">cURL</option>
          <option value="fetch">JavaScript fetch</option>
        </select>
      </div>
      <pre class="generated-code" aria-label="Generated request code">{requestGeneratedCode}</pre>
      <div class="button-row">
        <button type="button" on:click={closeRequestCode}>Close</button>
        <button class="primary" type="button" on:click={copyRequestCode} disabled={!requestGeneratedCode}>Copy</button>
      </div>
</Modal>
