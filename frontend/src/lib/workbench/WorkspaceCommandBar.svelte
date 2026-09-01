<script lang="ts">
  import { workspaceStore } from '../stores/workspaceStore.svelte'
  import type { Snippet } from 'svelte'
  import Icon from '../ui/Icon.svelte'
  import IconButton from '../ui/IconButton.svelte'
  import CommandOverflowMenu from './CommandOverflowMenu.svelte'
  import EnvironmentContextMenu from './EnvironmentContextMenu.svelte'
  import { moreItems, type WorkbenchCommandID } from './workbenchCommands'

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

  // D3 — the menu is built in workbenchCommands.ts now, because the sidebar
  // header opens the sibling New menu from the same module and two inline
  // copies of one list drift.
  const mainMenuItems = $derived(
    moreItems({
      canCreateRequest: workspaceStore.canCreateRequest,
      canCreateFolder: workspaceStore.canCreateFolder,
    }),
  )

  // D3 — the collection segment carries what the removed Local/Git button said.
  // Only the connected state gets a dot: "Local" is the default, and a marker
  // every collection wears tells the user nothing.
  const collectionTitle = $derived(
    `${gitConnected ? 'Git connected' : 'Local collection'} · Open collection settings for ${collectionName}`,
  )
</script>

<div class="workspace-command-bar" role="toolbar" aria-label="Workspace command bar">
  <div class="command-leading">
    <!--
      Not `pressed`: this toggle's two states are "sidebar showing" and "sidebar
      hidden", and showing is the default, so a pressed style would paint an
      accent-filled button into the corner of every default screen. The label
      already flips, which is what a screen reader and the tooltip read.
    -->
    <IconButton
      icon="sidebar"
      label={sidebarCollapsed ? 'Show sidebar (⌘\\)' : 'Hide sidebar (⌘\\)'}
      testId="toggle-sidebar-button"
      onclick={(event) => void onCommand('toggle-sidebar', event.currentTarget as HTMLElement)}
    />

    {#if workspaceOptions.length > 1}
      <select class="workspace-select" aria-label="Workspace" title="Workspace" value={workspaceValue} onchange={(event) => void onWorkspaceChange(event.currentTarget.value)}>
        {#each workspaceOptions as option (option.id)}<option value={option.id}>{option.name}</option>{/each}
      </select>
    {:else}
      <button class="workspace-button" type="button" aria-label={`Open another workspace in a new window. Current workspace: ${workspaceName}`} title="Open workspace in a new window" onclick={(event) => void onCommand('open-workspace', event.currentTarget)}>
        <span>{workspaceName}</span><Icon name="external" size={13} />
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
  </div>

  <div class="command-context" aria-label="Active context">
    <button type="button" class:active={activeView === 'collection'} aria-label={collectionTitle} title={collectionTitle} onclick={(event) => void onCommand('open-collection-settings', event.currentTarget)}>
      {#if gitConnected}<span class="git-dot" aria-hidden="true"></span>{/if}
      <span class="crumb-label">{collectionName}</span>
    </button>
    <span aria-hidden="true">/</span>
    <button type="button" class:active={activeView === 'request'} title={`Open request ${requestName}`} onclick={(event) => void onCommand('open-request', event.currentTarget)}><span class="crumb-label">{requestName}</span></button>
  </div>

  <div class="command-trailing">
    <!--
      The one control on this bar that keeps a word. The M3 QA contract wants a
      named cancel in the global toolbar while a run is in flight, and an icon
      alone cannot say "Cancelling" — so it renders only while there is a run,
      and nothing at rest.
    -->
    {#if runningCollectionName}
      <button
        class="command-running"
        type="button"
        aria-label={cancellingRun ? `Cancelling collection run: ${runningCollectionName}` : `Cancel collection run: ${runningCollectionName}`}
        title={cancellingRun ? 'Cancelling run…' : `Cancel ${runningCollectionName}`}
        disabled={cancellingRun}
        onclick={(event) => void onCommand('cancel-run', event.currentTarget)}
      >
        <Icon name="stop" size={14} />
        <span>{cancellingRun ? 'Cancelling' : 'Running'}</span>
      </button>
    {/if}

    <IconButton
      icon="search"
      label="Search workspace (⌘K)"
      testId="command-search-button"
      onclick={() => void onCommand('workspace-search', null)}
    />

    <!--
      D3 — this button wore the magnifier while the search command had no button
      at all, so the one icon every app agrees on pointed at the wrong modal.
    -->
    <IconButton
      icon="command"
      label="Command palette (⌘⇧P)"
      testId="command-palette-button"
      onclick={(event) => void onCommand('command-palette', event.currentTarget as HTMLElement)}
    />

    <span class="notification-anchor">
      <IconButton
        icon="bell"
        label={`Notifications${notificationCount ? `, ${notificationCount} unread` : ''}`}
        testId="notification-button"
        onclick={(event) => void onCommand('open-notifications', event.currentTarget as HTMLElement)}
      />
      {#if notificationCount > 0}<strong aria-hidden="true">{notificationCount}</strong>{/if}
    </span>

    {@render recovery?.()}

    <CommandOverflowMenu label="Main menu" icon="more" align="right" items={mainMenuItems} onSelect={onCommand} testId="command-main-menu" />
  </div>
</div>

<style>
  .workspace-command-bar {
    display: grid;
    grid-template-columns: minmax(0, auto) minmax(80px, 1fr) minmax(0, auto);
    align-items: center;
    gap: var(--space-8);
    min-height: 42px;
    padding: var(--space-5) var(--space-8);
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface);
  }
  .command-leading,
  .command-trailing { display: flex; align-items: center; gap: var(--space-3); min-width: 0; }
  .command-trailing { justify-content: flex-end; }
  select { min-height: 30px; }
  .workspace-button,
  .command-running,
  .command-context button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-6);
    min-height: 30px;
    padding: var(--space-4) var(--space-7);
    border-color: transparent;
    background: transparent;
  }
  .workspace-button:hover,
  .command-running:hover:not(:disabled),
  .command-context button:hover { border-color: var(--border); background: var(--surface-soft); }
  .workspace-button { min-width: 0; max-width: 150px; }
  .workspace-button span:first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .workspace-select { width: min(150px, 16vw); }
  .command-context { display: flex; align-items: center; justify-content: center; gap: var(--space-4); min-width: 0; color: var(--muted); }
  .command-context button { min-width: 0; max-width: 42%; color: var(--muted); }
  .command-context button:last-child { max-width: 58%; }
  .command-context button.active { color: var(--text); font-weight: 700; }
  .crumb-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .git-dot {
    flex: 0 0 auto;
    width: 6px;
    height: 6px;
    border-radius: var(--radius-pill);
    background: var(--method-get, var(--accent));
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--method-get, var(--accent)) 18%, transparent);
  }
  .command-running { color: var(--warning-text); font-size: var(--font-size-11); font-weight: 700; }
  .command-running:disabled { opacity: .6; }
  .notification-anchor { position: relative; display: inline-flex; }
  .notification-anchor strong {
    position: absolute;
    top: 0;
    right: 0;
    min-width: 14px;
    padding: 1px var(--space-3);
    border-radius: var(--radius-pill);
    background: var(--danger-strong);
    color: var(--on-dark);
    font-size: var(--font-size-9);
    line-height: 12px;
  }
  /*
    A4-11. These were 1180 / 800 / 610, chosen without reference to the shell
    they sit on top of, which reflows at 1180 / 960 / 680. The visible defect
    was the 960px step: the sidebar turns into an overlay there — the largest
    shape change the app makes — while this bar carried on unchanged until 800,
    and then took its own step at a width where nothing underneath moved. The
    chrome and the content were reflowing on unrelated schedules.

    The numbers now come from `layout.ts`'s SHELL_BREAKPOINTS. CSS cannot read a
    TypeScript constant, so they are still literals here; `layout.test.mts`
    greps this file and fails on any width that is not in that scale, which is
    what stops a fourth number from appearing the next time this bar gets a
    button.
  */
  @media (max-width: 1180px) {
    .workspace-command-bar { grid-template-columns: minmax(0, auto) minmax(36px, 1fr) minmax(0, auto); }
    .command-running span { display: none; }
    .workspace-select, .workspace-button { max-width: 120px; }
  }
  @media (max-width: 960px) {
    .workspace-command-bar { grid-template-columns: minmax(0, 1fr) auto; }
    .command-context { display: none; }
    .workspace-select, .workspace-button { max-width: 105px; }
  }
  @media (max-width: 680px) {
    .workspace-select, .workspace-button { display: none; }
  }
</style>
