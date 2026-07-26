<script lang="ts">
  // US-036 — the response-example Generate Code dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import type { main } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  export let generatedExample: main.ResponseExample
  export let responseExampleCodeLanguage: string
  export let responseExampleGeneratedCode: string
  export let changeResponseExampleCodeLanguage: (example: main.ResponseExample, language: string) => void
  export let copyResponseExampleCode: () => void
  export let closeResponseExampleCode: () => void
</script>

<Modal labelledBy="response-example-code-title" onClose={closeResponseExampleCode} dialogClass="prompt-dialog code-generator-dialog">
        <header>
          <h2 id="response-example-code-title">Generate Code - {generatedExample.name}</h2>
          <button type="button" class="icon-button" title="Close" on:click={closeResponseExampleCode}>x</button>
        </header>
        <div class="field-grid code-generator-controls">
          <span class="field-label">Language</span>
          <select
            aria-label="Response example code language"
            value={responseExampleCodeLanguage}
            on:change={(event) => changeResponseExampleCodeLanguage(generatedExample, event.currentTarget.value)}
          >
            <option value="curl">cURL</option>
            <option value="fetch">JavaScript fetch</option>
          </select>
        </div>
        <pre class="generated-code" aria-label="Generated response example code">{responseExampleGeneratedCode}</pre>
        <div class="button-row">
          <button type="button" on:click={closeResponseExampleCode}>Close</button>
          <button class="primary" type="button" on:click={copyResponseExampleCode} disabled={!responseExampleGeneratedCode}>Copy</button>
        </div>
</Modal>
