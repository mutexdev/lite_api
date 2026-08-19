<script lang="ts">
  import { workspaceStore } from '../stores/workspaceStore.svelte'
  import type { Snippet } from 'svelte'
  import CommandOverflowMenu from './CommandOverflowMenu.svelte'
  import EnvironmentContextMenu from './EnvironmentContextMenu.svelte'
  import type { WorkbenchCommandID, WorkbenchCommandItem } from './workbenchCommands'

  // US-026 — fourteen data props collapsed to store reads.
  //
  // What remains as props is what the store does not own: the shell's own view
  // state (sidebarCollapsed, activeView), values derived from surfaces outside
  // the workspace (notificationCount, the run indicators) and the callbacks.
  // Putting those in the store too would move App.svelte's shell state into a
  // module named for the workspace, which is a worse home for it.
  type Props = {
    sidebarCollapsed?: boolean
    activeView?: string
    notificationCount?: number
    runningCollectionName?: string
    cancellingRun?: boolean
    onCommand: (id: WorkbenchCommandID, invoker: HTMLElement | null) => void | Promise<void>
    onWorkspaceChange: (id: string) => void | Promise<void>
    onGlobalEnvironmentChange: (id: string) => void | Promise<void>
    onEnvironmentChange: (id: string) => void | Promise<void>
    // Replaces <slot name="recovery">, deprecated in runes mode.
    recovery?: Snippet
  }

  let {
    sidebarCollapsed = false,
    activeView = 'request',
    notificationCount = 0,
    runningCollectionName = '',
    cancellingRun = false,
    onCommand,
    onWorkspaceChange,
    onGlobalEnvironmentChange,
    onEnvironmentChange,
    recovery
  }: Props = $props()

  // Read straight from the store rather than received as props. Each of these
  // was previously computed inline at the call site in App.svelte and passed
  // down, which meant the same expression existed there and the prop existed
  // here — two places to keep in step for one value.
  const workspaceName = $derived(workspaceStore.activeWorkspace?.name ?? 'Workspace')
  const workspaceOptions = $derived(workspaceStore.workspaceOptions)
  const workspaceValue = $derived(workspaceStore.activeWorkspaceId)
  const collectionName = $derived(workspaceStore.activeCollection?.name ?? 'No collection')
  const requestName = $derived(workspaceStore.activeRequest?.name ?? 'No request')
  const globalEnvironmentOptions = $derived(workspaceStore.globalEnvironmentOptions)
  const environmentOptions = $derived(workspaceStore.environmentOptions)
  const globalEnvironmentValue = $derived(workspaceStore.activeWorkspace?.activeGlobalEnvironmentId ?? '')
  const environmentValue = $derived(workspaceStore.selectedEnvironmentId)
  const globalEnvironmentName = $derived(workspaceStore.activeGlobalEnvironment?.name ?? 'none')
  const environmentName = $derived(workspaceStore.environmentName)
  const gitConnected = $derived(Boolean(workspaceStore.activeCollection?.remote))
  const canCreateRequest = $derived(workspaceStore.canCreateRequest)
  const canCreateFolder = $derived(workspaceStore.canCreateFolder)

  const newItems = $derived([
    { id: 'new-http', label: 'HTTP', group: 'Request', disabled: !canCreateRequest, disabledReason: 'Open a collection first' },
    { id: 'new-graphql', label: 'GraphQL', group: 'Request', disabled: !canCreateRequest, disabledReason: 'Open a collection first' },
    { id: 'new-grpc', label: 'gRPC', group: 'Request', disabled: !canCreateRequest, disabledReason: 'Open a collection first' },
    { id: 'new-websocket', label: 'WebSocket', group: 'Request', disabled: !canCreateRequest, disabledReason: 'Open a collection first' },
    { id: 'new-folder', label: 'Folder', group: 'Organize', disabled: !canCreateFolder, disabledReason: 'Open a local collection first' },
    { id: 'new-collection', label: 'Collection', group: 'Organize' },
    { id: 'import', label: 'Import collection…', group: 'Workspace', shortcut: '⌘O' },
    { id: 'open-workspace', label: 'Open workspace in new window…', group: 'Workspace' }
  ] as WorkbenchCommandItem[])

  const moreItems = $derived([
    { id: 'workspace-search', label: 'Search workspace', group: 'Workspace', shortcut: '⌘K' },
    { id: 'open-environments', label: 'Manage environments', group: 'Workspace', shortcut: '⌘E' },
    { id: 'open-collection-settings', label: 'Collection settings', group: 'Workspace', disabled: !canCreateRequest },
    { id: 'open-network', label: 'Network log', group: 'Tools' },
    { id: 'toggle-devtools', label: 'Dev Tools', group: 'Tools', shortcut: '⌘⌥I' },
    { id: 'open-capabilities', label: 'Capabilities', group: 'App' },
    { id: 'import', label: 'Import', group: 'App', shortcut: '⌘O' },
    { id: 'open-keyboard-shortcuts', label: 'Keyboard shortcuts', group: 'App' },
    { id: 'open-preferences', label: 'Preferences', group: 'App', shortcut: '⌘,' }
  ] as WorkbenchCommandItem[])
</script>

<div class="workspace-command-bar" role="toolbar" aria-label="Workspace command bar">
  <div class="command-leading">
    <button
      class="command-icon"
      type="button"
      aria-label={sidebarCollapsed ? 'Show sidebar' : 'Hide sidebar'}
      title={sidebarCollapsed ? 'Show sidebar (⌘\\)' : 'Hide sidebar (⌘\\)'}
      data-testid="toggle-sidebar-button"
      onclick={(event) => void onCommand('toggle-sidebar', event.currentTarget)}
    >
      <svg viewBox="0 0 20 20" aria-hidden="true"><rect x="2.5" y="3" width="15" height="14" rx="2" /><path d="M7 3v14" />{#if sidebarCollapsed}<path d="m10 7 3 3-3 3" />{:else}<path d="m13 7-3 3 3 3" />{/if}</svg>
    </button>

    <CommandOverflowMenu label="Add resource" icon="add" align="left" items={newItems} onSelect={onCommand} testId="command-new-menu" />

    {#if workspaceOptions.length > 1}
      <select class="workspace-select" aria-label="Workspace" title="Workspace" value={workspaceValue} onchange={(event) => void onWorkspaceChange(event.currentTarget.value)}>
        {#each workspaceOptions as option (option.id)}<option value={option.id}>{option.name}</option>{/each}
      </select>
    {:else}
      <button class="workspace-button" type="button" aria-label={`Open another workspace in a new window. Current workspace: ${workspaceName}`} title="Open workspace in a new window" onclick={(event) => void onCommand('open-workspace', event.currentTarget)}>
        <span>{workspaceName}</span><span aria-hidden="true">↗</span>
      </button>
    {/if}

    <EnvironmentContextMenu
      globalOptions={globalEnvironmentOptions}
      environmentOptions={environmentOptions}
      globalValue={globalEnvironmentValue}
      environmentValue={environmentValue}
      globalName={globalEnvironmentName}
      environmentName={environmentName}
      onGlobalChange={onGlobalEnvironmentChange}
      onEnvironmentChange={onEnvironmentChange}
      onManage={() => onCommand('open-environments', null)}
    />

    <button class="command-icon cookie-button" type="button" aria-label="Open cookie jar" title="Cookie jar" onclick={(event) => void onCommand('open-cookies', event.currentTarget)}>
      <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M16.7 10.2A7 7 0 1 1 9.8 3.3a3.2 3.2 0 0 0 3.5 3.5 3.2 3.2 0 0 0 3.4 3.4Z" /><circle cx="7" cy="8" r=".8" /><circle cx="9.5" cy="13" r=".8" /><circle cx="5.8" cy="12.5" r=".8" /></svg>
      <span>Cookies</span>
    </button>
  </div>

  <div class="command-context" aria-label="Active context">
    <button type="button" class:active={activeView === 'collection'} title={`Open collection settings for ${collectionName}`} onclick={(event) => void onCommand('open-collection-settings', event.currentTarget)}>{collectionName}</button>
    <span aria-hidden="true">/</span>
    <button type="button" class:active={activeView === 'request'} title={`Open request ${requestName}`} onclick={(event) => void onCommand('open-request', event.currentTarget)}>{requestName}</button>
  </div>

  <div class="command-trailing">
    <button
      class:running={Boolean(runningCollectionName)}
      class="command-icon run-button"
      type="button"
      aria-label={runningCollectionName ? (cancellingRun ? `Cancelling collection run: ${runningCollectionName}` : `Cancel collection run: ${runningCollectionName}`) : 'Open collection runner'}
      title={runningCollectionName ? (cancellingRun ? 'Cancelling run…' : `Cancel ${runningCollectionName}`) : 'Collection runner'}
      disabled={cancellingRun}
      onclick={(event) => void onCommand(runningCollectionName ? 'cancel-run' : 'open-runner', event.currentTarget)}
    >
      {#if runningCollectionName}
        <svg viewBox="0 0 20 20" aria-hidden="true"><rect x="5" y="5" width="10" height="10" rx="1.5" /></svg><span>{cancellingRun ? 'Cancelling' : 'Running'}</span>
      {:else}
        <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m6 4 10 6-10 6z" /></svg><span>Run</span>
      {/if}
    </button>

    <button class:connected={gitConnected} class="command-status git-status" type="button" aria-label={gitConnected ? `Git connected for ${collectionName}` : 'Local collection. Open collection settings'} title={gitConnected ? 'Git connected' : 'Local collection'} onclick={(event) => void onCommand('open-collection-settings', event.currentTarget)}>
      <span class="status-dot" aria-hidden="true"></span><span>{gitConnected ? 'Git' : 'Local'}</span>
    </button>

    {@render recovery?.()}

    <button class="command-icon notification-button" type="button" aria-label={`Notifications${notificationCount ? `, ${notificationCount} unread` : ''}`} title="Notifications" onclick={(event) => void onCommand('open-notifications', event.currentTarget)}>
      <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4.5 14h11l-1.3-1.8V8a4.2 4.2 0 0 0-8.4 0v4.2zM8 16h4" /></svg>
      {#if notificationCount > 0}<strong>{notificationCount}</strong>{/if}
    </button>

    <button class="command-icon" type="button" aria-label="Change response orientation" title="Change response orientation (⌘J)" data-testid="command-layout-button" onclick={(event) => void onCommand('change-orientation', event.currentTarget)}>
      <svg viewBox="0 0 20 20" aria-hidden="true"><rect x="2.5" y="3" width="15" height="14" rx="2" /><path d="M10 3v14" /></svg>
    </button>

    <button class="command-icon command-palette-button" type="button" aria-label="Open command palette" title="Command palette (⌘⇧P)" onclick={(event) => void onCommand('command-palette', event.currentTarget)}>
      <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5" /><path d="m12.2 12.2 4 4" /></svg><span>Commands</span>
    </button>

    <CommandOverflowMenu label="Main menu" icon="more" align="right" items={moreItems} onSelect={onCommand} testId="command-main-menu" />
  </div>
</div>

<style>
  .workspace-command-bar {
    display: grid;
    grid-template-columns: minmax(0, auto) minmax(80px, 1fr) minmax(0, auto);
    align-items: center;
    gap: 8px;
    min-height: 42px;
    padding: 5px 8px;
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface);
  }
  .command-leading,
  .command-trailing { display: flex; align-items: center; gap: 3px; min-width: 0; }
  .command-trailing { justify-content: flex-end; }
  button, select { min-height: 30px; }
  .command-icon,
  .workspace-button,
  .command-status,
  .command-context button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    padding: 4px 7px;
    border-color: transparent;
    background: transparent;
  }
  .command-icon:hover,
  .workspace-button:hover,
  .command-status:hover,
  .command-context button:hover { border-color: var(--border); background: var(--surface-soft); }
  .command-icon { min-width: 30px; }
  .command-icon svg { width: 16px; height: 16px; flex: 0 0 auto; fill: none; stroke: currentColor; stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
  .workspace-button { min-width: 0; max-width: 150px; }
  .workspace-button span:first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .workspace-button span:last-child { color: var(--accent-strong); }
  .workspace-select { width: min(150px, 16vw); }
  .command-context { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 0; color: var(--muted); }
  .command-context button { min-width: 0; max-width: 42%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted); }
  .command-context button:last-child { max-width: 58%; }
  .command-context button.active { color: var(--text); font-weight: 700; }
  .command-status { color: var(--muted); font-size: 11px; }
  .status-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--muted-weak); }
  .git-status.connected .status-dot { background: var(--method-get, var(--accent)); box-shadow: 0 0 0 3px color-mix(in srgb, var(--method-get, var(--accent)) 18%, transparent); }
  .run-button.running { color: var(--warning-text); }
  .notification-button { position: relative; }
  .notification-button strong { position: absolute; top: 0; right: 0; min-width: 14px; padding: 1px 3px; border-radius: 999px; background: var(--danger-strong); color: var(--on-dark); font-size: 9px; line-height: 12px; }
  @media (max-width: 1180px) {
    .workspace-command-bar { grid-template-columns: minmax(0, auto) minmax(36px, 1fr) minmax(0, auto); }
    .cookie-button span, .run-button span, .git-status span:last-child, .command-palette-button span { display: none; }
    .workspace-select, .workspace-button { max-width: 120px; }
  }
  @media (max-width: 800px) {
    .workspace-command-bar { grid-template-columns: minmax(0, 1fr) auto; }
    .command-context { display: none; }
    .workspace-select, .workspace-button { max-width: 105px; }
    .git-status { display: none; }
  }
  @media (max-width: 610px) {
    .workspace-select, .workspace-button, .cookie-button { display: none; }
  }
</style>
