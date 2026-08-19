<script lang="ts">
  // US-036 — the import replace-confirmation dialog, lifted out of App.svelte
  // so its markup is not in the initial chunk. Imported dynamically from inside
  // the {#if} that gates it.
  import Modal from '../Modal.svelte'

  // Bindable: App.svelte holds the cancel button so it can focus it on open.
  export let importReplaceConfirmationCancelButton: HTMLButtonElement | null = null

  export let cancelImportReplaceConfirmation: () => Promise<void> | void
  export let confirmImportReplace: () => void
</script>

<Modal
    labelledBy="import-replace-confirmation-title"
    describedBy="import-replace-confirmation-description"
    onClose={() => void cancelImportReplaceConfirmation()}
    testId="import-replace-confirmation-modal"
    closeOnBackdrop={false}
  >
      <header>
        <h2 id="import-replace-confirmation-title">Replace existing collections?</h2>
      </header>
      <p id="import-replace-confirmation-description">Replace the selected existing collection folders? Backups are retained until import persistence succeeds.</p>
      <div class="button-row modal-footer">
        <button type="button" bind:this={importReplaceConfirmationCancelButton} data-testid="import-replace-confirmation-cancel" on:click={cancelImportReplaceConfirmation}>Cancel</button>
        <button class="danger-button" type="button" data-testid="import-replace-confirmation-confirm" on:click={confirmImportReplace}>Replace collections</button>
      </div>
</Modal>
