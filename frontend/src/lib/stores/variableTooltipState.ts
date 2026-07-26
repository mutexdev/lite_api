// US-026 — the variable-tooltip transitions, as pure functions.
//
// Split out of the .svelte.ts store deliberately. A rune module cannot be
// exercised by `node --test`: $state is a compiler construct, so importing the
// store directly fails with "$state is not defined". The choice is either to
// add a second test runner with the Svelte plugin, or to keep the logic
// somewhere plain and let the store be a thin reactive shell over it.
//
// The second is better here for the same reason it has been all along: the
// behaviour is what needs testing, and none of it is reactive. Every function
// below is state-in, state-out.

export type TooltipState = {
  /** Which tooltip is open. Empty means none. */
  active: string
  /** Which tooltip is in edit mode. Empty means none. */
  editing: string
  /** The in-progress edit value. */
  draft: string
  /** Secret values the user has chosen to reveal, keyed by variable name. */
  revealed: Record<string, boolean>
  /** Names showing a transient "copied" confirmation. */
  copied: Record<string, boolean>
}

export function emptyTooltipState(): TooltipState {
  return { active: '', editing: '', draft: '', revealed: {}, copied: {} }
}

/**
 * Opens a tooltip, or closes it if it is already open.
 *
 * Closing on a second activation is what makes the tooltip dismissable without
 * a separate close target, and the inline-token keyboard handler depends on it.
 */
export function toggleActive(state: TooltipState, name: string): TooltipState {
  return { ...state, active: state.active === name ? '' : name }
}

export function closeTooltip(state: TooltipState): TooltipState {
  return { ...state, active: '' }
}

/**
 * Opens a tooltip in edit mode, seeded with the current value.
 *
 * A variable that is NOT found seeds an empty draft rather than its rendered
 * placeholder. The placeholder is not what the user typed, and offering it as
 * the starting text would have them accidentally save a rendered default as a
 * real value.
 *
 * The copied flag is cleared because the confirmation would otherwise still be
 * showing over a field the user is now editing.
 */
export function beginEdit(
  state: TooltipState,
  name: string,
  rawValue: string,
  found: boolean,
  editable: boolean
): TooltipState {
  if (!editable) return state
  return {
    ...state,
    active: name,
    editing: name,
    draft: found ? rawValue : '',
    copied: { ...state.copied, [name]: false }
  }
}

/**
 * Leaves edit mode without closing the tooltip: cancelling an edit should not
 * also dismiss the panel the user is still reading.
 */
export function cancelEdit(state: TooltipState): TooltipState {
  return { ...state, editing: '', draft: '' }
}

export function toggleRevealed(state: TooltipState, name: string): TooltipState {
  return { ...state, revealed: { ...state.revealed, [name]: !state.revealed[name] } }
}

export function markCopied(state: TooltipState, name: string, copied: boolean): TooltipState {
  return { ...state, copied: { ...state.copied, [name]: copied } }
}

/**
 * Clears everything. Used when the active request changes: a tooltip left open
 * across that switch would be showing a variable from the previous request,
 * resolved against its scope.
 */
export function resetTooltips(): TooltipState {
  return emptyTooltipState()
}
