<script lang="ts">
  // US-036 — the OAuth2 section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  //
  // The one row here used to carry a second class, `oauth2-browser-toggle`,
  // that no rule in style.css ever defined — a styling hook left behind by
  // something that was removed or never written. It is gone rather than
  // implemented: under SettingRow the row's spacing is not this section's to
  // decide any more.
  import type { types } from '../../../../wailsjs/go/models'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  type Props = {
    state: types.AppState
    updateAppearancePreferences: (patch: Record<string, unknown>) => void
  }

  let { state, updateAppearancePreferences }: Props = $props()
</script>

<SettingSection title="OAuth2">
  <SettingRow
    label="Use system browser for OAuth"
    description="Sends you to your default browser to sign in, so an existing session there is reused instead of asking you to log in again inside LiteAPI."
    checked={state.preferences.oauth2UseSystemBrowser ?? false}
    onCheckedChange={(value) => updateAppearancePreferences({ oauth2UseSystemBrowser: value } as Partial<types.Preferences>)}
  />
</SettingSection>
