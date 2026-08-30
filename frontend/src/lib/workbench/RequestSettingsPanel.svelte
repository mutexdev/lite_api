<script lang="ts">
  import type { types } from '../../../wailsjs/go/models'

  // US-028 — runes.
  type Props = {
    requestType?: string
    settings: types.RequestSettings
    onChange: (updates: Partial<types.RequestSettings>) => void
    // The app-level SSL preference. Verification is the AND of this and the
    // per-request toggle below, so when this is off the checkbox no longer
    // describes what actually happens on the wire — and a checkbox that says
    // certificates are verified when they are not is the worst thing this
    // panel can show. Defaults to true to match the backend, where an absent
    // preference means verification is on.
    globalVerifyTlsEnabled?: boolean
  }

  let { requestType = 'http', settings, onChange, globalVerifyTlsEnabled = true }: Props = $props()

  const verifyTlsOverridden = $derived(!globalVerifyTlsEnabled)

  const isHTTPFamily = $derived(requestType === 'http' || requestType === 'graphql')
  const protocolName = $derived(
    requestType === 'grpc' ? 'gRPC' : requestType === 'websocket' ? 'WebSocket' : requestType === 'graphql' ? 'GraphQL' : 'HTTP'
  )

  function numberChange(field: keyof types.RequestSettings, event: Event) {
    const value = Number((event.currentTarget as HTMLInputElement).value)
    onChange({ [field]: Number.isFinite(value) ? value : 0 } as Partial<types.RequestSettings>)
  }
</script>

<fieldset class="request-settings" aria-describedby="request-settings-help">
  <legend>{protocolName} request settings</legend>
  <p id="request-settings-help">Only settings supported by this request protocol are shown.</p>

  <div class="settings-fields">
    {#if isHTTPFamily}
      <label class="setting-toggle">
        <input type="checkbox" checked={settings.encodeUrl} onchange={(event) => onChange({ encodeUrl: event.currentTarget.checked })} />
        <span>Encode URL</span>
      </label>
    {/if}

    <label class="setting-number">
      <span>Timeout (ms)</span>
      <input aria-label="Request timeout in milliseconds" type="number" min="0" value={settings.timeoutMs} oninput={(event) => numberChange('timeoutMs', event)} />
    </label>

    {#if isHTTPFamily}
      <label class="setting-toggle">
        <input type="checkbox" checked={settings.followRedirects} onchange={(event) => onChange({ followRedirects: event.currentTarget.checked })} />
        <span>Follow redirects</span>
      </label>

      {#if settings.followRedirects}
        <label class="setting-number">
          <span>Maximum redirects</span>
          <input aria-label="Maximum redirects" type="number" min="0" value={settings.maxRedirects} oninput={(event) => numberChange('maxRedirects', event)} />
        </label>
      {/if}

      <label class="setting-toggle">
        <input type="checkbox" checked={settings.storeCookies} onchange={(event) => onChange({ storeCookies: event.currentTarget.checked })} />
        <span>Store cookies</span>
      </label>
    {/if}

    <div class="setting-tls" class:is-overridden={verifyTlsOverridden}>
      <label class="setting-toggle">
        <input
          type="checkbox"
          checked={settings.verifyTls}
          aria-describedby={verifyTlsOverridden ? 'verify-tls-override' : undefined}
          onchange={(event) => onChange({ verifyTls: event.currentTarget.checked })}
        />
        <span>Verify TLS certificates</span>
      </label>
      {#if verifyTlsOverridden}
        <!-- The checkbox stays editable on purpose: the request's own setting
             still records intent for when the global preference goes back on.
             Muting it says "this is not in effect" without destroying the
             ability to set it. -->
        <p id="verify-tls-override" class="setting-note">Turned off globally in Preferences — certificates are not verified for any request.</p>
      {/if}
    </div>

    {#if requestType === 'websocket'}
      <label class="setting-number">
        <span>Keep-alive interval (ms)</span>
        <input aria-label="WebSocket keep-alive interval in milliseconds" type="number" min="0" value={settings.keepAliveInterval ?? 0} oninput={(event) => numberChange('keepAliveInterval', event)} />
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
  .setting-tls { display: grid; gap: 2px; min-width: 0; }
  .setting-tls.is-overridden .setting-toggle { opacity: 0.55; }
  /* Not --danger: nothing is broken, and colouring a preference the user chose
     as an error trains them to ignore the ones that matter. */
  .setting-note { margin: 0; color: var(--muted); font-size: 11px; font-weight: 600; line-height: 1.35; }
  .setting-number input { min-width: 0; }
  @media (max-width: 420px) { .settings-fields { grid-template-columns: minmax(0, 1fr); } }
</style>
