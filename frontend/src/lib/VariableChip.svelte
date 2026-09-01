<script lang="ts">
  /**
   * The `{{variable}}` pill. One of it.
   *
   * See variableChipState.ts for what the three previous implementations
   * disagreed about and why the five states are the states. This file is the
   * visual half: one shape, one radius, one border rule, five backgrounds.
   *
   * The CSS lives in this component's scoped block rather than in style.css
   * because style.css is owned by another pass in this wave. That is a
   * temporary home, not a design choice — the handoff carries the block
   * paste-ready, and the CodeMirror decoration theme (CodeEditor.svelte, also
   * not ours) needs the same values before the third surface joins these two.
   * Scoped styles are the right stopgap and not merely an expedient one: they
   * beat the global `.cm-variable-*` rules on specificity, so the two DOM
   * surfaces agree today rather than after the handoff lands.
   */
  import { variableChipLabel, variableChipState, type ChipStateInput } from './variableChipState'

  type Props = {
    /** The rendered text, braces included: `{{token}}`. Never a resolved value. */
    text: string
    name: string
    info?: ChipStateInput | undefined
    scope?: string
    prompt?: boolean
    /**
     * Omitted for a chip with nothing to open — a prompt variable has no stored
     * value to inspect. Rendering those as buttons anyway would put a focus stop
     * in the middle of a URL for a control that does nothing when pressed.
     */
    onActivate?: (() => void) | undefined
    /** Escape closes the open tooltip; the two overlay surfaces already did this and the inspector did not. */
    onDismiss?: (() => void) | undefined
  }

  let { text, name, info = undefined, scope = '', prompt = false, onActivate = undefined, onDismiss = undefined }: Props = $props()

  const state = $derived(variableChipState(info, prompt))
  const label = $derived(variableChipLabel(name, state, scope))

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onActivate?.()
    } else if (event.key === 'Escape' && onDismiss) {
      event.preventDefault()
      onDismiss()
    }
  }
</script>

<!--
  The lock glyph is a decorative twin of the state the aria-label already names,
  so it is hidden rather than read out — a screen reader announcing "lock"
  before a sentence that already says "secret variable" says it twice.
-->
{#if onActivate}
  <button type="button" class="variable-chip-pill" data-state={state} aria-label={label} onclick={onActivate} onkeydown={keydown}>
    {#if state === 'secret'}<span class="variable-chip-glyph" aria-hidden="true">&#128274;</span>{/if}
    <span class="variable-chip-text">{text}</span>
  </button>
{:else}
  <span class="variable-chip-pill" data-state={state} aria-label={label} role="note">
    {#if state === 'secret'}<span class="variable-chip-glyph" aria-hidden="true">&#128274;</span>{/if}
    <span class="variable-chip-text">{text}</span>
  </span>
{/if}
