<script lang="ts">
  // US-036 — the Request Details panel inside the Dev Tools Network tab.
  //
  // Network is the largest DevTools tab at 8,675 B. Rather than move it whole,
  // this takes its nested <section class="network-details-panel"> — the same
  // decomposition that made Preferences and DevTools tractable, one level
  // deeper. It renders only when a network row is selected, so the dynamic
  // import loads on first inspection of a request.
  import type { types } from '../../../../wailsjs/go/models'

  export let selectedDevToolsNetworkRow: types.NetworkLog
  export let devToolsNetworkDetailTab: string
  export let devToolsNetworkDetailTabs: Array<{ id: string; label: string }>
  export let networkHeaderRows: (headers: Record<string, string> | undefined) => Array<[string, string]>
  export let networkLogBody: (value: string | undefined) => string
  export let networkLogLines: (row: types.NetworkLog | undefined) => string[]
  export let normalizedNetworkMethod: (row: types.NetworkLog) => string
  export let startDevToolsDetailsPanelResize: (event: MouseEvent) => void
  // The subtab buttons used to assign to `devToolsNetworkDetailTab` directly.
  // App.svelte passes that prop one-way, so the assignment moved only this
  // component's local copy: switching to Response and then selecting a
  // different row snapped back to Request, because the parent's state had never
  // changed. A callback rather than `bind:` keeps the parent's narrower union
  // type as the single definition of what a tab id may be.
  export let onSelectDetailTab: (id: string) => void
</script>

                    <section class="network-details-panel" aria-label="Request Details">
                      <button
                        type="button"
                        class="details-panel-resizer"
                        aria-label="Resize request details"
                        on:mousedown={startDevToolsDetailsPanelResize}
                      ></button>
                    <header>
                      <h3>Request Details</h3>
                      <div class="subtabs">
                        {#each devToolsNetworkDetailTabs as detailTab (detailTab.id)}
                          <button type="button" class:active={devToolsNetworkDetailTab === detailTab.id} on:click={() => onSelectDetailTab(detailTab.id)}>{detailTab.label}</button>
                        {/each}
                      </div>
                    </header>
                    {#if devToolsNetworkDetailTab === 'request'}
                      <div class="network-detail-content">
                        <h4>General</h4>
                        <dl class="detail-list">
                          <div><dt>Request URL:</dt><dd>{selectedDevToolsNetworkRow.url}</dd></div>
                          <div><dt>Request Method:</dt><dd>{normalizedNetworkMethod(selectedDevToolsNetworkRow)}</dd></div>
                        </dl>
                        <h4>Request Headers</h4>
                        {#if networkHeaderRows(selectedDevToolsNetworkRow.requestHeaders).length === 0}
                          <div class="empty-state compact">No headers</div>
                        {:else}
                          <table class="details-table">
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
                          <table class="details-table">
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
