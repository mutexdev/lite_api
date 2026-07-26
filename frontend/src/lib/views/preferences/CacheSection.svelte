<script lang="ts">
  // US-036 — the Cache section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  import type { main } from '../../../../wailsjs/go/models'

  export let state: main.AppState
  export let fileCacheSize: number | undefined
  export let formatRuntimeBytes: (bytes: number) => string
  export let updateFileCache: (enabled: boolean) => Promise<void> | void
  export let updateSSLSessionCache: (enabled: boolean) => Promise<void> | void
  export let clearFileCache: () => void
  export let clearSSLSessionCache: () => void
</script>

            <section>
              <div class="settings-section-header">
                <h3>Cache</h3>
              </div>
              <div class="cache-preference-card">
                <div>
                  <strong>File cache <span class="beta-badge">Beta</span></strong>
                  <p>Loads your workspace faster by caching opened collections. Clearing it won't affect your original files.</p>
                  <p class="cache-size">Cache size <strong>{fileCacheSize === undefined ? '-' : formatRuntimeBytes(fileCacheSize)}</strong></p>
                </div>
                <label class="inline-toggle">
                  <input
                    data-testid="cache.file.enabled"
                    type="checkbox"
                    checked={state.preferences.cache?.file?.enabled ?? false}
                    on:change={(event) => updateFileCache(event.currentTarget.checked)}
                  />
                  Enabled
                </label>
                <button type="button" data-testid="file-cache-clear-btn" disabled={!fileCacheSize} on:click={clearFileCache}>Clear cache</button>
              </div>
              <div class="cache-preference-card">
                <div>
                  <strong>SSL session cache</strong>
                  <p>Reuses TLS sessions and connections across requests for faster handshakes.</p>
                </div>
                <label class="inline-toggle">
                  <input
                    data-testid="sslSession.enabled"
                    type="checkbox"
                    checked={state.preferences.cache?.sslSession?.enabled ?? false}
                    on:change={(event) => updateSSLSessionCache(event.currentTarget.checked)}
                  />
                  Enabled
                </label>
                <button type="button" data-testid="ssl-session-clear-btn" on:click={clearSSLSessionCache}>Clear cache</button>
              </div>
            </section>
