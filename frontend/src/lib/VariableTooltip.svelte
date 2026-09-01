<script lang="ts">
  /**
   * The panel that opens when a `{{variable}}` chip is activated. One of it.
   *
   * A5-07's second half. The chip had three visual implementations; the tooltip
   * beside it had three literal copies of the same forty lines of markup — the
   * URL bar overlay and the variable inspector, both inline in App.svelte, and
   * VariableTextOverlay.svelte. Same title row, same scope badge, same inline
   * editor, same Save/Cancel, same Copy, same Show/Hide, written out three
   * times.
   *
   * Three copies of a panel is how the chips ended up with three appearances:
   * nothing made them move together, so they stopped. Collapsing them removes
   * about eighty lines from App.svelte and, more to the point, removes the two
   * places a future edit could land in without landing in the others.
   *
   * The panel's own class is a prop because the two families are positioned by
   * different existing global rules (`.variable-tooltip` under the inspector
   * chip, `.CodeMirror-brunoVarInfo.inline-var-tooltip` under the inline
   * tokens), and unifying the POSITIONING means editing style.css, which
   * belongs to another pass in this wave. The contents — which is what the
   * audit found divergent — are one implementation as of now.
   */
  import { variableTooltips } from './stores/variableTooltipStore.svelte'
  import type { VariableTooltipInfo } from './variableResolution'

  type Props = {
    info: VariableTooltipInfo
    /** The existing wrapper class for this surface. See the note above. */
    panelClass: string
    busy?: string
    displayValue: (info: VariableTooltipInfo, revealed: boolean) => string
    invalidWarning: string
    onEditorKey: (event: KeyboardEvent, info: VariableTooltipInfo) => void
    onEditorBlur: (event: FocusEvent, info: VariableTooltipInfo) => void
    onSave: (info: VariableTooltipInfo) => void
    onBeginEdit: (info: VariableTooltipInfo) => void
    onCancelEdit: () => void
    onCopy: (info: VariableTooltipInfo) => void
    onToggleReveal: (name: string) => void
  }

  let {
    info,
    panelClass,
    busy = '',
    displayValue,
    invalidWarning,
    onEditorKey,
    onEditorBlur,
    onSave,
    onBeginEdit,
    onCancelEdit,
    onCopy,
    onToggleReveal
  }: Props = $props()
</script>

<div class={panelClass} role="tooltip">
  <div class="variable-tooltip-title">
    <strong class="var-name">{info.name}</strong>
    <span class="var-scope-badge">{info.scope}</span>
  </div>
  {#if !info.validName}
    <small class="var-warning-note">{invalidWarning}</small>
  {:else if variableTooltips.editing === info.name}
    <textarea
      class="var-value-editor"
      aria-label={'Edit variable ' + info.name}
      bind:value={variableTooltips.draft}
      onkeydown={(event) => onEditorKey(event, info)}
      onblur={(event) => onEditorBlur(event, info)}
    ></textarea>
    <div class="button-row compact">
      <button class="var-save-button" onclick={(event) => { event.stopPropagation(); onSave(info) }} disabled={busy !== ''}>Save</button>
      <button onclick={(event) => { event.stopPropagation(); onCancelEdit() }}>Cancel</button>
    </div>
  {:else if info.editable}
    <button type="button" class="var-value-editable-display" onclick={(event) => { event.stopPropagation(); onBeginEdit(info) }}>
      {displayValue(info, variableTooltips.isRevealed(info.name))}
    </button>
  {:else}
    <div class="var-value-editable-display">{displayValue(info, variableTooltips.isRevealed(info.name))}</div>
  {/if}
  {#if info.readOnly}
    <small class="var-readonly-note">read-only</small>
  {/if}
  <div class="button-row compact">
    <button
      class="copy-button"
      class:copy-success={variableTooltips.isCopied(info.name)}
      onclick={(event) => { event.stopPropagation(); onCopy(info) }}
      disabled={!info.found || !info.validName || variableTooltips.isCopied(info.name)}
    >
      {variableTooltips.isCopied(info.name) ? 'Copied' : 'Copy'}
    </button>
    {#if info.secret}
      <button class="secret-toggle-button" onclick={(event) => { event.stopPropagation(); onToggleReveal(info.name) }}>
        {variableTooltips.isRevealed(info.name) ? 'Hide' : 'Show'}
      </button>
    {/if}
  </div>
</div>
