<script lang="ts">
  import { onMount, tick } from 'svelte'
  import Icon from '../ui/Icon.svelte'
  import type { WorkbenchCommandID, WorkbenchCommandItem } from './workbenchCommands'

  // US-028 — runes.
  type Props = {
    label: string
    items?: WorkbenchCommandItem[]
    icon?: 'add' | 'more'
    align?: 'left' | 'right'
    testId?: string
    onSelect: (id: WorkbenchCommandID, invoker: HTMLElement | null) => void | Promise<void>
  }

  let { label, items = [], icon = 'more', align = 'right', testId = '', onSelect }: Props = $props()

  let root: HTMLDivElement
  let trigger: HTMLButtonElement
  // $state: `open` is what the dropdown's visibility reads. As a plain let it
  // would still be assigned and the menu would simply never appear — the
  // component compiles and renders either way.
  let panel = $state<HTMLDivElement | undefined>(undefined)
  let open = $state(false)

  function enabledItems() {
    return Array.from(panel?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [])
  }

  async function show(focus: 'first' | 'last' | 'none' = 'none') {
    open = true
    await tick()
    if (focus === 'first') enabledItems()[0]?.focus()
    if (focus === 'last') enabledItems().at(-1)?.focus()
  }

  function close(restoreFocus = false) {
    open = false
    if (restoreFocus) void tick().then(() => trigger?.focus())
  }

  function toggle() {
    if (open) close(true)
    else void show('first')
  }

  function triggerKeydown(event: KeyboardEvent) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      void show('first')
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      void show('last')
    } else if (event.key === 'Escape' && open) {
      event.preventDefault()
      close(true)
    }
  }

  function panelKeydown(event: KeyboardEvent) {
    const buttons = enabledItems()
    const current = buttons.indexOf(document.activeElement as HTMLButtonElement)
    if (event.key === 'Escape') {
      event.preventDefault()
      close(true)
      return
    }
    if (event.key === 'Tab') {
      close(false)
      return
    }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key) || buttons.length === 0) return
    event.preventDefault()
    if (event.key === 'Home') buttons[0]?.focus()
    else if (event.key === 'End') buttons.at(-1)?.focus()
    else {
      const delta = event.key === 'ArrowDown' ? 1 : -1
      buttons[(Math.max(0, current) + delta + buttons.length) % buttons.length]?.focus()
    }
  }

  async function choose(item: WorkbenchCommandItem) {
    if (item.disabled) return
    const invoker = trigger
    open = false
    await onSelect(item.id, invoker)
  }

  onMount(() => {
    const dismiss = (event: PointerEvent) => {
      if (open && root && !root.contains(event.target as Node)) close(false)
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  })
</script>

<div class="command-menu" bind:this={root}>
  <button
    class:command-add={icon === 'add'}
    class:command-more={icon === 'more'}
    type="button"
    bind:this={trigger}
    aria-label={label}
    title={label}
    aria-haspopup="menu"
    aria-expanded={open}
    data-testid={testId || undefined}
    onclick={toggle}
    onkeydown={triggerKeydown}
  >
    <!--
      D1/D9 — the `add` trigger used to render the literal word "New" beside its
      plus, so the button's visible text and its `label` were two strings that
      could disagree, and the sidebar header's version of this menu has no room
      for a word. `label` is the accessible name and the tooltip, as on
      IconButton; the icon is the whole content.
    -->
    <Icon name={icon === 'add' ? 'plus' : 'more'} />
  </button>

  {#if open}
    <div class:align-left={align === 'left'} class:align-right={align === 'right'} class="command-menu-panel" role="menu" tabindex="-1" aria-label={label} bind:this={panel} onkeydown={panelKeydown}>
      {#each items as item, index (item.id)}
        {#if index === 0 || items[index - 1]?.group !== item.group}
          <div class="command-menu-group" class:with-separator={index > 0}>{item.group}</div>
        {/if}
        <button
          type="button"
          role="menuitem"
          class:danger={item.tone === 'danger'}
          disabled={item.disabled}
          aria-disabled={item.disabled}
          title={item.disabled ? item.disabledReason : item.label}
          data-testid={item.testId || undefined}
          onclick={() => void choose(item)}
        >
          <span>{item.label}</span>
          {#if item.shortcut}<kbd>{item.shortcut}</kbd>{/if}
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .command-menu { position: relative; flex: 0 0 auto; }
  .command-menu > button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-width: 30px;
    min-height: 30px;
    padding: 4px 7px;
    border-color: transparent;
    background: transparent;
  }
  .command-menu > button:hover,
  .command-menu > button[aria-expanded="true"] { border-color: var(--border); background: var(--surface-soft); }
  .command-menu-panel {
    position: absolute;
    z-index: 80;
    top: calc(100% + 6px);
    width: 250px;
    max-height: min(440px, calc(100vh - 72px));
    overflow: auto;
    padding: 6px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    box-shadow: 0 16px 42px var(--shadow-medium);
  }
  .align-left { left: 0; }
  .align-right { right: 0; }
  .command-menu-group { padding: 5px 8px 4px; color: var(--muted); font-size: 10px; font-weight: 800; letter-spacing: .06em; text-transform: uppercase; }
  .command-menu-group.with-separator { margin-top: 5px; padding-top: 9px; border-top: 1px solid var(--border-subtle); }
  .command-menu-panel button {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: 12px;
    width: 100%;
    min-height: 30px;
    padding: 6px 8px;
    border: 0;
    background: transparent;
    text-align: left;
  }
  .command-menu-panel button:hover,
  .command-menu-panel button:focus-visible { background: var(--surface-soft); }
  .command-menu-panel button:disabled { opacity: .45; }
  .command-menu-panel button.danger { color: var(--danger); }
  kbd { color: var(--muted); font-family: var(--code-font-family); font-size: 10px; }
</style>
