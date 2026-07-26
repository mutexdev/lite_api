<script lang="ts">
  // US-036 — the Keybindings section of the Preferences panel, extracted so its
  // markup is not in the initial chunk. Preferences is decomposed section by
  // section: as a whole it carries ~60 props, each section only a handful.
  import type { main } from '../../../../wailsjs/go/models'

  export let state: main.AppState
  // Mirrors App.svelte's local KeyBindingSection / KeyBindingDefinition.
  // Checked first whether either was exported from a module this could import —
  // neither is, unlike WorkbenchCommandID. `never[]` was a shortcut that failed
  // the same way `any` does: it typechecks in isolation and breaks at the call
  // site.
  type KeyBindingDefinition = { name: string; mac?: string; windows?: string; readOnly?: boolean; hidden?: boolean }
  type KeyBindingSection = { heading: string; bindings: Record<string, KeyBindingDefinition> }

  export let keyBindingSections: KeyBindingSection[]
  export let visibleKeyBindingEntries: (section: KeyBindingSection) => Array<[string, KeyBindingDefinition]>
  export let keyBindingDisplayValue: (action: string) => string
  export let keyBindingCanEdit: (action: string) => boolean
  export let keyBindingIsCustomized: (action: string) => boolean
  export let keybindingDraft: string
  export let keybindingsAreEnabled: (preferences: main.Preferences | undefined) => boolean
  export let keybindingError: string
  export let recordingKeybindingAction: string
  export let formatKeyBinding: (combo: string) => string
  export let beginRecordKeyBinding: (action: string) => void
  export let recordKeyBinding: (action: string, event: KeyboardEvent) => void
  export let stopRecordKeyBinding: (action: string) => void
  export let resetKeyBinding: (action: string) => void
  export let resetAllKeyBindings: () => void
  export let updateKeybindingsEnabled: (enabled: boolean) => void
</script>

            <section class="keybindings-preference-section">
              <details class="keybindings-disclosure">
                <summary>
                  <span class="keybindings-summary-title">Keybindings</span>
                  <span class="keybindings-summary-status">
                    {#if keybindingsAreEnabled(state.preferences)}
                      {Object.keys(state.preferences.keyBindings ?? {}).length === 0
                        ? 'Enabled · defaults'
                        : `Enabled · ${Object.keys(state.preferences.keyBindings ?? {}).length} customized`}
                    {:else}
                      Disabled
                    {/if}
                  </span>
                </summary>
                <div class:settings-disabled={!keybindingsAreEnabled(state.preferences)} class="keybindings-table-wrap">
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
                        {@const canEdit = keyBindingCanEdit(action) && keybindingsAreEnabled(state.preferences)}
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
                              on:focus={() => beginRecordKeyBinding(action)}
                              on:keydown={(e) => recordKeyBinding(action, e)}
                              on:blur={() => stopRecordKeyBinding(action)}
                            />
                            {#if recordingKeybindingAction === action && keybindingError}
                              <small class="keybinding-error">{keybindingError}</small>
                            {/if}
                          </td>
                          <td>
                            {#if binding.readOnly}
                              <span class="muted">Locked</span>
                            {:else}
                              <button on:click={() => resetKeyBinding(action)} disabled={!keyBindingIsCustomized(action)}>Reset</button>
                            {/if}
                          </td>
                        </tr>
                      {/each}
                    {/each}
                  </tbody>
                </table>
                </div>
              </details>
              <div class="settings-section-actions keybindings-section-actions">
                <label class="inline-toggle">
                  <input
                    type="checkbox"
                    aria-label="Enable keybindings"
                    checked={keybindingsAreEnabled(state.preferences)}
                    on:change={(e) => updateKeybindingsEnabled(e.currentTarget.checked)}
                  />
                  Enabled
                </label>
                <button aria-label="Reset all keybindings to defaults" on:click={resetAllKeyBindings} disabled={Object.keys(state.preferences.keyBindings ?? {}).length === 0}>Reset Default</button>
              </div>
            </section>
