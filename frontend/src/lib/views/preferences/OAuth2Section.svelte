<script lang="ts">
  // US-036 — the OAuth2 section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  import type { main } from '../../../../wailsjs/go/models'

  export let state: main.AppState
  export let updateAppearancePreferences: (patch: Record<string, unknown>) => void
</script>

	            <section>
	              <div class="settings-section-header">
	                <h3>OAuth2</h3>
	              </div>
	              <label class="inline-toggle oauth2-browser-toggle">
	                <input
	                  type="checkbox"
	                  checked={state.preferences.oauth2UseSystemBrowser ?? false}
	                  on:change={(e) => updateAppearancePreferences({ oauth2UseSystemBrowser: e.currentTarget.checked } as Partial<main.Preferences>)}
	                />
	                Use system browser for OAuth
	              </label>
	            </section>
