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
  import Modal from '../Modal.svelte'

  type CommandPaletteAction = { id: string; label: string; shortcut?: string }

  // Bindable: the query, the active index and the input reference are all
  // driven by App.svelte's keyboard handling.
  export let commandPaletteQuery: string
  export let commandPaletteActiveIndex: number
  export let commandPaletteInput: HTMLInputElement | null = null

  export let visibleCommandPaletteActions: CommandPaletteAction[]
  export let handleCommandPaletteKeydown: (event: KeyboardEvent) => void
  export let runCommandPaletteAction: (action: CommandPaletteAction) => void
  export let closeCommandPalette: () => Promise<void> | void
</script>

<Modal labelledBy="command-palette-title" onClose={() => void closeCommandPalette()} dialogClass="global-search-modal command-palette">
      <header>
        <div><h2 id="command-palette-title">Command palette</h2></div>
        <button type="button" class="icon-button" aria-label="Close command palette" title="Close" on:click={() => void closeCommandPalette()}>×</button>
      </header>
      <input data-modal-autofocus class="global-search-input" bind:this={commandPaletteInput} bind:value={commandPaletteQuery} on:input={() => (commandPaletteActiveIndex = 0)} on:keydown={handleCommandPaletteKeydown} aria-label="Filter commands" aria-controls="command-palette-commands" aria-activedescendant={visibleCommandPaletteActions[commandPaletteActiveIndex] ? `command-palette-option-${visibleCommandPaletteActions[commandPaletteActiveIndex].id}` : undefined} placeholder="Type a command" />
      <div id="command-palette-commands" class="global-search-results" role="listbox" aria-label="Commands">
        {#each visibleCommandPaletteActions as action, index (action.id)}
          <button id={`command-palette-option-${action.id}`} type="button" role="option" aria-selected={index === commandPaletteActiveIndex} class:active={index === commandPaletteActiveIndex} on:mouseenter={() => (commandPaletteActiveIndex = index)} on:click={() => runCommandPaletteAction(action)}>
            <span class="global-search-main"><strong>{action.label}</strong></span>
            {#if action.shortcut}<kbd>{action.shortcut}</kbd>{/if}
          </button>
        {:else}
          <div class="empty-state">No commands match.</div>
        {/each}
      </div>
</Modal>
