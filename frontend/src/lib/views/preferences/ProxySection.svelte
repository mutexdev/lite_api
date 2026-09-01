<script lang="ts">
  // US-036 — the Proxy section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  //
  // "Auth enabled" used to be a bare checkbox input sitting in the
  // value column of a label/value grid — the one boolean in the whole panel
  // that did not look like the app's other booleans, and it sat two rows above
  // the username and password it gates. It is a normal boolean row now.
  import type { types } from '../../../../wailsjs/go/models'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  type Props = {
    state: types.AppState
    preferencesProxyMode: (preferences: types.Preferences | undefined) => 'pac' | 'inherit' | 'off' | 'manual'
    updatePreferencesProxy: (patch: Record<string, unknown>) => void
    updatePreferencesProxyAuth: (patch: Record<string, unknown>) => void
    updatePreferencesProxyConfig: (patch: Record<string, unknown>) => void
    updatePreferencesProxyMode: (mode: string) => void
  }

  let {
    state,
    preferencesProxyMode,
    updatePreferencesProxy,
    updatePreferencesProxyAuth,
    updatePreferencesProxyConfig,
    updatePreferencesProxyMode,
  }: Props = $props()

  const mode = $derived(preferencesProxyMode(state.preferences))
</script>

<SettingSection title="Proxy Settings">
  <SettingRow label="Mode">
    {#snippet control()}
      <select aria-label="App proxy mode" value={mode} onchange={(e) => updatePreferencesProxyMode(e.currentTarget.value)}>
        <option value="off">Off</option>
        <option value="manual">On</option>
        <option value="inherit">System Proxy</option>
        <option value="pac">PAC</option>
      </select>
    {/snippet}
  </SettingRow>

  {#if mode === 'manual'}
    <SettingRow label="Protocol">
      {#snippet control()}
        <select aria-label="App proxy protocol" value={state.preferences.proxy?.config?.protocol || 'http'} onchange={(e) => updatePreferencesProxyConfig({ protocol: e.currentTarget.value })}>
          <option value="http">HTTP</option>
          <option value="https">HTTPS</option>
          <option value="socks5">SOCKS5</option>
        </select>
      {/snippet}
    </SettingRow>

    <SettingRow label="Host">
      {#snippet control()}
        <input aria-label="App proxy host" value={state.preferences.proxy?.config?.hostname ?? ''} oninput={(e) => updatePreferencesProxyConfig({ hostname: e.currentTarget.value })} />
      {/snippet}
    </SettingRow>

    <SettingRow label="Port">
      {#snippet control()}
        <input aria-label="App proxy port" value={state.preferences.proxy?.config?.port ?? ''} oninput={(e) => updatePreferencesProxyConfig({ port: e.currentTarget.value })} />
      {/snippet}
    </SettingRow>

    <SettingRow label="Bypass">
      {#snippet control()}
        <input aria-label="App proxy bypass" value={state.preferences.proxy?.config?.bypassProxy ?? ''} oninput={(e) => updatePreferencesProxyConfig({ bypassProxy: e.currentTarget.value })} />
      {/snippet}
    </SettingRow>

    <SettingRow
      label="Auth enabled"
      checkboxAriaLabel="App proxy auth enabled"
      checked={!(state.preferences.proxy?.config?.auth?.disabled ?? false)}
      onCheckedChange={(value) => updatePreferencesProxyAuth({ disabled: !value })}
    />

    <SettingRow label="Username">
      {#snippet control()}
        <input aria-label="App proxy username" value={state.preferences.proxy?.config?.auth?.username ?? ''} oninput={(e) => updatePreferencesProxyAuth({ username: e.currentTarget.value })} />
      {/snippet}
    </SettingRow>

    <SettingRow label="Password">
      {#snippet control()}
        <input aria-label="App proxy password" type="password" value={state.preferences.proxy?.config?.auth?.password ?? ''} oninput={(e) => updatePreferencesProxyAuth({ password: e.currentTarget.value })} />
      {/snippet}
    </SettingRow>
  {:else if mode === 'pac'}
    <SettingRow label="PAC Source">
      {#snippet control()}
        <input aria-label="PAC source" placeholder="https://example.com/proxy.pac or file:///path/proxy.pac" value={state.preferences.proxy?.pac?.source ?? ''} onchange={(e) => updatePreferencesProxy({ pac: { source: e.currentTarget.value } as types.ProxyPACConfig })} />
      {/snippet}
    </SettingRow>
  {/if}
</SettingSection>
