<script lang="ts">
  // The Delete Flow confirmation.
  //
  // IT ASKS, AND IT SAYS WHY. Deleting a request keeps a recovery copy —
  // DeleteRequestModal says so, and that promise is what makes its dialog a
  // formality. Flows have no such copy: DeleteFlow rewrites the collection's
  // root config file and the definition is gone. So this dialog states the one
  // fact that distinguishes it from its neighbour rather than reusing their
  // reassuring sentence.
  //
  // Runes, following McpApprovalModal rather than the older confirm dialogs:
  // new components in this tree use them.
  import type { types } from '../../../../wailsjs/go/models'
  import Modal from '../Modal.svelte'

  type Props = {
    target: { flow: types.Flow }
    busy: string
    onConfirm: () => void | Promise<void>
    onCancel: () => void
  }

  let { target, busy, onConfirm, onCancel }: Props = $props()

  const name = $derived(target.flow.name?.trim() || target.flow.id)
</script>

<Modal labelledBy="delete-flow-title" onClose={onCancel} closeOnBackdrop={false}>
  <form
    onsubmit={(event) => {
      event.preventDefault()
      void onConfirm()
    }}
  >
    <header>
      <h2 id="delete-flow-title">Delete Flow</h2>
      <button
        type="button"
        class="icon-button"
        title="Cancel"
        data-testid="modal-close-button"
        onclick={onCancel}
      >x</button>
    </header>
    <div class="prompt-fields">
      <p>
        Delete <span class="font-medium">{name}</span>? The flow is removed from the collection
        file. Unlike a request, there is no recovery copy.
      </p>
    </div>
    <div class="button-row">
      <button type="button" onclick={onCancel}>Cancel</button>
      <button
        class="danger-button"
        type="submit"
        data-testid="modal-submit-btn"
        disabled={busy !== ''}
      >{busy === 'delete flow' ? 'Deleting...' : 'Delete'}</button>
    </div>
  </form>
</Modal>
