<script lang="ts">
  // US-036 — openapi-settings-title lifted out of App.svelte so its markup is not in the
  // initial chunk. Imported dynamically from inside the {#if} that gates it;
  // a static import would leave it in the initial graph and save nothing.
  import Modal from '../Modal.svelte'
  import IconButton from '../../ui/IconButton.svelte'

  // Bindable: the dialog writes these back to App.svelte.
  export let openAPISyncSettingsSourceURL: string
  export let openAPISyncSettingsAutoCheck: boolean
  export let openAPISyncSettingsInterval: number
  export let openAPISyncCheckIntervals: number[]
  export let busy: string
  export let saveOpenAPISyncSettings: () => void
  export let cancelOpenAPISyncSettings: () => void
  export let disconnectOpenAPISync: () => void
</script>

<Modal labelledBy="openapi-settings-title" onClose={cancelOpenAPISyncSettings} testId="openapi-sync-settings-modal" size="medium" busy={busy !== ''}>
		      <form on:submit|preventDefault={saveOpenAPISyncSettings}>
	        <header>
	          <h2 id="openapi-settings-title">Connection Settings</h2>
	          <IconButton icon="close" label="Close" onclick={cancelOpenAPISyncSettings} />
	        </header>
	        <div class="prompt-fields">
	          <label>
	            <span>Spec Source</span>
	            <input
	              aria-label="OpenAPI sync settings source"
	              data-testid="openapi-sync-settings-source"
	              placeholder="https://api.example.com/openapi.json"
	              bind:value={openAPISyncSettingsSourceURL}
	            />
	          </label>
	          <label class="checkbox-line openapi-settings-toggle">
	            <input type="checkbox" data-testid="openapi-sync-settings-auto-check" bind:checked={openAPISyncSettingsAutoCheck} />
	            <span>Auto-check for updates</span>
	          </label>
	          {#if openAPISyncSettingsAutoCheck}
	            <div class="openapi-settings-intervals">
	              <span>Check interval</span>
	              <div class="segmented compact" data-testid="openapi-sync-settings-intervals">
	                {#each openAPISyncCheckIntervals as minutes (minutes)}
	                  <button
	                    type="button"
	                    class:active={openAPISyncSettingsInterval === minutes}
	                    data-testid={`openapi-sync-settings-interval-${minutes}`}
	                    on:click={() => (openAPISyncSettingsInterval = minutes)}
	                  >
	                    {minutes} min
	                  </button>
	                {/each}
	              </div>
	            </div>
	          {/if}
	        </div>
	        <!--
	          FOOTER ORDER: neutral first, destructive last. "Disconnect sync" led
	          the row and so was the first tab stop after the form — the one control
	          here that throws the connection away sat where every other dialog in
	          the app puts Cancel.
	        -->
	        <div class="button-row">
	          <button type="button" data-testid="openapi-sync-settings-cancel" on:click={cancelOpenAPISyncSettings}>Cancel</button>
	          <button type="button" class="danger-button" data-testid="openapi-sync-settings-disconnect" on:click={disconnectOpenAPISync} disabled={busy !== ''}>Disconnect Sync</button>
	          <button class="primary" type="submit" data-testid="openapi-sync-settings-save" disabled={busy !== ''}>Save</button>
	        </div>
	      </form>
</Modal>
