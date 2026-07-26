// Resolving a keydown to the action it should trigger.
//
// Split out of the 158-line dispatcher it came from, and worth splitting for
// one reason: the ORDER of the checks is the whole behaviour, and order is
// invisible in a chain of early-returning `if` blocks. Every property below is
// a statement about which check runs before which, and none of them produces an
// error when it changes — the wrong thing simply happens on a keypress.
//
// This function decides; the caller executes. Nothing here touches the DOM or
// calls preventDefault, so the decision can be tested without a browser.

/** Every action a keydown can resolve to. */
export type ShortcutAction =
  | 'closeCommandPalette'
  | 'closeRequestActionMenus'
  | 'focusURL'
  | 'cancelActiveRequest'
  | 'commandPalette'
  | 'globalSearch'
  | 'sidebarSearch'
  | 'collapseSidebar'
  | 'closeAllTabs'
  | 'reopenLastClosedTab'
  | 'closeTab'
  | 'switchToPreviousTab'
  | 'switchToNextTab'
  | 'switchToLastTab'
  | `switchToTab${1 | 2 | 3 | 4 | 5 | 6 | 7 | 8}`
  | 'moveTabLeft'
  | 'moveTabRight'
  | 'newRequest'
  | 'importCollection'
  | 'editEnvironment'
  | 'openPreferences'
  | 'openTerminal'
  | 'sendRequest'
  | 'changeLayout'
  | 'zoomIn'
  | 'zoomOut'
  | 'resetZoom'
  | 'closeBruno'
  | 'save'
  | 'saveAllTabs'

export interface ShortcutContext {
  /** The palette is open and Escape should close it before anything else sees the key. */
  commandPaletteOpen: boolean
  /** A `<details class="request-actions">` menu is open. */
  requestActionMenuOpen: boolean
  /** A modal `[role=dialog][aria-modal=true]` is on screen. */
  modalOpen: boolean
  activeView: string
  /** Whether there is a request, transport or collection run that Escape can cancel. */
  canCancel: boolean
  /** False when the user has turned custom keybindings off in preferences. */
  keybindingsEnabled: boolean
  /** Resolves a configured binding against the event. */
  matches: (action: string) => boolean
}

/**
 * The bindings checked in order, after the fixed pre-checks.
 *
 * `switchToLastTab` is listed BEFORE the numbered tabs deliberately: the two
 * groups can be bound to overlapping combos, and whichever is checked first
 * wins. Reordering them silently changes which tab a keypress selects.
 */
const CONFIGURABLE_ACTIONS: readonly ShortcutAction[] = [
  'commandPalette',
  'globalSearch',
  'sidebarSearch',
  'collapseSidebar',
  'closeAllTabs',
  'reopenLastClosedTab',
  'closeTab',
  'switchToPreviousTab',
  'switchToNextTab',
  'switchToLastTab',
  'switchToTab1',
  'switchToTab2',
  'switchToTab3',
  'switchToTab4',
  'switchToTab5',
  'switchToTab6',
  'switchToTab7',
  'switchToTab8',
  'moveTabLeft',
  'moveTabRight',
  'newRequest',
  'importCollection',
  'editEnvironment',
  'openPreferences',
  'openTerminal',
  'sendRequest',
  'changeLayout',
  'zoomIn',
  'zoomOut',
  'resetZoom',
  'closeBruno',
  'save',
  'saveAllTabs',
]

/** The actions the caller may configure, in the order they are matched. */
export const configurableShortcutActions = CONFIGURABLE_ACTIONS

/**
 * Resolves a keydown to an action, or undefined to let the event through.
 *
 * The four fixed checks below run BEFORE the keybindings-enabled gate. That is
 * the important part: turning custom keybindings off in preferences must not
 * leave the user with no way to dismiss the palette or stop a running request.
 * Those keys are not configurable, so they are not the user's to disable.
 */
export function resolveShortcut(
  event: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey'>,
  context: ShortcutContext,
): ShortcutAction | undefined {
  const isEscape = event.key === 'Escape'

  // The palette is a layer over everything. If Escape reached a lower handler
  // first the palette would stay open with its input still focused, swallowing
  // every subsequent key.
  if (isEscape && context.commandPaletteOpen) return 'closeCommandPalette'

  if (isEscape && context.requestActionMenuOpen) return 'closeRequestActionMenus'

  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'l' && context.activeView === 'request') {
    return 'focusURL'
  }

  if (isEscape && context.canCancel) {
    // A modal owns Escape while it is open — that is how it is dismissed. Note
    // this returns rather than falling through to the configurable bindings:
    // Escape inside a modal must do exactly one thing, and letting it also
    // match a user binding is how a dialog closes AND fires an action at once.
    if (context.modalOpen) return undefined
    return 'cancelActiveRequest'
  }

  if (!context.keybindingsEnabled) return undefined

  for (const action of CONFIGURABLE_ACTIONS) {
    if (context.matches(action)) return action
  }
  return undefined
}

/** The 1-based tab number for a `switchToTabN` action, or undefined. */
export function shortcutTabNumber(action: ShortcutAction): number | undefined {
  const match = /^switchToTab([1-8])$/.exec(action)
  return match ? Number(match[1]) : undefined
}
