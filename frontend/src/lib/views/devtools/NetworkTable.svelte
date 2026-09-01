<script lang="ts">
  /**
   * The network log table. There is one of these now; there were two.
   *
   * WHAT WENT WRONG. `appState.networkLog` had two renderings in App.svelte,
   * three thousand lines apart, reachable independently — DevTools ▸ Network
   * (`devToolsTab === 'network'`) and the "Network Log" view the `open-network`
   * command opens (`activeView === 'network'`). Same array, same rows, same
   * fields. One of them was virtualised, sortable with a tri-state header,
   * column-resizable, method-filtered, row-selectable into a request/response
   * detail pane, and persisted its sort and widths to preferences. The other
   * was a nine-line `<table>` with a fixed Method/URL/Status/Time/Error header
   * that printed `row.status` and `row.durationMs` raw, had no empty state, and
   * did not so much as call the `networkLogTime` helper defined two hundred
   * lines above it in its own file.
   *
   * Both were on the main navigation. The audit called this the clearest single
   * instance of "the app looks like a different application in each section",
   * and it is: not two screens that drifted, but one screen implemented twice
   * at opposite ends of the quality range, by the same file.
   *
   * WHY THE FIX IS SHAPED THIS WAY. The obvious repair — delete the legacy view
   * and repoint `open-network` at DevTools — is the right END state and the
   * wrong first move, because `activeView` is a union threaded through the
   * sidebar, the command palette, tab restore and stored preferences: removing
   * a member of it breaks the restore of a session whose last view was that
   * one, which is a different change with a different blast radius. Extracting
   * the good table into a component both call sites mount closes the fidelity
   * gap now and makes deleting the route afterwards a route change rather than
   * a table change.
   *
   * WHAT THIS COMPONENT OWNS, AND WHY IT OWNS IT. Everything that is a fact
   * about the table rather than about the app: the method filters, the sort,
   * the column widths, which row is selected, which detail subtab is showing,
   * and the virtual window. That state used to be eleven `let`s and twelve
   * functions in App.svelte, which is why mounting the table a second time was
   * not something anyone could do cheaply — the markup was the small part.
   *
   * What it does NOT own is persistence. Preferences arrive as a prop and
   * changes leave through `onPreferencesChange`, because saving them is a call
   * to the Go layer and this component has no business knowing that. The
   * incoming/outgoing pair is guarded by a serialised key for the same reason
   * App.svelte guarded it: preferences are re-delivered on every app-state
   * refresh, and re-applying an unchanged payload mid-drag would snap the
   * column back under the pointer.
   *
   * There is deliberately no `<style>` block. Every selector this markup uses
   * — `.devtools-network-table`, `.network-layout`, `.network-filter-bar`,
   * `.method-filter-list`, `.column-resizer`, `.network-spacer` — is defined in
   * style.css and is unchanged, so moving the markup into a component moved no
   * pixels. A scoped copy would be a second definition free to drift from the
   * one the legacy view will now also be painted by.
   */
  import type { types } from '../../../../wailsjs/go/models'
  import { computeWindow } from '../../virtualList'
  import {
    DEFAULT_NETWORK_COLUMN_WIDTHS,
    NETWORK_COLUMNS,
    NETWORK_METHODS,
    NETWORK_SORT_KEYS,
    filteredNetworkRows,
    networkDomain,
    networkLogTime,
    networkPath,
    networkSizeDisplay,
    networkSortAriaValue,
    networkSortLabel,
    networkSortPreference,
    networkStatusDisplay,
    nextNetworkSort,
    normalizedNetworkColumnWidths,
    normalizedNetworkMethod,
    normalizedNetworkSortDirection,
    normalizedNetworkSortKey,
    resizeAdjacentColumns,
    sortNetworkRows,
    type NetworkSortDirection,
    type NetworkSortKey
  } from '../../networkSort'

  type NetworkPreferences = {
    sortKey: NetworkSortKey | ''
    sortDirection: NetworkSortDirection
    columnWidths: number[]
  }

  type Props = {
    /** The raw log, unfiltered and unsorted. Both of those happen in here. */
    rows: types.NetworkLog[]
    /**
     * Accessible name for the `<table>`. Defaulted rather than required so a
     * call site cannot end up with an unnamed grid by omission — the state
     * every table in the app was in before this wave.
     */
    label?: string
    preferences?: types.DevToolsNetworkPreferences | undefined
    onPreferencesChange?: (preferences: NetworkPreferences) => void
    /**
     * The detail pane. Width and its resize gesture stay with the caller
     * because the width is a stored preference of the DevTools shell; omit the
     * handler and the resizer is not rendered at all.
     */
    detailsPanelWidth?: number
    onStartDetailsResize?: (event: MouseEvent) => void
  }

  let {
    rows,
    label = 'Network requests',
    preferences = undefined,
    onPreferencesChange = () => {},
    detailsPanelWidth = 400,
    onStartDetailsResize = undefined
  }: Props = $props()

  const detailTabs: { id: string; label: string }[] = [
    { id: 'request', label: 'Request' },
    { id: 'response', label: 'Response' },
    { id: 'network', label: 'Network' }
  ]

  let methodFilters = $state<Record<string, boolean>>(
    Object.fromEntries(NETWORK_METHODS.map((method) => [method, true])) as Record<string, boolean>
  )
  let sortKey = $state<NetworkSortKey | ''>('')
  let sortDirection = $state<NetworkSortDirection>('')
  let columnWidths = $state<number[]>([...DEFAULT_NETWORK_COLUMN_WIDTHS])
  let resizingColumn = $state(-1)
  let selectedLogID = $state('')
  let detailTab = $state('request')
  let appliedPreferencesKey = $state('')

  function preferencePayload(
    key: NetworkSortKey | '',
    direction: NetworkSortDirection,
    widths: number[] | undefined
  ): NetworkPreferences {
    const sort = networkSortPreference(key, direction, NETWORK_SORT_KEYS)
    return {
      sortKey: sort.key,
      sortDirection: sort.direction,
      columnWidths: normalizedNetworkColumnWidths(widths)
    }
  }

  function payloadFor(stored: types.DevToolsNetworkPreferences | undefined): NetworkPreferences {
    return preferencePayload(
      normalizedNetworkSortKey(stored?.sortKey, NETWORK_SORT_KEYS),
      normalizedNetworkSortDirection(stored?.sortDirection),
      stored?.columnWidths
    )
  }

  // The guard is on the SERIALISED payload, not on the object identity: the
  // preferences object is rebuilt on every app-state delivery, so an identity
  // check would re-apply on every refresh and undo a resize the instant the
  // next snapshot arrived.
  $effect(() => {
    const incoming = payloadFor(preferences)
    const key = JSON.stringify(incoming)
    if (key === appliedPreferencesKey) return
    sortKey = incoming.sortKey
    sortDirection = incoming.sortDirection
    columnWidths = incoming.columnWidths
    appliedPreferencesKey = key
  })

  function commitPreferences(updates: Partial<NetworkPreferences>) {
    const payload = preferencePayload(
      updates.sortKey ?? sortKey,
      updates.sortDirection ?? sortDirection,
      updates.columnWidths ?? columnWidths
    )
    sortKey = payload.sortKey
    sortDirection = payload.sortDirection
    columnWidths = payload.columnWidths
    appliedPreferencesKey = JSON.stringify(payload)
    onPreferencesChange(payload)
  }

  const methodCounts = $derived(
    Object.fromEntries(
      NETWORK_METHODS.map((method) => [method, rows.filter((row) => normalizedNetworkMethod(row) === method).length])
    ) as Record<string, number>
  )
  const activeFilterCount = $derived(NETWORK_METHODS.filter((method) => methodFilters[method]).length)
  const visibleRows = $derived(sortNetworkRows(filteredNetworkRows(rows, methodFilters), sortKey, sortDirection))
  const sortLabels = $derived(
    Object.fromEntries(NETWORK_SORT_KEYS.map((key) => [key, networkSortLabel(key, sortKey, sortDirection)])) as Record<NetworkSortKey, string>
  )
  const ariaSort = $derived(
    Object.fromEntries(NETWORK_SORT_KEYS.map((key) => [key, networkSortAriaValue(key, sortKey, sortDirection)])) as Record<
      NetworkSortKey,
      'ascending' | 'descending' | 'none'
    >
  )
  const tableWidth = $derived(columnWidths.reduce((total, width) => total + width, 0))
  const selectedRow = $derived(visibleRows.find((row) => row.id === selectedLogID) ?? visibleRows[0])

  // US-032. The row height is measured from a rendered row rather than
  // hard-coded, because it follows the app's font-size and density settings;
  // the fallback keeps the window arithmetic sane until the first measurement,
  // rather than dividing by zero.
  const rowFallbackHeight = 28
  let scrollTop = $state(0)
  let viewportHeight = $state(0)
  let measuredRowHeight = $state(0)
  const rowHeight = $derived(measuredRowHeight || rowFallbackHeight)

  function measureViewport(node: HTMLElement) {
    const update = () => {
      viewportHeight = node.clientHeight
      const row = node.querySelector<HTMLElement>('tbody tr[data-network-row]')
      if (row && row.offsetHeight > 0) measuredRowHeight = row.offsetHeight
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(node)
    return {
      destroy() {
        observer.disconnect()
      }
    }
  }

  const virtualWindow = $derived(
    computeWindow({ total: visibleRows.length, rowHeight, viewportHeight, scrollTop })
  )
  const windowedRows = $derived(visibleRows.slice(virtualWindow.startIndex, virtualWindow.endIndex))

  $effect(() => {
    if (visibleRows.length > 0 && (!selectedLogID || !visibleRows.some((row) => row.id === selectedLogID))) {
      selectedLogID = visibleRows[0].id
      detailTab = 'request'
    }
  })

  $effect(() => {
    if (visibleRows.length === 0 && selectedLogID) selectedLogID = ''
  })

  function selectRow(row: types.NetworkLog) {
    selectedLogID = row.id
    detailTab = 'request'
  }

  function selectDetailTab(id: string) {
    if (detailTabs.some((tab) => tab.id === id)) detailTab = id
  }

  function cycleSort(key: NetworkSortKey) {
    const next = nextNetworkSort(sortKey, sortDirection, key)
    commitPreferences({ sortKey: next.key, sortDirection: next.direction })
  }

  function setMethodFilter(method: string, enabled: boolean) {
    methodFilters = { ...methodFilters, [method]: enabled }
  }

  function setAllMethodFilters(enabled: boolean) {
    methodFilters = Object.fromEntries(NETWORK_METHODS.map((method) => [method, enabled])) as Record<string, boolean>
  }

  // The widths are committed once on mouseup, not on every mousemove: each
  // commit is a round trip to the Go preferences store, and a drag produces
  // dozens of moves.
  function startColumnResize(index: number, event: MouseEvent) {
    event.preventDefault()
    event.stopPropagation()
    const startX = event.clientX
    const startWidths = [...columnWidths]
    let latestWidths = startWidths
    resizingColumn = index
    const handleMove = (moveEvent: MouseEvent) => {
      latestWidths = resizeAdjacentColumns(startWidths, index, moveEvent.clientX - startX)
      columnWidths = latestWidths
    }
    const cleanup = () => {
      window.removeEventListener('mousemove', handleMove)
      window.removeEventListener('mouseup', handleUp)
      resizingColumn = -1
    }
    const handleUp = () => {
      cleanup()
      commitPreferences({ columnWidths: latestWidths })
    }
    window.addEventListener('mousemove', handleMove)
    window.addEventListener('mouseup', handleUp)
  }
</script>

<div class="network-filter-bar" aria-label="Filter requests by method">
  <div>
    <strong>Filter by Method</strong>
    <span>{activeFilterCount === NETWORK_METHODS.length ? 'All' : `${activeFilterCount}/${NETWORK_METHODS.length}`}</span>
  </div>
  <div class="button-row compact">
    <button type="button" onclick={() => setAllMethodFilters(false)}>Hide All</button>
    <button type="button" onclick={() => setAllMethodFilters(true)}>Show All</button>
  </div>
  <div class="method-filter-list">
    {#each NETWORK_METHODS as method (method)}
      <label>
        <input type="checkbox" checked={methodFilters[method]} onchange={(event) => setMethodFilter(method, event.currentTarget.checked)} />
        <span>{method} {methodCounts[method] ?? 0}</span>
      </label>
    {/each}
  </div>
</div>

{#if rows.length === 0}
  <div class="empty-state devtools-empty">
    <strong>No network requests</strong>
    <span>Requests will appear here as you make API calls</span>
  </div>
{:else if visibleRows.length === 0}
  <!--
    A separate state from "no requests", because they need different actions
    from the reader. An empty log means make a request; an empty filtered log
    means the rows exist and the method filter above is hiding them, and the
    old table said "No network requests" to both — which reads as the log
    having been cleared.
  -->
  <div class="empty-state devtools-empty">
    <strong>No requests match the method filter</strong>
    <span>{rows.length} {rows.length === 1 ? 'request is' : 'requests are'} hidden. Choose Show All above to see them.</span>
  </div>
{:else}
  <div class="network-layout" style={`--network-details-width: ${detailsPanelWidth}px;`}>
    <div
      class="table-scroll network-table-scroll"
      class:resizing={resizingColumn >= 0}
      use:measureViewport
      onscroll={(event) => (scrollTop = event.currentTarget.scrollTop)}
    >
      <table class="devtools-network-table" aria-label={label} style={`min-width: ${tableWidth}px;`}>
        <colgroup>
          {#each columnWidths as width, index (index)}
            <col style={`width: ${width}px;`} />
          {/each}
        </colgroup>
        <thead>
          <tr>
            {#each NETWORK_COLUMNS as column, index (column.key)}
              <th aria-sort={ariaSort[column.key]}>
                <button type="button" class="network-sort-button" onclick={() => cycleSort(column.key)}>{column.label} {sortLabels[column.key]}</button>
                {#if index < NETWORK_COLUMNS.length - 1}
                  <button
                    type="button"
                    class="column-resizer"
                    class:active={resizingColumn === index}
                    aria-label={`Resize ${column.label} column`}
                    onmousedown={(event) => startColumnResize(index, event)}
                  ></button>
                {/if}
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          <!--
            Spacer rows rather than a transform or absolute positioning: this is
            a real <table>, and anything that takes rows out of flow breaks the
            colgroup widths the resizable columns depend on.
          -->
          {#if virtualWindow.topPadding > 0}
            <tr aria-hidden="true" class="network-spacer"><td colspan={NETWORK_COLUMNS.length} style={`height: ${virtualWindow.topPadding}px; padding: 0; border: none;`}></td></tr>
          {/if}
          {#each windowedRows as row (row.id)}
            <!--
              The whole row selects, not just the Method cell. Only that one
              cell was clickable before, so clicking the path, status or
              duration of a row did nothing while the details pane went on
              showing whichever row was selected — which reads as the pane
              showing the wrong request, not as the click having missed.

              role/tabindex/keydown rather than a <button> per cell: a table row
              cannot contain a button that wraps its cells, and the details pane
              is a disclosure of this row, so `button` is the honest role.
            -->
            <tr
              data-network-row
              class:selected={selectedRow?.id === row.id}
              role="button"
              tabindex="0"
              aria-pressed={selectedRow?.id === row.id}
              aria-label={`${normalizedNetworkMethod(row)} ${row.url}`}
              onclick={() => selectRow(row)}
              onkeydown={(event) => {
                if (event.key !== 'Enter' && event.key !== ' ') return
                // Space scrolls the table otherwise, which moves the row out
                // from under the user as they select it.
                event.preventDefault()
                selectRow(row)
              }}
            >
              <td>{normalizedNetworkMethod(row)}</td>
              <td>{networkStatusDisplay(row.status)}</td>
              <td>{networkDomain(row)}</td>
              <td><code>{networkPath(row)}</code></td>
              <td>{networkLogTime(row)}</td>
              <td>{row.durationMs} ms</td>
              <td>{networkSizeDisplay(row.size)}</td>
            </tr>
          {/each}
          {#if virtualWindow.bottomPadding > 0}
            <tr aria-hidden="true" class="network-spacer"><td colspan={NETWORK_COLUMNS.length} style={`height: ${virtualWindow.bottomPadding}px; padding: 0; border: none;`}></td></tr>
          {/if}
        </tbody>
      </table>
    </div>
    {#if selectedRow}
      {#await import('./RequestDetailsPanel.svelte') then RequestDetailsPanel}
        {@const RequestDetailsPanelComponent = RequestDetailsPanel.default}
        <RequestDetailsPanelComponent
          selectedDevToolsNetworkRow={selectedRow}
          devToolsNetworkDetailTab={detailTab}
          devToolsNetworkDetailTabs={detailTabs}
          onSelectDetailTab={selectDetailTab}
          startDevToolsDetailsPanelResize={onStartDetailsResize}
        />
      {/await}
    {/if}
  </div>
{/if}
