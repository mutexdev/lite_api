<script lang="ts">
  // US-036 — the global search dialog, lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it.
  //
  // ⌘K AND ⌘⇧P ARE ONE PAIR AND HAD TWO ACCESSIBILITY STORIES. The command
  // palette next door has always been a real single-select listbox: the input
  // carries aria-controls and aria-activedescendant, the results container is
  // role="listbox", each row is role="option" with aria-selected. This file —
  // the more used of the two — had none of it. The rows were plain buttons and
  // the highlight was a CSS class, so a screen reader was told "twelve
  // buttons", the arrow keys moved a highlight nothing announced, and Enter
  // opened something the user had never been told was selected.
  //
  // The wiring below is the palette's, deliberately verbatim rather than
  // improved, down to the id shape and the placement of role="listbox" on the
  // existing `.global-search-results` element. Two surfaces that must not drift
  // are cheapest to keep together when the second one is a copy of the first,
  // and `.global-search-results > button` is a CHILD selector — a wrapper
  // introduced to hold the role would have silently unstyled every result.
  //
  // The remaining structural difference is that this file draws its empty state
  // with `{:else}` INSIDE the listbox rather than beside it, which the palette
  // already did. A listbox that vanishes on an empty query is a control whose
  // aria-controls target stops existing mid-typing.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'
  import { emptyStateMessage } from '../../sidebar/emptyState'

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

  type Props = {
    // Bindable: the query, the highlighted index and the input element
    // reference all write back to App.svelte, which owns the keyboard
    // navigation state.
    globalSearchQuery: string
    globalSearchIndex: number
    globalSearchInput?: HTMLInputElement | null
    globalSearchResults: GlobalSearchResult[]
    handleGlobalSearchKeydown: (event: KeyboardEvent) => void
    selectGlobalSearchResult: (result: GlobalSearchResult) => Promise<void> | void
    closeGlobalSearch: () => void
  }

  let {
    globalSearchQuery = $bindable(),
    globalSearchIndex = $bindable(),
    globalSearchInput = $bindable(null),
    globalSearchResults,
    handleGlobalSearchKeydown,
    selectGlobalSearchResult,
    closeGlobalSearch
  }: Props = $props()

  const optionId = (id: string) => `global-search-option-${id}`

  // Named separately because aria-activedescendant must be dropped entirely —
  // not set to an empty string — when nothing is highlighted, and because the
  // index can outrun the list by one keystroke while results are re-filtering.
  const activeResult = $derived(globalSearchResults[globalSearchIndex])
</script>

<Modal labelledBy="global-search-title" onClose={closeGlobalSearch} dialogClass="global-search-modal">
      <header>
        <div>
          <h2 id="global-search-title">Global Search</h2>
        </div>
        <IconButton icon="close" label="Close" onclick={closeGlobalSearch} />
      </header>
      <input
        data-modal-autofocus
        class="global-search-input"
        aria-label="Global search"
        aria-controls="global-search-results"
        aria-activedescendant={activeResult ? optionId(activeResult.id) : undefined}
        placeholder="Search collections and requests"
        bind:this={globalSearchInput}
        bind:value={globalSearchQuery}
        onkeydown={handleGlobalSearchKeydown}
      />
      <div id="global-search-results" class="global-search-results" role="listbox" aria-label="Search results">
        {#each globalSearchResults as result, index (result.id)}
          <button
            id={optionId(result.id)}
            type="button"
            role="option"
            aria-selected={index === globalSearchIndex}
            class:active={index === globalSearchIndex}
            onmousemove={() => (globalSearchIndex = index)}
            onclick={() => selectGlobalSearchResult(result)}
          >
            <span class="global-search-type">{result.type}</span>
            <span class="global-search-main">
              <strong>{result.name}</strong>
              <small>{result.subtitle}</small>
            </span>
            <span class="global-search-meta">{result.meta}</span>
          </button>
        {:else}
          <!--
            Was "No results found", one of six sentences the audit found for
            this one state. The rule now lives in lib/sidebar/emptyState.ts and
            quotes the query back, which is the one fact the user cannot verify
            from a list that is empty.
          -->
          <div class="empty-state">{emptyStateMessage({ query: globalSearchQuery, noun: 'collections' })}</div>
        {/each}
      </div>
</Modal>
