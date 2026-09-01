<script lang="ts">
  // US-036 — the Display section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, but each <section> needs only a
  // handful.
  //
  // The two rows here used to be two bespoke grids — `.font-preference-grid` at
  // 600px wide with its labels above its inputs, and `.zoom-preference-row` at
  // 440px with them beside — so a user reading straight down saw the section's
  // right edge move twice inside three lines. Both are SettingRows now, and
  // neither knows how wide it is.
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  type Props = {
    appZoomPercentage: number
    zoomPercentages: number[]
    zoomDefaultPercentage: number
    codeFont: string
    codeFontSize: number
    resetZoomPercentage: () => void
    setZoomPercentage: (percentage: number) => void
    updateCodeFont: (font: string) => void
    updateCodeFontSize: (size: number) => void
  }

  let {
    appZoomPercentage,
    zoomPercentages,
    zoomDefaultPercentage,
    codeFont,
    codeFontSize,
    resetZoomPercentage,
    setZoomPercentage,
    updateCodeFont,
    updateCodeFontSize,
  }: Props = $props()
</script>

<SettingSection title="Display">
  {#snippet status()}
    <span class="preference-value" data-testid="zoom-percentage-value">{appZoomPercentage}%</span>
  {/snippet}

  <SettingRow label="Code Editor Font" labelFor="code-font-input">
    {#snippet control()}
      <input
        id="code-font-input"
        data-testid="code-font-input"
        aria-label="Code Editor Font"
        value={codeFont}
        autocapitalize="off"
        autocomplete="off"
        autocorrect="off"
        spellcheck="false"
        oninput={(event) => updateCodeFont(event.currentTarget.value)}
      />
    {/snippet}
  </SettingRow>

  <SettingRow label="Font Size" labelFor="code-font-size-input">
    {#snippet control()}
      <input
        id="code-font-size-input"
        data-testid="code-font-size-input"
        aria-label="Font Size"
        type="number"
        min="1"
        max="32"
        inputmode="numeric"
        value={codeFontSize}
        oninput={(event) => updateCodeFontSize(Number(event.currentTarget.value))}
      />
    {/snippet}
  </SettingRow>

  <SettingRow label="Zoom" labelFor="zoom-percentage">
    {#snippet control()}
      <!--
        Six options, so a select rather than a segmented group: the panel's rule
        is that an enum is a native <select> everywhere, with theme mode the one
        documented exception. Reset sits with the control it resets, inside the
        one control cell, rather than becoming a second column only this row has.
      -->
      <select
        id="zoom-percentage"
        data-testid="zoom-percentage-select"
        aria-label="App zoom"
        value={appZoomPercentage}
        onchange={(event) => setZoomPercentage(Number(event.currentTarget.value))}
      >
        {#each zoomPercentages as percentage (percentage)}
          <option value={percentage}>{percentage}%</option>
        {/each}
      </select>
      <button
        type="button"
        data-testid="zoom-reset-btn"
        onclick={resetZoomPercentage}
        disabled={appZoomPercentage === zoomDefaultPercentage}
      >
        Reset
      </button>
    {/snippet}
  </SettingRow>
</SettingSection>
