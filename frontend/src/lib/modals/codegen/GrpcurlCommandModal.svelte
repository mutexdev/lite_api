<script lang="ts">
  // US-036 — the grpcurl command dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  export let generatedGrpcurlCommand: string
  export let copyGrpcurlCommand: () => void
  export let closeGrpcurlCommand: () => void
</script>

<Modal labelledBy="grpcurl-code-title" onClose={closeGrpcurlCommand} dialogClass="prompt-dialog code-generator-dialog">
      <header>
        <h2 id="grpcurl-code-title">Generate grpcurl Command</h2>
        <button type="button" class="icon-button" title="Close" on:click={closeGrpcurlCommand}>x</button>
      </header>
      <pre class="generated-code" aria-label="Generated grpcurl command">{generatedGrpcurlCommand}</pre>
      <div class="button-row">
        <button type="button" on:click={closeGrpcurlCommand}>Close</button>
        <button class="primary" type="button" on:click={copyGrpcurlCommand}>Copy</button>
      </div>
</Modal>
