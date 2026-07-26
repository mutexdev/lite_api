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
