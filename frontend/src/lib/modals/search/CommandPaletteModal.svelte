<script lang="ts">
  // US-036 — the command palette, lifted out of App.svelte so its markup is not
  // in the initial chunk. Imported dynamically from inside the {#if} that gates
  // it.
  //
  // THE ID IS DELIBERATELY A STRING, and the history matters. This prop was once
  // widened to `id: string`, and that was reverted: App.svelte's
  // runCommandPaletteAction took a WorkbenchCommandID, so the widened prop was
  // not assignable by parameter contravariance, and importing the real
  // WorkbenchCommandID was the correct fix at the time.
  //
  // It is no longer. The palette now carries object-scoped entries alongside the
  // workbench commands — "Rename — Login" acts on the sidebar's current object,
  // not on a global command — and those use a `sidebar:` prefixed id that is not
  // a WorkbenchCommandID and never will be. App.svelte's handler was widened to
  // match, so this widening is the one that fits rather than the one that fails.
  //
  // THIS FILE IS THE TEMPLATE ITS SIBLING NOW COPIES. GlobalSearchModal had no
  // listbox semantics at all; it has this file's, verbatim. The two are kept
  // legible side by side on purpose — same prop shape, same option-id helper,
  // same `{:else}` empty state inside the listbox — because the pair drifting
  // apart unnoticed is exactly how one of them ended up with no ARIA.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'
  import { emptyStateMessage } from '../../sidebar/emptyState'

  type CommandPaletteAction = { id: string; label: string; shortcut?: string }

  type Props = {
    // Bindable: the query, the active index and the input reference are all
    // driven by App.svelte's keyboard handling.
    commandPaletteQuery: string
    commandPaletteActiveIndex: number
    commandPaletteInput?: HTMLInputElement | null
    visibleCommandPaletteActions: CommandPaletteAction[]
    handleCommandPaletteKeydown: (event: KeyboardEvent) => void
    runCommandPaletteAction: (action: CommandPaletteAction) => void
    closeCommandPalette: () => Promise<void> | void
  }

  let {
    commandPaletteQuery = $bindable(),
    commandPaletteActiveIndex = $bindable(),
    commandPaletteInput = $bindable(null),
    visibleCommandPaletteActions,
    handleCommandPaletteKeydown,
    runCommandPaletteAction,
    closeCommandPalette
  }: Props = $props()

  const optionId = (id: string) => `command-palette-option-${id}`

  const activeAction = $derived(visibleCommandPaletteActions[commandPaletteActiveIndex])
</script>

<Modal labelledBy="command-palette-title" onClose={() => void closeCommandPalette()} dialogClass="global-search-modal command-palette">
      <header>
        <div><h2 id="command-palette-title">Command palette</h2></div>
        <IconButton icon="close" label="Close" onclick={() => void closeCommandPalette()} />
      </header>
      <input
        data-modal-autofocus
        class="global-search-input"
        aria-label="Filter commands"
        aria-controls="command-palette-commands"
        aria-activedescendant={activeAction ? optionId(activeAction.id) : undefined}
        placeholder="Type a command"
        bind:this={commandPaletteInput}
        bind:value={commandPaletteQuery}
        oninput={() => (commandPaletteActiveIndex = 0)}
        onkeydown={handleCommandPaletteKeydown}
      />
      <div id="command-palette-commands" class="global-search-results" role="listbox" aria-label="Commands">
        {#each visibleCommandPaletteActions as action, index (action.id)}
          <button
            id={optionId(action.id)}
            type="button"
            role="option"
            aria-selected={index === commandPaletteActiveIndex}
            class:active={index === commandPaletteActiveIndex}
            onmouseenter={() => (commandPaletteActiveIndex = index)}
            onclick={() => runCommandPaletteAction(action)}
          >
            <span class="global-search-main"><strong>{action.label}</strong></span>
            {#if action.shortcut}<kbd>{action.shortcut}</kbd>{/if}
          </button>
        {:else}
          <!-- Was "No commands match." — the only one of the six empty-result
               strings that ended in a period. One rule, one sentence shape. -->
          <div class="empty-state">{emptyStateMessage({ query: commandPaletteQuery, noun: 'commands' })}</div>
        {/each}
      </div>
</Modal>
