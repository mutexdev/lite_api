<script lang="ts">
  // US-036 — the General section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, each section only a handful.
  import type { types } from '../../../../wailsjs/go/models'

  export let state: types.AppState
  export let customCaFileName: (filePath: string | undefined) => string
  export let browseDefaultLocation: () => void
  export let clearDefaultLocation: () => void
  export let browseCustomCaCertificate: () => void
  export let clearCustomCaCertificate: () => void
  export let updateAutoSavePreferences: (patch: Record<string, unknown>) => void
  export let updateRequestPreferences: (patch: Record<string, unknown>) => void
</script>

	            <section>
	              <div class="settings-section-header">
	                <h3>General Settings</h3>
	              </div>
	              <div class="general-preferences-grid">
	                <label class="inline-toggle">
	                  <input
	                    id="sslVerification"
	                    data-testid="ssl-verification-toggle"
	                    type="checkbox"
	                    checked={state.preferences.request?.sslVerification !== false}
	                    on:change={(event) => updateRequestPreferences({ sslVerification: event.currentTarget.checked } as Partial<types.RequestPreferences>)}
	                  />
	                  SSL/TLS Certificate Verification
	                </label>
	                <p class="settings-hint">
	                  Postman disables this by default. LiteAPI verifies certificates unless you turn it off,
	                  which is why a request that succeeds in Postman can fail here with a certificate error.
	                  Prefer adding the issuing CA below to switching verification off for everything.
	                </p>
	                <label class="inline-toggle">
	                  <input
	                    id="customCaCertificateEnabled"
	                    data-testid="custom-ca-enabled-toggle"
	                    type="checkbox"
	                    checked={state.preferences.request?.customCaCertificate?.enabled ?? false}
	                    on:change={(event) => updateRequestPreferences({
	                      customCaCertificate: {
	                        ...(state?.preferences.request?.customCaCertificate ?? {}),
	                        enabled: event.currentTarget.checked
	                      } as types.CustomCaCertificatePreferences
	                    })}
	                  />
	                  Use Custom CA Certificate
	                </label>
	                <div class:settings-disabled={!(state.preferences.request?.customCaCertificate?.enabled ?? false)} class="path-picker-row">
	                  {#if state.preferences.request?.customCaCertificate?.filePath}
	                    <span class="selected-path-chip" data-testid="custom-ca-file-name">
	                      {customCaFileName(state.preferences.request.customCaCertificate.filePath)}
	                      <button type="button" aria-label="Remove custom CA certificate" on:click={clearCustomCaCertificate}>x</button>
	                    </span>
	                  {:else}
	                    <button
	                      type="button"
	                      data-testid="custom-ca-select-btn"
	                      on:click={browseCustomCaCertificate}
	                      disabled={!(state.preferences.request?.customCaCertificate?.enabled ?? false)}
	                    >
	                      Select File
	                    </button>
	                  {/if}
	                </div>
	                <label class:settings-disabled={!((state.preferences.request?.customCaCertificate?.enabled ?? false) && state.preferences.request?.customCaCertificate?.filePath)} class="inline-toggle">
	                  <input
	                    id="keepDefaultCaCertificatesEnabled"
	                    data-testid="keep-default-ca-toggle"
	                    type="checkbox"
	                    checked={state.preferences.request?.keepDefaultCaCertificates?.enabled !== false}
	                    disabled={!((state.preferences.request?.customCaCertificate?.enabled ?? false) && state.preferences.request?.customCaCertificate?.filePath)}
	                    on:change={(event) => updateRequestPreferences({
	                      keepDefaultCaCertificates: { enabled: event.currentTarget.checked } as types.KeepDefaultCaCertificatesPreferences
	                    })}
	                  />
	                  Keep Default CA Certificates
	                </label>
	                <label class="inline-toggle">
	                  <input
	                    id="storeCookies"
	                    data-testid="store-cookies-toggle"
	                    type="checkbox"
	                    checked={state.preferences.request?.storeCookies ?? state.preferences.storeCookies ?? true}
	                    on:change={(event) => updateRequestPreferences({ storeCookies: event.currentTarget.checked } as Partial<types.RequestPreferences>)}
	                  />
	                  Store Cookies automatically
	                </label>
	                <label class="inline-toggle">
	                  <input
	                    id="sendCookies"
	                    data-testid="send-cookies-toggle"
	                    type="checkbox"
	                    checked={state.preferences.request?.sendCookies ?? true}
	                    on:change={(event) => updateRequestPreferences({ sendCookies: event.currentTarget.checked } as Partial<types.RequestPreferences>)}
	                  />
	                  Send Cookies automatically
	                </label>
	                <div class="field-grid compact-preference-grid">
	                  <label class="field-label" for="requestTimeout">Request Timeout (in ms)</label>
	                  <input
	                    id="requestTimeout"
	                    data-testid="request-timeout-input"
	                    value={state.preferences.request?.timeout ?? 0}
	                    inputmode="numeric"
	                    on:input={(event) => updateRequestPreferences({ timeout: Number(event.currentTarget.value) } as Partial<types.RequestPreferences>)}
	                  />
	                </div>
	                <label class="inline-toggle">
	                  <input
	                    id="autoSaveEnabled"
	                    data-testid="autosave-enabled-toggle"
	                    type="checkbox"
	                    checked={state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false}
	                    on:change={(event) => updateAutoSavePreferences({ enabled: event.currentTarget.checked } as Partial<types.AutoSavePreferences>)}
	                  />
	                  Enable Auto Save
	                </label>
	                <div class:settings-disabled={!(state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false)} class="field-grid compact-preference-grid">
	                  <label class="field-label" for="autoSaveInterval">Save Delay (in ms)</label>
	                  <input
	                    id="autoSaveInterval"
	                    data-testid="autosave-interval-input"
	                    value={state.preferences.autoSave?.interval ?? 1000}
	                    disabled={!(state.preferences.autoSave?.enabled ?? state.preferences.autosave ?? false)}
	                    inputmode="numeric"
	                    on:input={(event) => updateAutoSavePreferences({ interval: Number(event.currentTarget.value) } as Partial<types.AutoSavePreferences>)}
	                  />
	                </div>
	                <div class="field-grid default-location-grid">
	                  <label class="field-label" for="defaultLocation">Default Location</label>
	                  <div class="default-location-control">
	                    <input
	                      id="defaultLocation"
	                      data-testid="default-location-input"
	                      class="default-location-input"
	                      readonly
	                      value={state.preferences.general?.defaultLocation ?? state.preferences.defaultCollectionPath ?? ''}
	                      placeholder="Click to browse for default location"
	                      on:click={browseDefaultLocation}
	                    />
	                    <button type="button" data-testid="default-location-browse-btn" on:click={browseDefaultLocation}>Browse</button>
	                    <button
	                      type="button"
	                      data-testid="default-location-clear-btn"
	                      on:click={clearDefaultLocation}
	                      disabled={!((state.preferences.general?.defaultLocation ?? state.preferences.defaultCollectionPath ?? '').trim())}
	                    >
	                      Clear
	                    </button>
	                  </div>
	                </div>
	              </div>
	            </section>

<style>
  .settings-hint {
    grid-column: 1 / -1;
    margin: -4px 0 4px;
    max-width: 62ch;
    font-size: 0.8rem;
    opacity: 0.8;
  }
</style>
