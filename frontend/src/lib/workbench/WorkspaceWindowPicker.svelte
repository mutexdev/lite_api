<script module lang="ts">
  export type WorkspaceWindowTarget = {
    id: string
    name: string
    path: string
  }
  export type WorkspaceWindowBusyAction = 'loading' | 'opening' | 'creating' | ''
</script>

<script lang="ts">
  // The "open a workspace in a new window" picker.
  //
  // WHAT THIS FILE USED TO BE. It was the 29th hand-rolled dialog — written
  // AFTER US-025 consolidated the other 28 onto Modal.svelte, and re-deriving
  // every guarantee that consolidation exists to hold in one place: its own
  // backdrop, its own Tab trap, its own Escape handler, its own focus-restore
  // in onMount. It also invented a second visual language for "dialog": z-index
  // 65 against everyone else's 50, a 12px radius against .prompt-dialog's
  // --radius-8, a backdrop-filter used nowhere else in the app, an "eyebrow"
  // label used nowhere else in the app, and a private @keyframes duplicating
  // the app's own `spin`.
  //
  // None of that was wrong on its own terms. It was wrong because none of it
  // was shared: a fix to the focus trap in Modal.svelte reached 28 dialogs and
  // not this one, and the trap here was subtly different (no preventScroll on
  // restore, so returning focus could jerk the page).
  //
  // WHAT IS LEFT LOCAL, and why. Only the things Modal.svelte has no opinion
  // about: the listbox roving-tabindex and its arrow/Home/End/Enter keys, the
  // create-workspace form, and the option row's own layout. Escape, Tab, inert,
  // aria-modal and focus return are the shell's now.
  import Modal from '../modals/Modal.svelte'
  import IconButton from '../ui/IconButton.svelte'
  import { tick } from 'svelte'

  // US-028 — runes.
  type Props = {
    targets?: WorkspaceWindowTarget[]
    currentWorkspaceId?: string
    busy?: boolean
    busyAction?: WorkspaceWindowBusyAction
    error?: string
    onOpen: (target: WorkspaceWindowTarget) => void | Promise<void>
    onCreate: (name: string) => WorkspaceWindowTarget | void | Promise<WorkspaceWindowTarget | void>
    onCancel: () => void
  }

  let {
    targets = [],
    currentWorkspaceId = '',
    busy = false,
    busyAction = '',
    error = '',
    onOpen,
    onCreate,
    onCancel
  }: Props = $props()

  let listElement = $state<HTMLElement | undefined>(undefined)
  // All four drive the template. As plain lets the selection would never move,
  // the typed name would never appear and the submit button would never
  // re-enable, while the component kept rendering as though nothing was wrong.
  let selectedId = $state('')
  let workspaceName = $state('')
  let createNameError = $state('')
  let createSubmitting = $state(false)

  const eligibleTargets = $derived(targets.filter((target) => target.id !== currentWorkspaceId))
  const suggestedTarget = $derived(eligibleTargets[0])

  // The one genuine EFFECT in this block: it WRITES selectedId rather than
  // producing a value. As a $derived it would never run, and the selection
  // would keep pointing at a workspace that is no longer in the list.
  $effect(() => {
    if (!eligibleTargets.some((target) => target.id === selectedId)) {
      selectedId = suggestedTarget?.id ?? ''
    }
  })

  const selectedTarget = $derived(eligibleTargets.find((target) => target.id === selectedId))
  const canOpen = $derived(Boolean(selectedTarget && !busy))
  const normalizedWorkspaceName = $derived(workspaceName.trim())
  const duplicateWorkspaceName = $derived(
    Boolean(normalizedWorkspaceName) &&
      targets.some((target) => target.name.trim().toLocaleLowerCase() === normalizedWorkspaceName.toLocaleLowerCase())
  )
  const workspaceNameError = $derived(
    createNameError || (duplicateWorkspaceName ? 'A workspace with this name already exists.' : '')
  )
  const canCreate = $derived(
    Boolean(normalizedWorkspaceName && !duplicateWorkspaceName && !busy && !createSubmitting)
  )

  function selectTarget(target: WorkspaceWindowTarget, focus = false) {
    if (busy || target.id === currentWorkspaceId) return
    selectedId = target.id
    if (focus) requestAnimationFrame(() => optionFor(target.id)?.focus())
  }

  function optionFor(id: string) {
    return listElement?.querySelector<HTMLButtonElement>(`[data-workspace-id="${CSS.escape(id)}"]`)
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

  // Bound to each option rather than to the dialog box. The old handler lived on
  // the dialog element, matched event.target.closest('[data-workspace-option]')
  // and also carried Escape and Tab — the two keys the shell now owns, so all
  // that is left is the listbox's own vocabulary, and an option is where that
  // belongs.
  function handleOptionKeydown(event: KeyboardEvent, target: WorkspaceWindowTarget) {
    if (event.key === 'Enter') {
      event.preventDefault()
      openTarget(target)
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

<Modal
  labelledBy="workspace-picker-title"
  describedBy="workspace-picker-description"
  onClose={onCancel}
  dialogClass="prompt-dialog workspace-picker-dialog"
  testId="workspace-window-picker"
  {busy}
>
  <header>
    <div>
      <h2 id="workspace-picker-title">Open a Workspace</h2>
      <p id="workspace-picker-description">Choose a workspace to open in a separate LiteAPI window.</p>
    </div>
    <IconButton icon="close" label="Close" onclick={onCancel} />
  </header>

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
    <ul class="workspace-list" role="listbox" aria-label="Available workspaces" bind:this={listElement}>
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
            onclick={() => selectTarget(target)}
            ondblclick={() => openTarget(target)}
            onkeydown={(event) => handleOptionKeydown(event, target)}
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

  <form class="create-workspace" onsubmit={(event) => { event.preventDefault(); createWorkspace() }}>
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
        oninput={(event) => updateWorkspaceName(event.currentTarget.value)}
      />
      <button class="primary" type="submit" disabled={!canCreate} aria-label="Create workspace">
        {#if createSubmitting || busyAction === 'creating'}<span class="spinner" aria-hidden="true"></span>Creating…{:else}Create{/if}
      </button>
    </div>
    {#if workspaceNameError}
      <small id="new-workspace-name-error" class="create-workspace-error" role="alert">{workspaceNameError}</small>
    {:else}
      <small id="new-workspace-name-hint">The new workspace remains separate from this window.</small>
    {/if}
  </form>

  <!--
    Footer order matches every other dialog: neutral first, primary last. Cancel
    also names itself the initial focus, which is what the old onMount's
    requestAnimationFrame(() => cancelButton.focus()) was doing by hand — same
    landing spot, now through the one mechanism the shell already implements.
  -->
  <div class="button-row workspace-picker-actions">
    <span class="keyboard-hint"><kbd>↑</kbd><kbd>↓</kbd> navigate <span aria-hidden="true">·</span> <kbd>esc</kbd> cancel</span>
    <button type="button" data-modal-autofocus aria-label="Cancel opening workspace" onclick={onCancel}>Cancel</button>
    <button
      class="primary"
      type="button"
      aria-label={selectedTarget ? `Open ${selectedTarget.name} in a new window` : 'Open workspace in a new window'}
      disabled={!canOpen}
      onclick={() => openTarget(selectedTarget)}
    >
      {#if busyAction === 'opening'}<span class="spinner" aria-hidden="true"></span>Opening…{:else}Open in New Window{/if}
    </button>
  </div>
</Modal>

<style>
  /* Nothing here styles the dialog box, the backdrop or the buttons.
     .prompt-dialog owns the first two and they belong to Modal.svelte, so a
     scoped rule for them could never match anyway; the global `button` and
     `button.primary` rules own the third. What is left is this dialog's own
     content — the option rows, the create form, the two status boxes — written
     against the app's spacing, radius and type scales rather than the raw pixel
     literals this file was authored with. */
  header p {
    margin: var(--space-5) 0 0;
    color: var(--muted);
    font-size: var(--font-size-12);
    line-height: 1.45;
  }

  .picker-error {
    display: grid;
    gap: var(--space-2);
    margin: 0 0 var(--space-12);
    padding: var(--space-8) var(--space-11);
    border: 1px solid var(--danger-border);
    border-radius: var(--radius-8);
    background: var(--danger-bg-soft);
    color: var(--danger);
    font-size: var(--font-size-12);
  }
  .picker-error span { color: var(--text); overflow-wrap: anywhere; }

  .empty-eligible-workspaces {
    display: grid;
    gap: var(--space-2);
    margin: 0 0 var(--space-12);
    padding: var(--space-8) var(--space-11);
    border: 1px dashed var(--border-strong);
    border-radius: var(--radius-8);
    background: var(--surface-soft);
    color: var(--text);
    font-size: var(--font-size-11);
  }
  .empty-eligible-workspaces span { color: var(--muted); }

  .workspace-list {
    max-height: min(48dvh, 360px);
    display: grid;
    align-content: start;
    gap: var(--space-6);
    margin: 0 0 var(--space-14);
    padding: 0;
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
    gap: var(--space-10);
    min-height: 56px;
    padding: var(--space-8) var(--space-10);
    /* --surface-raised and --surface-hover were never declared anywhere: this
       file was the only place in the app that named them, always behind a
       fallback, so both had silently meant their fallback since the day it was
       written. Naming the real token says what the colour actually is. */
    background: var(--surface-soft);
    color: var(--text);
    text-align: left;
  }
  .workspace-option:hover:not([aria-disabled="true"]) {
    border-color: var(--border-strong);
    background: var(--surface-alt);
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
    border-radius: var(--radius-8);
    background: var(--accent-soft);
    color: var(--accent-strong);
    font-size: var(--font-size-13);
    font-weight: 800;
  }
  .workspace-copy { min-width: 0; display: grid; gap: var(--space-3); }
  .workspace-name, .workspace-path {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .workspace-name { font-size: var(--font-size-13); font-weight: 700; }
  .workspace-path { color: var(--muted); font-family: var(--code-font-family); font-size: var(--font-size-10); }

  .current-badge {
    display: inline-flex;
    align-items: center;
    gap: var(--space-5);
    max-width: 150px;
    padding: var(--space-4) var(--space-7);
    border: 1px solid var(--border);
    border-radius: var(--radius-pill);
    background: var(--surface);
    color: var(--muted);
    font-size: var(--font-size-9);
    font-weight: 700;
    white-space: nowrap;
  }
  .current-badge > span { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
  .selected-mark { color: var(--accent-strong); font-weight: 900; }

  .empty-workspaces {
    min-height: 160px;
    display: grid;
    place-content: center;
    justify-items: center;
    margin-bottom: var(--space-14);
    padding: var(--space-28);
    text-align: center;
  }
  .empty-workspaces > span {
    width: 38px;
    height: 38px;
    display: grid;
    place-items: center;
    margin-bottom: var(--space-8);
    border: 1px solid var(--border);
    border-radius: var(--radius-10);
    background: var(--surface-soft);
    color: var(--muted);
    font-size: var(--font-size-18);
  }
  .empty-workspaces strong { font-size: var(--font-size-13); }
  .empty-workspaces p { max-width: 300px; margin: var(--space-5) 0 0; color: var(--muted); font-size: var(--font-size-12); }
  .empty-workspaces .loading-mark {
    border: 2px solid var(--border-strong);
    border-right-color: var(--accent);
    border-radius: 50%;
    background: transparent;
    animation: spin 700ms linear infinite;
  }

  .create-workspace {
    display: grid;
    gap: var(--space-6);
    margin-bottom: var(--space-14);
    padding: var(--space-11) var(--space-12) var(--space-12);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-8);
    background: var(--surface-alt);
  }
  .create-workspace > label {
    color: var(--text-soft);
    font-size: var(--font-size-11);
    font-weight: 700;
  }
  .create-workspace-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--space-7);
  }
  .create-workspace input[aria-invalid="true"] { border-color: var(--danger-border); }
  .create-workspace button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-6);
  }
  .create-workspace small { color: var(--muted); font-size: var(--font-size-10); line-height: 1.35; }
  .create-workspace .create-workspace-error { color: var(--danger); }

  .workspace-picker-actions { justify-content: flex-end; }
  .keyboard-hint { margin-right: auto; color: var(--muted); font-size: var(--font-size-10); white-space: nowrap; }
  kbd {
    display: inline-grid;
    place-items: center;
    min-width: 18px;
    height: 18px;
    margin-right: var(--space-2);
    padding: 0 var(--space-4);
    border: 1px solid var(--border);
    border-bottom-color: var(--border-strong);
    border-radius: var(--radius-4);
    background: var(--surface);
    color: var(--text-soft);
    font-family: inherit;
    font-size: var(--font-size-9);
    line-height: 1;
  }
  .workspace-picker-actions button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-7);
  }

  /* @keyframes spin already exists in style.css, driving .loader. This file used
     to carry a private @keyframes workspace-picker-spin that did the identical
     rotation — the shared one is reused, but the size is not: .loader is a 36px
     page-level spinner and this is a 12px mark that sits inside a button label. */
  .spinner {
    width: 12px;
    height: 12px;
    border: 2px solid currentColor;
    border-right-color: transparent;
    border-radius: 50%;
    animation: spin 700ms linear infinite;
  }

  @media (max-width: 520px) {
    .workspace-option { grid-template-columns: 32px minmax(0, 1fr); }
    .current-badge, .selected-mark { grid-column: 2; justify-self: start; }
    .keyboard-hint { display: none; }
  }

  @media (prefers-contrast: more) {
    .workspace-option { border-color: var(--border-strong); }
    .workspace-option.selected { outline: 2px solid var(--focus); outline-offset: -3px; }
  }

  @media (prefers-reduced-motion: reduce) {
    .spinner, .loading-mark { animation-duration: 1.4s; }
  }
</style>
