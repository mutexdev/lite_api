<script lang="ts">
  // US-049 — the history surface.
  //
  // Filtering is done SERVER-SIDE through HistoryQuery rather than by loading
  // everything and filtering in the component. History is capped at 500 entries
  // but each carries its headers, so pulling the whole log on every keystroke
  // would move hundreds of kilobytes across the binding to render at most a
  // screenful.
  //
  // A8-03 — this is now one of three surfaces (Runner, Flow run, History) drawn
  // with the same row anatomy as the response Timeline, via RunResultRow. It
  // used to be a `<ul>` of flat cards with the actions always on screen and no
  // way to see what a request actually sent; the Timeline had expansion and
  // History did not, for no reason other than that they were written by
  // different hands. The expansion is where History GAINS something: the
  // headers were on the entry all along (HistoryEntry.requestHeaders /
  // responseHeaders) and there was nowhere to show them.
  //
  // A8-07 — the same is true of size. HistoryEntry.size arrives on every entry
  // and this panel silently dropped it, so History showed three of the four
  // response metrics the rest of the app shows. It is on the summary line now.
  import FindBar from '../ui/FindBar.svelte'
  import PaneToolbar from '../ui/PaneToolbar.svelte'
  import RunResultRow from '../RunResultRow.svelte'
  import { formatBytes, formatDurationMs, formatRelativeTime, formatStatusCode } from '../formatting'
  import { resultTone } from '../statusTone'
  import type { history, types } from '../../../wailsjs/go/models'

  type Props = {
    entries: history.HistoryEntry[]
    query: string
    onlyFailures: boolean
    methodFilter: string
    busy: string
    collections: { id: string; name: string }[]
    saveTargetCollectionID: string
    onSearch: () => void
    onOpenInTab: (entry: history.HistoryEntry) => void
    onSaveToCollection: (entry: history.HistoryEntry) => void
    onClear: () => void
    canOpenInTab: (entry: history.HistoryEntry) => boolean
  }

  let {
    entries,
    query = $bindable(),
    onlyFailures = $bindable(),
    methodFilter = $bindable(),
    busy,
    collections,
    saveTargetCollectionID = $bindable(),
    onSearch,
    onOpenInTab,
    onSaveToCollection,
    onClear,
    canOpenInTab,
  }: Props = $props()

  const methods = ['', 'GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

  // One open row at a time, by id. A list where every row can be open at once
  // turns a screenful of results into a page of headers you have to scroll past
  // to compare two entries — which is the whole reason anyone opens two.
  let expandedID = $state('')

  function toggle(id: string) {
    expandedID = expandedID === id ? '' : id
  }
</script>

<!--
  Declared once and rendered for both directions: request and response headers
  are the same table with a different heading, and two copies of it is how the
  two quietly grow apart.
-->
{#snippet headerTable(heading: string, rows: readonly types.KeyValue[])}
  {#if rows.length > 0}
    <div class="history-headers">
      <h4>{heading}</h4>
      <dl>
        {#each rows as row, index (index)}
          <dt>{row.name}</dt>
          <dd>{row.value}</dd>
        {/each}
      </dl>
    </div>
  {/if}
{/snippet}

<section class="history-panel">
  <PaneToolbar ariaLabel="History filters">
    {#snippet left()}
      <select data-testid="history-method" aria-label="Method filter" bind:value={methodFilter} onchange={onSearch}>
        {#each methods as method (method)}
          <option value={method}>{method === '' ? 'Any method' : method}</option>
        {/each}
      </select>
      <label class="checkbox-line">
        <input type="checkbox" data-testid="history-failures" bind:checked={onlyFailures} onchange={onSearch} />
        Failures only
      </label>
    {/snippet}
    {#snippet middle()}
      <!--
        The query goes to the BACKEND, so the count beside the box is the number
        of entries the server sent back, not a client-side tally. That is the
        honest number: with the log capped at 500 there is no total to be "N of"
        here, and inventing one would claim the panel had seen entries it never
        received.
      -->
      <FindBar
        testId="history-search"
        ariaLabel="Filter history"
        placeholder="Filter history"
        value={query}
        total={entries.length}
        noun="entries"
        onChange={(next) => { query = next; onSearch() }}
      />
    {/snippet}
    {#snippet right()}
      <span class="field-label">Save to</span>
      <select data-testid="history-save-target" aria-label="Collection to save into" bind:value={saveTargetCollectionID}>
        {#each collections as collection (collection.id)}
          <option value={collection.id}>{collection.name}</option>
        {/each}
      </select>
      <button type="button" data-testid="history-clear" onclick={onClear} disabled={busy !== '' || entries.length === 0}>
        Clear history
      </button>
    {/snippet}
  </PaneToolbar>

  {#if entries.length === 0}
    <div class="empty-state" data-testid="history-empty">No matching history.</div>
  {:else}
    <ul class="history-list">
      {#each entries as entry (entry.id)}
        <RunResultRow
          testId="history-row"
          tone={resultTone({ status: entry.status, error: entry.error })}
          status={formatStatusCode(entry.status, entry.error)}
          method={entry.method}
          title={entry.url}
          subtitle={entry.name ?? ''}
          metrics={[formatDurationMs(entry.durationMs) || '0 ms', formatBytes(entry.size), formatRelativeTime(entry.at)]}
          expanded={expandedID === entry.id}
          onToggle={() => toggle(entry.id)}
        >
          {#snippet detail()}
            {#if entry.error}
              <p class="history-error bad" data-testid="history-error">{entry.error}</p>
            {/if}
            {#if entry.redacted}
              <!--
                Stated rather than hidden: the entry deliberately does not carry
                credential values, so a request saved from it will need its auth
                filled in. Discovering that as a 401 later is worse than a note.
              -->
              <small class="muted" data-testid="history-redacted">Credential headers were not recorded.</small>
            {/if}
            {@render headerTable('Request headers', entry.requestHeaders ?? [])}
            {@render headerTable('Response headers', entry.responseHeaders ?? [])}
            <div class="button-row compact">
              <button
                type="button"
                data-testid="history-open-tab"
                onclick={() => onOpenInTab(entry)}
                disabled={busy !== '' || !canOpenInTab(entry)}
                title={canOpenInTab(entry) ? 'Open the original request' : 'The original request no longer exists'}
              >
                Open in tab
              </button>
              <button
                type="button"
                data-testid="history-save-collection"
                onclick={() => onSaveToCollection(entry)}
                disabled={busy !== '' || !saveTargetCollectionID}
              >
                Save to collection
              </button>
            </div>
          {/snippet}
        </RunResultRow>
      {/each}
    </ul>
  {/if}
</section>

<style>
  /* A8-05 — this file used to be the one outlier in the Flows/Runner/History
     group still written in raw `rem` and `px`, including a
     `var(--border, rgba(127, 127, 127, 0.3))` fallback used nowhere else in the
     app. The rgba was the worse half: a fallback on a token that is always
     defined never fires, so it was dead code that also made the declaration
     look like it had a reason not to trust the theme. */
  .history-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-12);
    min-width: 0;
  }

  .history-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-8);
    overflow-y: auto;
  }

  .history-error {
    margin: 0;
    font-size: var(--font-size-12);
    font-weight: 400;
    overflow-wrap: anywhere;
  }

  .history-headers h4 {
    margin: 0 0 var(--space-4);
    color: var(--muted-strong);
    font-size: var(--font-size-11);
  }

  /* Same two-column key/value shape as Flow's extracted values, so the two
     expanded regions in this app that list name/value pairs look alike. */
  .history-headers dl {
    display: grid;
    grid-template-columns: minmax(0, auto) minmax(0, 1fr);
    gap: var(--space-4) var(--space-10);
    margin: 0;
    font-family: var(--code-font-family);
    font-size: var(--font-size-11);
  }

  .history-headers dt {
    color: var(--muted-strong);
    font-weight: 700;
  }

  .history-headers dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
</style>
