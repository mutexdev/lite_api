<script lang="ts">
  import type { main } from '../../../wailsjs/go/models'

  export let requestType = 'http'
  export let settings: main.RequestSettings
  export let onChange: (updates: Partial<main.RequestSettings>) => void

  $: isHTTPFamily = requestType === 'http' || requestType === 'graphql'
  $: protocolName = requestType === 'grpc' ? 'gRPC' : requestType === 'websocket' ? 'WebSocket' : requestType === 'graphql' ? 'GraphQL' : 'HTTP'

  function numberChange(field: keyof main.RequestSettings, event: Event) {
    const value = Number((event.currentTarget as HTMLInputElement).value)
    onChange({ [field]: Number.isFinite(value) ? value : 0 } as Partial<main.RequestSettings>)
  }
</script>

<fieldset class="request-settings" aria-describedby="request-settings-help">
  <legend>{protocolName} request settings</legend>
  <p id="request-settings-help">Only settings supported by this request protocol are shown.</p>

  <div class="settings-fields">
    {#if isHTTPFamily}
      <label class="setting-toggle">
        <input type="checkbox" checked={settings.encodeUrl} on:change={(event) => onChange({ encodeUrl: event.currentTarget.checked })} />
        <span>Encode URL</span>
      </label>
    {/if}

    <label class="setting-number">
      <span>Timeout (ms)</span>
      <input aria-label="Request timeout in milliseconds" type="number" min="0" value={settings.timeoutMs} on:input={(event) => numberChange('timeoutMs', event)} />
    </label>

    {#if isHTTPFamily}
      <label class="setting-toggle">
        <input type="checkbox" checked={settings.followRedirects} on:change={(event) => onChange({ followRedirects: event.currentTarget.checked })} />
        <span>Follow redirects</span>
      </label>

      {#if settings.followRedirects}
        <label class="setting-number">
          <span>Maximum redirects</span>
          <input aria-label="Maximum redirects" type="number" min="0" value={settings.maxRedirects} on:input={(event) => numberChange('maxRedirects', event)} />
        </label>
      {/if}

      <label class="setting-toggle">
        <input type="checkbox" checked={settings.storeCookies} on:change={(event) => onChange({ storeCookies: event.currentTarget.checked })} />
        <span>Store cookies</span>
      </label>
    {/if}

    <label class="setting-toggle">
      <input type="checkbox" checked={settings.verifyTls} on:change={(event) => onChange({ verifyTls: event.currentTarget.checked })} />
      <span>Verify TLS certificates</span>
    </label>

    {#if requestType === 'websocket'}
      <label class="setting-number">
        <span>Keep-alive interval (ms)</span>
        <input aria-label="WebSocket keep-alive interval in milliseconds" type="number" min="0" value={settings.keepAliveInterval ?? 0} on:input={(event) => numberChange('keepAliveInterval', event)} />
      </label>
    {/if}
  </div>
</fieldset>

<style>
  .request-settings { min-width: 0; margin: 0; padding: 12px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface-soft); }
  legend { padding: 0 5px; color: var(--text); font-size: 13px; font-weight: 800; }
  p { margin: 0 0 12px; color: var(--muted); font-size: 12px; }
  .settings-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 9px 12px; }
  .setting-toggle, .setting-number { display: flex; gap: 8px; min-width: 0; color: var(--text); font-size: 12px; font-weight: 700; }
  .setting-toggle { align-items: center; min-height: 34px; }
  .setting-toggle input { width: auto; min-height: auto; }
  .setting-number { display: grid; gap: 4px; }
  .setting-number span { color: var(--muted); }
  .setting-number input { min-width: 0; }
  @media (max-width: 420px) { .settings-fields { grid-template-columns: minmax(0, 1fr); } }
</style>
