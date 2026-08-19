// US-057 — keybinding defaults and selectable presets.
//
// The default table lived inside App.svelte, where nothing could test it. It
// moves here so both it and the Postman preset are checked by the same
// collision rule the UI enforces — a preset that introduces a duplicate combo
// is exactly what the story's "collisions still rejected" is about, and a
// preset shipped with one would silently shadow an action for every user who
// selected it.

export type KeyBindingOS = 'mac' | 'windows'

export type KeyBindingDefinition = {
  name: string
  mac?: string
  windows?: string
  readOnly?: boolean
  hidden?: boolean
  displayValue?: Partial<Record<KeyBindingOS, string>>
}

export type KeyBindingSection = {
  heading: string
  bindings: Record<string, KeyBindingDefinition>
}

export const keyBindingSections: KeyBindingSection[] = [
  {
    heading: 'Tabs',
    bindings: {
      closeTab: { mac: 'command+bind+w', windows: 'ctrl+bind+w', name: 'Close Tab' },
      closeAllTabs: { mac: 'command+bind+shift+bind+w', windows: 'ctrl+bind+shift+bind+w', name: 'Close All Tabs' },
      save: { mac: 'command+bind+s', windows: 'ctrl+bind+s', name: 'Save' },
      saveAllTabs: { mac: 'command+bind+shift+bind+s', windows: 'ctrl+bind+shift+bind+s', name: 'Save All Tabs' },
      reopenLastClosedTab: { mac: 'command+bind+shift+bind+t', windows: 'ctrl+bind+shift+bind+t', name: 'Reopen Last Closed Tab' },
      switchToTabAtPosition: {
        mac: 'command+bind+1+bind+command+bind+8',
        windows: 'ctrl+bind+1+bind+ctrl+bind+8',
        name: 'Switch to Tab at Position',
        readOnly: true,
        displayValue: { mac: 'command+bind+1 - command+bind+8', windows: 'ctrl+bind+1 - ctrl+bind+8' }
      },
      switchToLastTab: { mac: 'command+bind+9', windows: 'ctrl+bind+9', name: 'Switch to Last Tab' },
      switchToPreviousTab: { mac: 'shift+bind+command+bind+[', windows: 'shift+bind+ctrl+bind+[', name: 'Switch to Previous Tab' },
      switchToNextTab: { mac: 'shift+bind+command+bind+]', windows: 'shift+bind+ctrl+bind+]', name: 'Switch to Next Tab' },
      moveTabLeft: { mac: 'command+bind+[', windows: 'ctrl+bind+[', name: 'Move Tab Left' },
      moveTabRight: { mac: 'command+bind+]', windows: 'ctrl+bind+]', name: 'Move Tab Right' },
      switchToTab1: { mac: 'command+bind+1', windows: 'ctrl+bind+1', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab2: { mac: 'command+bind+2', windows: 'ctrl+bind+2', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab3: { mac: 'command+bind+3', windows: 'ctrl+bind+3', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab4: { mac: 'command+bind+4', windows: 'ctrl+bind+4', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab5: { mac: 'command+bind+5', windows: 'ctrl+bind+5', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab6: { mac: 'command+bind+6', windows: 'ctrl+bind+6', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab7: { mac: 'command+bind+7', windows: 'ctrl+bind+7', name: 'Switch to Tab at Position', readOnly: true, hidden: true },
      switchToTab8: { mac: 'command+bind+8', windows: 'ctrl+bind+8', name: 'Switch to Tab at Position', readOnly: true, hidden: true }
    }
  },
  {
    heading: 'Sidebar',
    bindings: {
      sidebarSearch: { mac: 'command+bind+f', windows: 'ctrl+bind+f', name: 'Search Sidebar' },
      copyItem: { mac: 'command+bind+c', windows: 'ctrl+bind+c', name: 'Copy Item' },
      pasteItem: { mac: 'command+bind+v', windows: 'ctrl+bind+v', name: 'Paste Item' },
      cloneItem: { mac: 'command+bind+d', windows: 'ctrl+bind+d', name: 'Clone Item' },
      renameItem: { mac: 'command+bind+r', windows: 'ctrl+bind+r', name: 'Rename Item' },
      // Command-Delete is "move to trash" on macOS, which is what this does.
      // Added alongside cloneItem and renameItem when the sidebar tree gained a
      // keyboard focus for them to act on; see lib/sidebar/sidebarActions.ts.
      deleteItem: { mac: 'command+bind+backspace', windows: 'ctrl+bind+backspace', name: 'Delete Item' },
      collapseSidebar: { mac: 'command+bind+\\', windows: 'ctrl+bind+\\', name: 'Collapse Sidebar' }
    }
  },
  {
    heading: 'Requests',
    bindings: {
      sendRequest: { mac: 'command+bind+enter', windows: 'ctrl+bind+enter', name: 'Send Request' },
      changeLayout: { mac: 'command+bind+j', windows: 'ctrl+bind+j', name: 'Change Orientation' }
    }
  },
  {
    heading: 'Collections & Environment',
    bindings: {
      importCollection: { mac: 'command+bind+o', windows: 'ctrl+bind+o', name: 'Import Collection' },
      editEnvironment: { mac: 'command+bind+e', windows: 'ctrl+bind+e', name: 'Edit Environment' },
      newRequest: { mac: 'command+bind+n', windows: 'ctrl+bind+n', name: 'New Request' }
    }
  },
  {
    heading: 'Search',
    bindings: {
      globalSearch: { mac: 'command+bind+k', windows: 'ctrl+bind+k', name: 'Global Search' },
      // US-055. Listed beside Global Search on purpose: the audit asks for
      // two distinct surfaces, and showing them together in Preferences is
      // what makes the distinction visible rather than folklore.
      commandPalette: { mac: 'command+bind+shift+bind+p', windows: 'ctrl+bind+shift+bind+p', name: 'Command Palette' }
    }
  },
  {
    heading: 'View',
    bindings: {
      zoomIn: { mac: 'command+bind+=', windows: 'ctrl+bind+=', name: 'Zoom In' },
      zoomOut: { mac: 'command+bind+-', windows: 'ctrl+bind+-', name: 'Zoom Out' },
      resetZoom: { mac: 'command+bind+0', windows: 'ctrl+bind+0', name: 'Reset Zoom' }
    }
  },
  { heading: 'Developer Tool', bindings: { openTerminal: { mac: 'command+bind+t', windows: 'ctrl+bind+t', name: 'Open in Terminal' } } },
  {
    heading: 'Others',
    bindings: {
      openPreferences: { mac: 'command+bind+,', windows: 'ctrl+bind+,', name: 'Open Preferences' },
      closeBruno: { mac: 'command+bind+q', windows: 'ctrl+bind+shift+bind+q', name: 'Close LiteAPI' }
    }
  }
]

export type KeyBindingPresetID = 'default' | 'postman'

/** One preset's overrides, keyed by action. Only the entries that DIFFER. */
export type KeyBindingPreset = Record<string, { mac?: string; windows?: string }>

/**
 * The Postman preset is deliberately SMALL, and that is a finding rather than
 * an omission.
 *
 * These defaults descend from Bruno's, which were themselves modelled on
 * Postman, so the great majority already match: Cmd+Enter to send, Cmd+S to
 * save, Cmd+W to close a tab, Cmd+\\ for the sidebar, Cmd+, for settings,
 * Cmd+O to import. Listing those again would be noise that implies a change
 * where there is none, and would have to be kept in sync with the defaults
 * forever.
 *
 * Only entries where this app genuinely diverges from a Postman shortcut are
 * listed. Cmd+T is the substantive one: Postman reserves it for New Tab, while
 * this app binds Open in Terminal — a command Postman has no equivalent of.
 * Freeing Cmd+T is the point of the preset, and moving Open in Terminal
 * somewhere unclaimed is what stops that freeing from creating a collision.
 */
export const keyBindingPresets: Record<KeyBindingPresetID, KeyBindingPreset> = {
  default: {},
  postman: {
    // Postman: Cmd/Ctrl+T is New Tab. Open in Terminal moves off it.
    openTerminal: { mac: 'command+bind+alt+bind+t', windows: 'ctrl+bind+alt+bind+t' },
    // Postman: Cmd/Ctrl+Alt+Left / Right cycle tabs.
    switchToPreviousTab: { mac: 'command+bind+alt+bind+arrowleft', windows: 'ctrl+bind+alt+bind+arrowleft' },
    switchToNextTab: { mac: 'command+bind+alt+bind+arrowright', windows: 'ctrl+bind+alt+bind+arrowright' }
  }
}

export function normalizeKeyBindingPreset(value: string | undefined): KeyBindingPresetID {
  return value === 'postman' ? 'postman' : 'default'
}

/**
 * effectiveKeyBindings layers preset over defaults.
 *
 * User overrides are applied by the caller ON TOP of this, which is the
 * ordering that matters: a shortcut someone deliberately set must not be
 * silently replaced by switching preset.
 */
export function effectiveKeyBindings(
  sections: KeyBindingSection[],
  preset: KeyBindingPreset
): Record<string, KeyBindingDefinition> {
  const out: Record<string, KeyBindingDefinition> = {}
  for (const section of sections) {
    for (const [action, definition] of Object.entries(section.bindings)) {
      const override = preset[action]
      out[action] = override ? { ...definition, ...override } : { ...definition }
    }
  }
  return out
}

export const keyBindingSeparator = '+bind+'

const keyBindingModifiers = ['ctrl', 'command', 'alt', 'shift']

export function keyBindingParts(value: string): string[] {
  return value.split(keyBindingSeparator).map((part) => part.trim()).filter(Boolean)
}

export function isKeyBindingModifier(value: string): boolean {
  return keyBindingModifiers.includes(value)
}

/**
 * keyBindingSignature orders modifiers so equivalent combos compare equal.
 *
 * Lowercased first. Every built-in combo is already lowercase and the capture
 * handler lowercases what the user presses, so today this changes nothing — but
 * a signature that is case-sensitive means "Ctrl+K" and "ctrl+k" are different
 * shortcuts, and the collision check would wave through a duplicate.
 *
 * There used to be a SECOND copy of this in App.svelte that lowercased where
 * this one did not, and did not sort the non-modifier keys. The two agreed only
 * because every input reaching them happened to be lowercase and single-keyed.
 * One implementation now, so the settings validator and the preset collision
 * check cannot disagree about what counts as the same shortcut.
 */
export function keyBindingSignature(value: string): string {
  const parts = keyBindingParts(value.toLowerCase())
  const modifiers = parts.filter(isKeyBindingModifier).sort()
  const keys = parts.filter((part) => !isKeyBindingModifier(part)).sort()
  return [...modifiers, ...keys].join(keyBindingSeparator)
}

/**
 * findKeyBindingCollisions returns every pair of actions sharing a combo.
 *
 * Hidden and display-only entries are skipped: the tab-number row is declared
 * once as a hidden range (Cmd+1 - Cmd+8) AND as eight individual actions, so
 * counting both would report a collision that does not exist.
 */
export function findKeyBindingCollisions(
  bindings: Record<string, KeyBindingDefinition>,
  os: KeyBindingOS
): [string, string][] {
  const seen = new Map<string, string>()
  const collisions: [string, string][] = []

  for (const [action, definition] of Object.entries(bindings)) {
    if (definition.hidden) continue
    const combo = definition[os]
    if (!combo) continue
    const signature = keyBindingSignature(combo)
    const existing = seen.get(signature)
    if (existing) collisions.push([existing, action])
    else seen.set(signature, action)
  }
  return collisions
}

/**
 * validateKeyBinding reports why a combo cannot be assigned, or "" if it can.
 *
 * The caller supplies the RESOLVED bindings (what effectiveKeyBindings returns,
 * merged with the user's overrides) rather than this reading them, because the
 * merge depends on app state that has no business in this module.
 *
 * Note the deliberate difference from findKeyBindingCollisions: that one SKIPS
 * hidden definitions, because the tab-number row is declared both as a hidden
 * range and as eight individual actions and counting both would report a
 * collision that does not exist. This one does NOT skip them, because a hidden
 * binding still occupies its combo — telling a user that Cmd+1 is free when
 * switchToTab1 owns it would hand them a shortcut that silently never fires.
 */
export function validateKeyBinding(
  action: string,
  combo: string,
  bindings: Record<string, KeyBindingDefinition>,
  os: KeyBindingOS
): string {
  const parts = keyBindingParts(combo)
  const nonModifiers = parts.filter((part) => !isKeyBindingModifier(part))
  if (parts.length < 2 || parts.length > 4 || nonModifiers.length !== 1) {
    return 'Use one key plus at least one modifier.'
  }
  if (!parts.some(isKeyBindingModifier)) {
    return 'Use at least one modifier.'
  }
  const signature = keyBindingSignature(combo)
  for (const [otherAction, definition] of Object.entries(bindings)) {
    if (otherAction === action) continue
    const other = definition[os]
    if (other && keyBindingSignature(other) === signature) {
      return 'This shortcut is already in use.'
    }
  }
  return ''
}

/**
 * normalizeEventKey turns a KeyboardEvent into the vocabulary a combo uses.
 *
 * The named keys map to the short forms the binding table stores — "esc" not
 * "Escape", "command" not "Meta" — so a captured shortcut can be compared
 * against a stored one without a second translation step.
 *
 * LETTERS AND DIGITS COME FROM event.code, NOT event.key, and that is the
 * important part. event.key is the character the layout PRODUCES; event.code is
 * the physical key. On a French AZERTY keyboard the key where QWERTY has Q
 * reports event.key "a", so a shortcut stored as command+q would fire on what
 * the user sees as A, and command+a would not fire at all. Reading the code
 * means Cmd+Q is the same physical chord everywhere, which is what a keyboard
 * shortcut is.
 *
 * The event.key fallbacks below still matter: event.code is empty for synthetic
 * events and for keys that produce no code, and a shortcut that silently stops
 * working under a test harness is worse than one that reads the character.
 */
export function normalizeEventKey(event: Pick<KeyboardEvent, 'key' | 'code'>): string {
  if (event.key === ' ') return 'space'
  if (event.key === 'Escape') return 'esc'
  if (event.key === 'Enter') return 'enter'
  if (event.key === 'Backspace') return 'backspace'
  if (event.key === 'Tab') return 'tab'
  if (event.key === 'Delete') return 'delete'
  if (event.key === 'Control') return 'ctrl'
  if (event.key === 'Meta') return 'command'
  if (event.key === 'Alt') return 'alt'
  if (event.key === 'Shift') return 'shift'
  if (event.code?.startsWith('Key')) return event.code.slice(3).toLowerCase()
  if (event.code?.startsWith('Digit')) return event.code.slice(5)
  // No separate single-character branch: lower-casing the key is the same
  // answer for "k" as for "ArrowDown", and a control proved removing it changed
  // no test. One fallback rather than two that agree.
  return event.key.toLowerCase()
}

/**
 * The platform whose column of the binding table applies.
 *
 * Everything that is not a Mac reads the windows column, Linux included. The
 * table has two columns and the meaningful split is Command vs Ctrl, so a third
 * case would only duplicate one of them.
 */
export function currentKeyBindingOS(): KeyBindingOS {
  if (typeof navigator !== 'undefined' && navigator.platform.toLowerCase().includes('mac')) return 'mac'
  return 'windows'
}

/**
 * Whether custom keybindings are on.
 *
 * Compared against false rather than read as truthy, so a preferences file
 * written before the flag existed keeps its shortcuts working. Only an explicit
 * false turns them off.
 */
export function keybindingsAreEnabled(keybindingsEnabled: boolean | undefined): boolean {
  return keybindingsEnabled !== false
}

/**
 * Merges a preset binding with the user's override for one action.
 *
 * The order is the point (US-057): defaults, then preset, then override. A
 * shortcut somebody deliberately set must not be silently replaced by switching
 * preset — which is exactly what merging the other way round would do.
 *
 * `name` is restored from the base when the override carries an empty one,
 * because an override that only changes a key combo has no name to contribute
 * and an unnamed row renders as a blank line in the shortcuts sheet.
 */
export function mergeKeyBinding(
  base: KeyBindingDefinition | undefined,
  override: Partial<KeyBindingDefinition> | undefined
): KeyBindingDefinition | undefined {
  if (!base) return undefined
  return {
    ...base,
    ...(override ?? {}),
    name: override?.name || base.name
  }
}

/** The combo stored for one platform, or "" when the action has none there. */
export function keyBindingValueFor(
  binding: KeyBindingDefinition | undefined,
  os: KeyBindingOS
): string {
  return (binding?.[os] as string | undefined) || ''
}

/**
 * The combo to SHOW, which is not always the combo that fires.
 *
 * A binding may carry a `displayValue` that reads more naturally than the one
 * the matcher uses — and falling back to the real value keeps the sheet honest
 * when it does not.
 */
export function keyBindingDisplayValueFor(
  binding: KeyBindingDefinition | undefined,
  os: KeyBindingOS
): string {
  return binding?.displayValue?.[os] || keyBindingValueFor(binding, os)
}

/**
 * The combo string for a keydown.
 *
 * Modifiers are emitted in a FIXED order regardless of which the user pressed
 * first, because the stored bindings are written in that order and the two are
 * compared as strings via `keyBindingSignature`.
 *
 * A keydown that is only a modifier produces no key part, so holding Shift
 * alone does not match a binding whose combo is "shift".
 */
export function keyBindingComboFromEvent(
  event: Pick<KeyboardEvent, 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey' | 'key' | 'code'>
): string {
  const parts: string[] = []
  if (event.ctrlKey) parts.push('ctrl')
  if (event.metaKey) parts.push('command')
  if (event.altKey) parts.push('alt')
  if (event.shiftKey) parts.push('shift')
  const key = normalizeEventKey(event)
  if (key && !isKeyBindingModifier(key)) parts.push(key)
  return parts.join(keyBindingSeparator)
}

const MAC_TOKEN_LABELS: Record<string, string> = {
  command: 'Cmd', ctrl: 'Ctrl', alt: 'Opt', shift: 'Shift',
  enter: 'Enter', esc: 'Esc', space: 'Space',
  arrowup: 'Up', arrowdown: 'Down', arrowleft: 'Left', arrowright: 'Right'
}

const WINDOWS_TOKEN_LABELS: Record<string, string> = {
  ...MAC_TOKEN_LABELS,
  // The same physical key, and the only two tokens that differ: on Windows the
  // Command position is the Windows key, and Option is Alt.
  command: 'Win',
  alt: 'Alt'
}

/** The human label for one token of a combo. */
export function formatKeyBindingToken(token: string, os: KeyBindingOS): string {
  const labels = os === 'mac' ? MAC_TOKEN_LABELS : WINDOWS_TOKEN_LABELS
  return labels[token] || token.toUpperCase()
}

/**
 * Renders a stored combo for display.
 *
 * The " - " split handles CHORDS — two combos pressed in sequence — and is
 * applied before the parts split so a chord renders as "Cmd + K - Cmd + S"
 * rather than collapsing into one impossible combination.
 */
export function formatKeyBinding(value: string, os: KeyBindingOS): string {
  if (!value) return ''
  return value
    .split(/\s+-\s+/)
    .map((part) => keyBindingParts(part).map((token) => formatKeyBindingToken(token, os)).join(' + '))
    .join(' - ')
}

/** The rows of a section that the shortcuts sheet lists. */
export function visibleKeyBindingEntries(
  section: KeyBindingSection
): [string, KeyBindingDefinition][] {
  return Object.entries(section.bindings).filter(([, binding]) => !binding.hidden)
}

/** Whether an action's combo may be edited. */
export function keyBindingCanEdit(binding: KeyBindingDefinition | undefined): boolean {
  return Boolean(binding && !binding.readOnly)
}

/** Every action's default definition, flattened across the sections. */
export function keyBindingDefaultsByAction(): Record<string, KeyBindingDefinition> {
  const defaults: Record<string, KeyBindingDefinition> = {}
  for (const section of keyBindingSections) {
    for (const [action, binding] of Object.entries(section.bindings)) {
      defaults[action] = binding
    }
  }
  return defaults
}
