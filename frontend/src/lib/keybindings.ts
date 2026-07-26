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
