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
  import SegmentedControl from '../../ui/SegmentedControl.svelte'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  // Mirrors App.svelte's local ThemeMode. Checked first whether it was
  // exported anywhere importable — it is not, unlike WorkbenchCommandID, which
  // turned out to be exported from workbenchCommands.ts and cost a revert when
  // I assumed otherwise. Mirroring is correct here; widening to `string` is not,
  // because updateThemeMode is typed against the union.
  type ThemeMode = 'system' | 'light' | 'dark'
  type ThemeVariant = { id: string; name: string; preview: Record<string, string> }

  type Props = {
    state: types.AppState
    selectedThemeMode: ThemeMode
    themeModes: Array<{ id: ThemeMode; label: string }>
    lightThemeVariants: ThemeVariant[]
    darkThemeVariants: ThemeVariant[]
    updateThemeMode: (mode: ThemeMode) => Promise<void> | void
    updateThemeVariant: (mode: 'light' | 'dark', variant: string) => void
  }

  let {
    state,
    selectedThemeMode,
    themeModes,
    lightThemeVariants,
    darkThemeVariants,
    updateThemeMode,
    updateThemeVariant,
  }: Props = $props()

  // SegmentedControl speaks {value,label}; the panel's own vocabulary is
  // {id,label}. Translating here rather than changing the prop keeps
  // App.svelte, which cannot be edited alongside this file, untouched.
  const modeOptions = $derived(themeModes.map((mode) => ({ value: mode.id, label: mode.label })))
</script>

<SettingSection title="Appearance">
  <!--
    WHAT WENT WRONG. This picker was a hand-rolled group of `<button>`s in a
    `.theme-mode-selector`, which is `.segmented` reimplemented with different
    padding, a different radius and a box-shadow instead of a filled selected
    state — the same "pick one of three" problem the Notifications modal had
    already solved with the shared class. Two components, one job, and they did
    not look alike.

    The replacement is not only a repaint: a row of plain buttons is a row of
    tab stops, and SegmentedControl is a real radiogroup, so the whole picker is
    one stop with arrows moving between the options.
  -->
  <SettingRow label="Theme mode">
    {#snippet control()}
      <SegmentedControl
        options={modeOptions}
        value={selectedThemeMode}
        onChange={(value) => updateThemeMode(value as ThemeMode)}
        ariaLabel="Theme mode"
        compact={false}
        testId="theme-mode"
      />
    {/snippet}
  </SettingRow>

  {#if selectedThemeMode === 'light' || selectedThemeMode === 'system'}
    <SettingRow label="Light Theme" stacked>
      {#snippet control()}
        <div class="theme-variants">
          {#each lightThemeVariants as variant (variant.id)}
            <button
              class="theme-variant-card"
              class:selected={(state.preferences.themeVariantLight || 'light') === variant.id}
              aria-label={`Light theme ${variant.name}`}
              aria-pressed={(state.preferences.themeVariantLight || 'light') === variant.id}
              onclick={() => updateThemeVariant('light', variant.id)}
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
      {/snippet}
    </SettingRow>
  {/if}

  {#if selectedThemeMode === 'dark' || selectedThemeMode === 'system'}
    <SettingRow label="Dark Theme" stacked>
      {#snippet control()}
        <div class="theme-variants">
          {#each darkThemeVariants as variant (variant.id)}
            <button
              class="theme-variant-card"
              class:selected={(state.preferences.themeVariantDark || 'dark') === variant.id}
              aria-label={`Dark theme ${variant.name}`}
              aria-pressed={(state.preferences.themeVariantDark || 'dark') === variant.id}
              onclick={() => updateThemeVariant('dark', variant.id)}
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
      {/snippet}
    </SettingRow>
  {/if}
</SettingSection>
