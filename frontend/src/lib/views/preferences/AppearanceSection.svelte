<script lang="ts">
  // US-036 — the Appearance section of the Preferences panel.
  //
  // Extracted as a SECTION rather than as the whole panel. Preferences is
  // ~24 KB with roughly 60 props, which is where the extraction workflow that
  // carried all 29 dialogs breaks down — Import failed the same way, at 39
  // svelte-check errors. But the panel divides cleanly into seven <section>
  // blocks with h3 headings, and each is small enough to extract and verify on
  // its own. This one needs 7 props, not 60.
  //
  // App.svelte imports it dynamically from inside the preferences branch, so it
  // loads only when a user opens Preferences.
  import type { types } from '../../../../wailsjs/go/models'

  // Mirrors App.svelte's local ThemeMode. Checked first whether it was
  // exported anywhere importable — it is not, unlike WorkbenchCommandID, which
  // turned out to be exported from workbenchCommands.ts and cost a revert when
  // I assumed otherwise. Mirroring is correct here; widening to `string` is not,
  // because updateThemeMode is typed against the union.
  type ThemeMode = 'system' | 'light' | 'dark'
  type ThemeVariant = { id: string; name: string; preview: Record<string, string> }

  export let state: types.AppState
  export let selectedThemeMode: ThemeMode
  export let themeModes: Array<{ id: ThemeMode; label: string }>
  export let lightThemeVariants: ThemeVariant[]
  export let darkThemeVariants: ThemeVariant[]
  export let updateThemeMode: (mode: ThemeMode) => Promise<void> | void
  export let updateThemeVariant: (mode: 'light' | 'dark', variant: string) => void
</script>

            <section>
              <div class="settings-section-header">
                <h3>Appearance</h3>
              </div>
              <div class="theme-mode-selector" aria-label="Theme mode">
                {#each themeModes as mode (mode.id)}
                  <button
                    class:selected={selectedThemeMode === mode.id}
                    aria-pressed={selectedThemeMode === mode.id}
                    on:click={() => updateThemeMode(mode.id)}
                  >
                    {mode.label}
                  </button>
                {/each}
              </div>

              {#if selectedThemeMode === 'light' || selectedThemeMode === 'system'}
                <div class="theme-variant-section">
                  <span class="field-label">Light Theme</span>
                  <div class="theme-variants">
                    {#each lightThemeVariants as variant (variant.id)}
                      <button
                        class="theme-variant-card"
                        class:selected={(state.preferences.themeVariantLight || 'light') === variant.id}
                        aria-label={`Light theme ${variant.name}`}
                        aria-pressed={(state.preferences.themeVariantLight || 'light') === variant.id}
                        on:click={() => updateThemeVariant('light', variant.id)}
                      >
                        <span class="theme-preview" style={`--preview-bg: ${variant.preview.background}; --preview-sidebar: ${variant.preview.sidebar}; --preview-accent: ${variant.preview.accent};`}>
                          <span class="theme-preview-sidebar"></span>
                          <span class="theme-preview-main">
                            <span></span>
                            <span></span>
                            <span></span>
                          </span>
                        </span>
                        <span>{variant.name}</span>
                      </button>
                    {/each}
                  </div>
                </div>
              {/if}

              {#if selectedThemeMode === 'dark' || selectedThemeMode === 'system'}
                <div class="theme-variant-section">
                  <span class="field-label">Dark Theme</span>
                  <div class="theme-variants">
                    {#each darkThemeVariants as variant (variant.id)}
                      <button
                        class="theme-variant-card"
                        class:selected={(state.preferences.themeVariantDark || 'dark') === variant.id}
                        aria-label={`Dark theme ${variant.name}`}
                        aria-pressed={(state.preferences.themeVariantDark || 'dark') === variant.id}
                        on:click={() => updateThemeVariant('dark', variant.id)}
                      >
                        <span class="theme-preview" style={`--preview-bg: ${variant.preview.background}; --preview-sidebar: ${variant.preview.sidebar}; --preview-accent: ${variant.preview.accent};`}>
                          <span class="theme-preview-sidebar"></span>
                          <span class="theme-preview-main">
                            <span></span>
                            <span></span>
                            <span></span>
                          </span>
                        </span>
                        <span>{variant.name}</span>
                      </button>
                    {/each}
                  </div>
                </div>
              {/if}
	            </section>
