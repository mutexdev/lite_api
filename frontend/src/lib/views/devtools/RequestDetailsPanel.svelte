<script lang="ts">
  // US-036 — the Request Details panel inside the Dev Tools Network tab.
  //
  // Network is the largest DevTools tab at 8,675 B. Rather than move it whole,
  // this takes its nested <section class="network-details-panel"> — the same
  // decomposition that made Preferences and DevTools tractable, one level
  // deeper. It renders only when a network row is selected, so the dynamic
  // import loads on first inspection of a request.
  import type { types } from '../../../../wailsjs/go/models'
  // Imported, not received. Four of this component's props used to be the
  // functions that turn a NetworkLog into strings, passed down from App.svelte
  // because that is where they happened to be defined — so the detail pane
  // could not render a header row without its parent handing it a formatter,
  // and the parent's own legacy network table, three thousand lines further
  // down the same file, rendered the same fields raw because nobody thought to
  // pass them to it. They live in lib/networkSort.ts now and every network
  // surface imports them directly.
  import {
    networkHeaderRows as headerRows,
    networkLogBody as logBody,
    networkLogLines as logLines,
    normalizedNetworkMethod as networkMethod
  } from '../../networkSort'

  type Props = {
    selectedDevToolsNetworkRow: types.NetworkLog
    devToolsNetworkDetailTab: string
    devToolsNetworkDetailTabs: Array<{ id: string; label: string }>
    // The subtab buttons used to assign to `devToolsNetworkDetailTab` directly.
    // The parent passes that prop one-way, so the assignment moved only this
    // component's local copy: switching to Response and then selecting a
    // different row snapped back to Request, because the parent's state had
    // never changed. A callback rather than `bind:` keeps the parent's narrower
    // union type as the single definition of what a tab id may be.
    onSelectDetailTab: (id: string) => void
    /**
     * Omitted where the panel's width is not a stored preference, in which case
     * no resizer is drawn at all rather than a grab bar that moves nothing.
     */
    startDevToolsDetailsPanelResize?: (event: MouseEvent) => void
    /**
     * The four formatters below are still accepted, and must stay accepted
     * until App.svelte's own mount of this component is replaced by
     * NetworkTable (§2.1 of handoff-tables-2.md). App.svelte cannot be edited
     * from this change, and a required-prop set it no longer satisfies is a
     * type error in a file nobody here can fix. They default to the module
     * these functions now live in, so the argument App.svelte passes and the
     * argument NetworkTable does not pass are the same function either way —
     * delete all four the moment that mount goes.
     */
    networkHeaderRows?: (headers: Record<string, string> | undefined) => Array<[string, string]>
    networkLogBody?: (value: string | undefined) => string
    networkLogLines?: (row: types.NetworkLog | undefined) => string[]
    normalizedNetworkMethod?: (row: types.NetworkLog) => string
  }

  let {
    selectedDevToolsNetworkRow,
    devToolsNetworkDetailTab,
    devToolsNetworkDetailTabs,
    onSelectDetailTab,
    startDevToolsDetailsPanelResize = undefined,
    networkHeaderRows = headerRows,
    networkLogBody = logBody,
    networkLogLines = logLines,
    normalizedNetworkMethod = networkMethod
  }: Props = $props()
</script>

                    <section class="network-details-panel" aria-label="Request Details">
                      <!--
                        Conditional because the resizer is only honest where the
                        width it drags is actually held and stored. A surface
                        that mounts the network table without one would render a
                        grab bar that moves nothing.
                      -->
                      {#if startDevToolsDetailsPanelResize}
                        <button
                          type="button"
                          class="details-panel-resizer"
                          aria-label="Resize request details"
                          onmousedown={startDevToolsDetailsPanelResize}
                        ></button>
                      {/if}
                    <header>
                      <h3>Request Details</h3>
                      <div class="subtabs">
                        {#each devToolsNetworkDetailTabs as detailTab (detailTab.id)}
                          <button type="button" class:active={devToolsNetworkDetailTab === detailTab.id} onclick={() => onSelectDetailTab(detailTab.id)}>{detailTab.label}</button>
                        {/each}
                      </div>
                    </header>
                    {#if devToolsNetworkDetailTab === 'request'}
                      <div class="network-detail-content">
                        <h4>General</h4>
                        <!--
                          The URL and the method are the same category of string
                          as the header values three lines below, which have
                          always been monospaced — and they were the only two
                          rendered in the UI font. A URL in a proportional font
                          is where `l`, `1` and `I` stop being distinguishable,
                          which is the whole reason the rest of this panel is
                          monospaced.
                        -->
                        <dl class="detail-list">
                          <div><dt>Request URL:</dt><dd><code>{selectedDevToolsNetworkRow.url}</code></dd></div>
                          <div><dt>Request Method:</dt><dd><code>{normalizedNetworkMethod(selectedDevToolsNetworkRow)}</code></dd></div>
                        </dl>
                        <h4>Request Headers</h4>
                        {#if networkHeaderRows(selectedDevToolsNetworkRow.requestHeaders).length === 0}
                          <div class="empty-state compact">No headers</div>
                        {:else}
                          <table class="details-table" aria-label="Request headers">
                            <thead><tr><th>Name</th><th>Value</th></tr></thead>
                            <tbody>
                              {#each networkHeaderRows(selectedDevToolsNetworkRow.requestHeaders) as [name, value] (name)}
                                <tr><td>{name}</td><td><code>{value}</code></td></tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        <h4>Request Body</h4>
                        {#if networkLogBody(selectedDevToolsNetworkRow.requestBody)}
                          <pre class="network-body">{networkLogBody(selectedDevToolsNetworkRow.requestBody)}</pre>
                        {:else}
                          <div class="empty-state compact">No body</div>
                        {/if}
                      </div>
                    {:else if devToolsNetworkDetailTab === 'response'}
                      <div class="network-detail-content">
                        <h4>Response Headers</h4>
                        {#if networkHeaderRows(selectedDevToolsNetworkRow.responseHeaders).length === 0}
                          <div class="empty-state compact">No headers</div>
                        {:else}
                          <table class="details-table" aria-label="Response headers">
                            <thead><tr><th>Name</th><th>Value</th></tr></thead>
                            <tbody>
                              {#each networkHeaderRows(selectedDevToolsNetworkRow.responseHeaders) as [name, value] (name)}
                                <tr><td>{name}</td><td><code>{value}</code></td></tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        <h4>Response Body</h4>
                        {#if networkLogBody(selectedDevToolsNetworkRow.responseBody)}
                          <pre class="network-body">{networkLogBody(selectedDevToolsNetworkRow.responseBody)}</pre>
                        {:else}
                          <div class="empty-state compact">No response data</div>
                        {/if}
                      </div>
                    {:else}
                      <div class="network-detail-content">
                        <h4>Network Logs</h4>
                        {#if networkLogLines(selectedDevToolsNetworkRow).length === 0}
                          <div class="empty-state compact">No network logs available</div>
                        {:else}
                          <div class="progress-log">
                            {#each networkLogLines(selectedDevToolsNetworkRow) as line, index (index)}
                              <div class="progress-row"><span>net</span><code>{line}</code></div>
                            {/each}
                          </div>
                        {/if}
                      </div>
                    {/if}
                    </section>

<style>
  /*
    DevTools looks like DevTools. That is the decision the audit asked for and
    did not find recorded anywhere: this panel is deliberately denser, more
    tabular and more monospaced than the request/response editors, because it is
    a log inspector and the strings in it are meant to be compared character by
    character, not read as prose.

    What was NOT deliberate is that the panel disagreed with itself about what
    monospace means. `.console-row code` in the stylesheet sets
    --code-font-family; every other code surface in DevTools — this file's
    header tables, request/response bodies and network log lines — set only
    colour and wrapping, so they fell through to the browser's default monospace
    at the app's proportional font SIZE. Two monospace faces at two sizes,
    inside one panel, for the same category of string.

    Scoped here rather than in style.css because the rules those declarations
    belong to are in a file this change does not own; the equivalent edit for
    the network table's own cells is in the handoff.
  */
  .details-table code,
  .network-body,
  .progress-row code,
  .detail-list dd code {
    font-family: var(--code-font-family);
    font-size: var(--code-font-size);
  }
</style>
