export type WorkbenchCommandID =
  | 'new-request'
  | 'new-http'
  | 'new-graphql'
  | 'new-grpc'
  | 'new-websocket'
  | 'new-folder'
  | 'new-collection'
  | 'send-request'
  | 'save-request'
  | 'open-workspace'
  | 'workspace-search'
  | 'command-palette'
  | 'toggle-sidebar'
  | 'open-request'
  | 'open-collection-settings'
	| 'open-git-workbench'
  | 'open-environments'
  | 'import'
  | 'open-network'
  | 'open-cookies'
  | 'open-history'
  | 'toggle-devtools'
  | 'open-capabilities'
  | 'open-runner'
  | 'open-preferences'
  | 'open-keyboard-shortcuts'
  | 'change-orientation'
  | 'open-notifications'
  | 'cancel-run'

export type WorkbenchCommandItem = {
  id: WorkbenchCommandID
  label: string
  group: string
  shortcut?: string
  disabled?: boolean
  disabledReason?: string
  tone?: 'default' | 'danger'
  testId?: string
}

export type CommandOption = { id: string; name: string }

export const commandPaletteCommandIDs: WorkbenchCommandID[] = [
  'new-request',
  'send-request',
  'save-request',
  'workspace-search',
  'open-collection-settings',
	'open-git-workbench',
  'open-environments',
  'open-cookies',
  'open-history',
  'open-runner',
  'change-orientation',
  'toggle-sidebar',
  'open-network',
  'toggle-devtools',
  'open-preferences'
]

const commandMetadata: Record<WorkbenchCommandID, { label: string; shortcut?: string }> = {
  'new-request': { label: 'New request', shortcut: '⌘N' },
  'new-http': { label: 'HTTP request' },
  'new-graphql': { label: 'GraphQL request' },
  'new-grpc': { label: 'gRPC request' },
  'new-websocket': { label: 'WebSocket request' },
  'new-folder': { label: 'Folder' },
  'new-collection': { label: 'Collection' },
  'send-request': { label: 'Send active request', shortcut: '⌘↵' },
  'save-request': { label: 'Save active request', shortcut: '⌘S' },
  'open-workspace': { label: 'Open workspace in new window' },
  'workspace-search': { label: 'Search workspace', shortcut: '⌘K' },
  'command-palette': { label: 'Command palette', shortcut: '⌘⇧P' },
  'toggle-sidebar': { label: 'Toggle collection sidebar', shortcut: '⌘\\' },
  'open-request': { label: 'Open active request' },
  'open-collection-settings': { label: 'Collection settings' },
	'open-git-workbench': { label: 'Git workbench' },
  'open-environments': { label: 'Manage environments', shortcut: '⌘E' },
  import: { label: 'Import collection', shortcut: '⌘O' },
  'open-network': { label: 'Network log' },
  'open-cookies': { label: 'Cookie jar' },
  'open-history': { label: 'Send history' },
  'toggle-devtools': { label: 'Toggle Dev Tools', shortcut: '⌘⌥I' },
  'open-capabilities': { label: 'Capabilities' },
  'open-runner': { label: 'Collection runner' },
  'open-preferences': { label: 'Preferences', shortcut: '⌘,' },
  'open-keyboard-shortcuts': { label: 'Keyboard shortcuts' },
  'change-orientation': { label: 'Change response layout', shortcut: '⌘J' },
  'open-notifications': { label: 'Notifications' },
  'cancel-run': { label: 'Cancel collection run', shortcut: 'Esc' }
}

export function workbenchCommandMetadata(id: WorkbenchCommandID) {
  return commandMetadata[id]
}

/**
 * What the shell's chrome may offer right now.
 *
 * Structural on purpose: `workspaceStore` already exposes both getters, so a
 * caller can pass the store itself and a caller that only has the two flags can
 * pass an object literal.
 */
export type WorkbenchMenuScope = {
  canCreateRequest: boolean
  canCreateFolder: boolean
}

/**
 * The New menu, and the main overflow menu.
 *
 * D1 moves the New menu out of the command bar and into the sidebar header,
 * which is the first time two surfaces open the same list. Built inline in a
 * component, the second one is a copy — and a copy is how the sidebar's New
 * menu ends up offering WebSocket six months after the top bar stopped.
 */
export function newItems(scope: WorkbenchMenuScope): WorkbenchCommandItem[] {
  const requestReason = 'Open a collection first'
  return [
    { id: 'new-http', label: 'HTTP', group: 'Request', disabled: !scope.canCreateRequest, disabledReason: requestReason },
    { id: 'new-graphql', label: 'GraphQL', group: 'Request', disabled: !scope.canCreateRequest, disabledReason: requestReason },
    { id: 'new-grpc', label: 'gRPC', group: 'Request', disabled: !scope.canCreateRequest, disabledReason: requestReason },
    { id: 'new-websocket', label: 'WebSocket', group: 'Request', disabled: !scope.canCreateRequest, disabledReason: requestReason },
    { id: 'new-folder', label: 'Folder', group: 'Organize', disabled: !scope.canCreateFolder, disabledReason: 'Open a local collection first' },
    { id: 'new-collection', label: 'Collection', group: 'Organize' },
    { id: 'import', label: 'Import collection…', group: 'Workspace', shortcut: '⌘O' },
    { id: 'open-workspace', label: 'Open workspace in new window…', group: 'Workspace' }
  ]
}

/**
 * D3 takes the Cookies and Run buttons off the bar, so both land here — the
 * menu is where a capability goes when it stops earning a row, and neither is
 * allowed to simply disappear.
 */
export function moreItems(scope: WorkbenchMenuScope): WorkbenchCommandItem[] {
  return [
    { id: 'workspace-search', label: 'Search workspace', group: 'Workspace', shortcut: '⌘K' },
    { id: 'open-environments', label: 'Manage environments', group: 'Workspace', shortcut: '⌘E' },
    { id: 'open-collection-settings', label: 'Collection settings', group: 'Workspace', disabled: !scope.canCreateRequest },
    { id: 'open-runner', label: 'Collection runner', group: 'Tools' },
    { id: 'open-cookies', label: 'Cookie jar', group: 'Tools' },
    { id: 'open-network', label: 'Network log', group: 'Tools' },
    { id: 'toggle-devtools', label: 'Dev Tools', group: 'Tools', shortcut: '⌘⌥I' },
    { id: 'open-capabilities', label: 'Capabilities', group: 'App' },
    { id: 'import', label: 'Import', group: 'App', shortcut: '⌘O' },
    { id: 'open-keyboard-shortcuts', label: 'Keyboard shortcuts', group: 'App' },
    { id: 'open-preferences', label: 'Preferences', group: 'App', shortcut: '⌘,' }
  ]
}
