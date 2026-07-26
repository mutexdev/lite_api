<script lang="ts">
  // US-036 — the global search dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  import Modal from '../Modal.svelte'

  // Bindable: the query, the highlighted index and the input element reference
  // all write back to App.svelte, which owns the keyboard navigation state.
  export let globalSearchQuery: string
  export let globalSearchIndex: number
  export let globalSearchInput: HTMLInputElement | null = null

  // Mirrors App.svelte's local GlobalSearchResult. The structural shape I first
  // wrote was rejected at the call site: selectGlobalSearchResult is typed
  // against the full record, so a narrower parameter type is not assignable.
  type GlobalSearchResult = {
    id: string
    type: 'collection' | 'folder' | 'request'
    collectionId: string
    itemId?: string
    name: string
    subtitle: string
    meta: string
    rank: number
  }

  export let globalSearchResults: GlobalSearchResult[]
  export let handleGlobalSearchKeydown: (event: KeyboardEvent) => void
  export let selectGlobalSearchResult: (result: GlobalSearchResult) => Promise<void> | void
  export let closeGlobalSearch: () => void
</script>

<Modal labelledBy="global-search-title" onClose={closeGlobalSearch} dialogClass="global-search-modal">
      <header>
        <div>
          <h2 id="global-search-title">Global Search</h2>
        </div>
        <button type="button" class="icon-button" title="Close" on:click={closeGlobalSearch}>x</button>
      </header>
      <input
        class="global-search-input"
        aria-label="Global search"
        placeholder="Search collections and requests"
        bind:this={globalSearchInput}
        bind:value={globalSearchQuery}
        on:keydown={handleGlobalSearchKeydown}
      />
      {#if globalSearchResults.length === 0}
        <div class="empty-state">No results found</div>
      {:else}
        <div class="global-search-results">
          {#each globalSearchResults as result, index (result.id)}
            <button
              type="button"
              class:active={index === globalSearchIndex}
              on:mousemove={() => (globalSearchIndex = index)}
              on:click={() => selectGlobalSearchResult(result)}
            >
              <span class="global-search-type">{result.type}</span>
              <span class="global-search-main">
                <strong>{result.name}</strong>
                <small>{result.subtitle}</small>
              </span>
              <span class="global-search-meta">{result.meta}</span>
            </button>
          {/each}
        </div>
      {/if}
</Modal>
