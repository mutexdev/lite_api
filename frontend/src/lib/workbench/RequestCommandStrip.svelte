<script lang="ts">
  import type { RequestCommandActions, RequestCommandState } from './types'

  export let command: RequestCommandState
  export let actions: RequestCommandActions
  export let disabled = false
  export let orientation: 'horizontal' | 'vertical' = 'horizontal'
</script>

<section class="request-command-strip" aria-label="Request command center" aria-busy={Boolean(command.runningLabel) || Boolean(command.backgroundCancellation)}>
  <div class="request-command-entry">
    <slot name="request-line" />
  </div>

  <div class="request-command-meta">
    <div class="request-command-context" aria-label="Request context">
      <span class="command-protocol">{command.protocol}</span>
      <span class="command-environment" title={command.environmentName}>Env: {command.environmentName}</span>
      {#if command.dirty}
        <span class="command-dirty">Unsaved</span>
      {:else}
        <span class="command-saved">Saved</span>
      {/if}
      {#each command.transportCues as cue, index (index)}
        <span class="command-cue">{cue}</span>
      {/each}
    </div>

    <div class="request-command-actions">
      <button type="button" on:click={() => void actions.onSave()} disabled={disabled}>
        {command.saveLabel}
        <kbd>⌘S</kbd>
      </button>
      <button type="button" on:click={() => void actions.onRun()} disabled={disabled}>Run</button>
      {#if command.canCancel && actions.onCancel}
        <button
          type="button"
          class="command-cancel"
          on:click={() => void actions.onCancel?.()}
          disabled={command.cancellationPending || (disabled && !command.cancelDuringBusy)}
          title={disabled && !command.cancelDuringBusy && command.backgroundCancellation
            ? `Cancel ${command.backgroundCancellation.requestName} before ${command.cancelLabel.toLowerCase()}`
            : undefined}
          aria-label={command.cancellationPending
            ? 'Cancelling request'
            : disabled && !command.cancelDuringBusy && command.backgroundCancellation
              ? `${command.cancelLabel} unavailable while a background HTTP request is active`
              : `${command.cancelLabel} (Escape)`}
        >
          {command.cancellationPending ? 'Cancelling…' : command.cancelLabel}
          {#if !(disabled && !command.cancelDuringBusy && command.backgroundCancellation)}
            <kbd>Esc</kbd>
          {/if}
        </button>
      {:else}
        <button type="button" class="primary" on:click={() => void actions.onSend()} disabled={disabled}>
          {command.runningLabel ? 'Sending…' : 'Send'}
          <kbd>⌘↵</kbd>
        </button>
      {/if}
      {#if command.backgroundCancellation && actions.onCancelBackground}
        <button
          type="button"
          class="command-cancel command-background-cancel"
          title={`Cancel background request: ${command.backgroundCancellation.requestName}`}
          aria-label={command.backgroundCancellation.pending
            ? `Cancelling background request: ${command.backgroundCancellation.requestName}`
            : `Cancel background request: ${command.backgroundCancellation.requestName}`}
          on:click={() => void actions.onCancelBackground?.()}
          disabled={command.backgroundCancellation.pending}
        >
          {command.backgroundCancellation.pending ? 'Cancelling…' : `Cancel ${command.backgroundCancellation.requestName}`}
        </button>
      {/if}
    </div>

    <button
      type="button"
      class="orientation-toggle"
      data-testid="response-layout-toggle-btn"
      title="Change response orientation"
      aria-label="Change response orientation"
      on:click={() => void actions.onToggleOrientation()}
    >
      <span aria-hidden="true">{orientation === 'horizontal' ? '⇄' : '⇅'}</span>
      <span class="sr-only">{orientation === 'horizontal' ? 'Split vertically' : 'Split horizontally'}</span>
    </button>
  </div>
</section>

<style>
  .request-command-strip {
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface);
  }

  .request-command-entry :global(.request-line) {
    border-bottom: 0;
  }

  .request-command-meta {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: 10px;
    min-height: 42px;
    padding: 6px 12px 8px;
    border-top: 1px solid var(--border-subtle);
    background: var(--surface-soft);
  }

  .request-command-context,
  .request-command-actions {
    display: flex;
    align-items: center;
    min-width: 0;
    gap: 6px;
  }

  .request-command-context {
    overflow: hidden;
    white-space: nowrap;
  }

  .request-command-context span {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .command-protocol,
  .command-dirty,
  .command-saved,
  .command-cue {
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 2px 5px;
    color: var(--muted);
    font-size: 10px;
    font-weight: 750;
    letter-spacing: 0.02em;
    text-transform: uppercase;
  }

  .command-protocol {
    border-color: var(--accent-border);
    color: var(--accent-strong);
  }

  .command-dirty {
    color: var(--warning-text);
  }

  .command-saved {
    color: var(--method-get, var(--accent-strong));
  }

  .command-environment {
    color: var(--muted);
    font-size: 11px;
  }

  .request-command-actions button,
  .orientation-toggle {
    min-height: 28px;
    padding: 4px 7px;
    font-size: 11px;
  }

  .request-command-actions button.primary {
    min-height: 28px;
    padding: 4px 18px;
    font-size: 12px;
  }

  .request-command-actions kbd {
    margin-left: 4px;
    color: currentColor;
    font-family: var(--code-font-family);
    font-size: 9px;
    opacity: 0.72;
  }

  .command-cancel {
    border-color: var(--danger-border);
    color: var(--danger);
  }

  .command-background-cancel {
    max-width: 180px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }


  .orientation-toggle {
    width: 30px;
    padding: 0;
    font-size: 15px;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  @media (max-width: 1180px) {
    .request-command-meta { grid-template-columns: minmax(0, 1fr) auto auto; }
  }

  @media (max-width: 640px) {
    .request-command-meta {
      grid-template-columns: 1fr;
    }

    .request-command-actions {
      justify-content: flex-start;
    }
  }
</style>
