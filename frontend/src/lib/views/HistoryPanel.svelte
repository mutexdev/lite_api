<script lang="ts">
  // US-049 — the history surface.
  //
  // Filtering is done SERVER-SIDE through HistoryQuery rather than by loading
  // everything and filtering in the component. History is capped at 500 entries
  // but each carries its headers, so pulling the whole log on every keystroke
  // would move hundreds of kilobytes across the binding to render at most a
  // screenful.
  import type { main } from '../../../wailsjs/go/models'

  export let entries: main.HistoryEntry[]
  export let query: string
  export let onlyFailures: boolean
  export let methodFilter: string
  export let busy: string
  export let collections: { id: string; name: string }[]
  export let saveTargetCollectionID: string
  export let onSearch: () => void
  export let onOpenInTab: (entry: main.HistoryEntry) => void
  export let onSaveToCollection: (entry: main.HistoryEntry) => void
  export let onClear: () => void
  export let canOpenInTab: (entry: main.HistoryEntry) => boolean

  const methods = ['', 'GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

  function statusClass(entry: main.HistoryEntry) {
    if (entry.error) return 'bad'
    if ((entry.status ?? 0) >= 400) return 'bad'
    if ((entry.status ?? 0) >= 200) return 'ok'
    return ''
  }

  function relativeTime(value: string | undefined) {
    if (!value) return ''
    const at = new Date(value).getTime()
    if (Number.isNaN(at)) return ''
    const seconds = Math.max(0, Math.floor((Date.now() - at) / 1000))
    if (seconds < 60) return `${seconds}s ago`
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
    return `${Math.floor(seconds / 86400)}d ago`
  }
</script>

<section class="history-panel">
  <header class="history-toolbar">
    <input
      data-testid="history-search"
      type="search"
      placeholder="Filter history"
      aria-label="Filter history"
      bind:value={query}
      on:input={onSearch}
    />
    <select data-testid="history-method" aria-label="Method filter" bind:value={methodFilter} on:change={onSearch}>
      {#each methods as method (method)}
        <option value={method}>{method === '' ? 'Any method' : method}</option>
      {/each}
    </select>
    <label class="checkbox-line">
      <input type="checkbox" data-testid="history-failures" bind:checked={onlyFailures} on:change={onSearch} />
      Failures only
    </label>
    <button type="button" data-testid="history-clear" on:click={onClear} disabled={busy !== '' || entries.length === 0}>
      Clear history
    </button>
  </header>

  <div class="history-save-target">
    <span class="field-label">Save to</span>
    <select data-testid="history-save-target" aria-label="Collection to save into" bind:value={saveTargetCollectionID}>
      {#each collections as collection (collection.id)}
        <option value={collection.id}>{collection.name}</option>
      {/each}
    </select>
  </div>

  {#if entries.length === 0}
    <div class="empty-state" data-testid="history-empty">No matching history.</div>
  {:else}
    <ul class="history-list">
      {#each entries as entry (entry.id)}
        <li class="history-row" data-testid="history-row">
          <div class="history-summary">
            <strong>{entry.method}</strong>
            <span class="history-url" title={entry.url}>{entry.url}</span>
            <span class={statusClass(entry)}>{entry.error ? 'error' : entry.status}</span>
            <small class="muted">{entry.durationMs ?? 0} ms · {relativeTime(entry.at)}</small>
          </div>
          {#if entry.name}<small class="muted">{entry.name}</small>{/if}
          {#if entry.redacted}
            <!--
              Stated rather than hidden: the entry deliberately does not carry
              credential values, so a request saved from it will need its auth
              filled in. Discovering that as a 401 later is worse than a note.
            -->
            <small class="muted" data-testid="history-redacted">Credential headers were not recorded.</small>
          {/if}
          <div class="button-row compact">
            <button
              type="button"
              data-testid="history-open-tab"
              on:click={() => onOpenInTab(entry)}
              disabled={busy !== '' || !canOpenInTab(entry)}
              title={canOpenInTab(entry) ? 'Open the original request' : 'The original request no longer exists'}
            >
              Open in tab
            </button>
            <button
              type="button"
              data-testid="history-save-collection"
              on:click={() => onSaveToCollection(entry)}
              disabled={busy !== '' || !saveTargetCollectionID}
            >
              Save to collection
            </button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .history-panel {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    min-width: 0;
  }

  .history-toolbar,
  .history-save-target {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    flex-wrap: wrap;
  }

  .history-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    overflow-y: auto;
  }

  .history-row {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    padding: 0.5rem;
    border: 1px solid var(--border, rgba(127, 127, 127, 0.3));
    border-radius: 4px;
    min-width: 0;
  }

  .history-summary {
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
    min-width: 0;
  }

  /* The URL is the one field that can be arbitrarily long; without this the
     row forces the whole panel to scroll horizontally. */
  .history-url {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    min-width: 0;
  }
</style>
