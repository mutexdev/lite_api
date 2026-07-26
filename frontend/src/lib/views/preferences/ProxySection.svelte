<script lang="ts">
  // US-036 — the Proxy section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  import type { main, types } from '../../../../wailsjs/go/models'

  export let state: main.AppState
  export let preferencesProxyMode: (preferences: main.Preferences | undefined) => 'pac' | 'inherit' | 'off' | 'manual'
  export let updatePreferencesProxy: (patch: Record<string, unknown>) => void
  export let updatePreferencesProxyAuth: (patch: Record<string, unknown>) => void
  export let updatePreferencesProxyConfig: (patch: Record<string, unknown>) => void
  export let updatePreferencesProxyMode: (mode: string) => void
</script>

            <section>
              <div class="settings-section-header">
                <h3>Proxy Settings</h3>
              </div>
              <div class="field-grid">
                <span class="field-label">Mode</span>
                <select aria-label="App proxy mode" value={preferencesProxyMode(state.preferences)} on:change={(e) => updatePreferencesProxyMode(e.currentTarget.value)}>
                  <option value="off">Off</option>
                  <option value="manual">On</option>
                  <option value="inherit">System Proxy</option>
                  <option value="pac">PAC</option>
                </select>
              </div>

              {#if preferencesProxyMode(state.preferences) === 'manual'}
                <div class="field-grid">
                  <span class="field-label">Protocol</span>
                  <select aria-label="App proxy protocol" value={state.preferences.proxy?.config?.protocol || 'http'} on:change={(e) => updatePreferencesProxyConfig({ protocol: e.currentTarget.value })}>
                    <option value="http">HTTP</option>
                    <option value="https">HTTPS</option>
                    <option value="socks5">SOCKS5</option>
                  </select>
                  <span class="field-label">Host</span>
                  <input aria-label="App proxy host" value={state.preferences.proxy?.config?.hostname ?? ''} on:input={(e) => updatePreferencesProxyConfig({ hostname: e.currentTarget.value })} />
                  <span class="field-label">Port</span>
                  <input aria-label="App proxy port" value={state.preferences.proxy?.config?.port ?? ''} on:input={(e) => updatePreferencesProxyConfig({ port: e.currentTarget.value })} />
                  <span class="field-label">Bypass</span>
                  <input aria-label="App proxy bypass" value={state.preferences.proxy?.config?.bypassProxy ?? ''} on:input={(e) => updatePreferencesProxyConfig({ bypassProxy: e.currentTarget.value })} />
                  <span class="field-label">Auth enabled</span>
                  <input aria-label="App proxy auth enabled" type="checkbox" checked={!(state.preferences.proxy?.config?.auth?.disabled ?? false)} on:change={(e) => updatePreferencesProxyAuth({ disabled: !e.currentTarget.checked })} />
                  <span class="field-label">Username</span>
                  <input aria-label="App proxy username" value={state.preferences.proxy?.config?.auth?.username ?? ''} on:input={(e) => updatePreferencesProxyAuth({ username: e.currentTarget.value })} />
                  <span class="field-label">Password</span>
                  <input aria-label="App proxy password" type="password" value={state.preferences.proxy?.config?.auth?.password ?? ''} on:input={(e) => updatePreferencesProxyAuth({ password: e.currentTarget.value })} />
                </div>
              {:else if preferencesProxyMode(state.preferences) === 'pac'}
                <div class="field-grid">
                  <span class="field-label">PAC Source</span>
                  <input aria-label="PAC source" placeholder="https://example.com/proxy.pac or file:///path/proxy.pac" value={state.preferences.proxy?.pac?.source ?? ''} on:change={(e) => updatePreferencesProxy({ pac: { source: e.currentTarget.value } as types.ProxyPACConfig })} />
                </div>
              {/if}
            </section>
