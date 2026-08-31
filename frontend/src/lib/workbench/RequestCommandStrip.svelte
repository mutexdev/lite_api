<script lang="ts">
  import type { Snippet } from 'svelte'
  import Icon from '../ui/Icon.svelte'
  import OrientationToggleButton from './OrientationToggleButton.svelte'
  import type { RequestCommandActions, RequestCommandState } from './types'

  // US-028 — runes. App.svelte uses bind:this on this component, which is an
  // instance reference rather than a prop binding and needs no $bindable.
  type Props = {
    command: RequestCommandState
    actions: RequestCommandActions
    disabled?: boolean
    orientation?: 'horizontal' | 'vertical'
    /**
     * A1-02. How many requests the Collection Runner currently has selected.
     *
     * WHAT WENT WRONG. `actions.onRun` is `runCollection`, and `runCollection`
     * opens with `if (selectedItemIds.length === 0) return`. The `disabled`
     * prop above is `busy !== '' || hasActiveHTTPTransport` and knows nothing
     * about that selection, so the button rendered enabled, took the click, and
     * returned — no run, no view change, no message. A button that is bright,
     * pressable and inert is worse than one that is greyed out, because the
     * user's next move is to press it again.
     *
     * Optional, and `undefined` means "the mount has not been updated to pass
     * it". `App.svelte` belongs to another pass this wave and already computes
     * exactly this value as `runnerSelectedCount`; the handoff carries the one
     * line. Until it lands the button cannot be disabled on an empty selection
     * — nothing in this component can know — so it falls back to the other half
     * of the fix and says what it will run instead of implying it runs this.
     */
    runSelectionCount?: number
    /** Named in the tooltip so "collection" is not an abstraction. */
    runCollectionName?: string
    // Replaces <slot name="request-line">, which is deprecated in runes mode.
    requestLine?: Snippet
  }

  let {
    command,
    actions,
    disabled = false,
    orientation = 'horizontal',
    runSelectionCount = undefined,
    runCollectionName = '',
    requestLine,
  }: Props = $props()

  const runSelectionKnown = $derived(typeof runSelectionCount === 'number')
  const runSelectionEmpty = $derived(runSelectionCount === 0)
  const runTarget = $derived(runCollectionName ? `the collection "${runCollectionName}"` : 'this collection')

  /*
   * The whole of A1-02's second half in one string. Every branch says which
   * scope the command acts on, because the button sat beside Save and Send —
   * two per-request actions — with the bare word "Run" on it, and there was
   * nothing in its label, styling or position to suggest it ran anything other
   * than the request the user was looking at.
   */
  const runTitle = $derived(
    runSelectionEmpty
      ? `Nothing is selected in the Collection Runner. Open the Runner and choose requests before running ${runTarget}.`
      : runSelectionKnown
        ? `Run the ${runSelectionCount} request${runSelectionCount === 1 ? '' : 's'} selected in the Collection Runner for ${runTarget}. This does not send the request open here — use Send for that.`
        : `Runs the Collection Runner's current selection for ${runTarget}. This does not send the request open here — use Send for that.`,
  )
</script>

<section class="request-command-strip" aria-label="Request command center" aria-busy={Boolean(command.runningLabel) || Boolean(command.backgroundCancellation)}>
  <div class="request-command-entry">
    {@render requestLine?.()}
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
      <!--
        A1-02, first half. This command's scope is the collection, and the two
        buttons to its right have the scope of the open request, so it is drawn
        as a different KIND of button and fenced off from them: quiet fill, a
        list mark, the word "collection" in the label, and a rule between the
        two groups. Reading left to right the row is now "something else" |
        "this request", instead of three identical buttons one of which lied.

        It is first rather than between Save and Send because the primary
        action belongs at the end of the group — putting the cross-scope
        command in the middle is what made it read as a sibling of both.
      -->
      <button
        type="button"
        class="command-scope-collection"
        title={runTitle}
        aria-label={runTitle}
        onclick={() => void actions.onRun()}
        disabled={disabled || runSelectionEmpty}
      >
        <Icon name="list" size={13} />
        <span>Run collection</span>
        {#if runSelectionKnown && !runSelectionEmpty}<span class="command-scope-count">{runSelectionCount}</span>{/if}
      </button>

      <span class="command-scope-divider" aria-hidden="true"></span>

      <button type="button" onclick={() => void actions.onSave()} disabled={disabled}>
        {command.saveLabel}
        <kbd>⌘S</kbd>
      </button>
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
    </div>

    <!--
      A4-02. This was a `⇄`/`⇅` text glyph in a 30px box while the command bar
      a few hundred pixels above rendered a stroke SVG for the same command,
      calling the same handler, with the ⌘J hint that this one did not show.
      Both call sites now render the same component, so there is one mark, one
      label and one shortcut disclosure for one command.
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

  .request-command-actions button {
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

  /*
    A1-02. The visual half of "this does not act on the request you are looking
    at": no border and no fill at rest, so it does not read as a peer of the
    bordered Save beside it, and the muted colour of the context chips on the
    other end of the row — which is where the rest of the row's "about the
    surroundings" information already lives.
  */
  .command-scope-collection {
    display: inline-flex;
    align-items: center;
    gap: var(--space-5);
    border-color: transparent;
    background: transparent;
    color: var(--muted-strong);
  }

  .command-scope-collection:hover:not(:disabled) {
    border-color: var(--border);
    background: var(--surface);
    color: var(--text);
  }

  /*
    The count is the "say what it will run" half made visible without opening a
    tooltip. Tabular figures so the button does not change width as the runner
    selection changes underneath it.
  */
  .command-scope-count {
    /* No pill token exists; 999px is the literal the notification badge in
       WorkspaceCommandBar already uses for the same shape. */
    border-radius: 999px;
    padding: 0 var(--space-5);
    background: var(--surface-alt);
    color: var(--muted);
    font-size: var(--font-size-10);
    font-weight: 800;
    font-variant-numeric: tabular-nums;
  }

  /* The fence between the collection-scoped command and the request-scoped ones. */
  .command-scope-divider {
    width: 1px;
    height: 18px;
    margin-inline: var(--space-3);
    background: var(--border);
  }

  /*
    A4-11. The 1180px query used to be here and set `grid-template-columns` to
    the identical value the base rule already declares — a rule that had done
    nothing since it was written. The remaining step is the shell's own compact
    breakpoint from `layout.ts` (680), not the 640 this file had picked, which
    put the strip's reflow 40px away from the one `style.css` performs on
    `.request-command-meta` — this element — at 680.
  */
  @media (max-width: 680px) {
    .request-command-meta {
      grid-template-columns: 1fr;
    }

    .request-command-actions {
      justify-content: flex-start;
    }
  }
</style>
