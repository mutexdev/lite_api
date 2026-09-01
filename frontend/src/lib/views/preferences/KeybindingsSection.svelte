<script lang="ts">
  // US-036 — the Keybindings section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, each section only a handful.
  //
  // WHY THE DISCLOSURE SURVIVED THE ROW MIGRATION. Every other section is now a
  // flat list of SettingRows, and so is this one for the three settings it
  // actually has — Enabled, Preset, and the table. But the `<details
  // class="keybindings-disclosure">` and its `<summary>` are not decoration:
  // App.svelte's openKeyboardShortcuts() reaches into this markup by selector,
  // opens the details and focuses the summary, and App.svelte is not this
  // change's to edit. Renaming or flattening either one would break the
  // Keyboard Shortcuts command with no compile error and no test failure.
  //
  // What did change is that the settings moved OUT of the summary. The Enabled
  // toggle and the Reset button used to be absolutely positioned over the
  // section's top-right corner, which is why the summary carried 210px of
  // right padding — a layout no other section had, for controls that are
  // ordinary settings.
  import type { types } from '../../../../wailsjs/go/models'
  // Mirrors App.svelte's local KeyBindingSection / KeyBindingDefinition.
  // Checked first whether either was exported from a module this could import —
  // neither is, unlike WorkbenchCommandID. `never[]` was a shortcut that failed
  // the same way `any` does: it typechecks in isolation and breaks at the call
  // site.
  // US-057 moved these into lib/keybindings.ts alongside the default table, so
  // the mirrored declarations noted above are gone and there is one source.
  import type { KeyBindingDefinition, KeyBindingSection } from '../../keybindings'
  import SettingRow from './SettingRow.svelte'
  import SettingSection from './SettingSection.svelte'

  type Props = {
    state: types.AppState
    keyBindingSections: KeyBindingSection[]
    visibleKeyBindingEntries: (section: KeyBindingSection) => Array<[string, KeyBindingDefinition]>
    keyBindingDisplayValue: (action: string) => string
    keyBindingCanEdit: (action: string) => boolean
    keyBindingIsCustomized: (action: string) => boolean
    keybindingDraft: string
    keybindingsAreEnabled: (preferences: types.Preferences | undefined) => boolean
    keybindingError: string
    recordingKeybindingAction: string
    formatKeyBinding: (combo: string) => string
    beginRecordKeyBinding: (action: string) => void
    recordKeyBinding: (action: string, event: KeyboardEvent) => void
    stopRecordKeyBinding: (action: string) => void
    resetKeyBinding: (action: string) => void
    resetAllKeyBindings: () => void
    updateKeybindingsEnabled: (enabled: boolean) => void
    keyBindingPreset: string
    updateKeyBindingPreset: (preset: string) => void
  }

  let {
    state,
    keyBindingSections,
    visibleKeyBindingEntries,
    keyBindingDisplayValue,
    keyBindingCanEdit,
    keyBindingIsCustomized,
    keybindingDraft,
    keybindingsAreEnabled,
    keybindingError,
    recordingKeybindingAction,
    formatKeyBinding,
    beginRecordKeyBinding,
    recordKeyBinding,
    stopRecordKeyBinding,
    resetKeyBinding,
    resetAllKeyBindings,
    updateKeybindingsEnabled,
    keyBindingPreset,
    updateKeyBindingPreset,
  }: Props = $props()

  const enabled = $derived(keybindingsAreEnabled(state.preferences))
  const customizedCount = $derived(Object.keys(state.preferences.keyBindings ?? {}).length)
  const summaryStatus = $derived(
    !enabled
      ? 'Disabled'
      : customizedCount === 0
        ? 'Enabled · defaults'
        : `Enabled · ${customizedCount} customized`,
  )
</script>

<SettingSection>
  <details class="keybindings-disclosure">
    <summary>
      <span class="keybindings-summary-title">Keybindings</span>
      <span class="keybindings-summary-status">{summaryStatus}</span>
    </summary>

    <div class="keybindings-rows">
      <SettingRow
        label="Enabled"
        checkboxAriaLabel="Enable keybindings"
        checked={enabled}
        onCheckedChange={updateKeybindingsEnabled}
      />

      <SettingRow
        label="Preset"
        labelFor="keybinding-preset"
        description="A preset changes the defaults only. Shortcuts you have customized stay as you set them, and are listed as customized below."
        disabled={!enabled}
      >
        {#snippet control()}
          <select
            id="keybinding-preset"
            data-testid="keybinding-preset"
            aria-label="Keybinding preset"
            value={keyBindingPreset}
            disabled={!enabled}
            onchange={(event) => updateKeyBindingPreset(event.currentTarget.value)}
          >
            <option value="default">LiteAPI defaults</option>
            <option value="postman">Postman</option>
          </select>
          <button
            type="button"
            aria-label="Reset all keybindings to defaults"
            onclick={resetAllKeyBindings}
            disabled={customizedCount === 0}
          >Reset Default</button>
        {/snippet}
      </SettingRow>

      <!--
        The table is the one control in Preferences that genuinely is a table —
        three columns of command, combo and reset, repeated eighty times — so it
        stays one, as the wide control of a stacked row rather than as a fourth
        row anatomy of its own. `.keybindings-table-wrap` keeps its name because
        the 400px bounded scroller on it is the fix for the 1024x768 overflow
        bug, and that fix lives in style.css.
      -->
      <SettingRow label="Shortcuts" stacked disabled={!enabled}>
        {#snippet control()}
          <div class="keybindings-table-wrap">
            <table class="keybindings-table">
              <thead>
                <tr>
                  <th>Command</th>
                  <th>Keybinding</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {#each keyBindingSections as section (section.heading)}
                  <tr class="keybinding-section-row">
                    <td colspan="3">{section.heading}</td>
                  </tr>
                  {#each visibleKeyBindingEntries(section) as [action, binding] (action)}
                    {@const value = recordingKeybindingAction === action ? keybindingDraft : keyBindingDisplayValue(action)}
                    {@const canEdit = keyBindingCanEdit(action) && enabled}
                    <tr>
                      <td>
                        <span>{binding.name}</span>
                      </td>
                      <td>
                        <input
                          class="keybinding-input"
                          class:error={recordingKeybindingAction === action && Boolean(keybindingError)}
                          aria-label={`Keybinding ${binding.name}`}
                          readonly
                          disabled={!canEdit}
                          value={formatKeyBinding(value)}
                          placeholder="Press shortcut"
                          onfocus={() => beginRecordKeyBinding(action)}
                          onkeydown={(e) => recordKeyBinding(action, e)}
                          onblur={() => stopRecordKeyBinding(action)}
                        />
                        {#if recordingKeybindingAction === action && keybindingError}
                          <small class="keybinding-error">{keybindingError}</small>
                        {/if}
                      </td>
                      <td>
                        {#if binding.readOnly}
                          <span class="muted">Locked</span>
                        {:else}
                          <button type="button" onclick={() => resetKeyBinding(action)} disabled={!keyBindingIsCustomized(action)}>Reset</button>
                        {/if}
                      </td>
                    </tr>
                  {/each}
                {/each}
              </tbody>
            </table>
          </div>
        {/snippet}
      </SettingRow>
    </div>
  </details>
</SettingSection>

<style>
  /*
    The rows inside the disclosure carry the same gap SettingSection gives the
    rows in every other section, so opening Keybindings does not change the
    rhythm of the page under it.
  */
  .keybindings-rows {
    display: grid;
    gap: var(--space-10);
    margin-top: var(--space-10);
  }

  /*
    The summary is this section's header, standing in for the `<h3>` its
    siblings get from SettingSection. It is styled to match that heading rather
    than left at the browser's default, because a disclosure triangle is already
    difference enough.

    padding-right is reset to 0 on purpose: style.css still reserves 210px there
    for the Enabled toggle and Reset button that used to float over this
    corner, and without the reset the heading would sit indented past a gutter
    holding nothing.
  */
  .keybindings-disclosure summary {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-height: 34px;
    padding-right: 0;
    cursor: pointer;
    color: var(--text);
  }
</style>
