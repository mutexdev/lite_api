<script lang="ts">
  // US-036 — the Console tab of the Dev Tools panel.
  //
  // devToolsConsoleRows is the only prop, and it appears nowhere on an
  // attribute line — it is read inside {#if devToolsConsoleRows.length === 0}
  // and {#each devToolsConsoleRows ...}. The attribute-line grep that drafts
  // these prop lists reported zero props for this tab. Reading the markup is
  // what found it, which is the same blind spot every extraction in this story
  // has hit.
  export let devToolsConsoleRows: Array<{ level: string; message: string; source: string }>
</script>

              {#if devToolsConsoleRows.length === 0}
                <div class="empty-state devtools-empty">
                  <strong>No logs to display</strong>
                  <span>Logs will appear here as your application runs</span>
                </div>
              {:else}
                <div class="console-log-list devtools-console-list" aria-label="DevTools console logs">
                  {#each devToolsConsoleRows as log, index (index)}
                    <div class={`console-row ${log.level}`}>
                      <span>{log.level}</span>
                      <div>
                        <code>{log.message}</code>
                        <small>{log.source}</small>
                      </div>
                    </div>
                  {/each}
                </div>
              {/if}
