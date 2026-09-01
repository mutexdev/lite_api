<script lang="ts">
  import type { Snippet } from 'svelte'
  import IconButton from '../ui/IconButton.svelte'
  import OrientationToggleButton from './OrientationToggleButton.svelte'
  import type { RequestCommandActions, RequestCommandState } from './types'

  /*
   * D4 — one row.
   *
   * WHAT WENT WRONG. This component rendered two rows: the URL line, and under
   * it a 42px band carrying five uppercase chips (protocol, `Env: …`, SAVED,
   * TLS verify, Proxy: system) plus a collection-scoped Run button. Every one
   * of those facts was already on screen or unchanging: the protocol is on the
   * tab, the environment picker is 40px above in the command bar, "saved" is
   * now the tab's dirty dot (D6), and the two transport cues read "TLS verify"
   * and "Proxy: system" — the defaults — on every request anyone ever opens, so
   * a row of chrome burned itself in as wallpaper and stopped being read at
   * all. That is what made the genuinely interesting values ("TLS off") vanish
   * with it. `commandState.ts` now yields cues ONLY when they are non-default,
   * and App.svelte renders the survivors at the end of the sub-tab row.
   */
  // US-028 — runes. App.svelte uses bind:this on this component, which is an
  // instance reference rather than a prop binding and needs no $bindable.
  type Props = {
    command: RequestCommandState
    actions: RequestCommandActions
    disabled?: boolean
    orientation?: 'horizontal' | 'vertical'
    // Replaces <slot name="request-line">, which is deprecated in runes mode.
    requestLine?: Snippet
  }

  let {
    command,
    actions,
    disabled = false,
    orientation = 'horizontal',
    requestLine,
  }: Props = $props()

  /*
   * The dot is decorative — `aria-hidden` on a `::after` is not expressible —
   * so the unsaved state has to reach the accessible name, which is also the
   * tooltip. `saveLabel` rather than a literal "Save" because a transient
   * request saves as "Save temp" and that distinction is the whole point of it.
   */
  const saveLabel = $derived(`${command.saveLabel} (⌘S)${command.dirty ? ' — unsaved changes' : ''}`)
</script>

<section class="request-command-strip" aria-label="Request command center" aria-busy={Boolean(command.runningLabel) || Boolean(command.backgroundCancellation)}>
  <div class="request-command-entry">
    {@render requestLine?.()}
  </div>

  <div class="request-command-actions">
    {#if command.canCancel && actions.onCancel}
      <button
        type="button"
        class="command-cancel"
        onclick={() => void actions.onCancel?.()}
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
      <button type="button" class="primary" onclick={() => void actions.onSend()} disabled={disabled}>
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
        onclick={() => void actions.onCancelBackground?.()}
        disabled={command.backgroundCancellation.pending}
      >
        {command.backgroundCancellation.pending ? 'Cancelling…' : `Cancel ${command.backgroundCancellation.requestName}`}
      </button>
    {/if}

    <!--
      D9. Save was a bordered text button with a `⌘S` kbd sitting beside Send,
      which made the row read as two equally weighted commands; it is the
      secondary one, and the shortcut belongs in the tooltip like every other
      icon button in the shell.
    -->
    <span class="request-save" class:dirty={command.dirty}>
      <IconButton icon="save" label={saveLabel} onclick={() => void actions.onSave()} {disabled} />
    </span>

    <!--
      A4-02. This was a `⇄`/`⇅` text glyph in a 30px box while the command bar
      a few hundred pixels above rendered a stroke SVG for the same command,
      calling the same handler, with the ⌘J hint that this one did not show.
      The command bar's copy is gone (D3) and this is now the only one.
    -->
    <OrientationToggleButton
      {orientation}
      variant="strip"
      testId="response-layout-toggle-btn"
      onclick={() => void actions.onToggleOrientation()}
    />
  </div>
</section>

<style>
  .request-command-strip {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    padding-right: var(--space-12);
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface);
  }

  /*
    The request line brings its own 12px padding and used to bring the row's
    bottom border too. Both rows are one row now, so the border belongs to the
    section and the entry is the flexible half.
  */
  .request-command-entry {
    flex: 1 1 auto;
    min-width: 0;
  }

  .request-command-entry :global(.request-line) {
    border-bottom: 0;
  }

  .request-command-actions {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--space-6);
  }

  .request-command-actions button {
    min-height: 28px;
    padding: var(--space-4) var(--space-7);
    font-size: var(--font-size-11);
  }

  .request-command-actions button.primary {
    min-height: 28px;
    padding: var(--space-4) var(--space-18);
    font-size: var(--font-size-12);
  }

  .request-command-actions kbd {
    margin-left: var(--space-4);
    color: currentColor;
    font-family: var(--code-font-family);
    font-size: var(--font-size-9);
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

  /*
    D4/D6. The unsaved marker is a dot on the control that clears it, the same
    mark the tab carries, instead of the SAVED/UNSAVED chip pair that spent a
    row saying "nothing has happened" on every untouched request.
  */
  .request-save {
    position: relative;
    display: inline-flex;
  }

  .request-save.dirty::after {
    content: '';
    position: absolute;
    top: var(--space-2);
    right: var(--space-2);
    width: 6px;
    height: 6px;
    border-radius: var(--radius-pill);
    background: var(--accent);
    pointer-events: none;
  }

  /*
    A4-11. The 1180px query used to be here and set `grid-template-columns` to
    the identical value the base rule already declared — a rule that had done
    nothing since it was written. The remaining step is the shell's own compact
    breakpoint from `layout.ts` (680), not the 640 this file had picked. At that
    width the URL field needs the full line, so the actions wrap under it.
  */
  @media (max-width: 680px) {
    .request-command-strip {
      flex-wrap: wrap;
      align-items: stretch;
    }

    .request-command-entry {
      flex: 1 0 100%;
    }

    .request-command-actions {
      flex: 1 0 100%;
      padding-bottom: var(--space-8);
    }
  }
</style>
