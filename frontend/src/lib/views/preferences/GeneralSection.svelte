<script lang="ts">
  // US-036 — the General section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, each section only a handful.
  //
  // This section alone carried three of the panel's row anatomies —
  // `.inline-toggle` rows, `.field-grid.compact-preference-grid` pairs and a
  // `.path-picker-row` — plus a hint paragraph that only some rows had. All of
  // it is SettingRow now, and the hints that used to float between rows are
  // each attached to the row they explain, which is the only place a reader can
  // tell which control a sentence is about.
  import type { types } from '../../../../wailsjs/go/models'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  type Props = {
    state: types.AppState
    customCaFileName: (filePath: string | undefined) => string
    browseDefaultLocation: () => void
    clearDefaultLocation: () => void
    browseCustomCaCertificate: () => void
    clearCustomCaCertificate: () => void
    updateAutoSavePreferences: (patch: Record<string, unknown>) => void
    updateRequestPreferences: (patch: Record<string, unknown>) => void
  }

  let {
    state,
    customCaFileName,
    browseDefaultLocation,
    clearDefaultLocation,
    browseCustomCaCertificate,
    clearCustomCaCertificate,
    updateAutoSavePreferences,
    updateRequestPreferences,
  }: Props = $props()

  const customCaEnabled = $derived(state.preferences.request?.customCaCertificate?.enabled ?? false)
  const customCaFilePath = $derived(state.preferences.request?.customCaCertificate?.filePath ?? '')
  const keepDefaultCaLocked = $derived(!(customCaEnabled && customCaFilePath))
  const autoSaveEnabled = $derived(state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false)
  const defaultLocation = $derived(state.preferences.general?.defaultLocation ?? state.preferences.defaultCollectionPath ?? '')
</script>

<SettingSection title="General Settings">
  <SettingRow
    label="SSL/TLS Certificate Verification"
    description="Postman disables this by default. LiteAPI verifies certificates unless you turn it off, which is why a request that succeeds in Postman can fail here with a certificate error. Prefer adding the issuing CA below to switching verification off for everything."
    checkboxId="sslVerification"
    data-testid="ssl-verification-toggle"
    checked={state.preferences.request?.sslVerification !== false}
    onCheckedChange={(value) => updateRequestPreferences({ sslVerification: value } as Partial<types.RequestPreferences>)}
  />

  <SettingRow
    label="Use Custom CA Certificate"
    checkboxId="customCaCertificateEnabled"
    data-testid="custom-ca-enabled-toggle"
    checked={customCaEnabled}
    onCheckedChange={(value) => updateRequestPreferences({
      customCaCertificate: {
        ...(state?.preferences.request?.customCaCertificate ?? {}),
        enabled: value
      } as types.CustomCaCertificatePreferences
    })}
  />

  <SettingRow label="Certificate file" disabled={!customCaEnabled}>
    {#snippet control()}
      {#if customCaFilePath}
        <span class="selected-path-chip" data-testid="custom-ca-file-name">
          {customCaFileName(customCaFilePath)}
          <button type="button" aria-label="Remove custom CA certificate" onclick={clearCustomCaCertificate}>x</button>
        </span>
      {:else}
        <button
          type="button"
          data-testid="custom-ca-select-btn"
          onclick={browseCustomCaCertificate}
          disabled={!customCaEnabled}
        >
          Select File
        </button>
      {/if}
    {/snippet}
  </SettingRow>

  <SettingRow
    label="Keep Default CA Certificates"
    disabled={keepDefaultCaLocked}
    checkboxId="keepDefaultCaCertificatesEnabled"
    data-testid="keep-default-ca-toggle"
    checked={state.preferences.request?.keepDefaultCaCertificates?.enabled !== false}
    onCheckedChange={(value) => updateRequestPreferences({
      keepDefaultCaCertificates: { enabled: value } as types.KeepDefaultCaCertificatesPreferences
    })}
  />

  <SettingRow
    label="Store Cookies automatically"
    checkboxId="storeCookies"
    data-testid="store-cookies-toggle"
    checked={state.preferences.request?.storeCookies ?? state.preferences.storeCookies ?? true}
    onCheckedChange={(value) => updateRequestPreferences({ storeCookies: value } as Partial<types.RequestPreferences>)}
  />

  <SettingRow
    label="Send Cookies automatically"
    checkboxId="sendCookies"
    data-testid="send-cookies-toggle"
    checked={state.preferences.request?.sendCookies ?? true}
    onCheckedChange={(value) => updateRequestPreferences({ sendCookies: value } as Partial<types.RequestPreferences>)}
  />

  <SettingRow label="Request Timeout (in ms)" labelFor="requestTimeout">
    {#snippet control()}
      <input
        id="requestTimeout"
        data-testid="request-timeout-input"
        type="number"
        min="0"
        value={state.preferences.request?.timeout ?? 0}
        inputmode="numeric"
        oninput={(event) => updateRequestPreferences({ timeout: Number(event.currentTarget.value) } as Partial<types.RequestPreferences>)}
      />
    {/snippet}
  </SettingRow>

  <SettingRow
    label="Enable Auto Save"
    checkboxId="autoSaveEnabled"
    data-testid="autosave-enabled-toggle"
    checked={autoSaveEnabled}
    onCheckedChange={(value) => updateAutoSavePreferences({ enabled: value } as Partial<types.AutoSavePreferences>)}
  />

  <SettingRow label="Save Delay (in ms)" labelFor="autoSaveInterval" disabled={!autoSaveEnabled}>
    {#snippet control()}
      <input
        id="autoSaveInterval"
        data-testid="autosave-interval-input"
        type="number"
        min="0"
        value={state.preferences.autoSave?.interval ?? 1000}
        disabled={!autoSaveEnabled}
        inputmode="numeric"
        oninput={(event) => updateAutoSavePreferences({ interval: Number(event.currentTarget.value) } as Partial<types.AutoSavePreferences>)}
      />
    {/snippet}
  </SettingRow>

  <SettingRow label="Default Location" labelFor="defaultLocation">
    {#snippet control()}
      <!--
        Readonly input plus Browse plus Clear: the path-picker group the
        SettingRow contract names as the one case where "exactly one control"
        means a group. It stays one control cell, so it lines up with the single
        inputs above it instead of getting a grid of its own as it had before.
      -->
      <input
        id="defaultLocation"
        data-testid="default-location-input"
        class="path-input"
        readonly
        value={defaultLocation}
        placeholder="Click to browse for default location"
        onclick={browseDefaultLocation}
      />
      <button type="button" data-testid="default-location-browse-btn" onclick={browseDefaultLocation}>Browse</button>
      <button
        type="button"
        data-testid="default-location-clear-btn"
        onclick={clearDefaultLocation}
        disabled={!defaultLocation.trim()}
      >
        Clear
      </button>
    {/snippet}
  </SettingRow>
</SettingSection>

<style>
  /*
    Readonly, but clicking it opens the picker — so it has to say "pressable"
    rather than "inert", which a readonly input otherwise reads as.
  */
  .path-input {
    cursor: pointer;
  }
</style>
