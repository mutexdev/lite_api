<script lang="ts">
  // US-036 — the Performance tab of the Dev Tools panel.
  //
  // DevTools does decompose, unlike Import: its body is an
  // {#if devToolsTab === '...'} chain, so each tab is separately extractable.
  // That is the same decomposition that made Preferences tractable, applied to
  // a snippet rather than a branch. The tab is only rendered when selected, so
  // the dynamic import loads on first use of the Performance tab specifically.
  import type { main } from '../../../../wailsjs/go/models'

  export let devToolsSnapshot: main.DevToolsSnapshot | null | undefined
  export let devToolsPerformanceProcesses: main.DevToolsProcessMetric[]
  export let displayedDevToolsCPUPercent: number | undefined
  export let displayedDevToolsMemoryBytes: number | undefined
  export let displayedDevToolsUptimeSeconds: number | undefined
  export let devToolsPerformanceView: string
  export let displayedDevToolsPID: number | undefined
  export let selectedDevToolsPerformanceProcess: main.DevToolsProcessMetric | undefined
  export let formatCPUPercent: (value: number | undefined) => string
  export let formatRuntimeBytes: (bytes: number | undefined) => string
  export let formatUptime: (seconds: number | undefined) => string
  export let refreshDevToolsSnapshot: () => void
</script>

              <div class="performance-toolbar">
                <label>
                  <span>View:</span>
                  <select aria-label="Performance process view" bind:value={devToolsPerformanceView}>
                    <option value="cumulative">Cumulative (All Processes)</option>
                    {#each devToolsPerformanceProcesses as process (process.pid)}
                      <option value={String(process.pid)}>PID {process.pid} - {process.title || 'LiteAPI'} ({process.type || 'main'})</option>
                    {/each}
                  </select>
                </label>
                <button type="button" on:click={refreshDevToolsSnapshot}>Refresh</button>
              </div>
              <h3>System Resources</h3>
              <div class="resource-cards">
                <article>
                  <span>CPU Usage</span>
                  <strong>{formatCPUPercent(displayedDevToolsCPUPercent)}</strong>
                  <small>{selectedDevToolsPerformanceProcess ? 'Current CPU usage' : 'Total CPU usage'}</small>
                </article>
                <article>
                  <span>Memory Usage</span>
                  <strong>{formatRuntimeBytes(displayedDevToolsMemoryBytes)}</strong>
                  <small>{selectedDevToolsPerformanceProcess ? 'Current memory usage' : 'Total memory usage'}</small>
                </article>
                <article>
                  <span>Uptime</span>
                  <strong>{formatUptime(displayedDevToolsUptimeSeconds)}</strong>
                  <small>Process runtime</small>
                </article>
                <article>
                  <span>Process ID</span>
                  <strong>{displayedDevToolsPID ?? '-'}</strong>
                  <small>{selectedDevToolsPerformanceProcess ? 'Process PID' : 'Main process PID'}</small>
                </article>
                <article>
                  <span>Heap Alloc</span>
                  <strong>{formatRuntimeBytes(devToolsSnapshot?.heapAllocBytes)}</strong>
                  <small>Go heap allocation</small>
                </article>
                <article>
                  <span>Goroutines</span>
                  <strong>{devToolsSnapshot?.goroutines ?? '-'}</strong>
                  <small>Runtime workers</small>
                </article>
              </div>
