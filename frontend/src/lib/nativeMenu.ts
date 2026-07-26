// Mapping a native menu click to something the app can do.
//
// The native menu is built in Go (native_menu.go) and its items reach the
// frontend as bare command strings over an event. Nothing type-checks across
// that boundary: a menu item whose string this file does not handle is still
// drawn, still enabled, and still clickable — it just does nothing, with no
// error anywhere. That silence is the whole reason this mapping is separated
// out and tested against the Go source.

import type { WorkbenchCommandID } from './workbench/workbenchCommands'

/** Menu commands that are not workbench commands and have their own handler. */
export type NativeMenuDirectAction =
  | 'open-native-new-window'
  | 'save-request'
  | 'save-all-tabs'
  | 'close-active-tab'
  | 'reopen-last-closed-tab'
  | 'cancel-active-request'
  | 'send-request'
  | 'run-collection'

export type NativeMenuResolution =
  | { kind: 'workbench'; command: WorkbenchCommandID }
  | { kind: 'direct'; action: NativeMenuDirectAction }

/**
 * Menu commands that forward straight to a workbench command.
 *
 * Most names match on both sides, but three do not, and those are the entries
 * that matter: `open-git` is `open-git-workbench` in the workbench,
 * `open-workspace-in-new-window` is `open-workspace`, and BOTH `import` and
 * `open-collection` reach the single `import` command. A rename on either side
 * of the boundary breaks only the mismatched ones, and only at runtime.
 */
const WORKBENCH_COMMANDS: Record<string, WorkbenchCommandID> = {
  'open-workspace-in-new-window': 'open-workspace',
  'new-request': 'new-request',
  import: 'import',
  'open-collection': 'import',
  'command-palette': 'command-palette',
  'workspace-search': 'workspace-search',
  'toggle-sidebar': 'toggle-sidebar',
  'toggle-devtools': 'toggle-devtools',
  'change-orientation': 'change-orientation',
  'open-runner': 'open-runner',
  'new-collection': 'new-collection',
  'open-environments': 'open-environments',
  'open-git': 'open-git-workbench',
  'open-preferences': 'open-preferences',
  'open-network': 'open-network',
  'open-cookies': 'open-cookies',
  'open-history': 'open-history',
  'open-capabilities': 'open-capabilities',
  'open-keyboard-shortcuts': 'open-keyboard-shortcuts',
}

/** Menu commands handled directly, with no workbench command behind them. */
const DIRECT_ACTIONS: Record<string, NativeMenuDirectAction> = {
  'new-window': 'open-native-new-window',
  save: 'save-request',
  'save-all': 'save-all-tabs',
  'close-tab': 'close-active-tab',
  'reopen-tab': 'reopen-last-closed-tab',
  'cancel-active': 'cancel-active-request',
}

/** Every menu command string this file handles. */
export function handledNativeMenuCommands(): string[] {
  return [...Object.keys(WORKBENCH_COMMANDS), ...Object.keys(DIRECT_ACTIONS), 'send-or-start'].sort()
}

export interface NativeMenuContext {
  /** Which screen is showing, so the one context-sensitive item can branch. */
  activeView: string
}

/**
 * Resolves a native menu command, or undefined when nothing handles it.
 *
 * `send-or-start` is the only command whose meaning depends on the screen: it
 * backs a single menu item that reads "Send" over a request and "Run" over the
 * collection runner. Two items would be the alternative, and one of them would
 * always be greyed out.
 */
export function resolveNativeMenuCommand(
  command: string,
  context: NativeMenuContext,
): NativeMenuResolution | undefined {
  if (command === 'send-or-start') {
    return {
      kind: 'direct',
      action: context.activeView === 'runner' ? 'run-collection' : 'send-request',
    }
  }
  const direct = DIRECT_ACTIONS[command]
  if (direct) return { kind: 'direct', action: direct }
  const workbench = WORKBENCH_COMMANDS[command]
  if (workbench) return { kind: 'workbench', command: workbench }
  return undefined
}
