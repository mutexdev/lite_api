// US-026 — the variable-tooltip store.
//
// This cluster was threaded as props through three components — KeyValueTable,
// MultipartTable and VariableTextOverlay — from five call sites in App.svelte.
// The intermediate components used almost none of it; they passed it down. The
// cost of that drilling is not verbosity: every hop is a place where a rename
// or a missed prop silently stops the tooltip responding, with nothing to
// report it.
//
// A thin reactive shell only. Every transition lives in variableTooltipState.ts
// as a pure function, because a .svelte.ts module cannot be exercised by
// `node --test` — $state is a compiler construct — and the behaviour is what
// needs testing.
//
// Saving and copying stay OUTSIDE this store: they need the app's collection
// actions and its error channel, and a store reaching for those would need the
// whole app injected and would stop being testable at all.
import {
  emptyTooltipState,
  toggleActive,
  closeTooltip,
  beginEdit,
  cancelEdit,
  toggleRevealed,
  markCopied,
  resetTooltips,
  type TooltipState
} from './variableTooltipState'

class VariableTooltipStore {
  private state = $state<TooltipState>(emptyTooltipState())

  get active() {
    return this.state.active
  }

  get editing() {
    return this.state.editing
  }

  get draft() {
    return this.state.draft
  }

  set draft(value: string) {
    // Writable because the editor textarea binds to it.
    this.state = { ...this.state, draft: value }
  }

  toggleActive(name: string) {
    this.state = toggleActive(this.state, name)
  }

  close() {
    this.state = closeTooltip(this.state)
  }

  beginEdit(name: string, rawValue: string, found: boolean, editable: boolean) {
    this.state = beginEdit(this.state, name, rawValue, found, editable)
  }

  cancelEdit() {
    this.state = cancelEdit(this.state)
  }

  toggleRevealed(name: string) {
    this.state = toggleRevealed(this.state, name)
  }

  isRevealed(name: string) {
    return Boolean(this.state.revealed[name])
  }

  markCopied(name: string, copied: boolean) {
    this.state = markCopied(this.state, name, copied)
  }

  isCopied(name: string) {
    return Boolean(this.state.copied[name])
  }

  reset() {
    this.state = resetTooltips()
  }
}

export const variableTooltips = new VariableTooltipStore()
export type { VariableTooltipStore }
