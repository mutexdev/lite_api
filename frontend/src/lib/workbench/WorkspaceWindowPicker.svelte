<script context="module" lang="ts">
  export type WorkspaceWindowTarget = {
    id: string
    name: string
    path: string
  }
  export type WorkspaceWindowBusyAction = 'loading' | 'opening' | 'creating' | ''
</script>

<script lang="ts">
  import { onMount, tick } from 'svelte'

  export let targets: WorkspaceWindowTarget[] = []
  export let currentWorkspaceId = ''
  export let busy = false
  export let busyAction: WorkspaceWindowBusyAction = ''
  export let error = ''
  export let onOpen: (target: WorkspaceWindowTarget) => void | Promise<void>
  export let onCreate: (name: string) => WorkspaceWindowTarget | void | Promise<WorkspaceWindowTarget | void>
  export let onCancel: () => void

  let dialogElement: HTMLElement
  let cancelButton: HTMLButtonElement
  let selectedId = ''
  let workspaceName = ''
  let createNameError = ''
  let createSubmitting = false

  $: eligibleTargets = targets.filter((target) => target.id !== currentWorkspaceId)
  $: suggestedTarget = eligibleTargets[0]
  $: if (!eligibleTargets.some((target) => target.id === selectedId)) selectedId = suggestedTarget?.id ?? ''
  $: selectedTarget = eligibleTargets.find((target) => target.id === selectedId)
  $: canOpen = Boolean(selectedTarget && !busy)
  $: normalizedWorkspaceName = workspaceName.trim()
  $: duplicateWorkspaceName = Boolean(normalizedWorkspaceName) && targets.some(
    (target) => target.name.trim().toLocaleLowerCase() === normalizedWorkspaceName.toLocaleLowerCase()
  )
  $: workspaceNameError = createNameError || (duplicateWorkspaceName ? 'A workspace with this name already exists.' : '')
  $: canCreate = Boolean(normalizedWorkspaceName && !duplicateWorkspaceName && !busy && !createSubmitting)

  onMount(() => {
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const focusFrame = requestAnimationFrame(() => cancelButton?.focus())

    return () => {
      cancelAnimationFrame(focusFrame)
      if (previouslyFocused?.isConnected) previouslyFocused.focus()
    }
  })

  function selectTarget(target: WorkspaceWindowTarget, focus = false) {
    if (busy || target.id === currentWorkspaceId) return
    selectedId = target.id
    if (focus) requestAnimationFrame(() => optionFor(target.id)?.focus())
  }

  function optionFor(id: string) {
    return dialogElement?.querySelector<HTMLButtonElement>(`[data-workspace-id="${CSS.escape(id)}"]`)
  }

  function focusableElements() {
    return Array.from(dialogElement.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )).filter((element) => !element.hasAttribute('hidden') && element.getAttribute('aria-hidden') !== 'true')
  }

  function trapTab(event: KeyboardEvent) {
    const focusable = focusableElements()
    if (focusable.length === 0) {
      event.preventDefault()
      dialogElement.focus()
      return
    }

    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    const active = document.activeElement
    if (event.shiftKey && (active === first || !dialogElement.contains(active))) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && (active === last || !dialogElement.contains(active))) {
      event.preventDefault()
      first.focus()
    }
  }

  function moveOptionFocus(key: 'ArrowUp' | 'ArrowDown' | 'Home' | 'End') {
    if (eligibleTargets.length === 0) return
    const currentIndex = Math.max(0, eligibleTargets.findIndex((target) => target.id === selectedId))
    const nextIndex = key === 'Home'
      ? 0
      : key === 'End'
        ? eligibleTargets.length - 1
        : (currentIndex + (key === 'ArrowDown' ? 1 : -1) + eligibleTargets.length) % eligibleTargets.length
    selectTarget(eligibleTargets[nextIndex], true)
  }

  function handleDialogKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      onCancel()
      return
    }
    if (event.key === 'Tab') {
      trapTab(event)
      return
    }
    if (!(event.target instanceof Element)) return
    const option = event.target.closest<HTMLElement>('[data-workspace-option]')
    if (!option) return
    const focusedTarget = eligibleTargets.find((target) => target.id === option.dataset.workspaceId)
    if (event.key === 'Enter') {
      event.preventDefault()
      openTarget(focusedTarget)
      return
    }
    if (event.key === 'ArrowUp' || event.key === 'ArrowDown' || event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      moveOptionFocus(event.key)
    }
  }

  function openTarget(target: WorkspaceWindowTarget | undefined) {
    if (!target || target.id === currentWorkspaceId || busy) return
    selectedId = target.id
    void onOpen(target)
  }

  function updateWorkspaceName(value: string) {
    workspaceName = value
    createNameError = ''
  }

  async function createWorkspace() {
    if (busy || createSubmitting) return
    if (!normalizedWorkspaceName) {
      createNameError = 'Enter a workspace name.'
      return
    }
    if (duplicateWorkspaceName) {
      createNameError = 'A workspace with this name already exists.'
      return
    }

    createSubmitting = true
    createNameError = ''
    try {
      const createdTarget = await onCreate(normalizedWorkspaceName)
      if (!createdTarget) return
      workspaceName = ''
      await tick()
      selectedId = createdTarget.id
      await tick()
      optionFor(createdTarget.id)?.focus()
    } catch (err) {
      createNameError = err instanceof Error ? err.message : String(err)
    } finally {
      createSubmitting = false
    }
  }
</script>

<div class="workspace-picker-backdrop">
  <div
    class="workspace-picker"
    role="dialog"
    aria-modal="true"
    aria-labelledby="workspace-picker-title"
    aria-describedby="workspace-picker-description"
    aria-busy={busy}
    tabindex="-1"
    bind:this={dialogElement}
    on:keydown={handleDialogKeydown}
  >
    <header>
      <div class="heading-copy">
        <span class="eyebrow">New window</span>
        <h2 id="workspace-picker-title">Open a workspace</h2>
        <p id="workspace-picker-description">Choose a workspace to open in a separate LiteAPI window.</p>
      </div>
      <button
        class="close-button"
        type="button"
        aria-label="Cancel opening workspace"
        title="Cancel"
        on:click={onCancel}
      >×</button>
    </header>

    <div class="picker-content">
      {#if error}
        <div class="picker-error" role="alert">
          <strong>Workspace action failed</strong>
          <span>{error}</span>
        </div>
      {/if}

      {#if busy && targets.length === 0}
        <div class="empty-workspaces" role="status" aria-live="polite">
          <span class="loading-mark" aria-hidden="true"></span>
          <strong>Loading workspaces…</strong>
          <p>Checking the workspace registry.</p>
        </div>
      {:else if targets.length === 0}
        <div class="empty-workspaces" role="status">
          <span aria-hidden="true">⌂</span>
          <strong>No workspaces found</strong>
          <p>Create a workspace below, then open it in a new window.</p>
        </div>
      {:else}
        {#if eligibleTargets.length === 0}
          <div class="empty-eligible-workspaces" role="status">
            <strong>No other workspaces yet</strong>
            <span>Create a second workspace below to open another window.</span>
          </div>
        {/if}
        <ul class="workspace-list" role="listbox" aria-label="Available workspaces">
          {#each targets as target (target.id)}
            <li role="presentation">
              <button
                class="workspace-option"
                class:selected={target.id === selectedId}
                class:current={target.id === currentWorkspaceId}
                type="button"
                role="option"
                aria-selected={target.id !== currentWorkspaceId && target.id === selectedId}
                aria-disabled={target.id === currentWorkspaceId || busy}
                disabled={target.id === currentWorkspaceId}
                tabindex={target.id !== currentWorkspaceId && target.id === selectedId ? 0 : -1}
                data-workspace-option
                data-workspace-id={target.id}
                on:click={() => selectTarget(target)}
                on:dblclick={() => openTarget(target)}
              >
                <span class="workspace-mark" aria-hidden="true">{target.name.trim().charAt(0).toUpperCase() || 'W'}</span>
                <span class="workspace-copy">
                  <span class="workspace-name" title={target.name}>{target.name}</span>
                  <span class="workspace-path" title={target.path}>{target.path}</span>
                </span>
                {#if target.id === currentWorkspaceId}
                  <span class="current-badge"><span aria-hidden="true"></span>Current · already open</span>
                {:else if target.id === selectedId}
                  <span class="selected-mark" aria-hidden="true">✓</span>
                {/if}
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <form class="create-workspace" on:submit|preventDefault={createWorkspace}>
        <label for="new-workspace-name">Create another workspace</label>
        <div class="create-workspace-row">
          <input
            id="new-workspace-name"
            type="text"
            value={workspaceName}
            placeholder="Workspace name"
            autocomplete="off"
            aria-invalid={Boolean(workspaceNameError)}
            aria-describedby={workspaceNameError ? 'new-workspace-name-error' : 'new-workspace-name-hint'}
            disabled={busy}
            on:input={(event) => updateWorkspaceName(event.currentTarget.value)}
          />
          <button type="submit" disabled={!canCreate} aria-label="Create workspace">
            {#if createSubmitting || busyAction === 'creating'}<span class="spinner" aria-hidden="true"></span>Creating…{:else}Create{/if}
          </button>
        </div>
        {#if workspaceNameError}
          <small id="new-workspace-name-error" class="create-workspace-error" role="alert">{workspaceNameError}</small>
        {:else}
          <small id="new-workspace-name-hint">The new workspace remains separate from this window.</small>
        {/if}
      </form>
    </div>

    <footer>
      <span class="keyboard-hint"><kbd>↑</kbd><kbd>↓</kbd> navigate <span aria-hidden="true">·</span> <kbd>esc</kbd> cancel</span>
      <div class="actions">
        <button
          type="button"
          aria-label="Cancel opening workspace"
          bind:this={cancelButton}
          on:click={onCancel}
        >Cancel</button>
        <button
          class="open-button"
          type="button"
          aria-label={selectedTarget ? `Open ${selectedTarget.name} in a new window` : 'Open workspace in a new window'}
          disabled={!canOpen}
          on:click={() => openTarget(selectedTarget)}
        >
          {#if busyAction === 'opening'}<span class="spinner" aria-hidden="true"></span>Opening…{:else}Open in New Window{/if}
        </button>
      </div>
    </footer>
  </div>
</div>

<style>
  .workspace-picker-backdrop {
    position: fixed;
    inset: 0;
    z-index: 65;
    display: grid;
    place-items: center;
    padding: clamp(12px, 3vw, 28px);
    background: var(--overlay);
    backdrop-filter: blur(7px) saturate(0.88);
  }

  .workspace-picker {
    width: min(580px, 100%);
    max-height: min(680px, calc(100dvh - 2 * clamp(12px, 3vw, 28px)));
    display: grid;
    grid-template-rows: auto minmax(0, 1fr) auto;
    overflow: hidden;
    border: 1px solid var(--border-strong);
    border-radius: 12px;
    background: var(--surface);
    color: var(--text);
    box-shadow: 0 24px 72px var(--shadow-strong), 0 2px 8px var(--shadow-soft);
    outline: none;
  }

  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 18px;
    padding: 18px 18px 14px;
    border-bottom: 1px solid var(--border-subtle);
  }

  .heading-copy { min-width: 0; }
  .eyebrow {
    display: block;
    margin-bottom: 4px;
    color: var(--accent-strong);
    font-size: 10px;
    font-weight: 800;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }
  h2 { margin: 0; font-size: 18px; line-height: 1.25; letter-spacing: -0.015em; }
  p { margin: 5px 0 0; color: var(--muted); font-size: 12px; line-height: 1.45; }

  button {
    font: inherit;
  }
  button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  button:disabled { cursor: not-allowed; opacity: 0.58; }

  .close-button {
    flex: 0 0 auto;
    width: 30px;
    height: 30px;
    display: grid;
    place-items: center;
    padding: 0;
    border: 1px solid transparent;
    border-radius: 7px;
    background: transparent;
    color: var(--muted);
    font-size: 21px;
    line-height: 1;
    cursor: pointer;
  }
  .close-button:hover { border-color: var(--border); background: var(--surface-soft); color: var(--text); }

  .picker-content {
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .picker-error {
    display: grid;
    gap: 2px;
    margin: 12px 14px 0;
    padding: 9px 11px;
    border: 1px solid var(--danger-border);
    border-radius: 8px;
    background: var(--danger-bg-soft);
    color: var(--danger);
    font-size: 12px;
  }
  .picker-error span { color: var(--text); overflow-wrap: anywhere; }

  .empty-eligible-workspaces {
    display: grid;
    gap: 2px;
    margin: 12px 14px 0;
    padding: 9px 11px;
    border: 1px dashed var(--border-strong);
    border-radius: 8px;
    background: var(--surface-soft);
    color: var(--text);
    font-size: 11px;
  }
  .empty-eligible-workspaces span { color: var(--muted); }

  .workspace-list {
    flex: 1 1 auto;
    min-height: 0;
    max-height: min(52dvh, 420px);
    display: grid;
    align-content: start;
    gap: 6px;
    margin: 0;
    padding: 12px 14px;
    overflow: auto;
    list-style: none;
    scrollbar-gutter: stable;
  }

  .workspace-option {
    width: 100%;
    min-width: 0;
    display: grid;
    grid-template-columns: 34px minmax(0, 1fr) auto;
    align-items: center;
    gap: 10px;
    min-height: 56px;
    padding: 8px 10px;
    border: 1px solid var(--border-subtle);
    border-radius: 9px;
    background: var(--surface-raised, var(--surface-soft));
    color: var(--text);
    text-align: left;
    cursor: pointer;
    transition: border-color 120ms ease, background 120ms ease, box-shadow 120ms ease, transform 120ms ease;
  }
  .workspace-option:hover:not([aria-disabled="true"]) {
    border-color: var(--border-strong);
    background: var(--surface-hover, var(--surface-alt));
    transform: translateY(-1px);
  }
  .workspace-option.selected {
    border-color: var(--selected-border);
    background: var(--selected-bg);
    box-shadow: inset 3px 0 0 var(--accent);
  }
  .workspace-option.current { cursor: default; }

  .workspace-mark {
    width: 32px;
    height: 32px;
    display: grid;
    place-items: center;
    border: 1px solid var(--accent-border);
    border-radius: 8px;
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: 13px;
    font-weight: 800;
  }
  .workspace-copy { min-width: 0; display: grid; gap: 3px; }
  .workspace-name, .workspace-path {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .workspace-name { font-size: 13px; font-weight: 750; }
  .workspace-path { color: var(--muted); font-family: var(--code-font-family); font-size: 10.5px; }

  .current-badge {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    max-width: 150px;
    padding: 4px 7px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: var(--surface);
    color: var(--muted);
    font-size: 9.5px;
    font-weight: 750;
    white-space: nowrap;
  }
  .current-badge > span { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
  .selected-mark { color: var(--accent-strong); font-size: 15px; font-weight: 900; }

  .empty-workspaces {
    min-height: 180px;
    display: grid;
    place-content: center;
    justify-items: center;
    padding: 28px;
    text-align: center;
  }
  .empty-workspaces > span {
    width: 38px;
    height: 38px;
    display: grid;
    place-items: center;
    margin-bottom: 9px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface-soft);
    color: var(--muted);
    font-size: 19px;
  }
  .empty-workspaces strong { font-size: 13px; }
  .empty-workspaces p { max-width: 300px; }
  .empty-workspaces .loading-mark {
    border: 2px solid var(--border-strong);
    border-right-color: var(--accent);
    border-radius: 50%;
    background: transparent;
    animation: workspace-picker-spin 700ms linear infinite;
  }

  .create-workspace {
    flex: 0 0 auto;
    display: grid;
    gap: 6px;
    padding: 11px 14px 12px;
    border-top: 1px solid var(--border-subtle);
    background: var(--surface-alt);
  }
  .create-workspace > label {
    color: var(--text-soft);
    font-size: 11px;
    font-weight: 750;
  }
  .create-workspace-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 7px;
  }
  .create-workspace input {
    min-width: 0;
    min-height: 32px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 12px;
    outline: none;
  }
  .create-workspace input:focus {
    border-color: var(--focus);
    box-shadow: 0 0 0 2px var(--focus-ring);
  }
  .create-workspace input[aria-invalid="true"] { border-color: var(--danger-border); }
  .create-workspace button {
    min-width: 78px;
    min-height: 32px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    padding: 6px 10px;
    border: 1px solid var(--accent);
    border-radius: 7px;
    background: var(--accent);
    color: var(--on-accent);
    font-size: 11.5px;
    font-weight: 750;
    cursor: pointer;
  }
  .create-workspace button:hover:not(:disabled) { border-color: var(--accent-strong); background: var(--accent-strong); }
  .create-workspace small { color: var(--muted); font-size: 10px; line-height: 1.35; }
  .create-workspace .create-workspace-error { color: var(--danger); }

  footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
    padding: 12px 14px;
    border-top: 1px solid var(--border-subtle);
    background: var(--surface-alt);
  }
  .keyboard-hint { color: var(--muted); font-size: 10px; white-space: nowrap; }
  kbd {
    display: inline-grid;
    place-items: center;
    min-width: 18px;
    height: 18px;
    margin-right: 2px;
    padding: 0 4px;
    border: 1px solid var(--border);
    border-bottom-color: var(--border-strong);
    border-radius: 4px;
    background: var(--surface);
    color: var(--text-soft);
    font-family: inherit;
    font-size: 9px;
    line-height: 1;
  }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
  .actions button {
    min-height: 32px;
    padding: 6px 11px;
    border: 1px solid var(--border);
    border-radius: 7px;
    background: var(--surface);
    color: var(--text);
    font-size: 11.5px;
    font-weight: 700;
    cursor: pointer;
  }
  .actions button:hover:not(:disabled) { border-color: var(--border-strong); background: var(--surface-soft); }
  .actions .open-button {
    min-width: 142px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    border-color: var(--accent);
    background: var(--accent);
    color: var(--on-accent);
  }
  .actions .open-button:hover:not(:disabled) { border-color: var(--accent-strong); background: var(--accent-strong); color: var(--on-accent); }

  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: workspace-picker-spin 700ms linear infinite;
  }

  @keyframes workspace-picker-spin { to { transform: rotate(360deg); } }

  @media (max-width: 520px) {
    .workspace-picker-backdrop { place-items: end center; padding: 8px; }
    .workspace-picker { max-height: calc(100dvh - 16px); border-radius: 12px 12px 8px 8px; }
    header { padding: 15px 14px 12px; }
    .workspace-list { padding: 10px; }
    .create-workspace { padding: 10px; }
    .workspace-option { grid-template-columns: 32px minmax(0, 1fr); }
    .current-badge, .selected-mark { grid-column: 2; justify-self: start; }
    footer { align-items: stretch; flex-direction: column; padding: 10px; }
    .keyboard-hint { display: none; }
    .actions { display: grid; grid-template-columns: minmax(0, 0.72fr) minmax(0, 1.28fr); }
    .actions button { width: 100%; }
  }

  @media (prefers-contrast: more) {
    .workspace-picker { border-width: 2px; }
    .workspace-option { border-color: var(--border-strong); }
    .workspace-option.selected { outline: 2px solid var(--focus); outline-offset: -3px; }
  }

  @media (prefers-reduced-motion: reduce) {
    .workspace-option { transition: none; }
    .workspace-option:hover:not([aria-disabled="true"]) { transform: none; }
    .spinner, .loading-mark { animation-duration: 1.4s; }
  }
</style>
