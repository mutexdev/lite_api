<script lang="ts">
  // US-036 — the grpcurl command dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  export let generatedGrpcurlCommand: string
  export let copyGrpcurlCommand: () => void
  export let closeGrpcurlCommand: () => void
</script>

<Modal labelledBy="grpcurl-code-title" onClose={closeGrpcurlCommand} size="large">
      <header>
        <h2 id="grpcurl-code-title">Generate grpcurl Command</h2>
        <IconButton icon="close" label="Close" onclick={closeGrpcurlCommand} />
      </header>
      <pre class="generated-code" aria-label="Generated grpcurl command">{generatedGrpcurlCommand}</pre>
      <div class="button-row">
        <button type="button" on:click={closeGrpcurlCommand}>Close</button>
        <!--
          disabled when there is nothing to copy, which its two siblings in this
          family (RequestCodeModal, ResponseExampleCodeModal) already were. Open
          before generation finishes, or on a command that came back empty, and
          Copy was live and put nothing on the clipboard — a click that reports
          success and does nothing.
        -->
        <button class="primary" type="button" on:click={copyGrpcurlCommand} disabled={!generatedGrpcurlCommand}>Copy</button>
      </div>
</Modal>
