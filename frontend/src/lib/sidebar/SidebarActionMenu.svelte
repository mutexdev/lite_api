<script lang="ts">
  // The keyboard-reachable action menu for one sidebar object.
  //
  // It renders whatever sidebarActionsFor returns, which is the SAME list the
  // pointer menus render. That is the whole point of the registry: a folder's
  // seven inline buttons and a request's six disclosure buttons and this menu
  // are three views of one answer to "what can be done to this thing", rather
  // than three hardcoded lists that drift.
  //
  // Unlike the tree behind it, this menu is small and fully rendered, so it uses
  // ordinary DOM focus rather than the container-cursor pattern — there is no
  // virtualisation here to make focus land on an element that does not exist.

  import type { SidebarAction } from './sidebarActions'

  type Props = {
    actions: SidebarAction[]
    /** Names the object being acted on, e.g. 'Login'. */
    label: string
    onrun: (action: SidebarAction) => void
    onclose: () => void
  }

  let { actions, label, onrun, onclose }: Props = $props()

  let root = $state<HTMLElement | undefined>(undefined)

  // Focus the first item on open. Without this the menu appears but the keys
  // that opened it keep going to the tree, which reads as the menu ignoring you.
  $effect(() => {
    root?.querySelector<HTMLButtonElement>('button')?.focus()
  })

  function items(): HTMLButtonElement[] {
    return [...(root?.querySelectorAll<HTMLButtonElement>('button') ?? [])]
  }

  function moveFocus(step: number) {
    const buttons = items()
    if (buttons.length === 0) return
    const current = buttons.findIndex((button) => button === document.activeElement)
    // Wraps, and treats "focus is somewhere else entirely" as starting before
    // the first item so a stray Down still enters the menu.
    const next = (current + step + buttons.length) % buttons.length
    buttons[current < 0 ? 0 : next].focus()
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'ArrowDown') { event.preventDefault(); moveFocus(1); return }
    if (event.key === 'ArrowUp') { event.preventDefault(); moveFocus(-1); return }
    if (event.key === 'Home') { event.preventDefault(); items()[0]?.focus(); return }
    if (event.key === 'End') { event.preventDefault(); items().at(-1)?.focus(); return }
    // Escape and Tab both leave, and both hand the cursor back to the tree —
    // a menu that closes into nowhere loses the user's place in the list.
    if (event.key === 'Escape' || event.key === 'Tab') {
      event.preventDefault()
      event.stopPropagation()
      onclose()
    }
  }

  // Closing on outside pointerdown rather than on blur: blur fires when focus
  // moves BETWEEN the menu's own buttons, which would shut it mid-arrow-key.
  $effect(() => {
    const dismiss = (event: PointerEvent) => {
      if (root && !root.contains(event.target as Node)) onclose()
    }
    document.addEventListener('pointerdown', dismiss, true)
    return () => document.removeEventListener('pointerdown', dismiss, true)
  })
</script>

<div
  class="sidebar-action-menu"
  role="menu"
  tabindex="-1"
  aria-label={`Actions for ${label}`}
  data-testid="sidebar-action-menu"
  bind:this={root}
  onkeydown={keydown}
>
  {#each actions as action (action.id)}
    <button
      type="button"
      role="menuitem"
      class="sidebar-action-menu-item"
      class:danger-inline={action.tone === 'danger'}
      data-testid={action.testId}
      onclick={() => onrun(action)}
    >
      <span class="sidebar-action-menu-label">{action.label}</span>
      {#if action.shortcut}<kbd class="sidebar-action-menu-key">{action.shortcut}</kbd>{/if}
    </button>
  {/each}
</div>

<style>
  /* Positioned by the row shell, which is already position: relative for the
     pointer disclosure that sits in the same corner. */
  .sidebar-action-menu {
    position: absolute;
    top: 100%;
    right: 4px;
    z-index: 6;
    display: grid;
    min-width: 190px;
    padding: var(--space-3);
    border: 1px solid var(--rail-divider);
    border-radius: var(--radius-7);
    background: var(--rail-bg);
    box-shadow: 0 8px 20px var(--shadow-medium);
  }
  .sidebar-action-menu-item {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--space-8);
    align-items: center;
    width: 100%;
    min-height: 26px;
    padding: 0 var(--space-8);
    border: 0;
    border-radius: var(--radius-5);
    background: transparent;
    color: var(--rail-text);
    font-size: var(--font-size-12);
    text-align: left;
  }
  .sidebar-action-menu-item:hover,
  .sidebar-action-menu-item:focus-visible {
    background: var(--rail-hover);
    outline: none;
  }
  .sidebar-action-menu-item.danger-inline:hover,
  .sidebar-action-menu-item.danger-inline:focus-visible {
    color: var(--danger, #e5484d);
  }
  .sidebar-action-menu-label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* The hint is a reminder, not a control: it must never out-shout the label. */
  .sidebar-action-menu-key {
    color: var(--rail-muted);
    font-family: inherit;
    font-size: var(--font-size-11);
  }
</style>
