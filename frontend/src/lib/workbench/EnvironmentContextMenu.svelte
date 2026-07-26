<script lang="ts">
  import { onMount, tick } from 'svelte'
  import type { CommandOption } from './workbenchCommands'

  export let globalOptions: CommandOption[] = []
  export let environmentOptions: CommandOption[] = []
  export let globalValue = ''
  export let environmentValue = ''
  export let globalName = 'none'
  export let environmentName = 'No environment'
  export let onGlobalChange: (id: string) => void | Promise<void>
  export let onEnvironmentChange: (id: string) => void | Promise<void>
  export let onManage: () => void | Promise<void>

  let root: HTMLDivElement
  let trigger: HTMLButtonElement
  let panel: HTMLDivElement
  let open = false

  async function show() {
    open = true
    await tick()
    panel?.querySelector<HTMLElement>('select, button')?.focus()
  }

  function close(restoreFocus = false) {
    open = false
    if (restoreFocus) void tick().then(() => trigger?.focus())
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      close(true)
    } else if (event.key === 'Tab' && open) {
      const focusable = Array.from(panel?.querySelectorAll<HTMLElement>('select, button:not(:disabled)') ?? [])
      const first = focusable[0]
      const last = focusable.at(-1)
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last?.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first?.focus()
      }
    }
  }

  onMount(() => {
    const dismiss = (event: PointerEvent) => {
      if (open && root && !root.contains(event.target as Node)) close(false)
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  })
</script>

<div class="environment-menu" role="group" aria-label="Environment controls" bind:this={root}>
  <button
    type="button"
    bind:this={trigger}
    aria-haspopup="dialog"
    aria-expanded={open}
    aria-label={`Environment context. Global: ${globalName}. Collection: ${environmentName}`}
    title={`Global: ${globalName} · Collection: ${environmentName}`}
    data-testid="environment-context-menu"
    on:click={() => open ? close(true) : void show()}
    on:keydown={keydown}
  >
    <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m10 2.5 7 4-7 4-7-4zM3 10l7 4 7-4M3 13.5l7 4 7-4" /></svg>
    <span>{environmentName}</span>
    <span class="chevron" aria-hidden="true">⌄</span>
  </button>

  {#if open}
    <div class="environment-panel" role="dialog" tabindex="-1" aria-label="Environment context" bind:this={panel} on:keydown={keydown}>
      <label>
        <span>Global environment</span>
        <select aria-label="Global environment" value={globalValue} on:change={(event) => void onGlobalChange(event.currentTarget.value)}>
          <option value="">None</option>
          {#each globalOptions as option (option.id)}<option value={option.id}>{option.name}</option>{/each}
        </select>
      </label>
      <label>
        <span>Collection environment</span>
        <select aria-label="Active environment" value={environmentValue} on:change={(event) => void onEnvironmentChange(event.currentTarget.value)}>
          <option value="">No environment</option>
          {#each environmentOptions as option (option.id)}<option value={option.id}>{option.name}</option>{/each}
        </select>
      </label>
      <button type="button" on:click={() => { close(false); void onManage() }}>Manage environments…</button>
    </div>
  {/if}
</div>

<style>
  .environment-menu { position: relative; min-width: 0; flex: 0 1 auto; }
  .environment-menu > button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    /* US-075. `max-width: 170px` alone was a FIXED cap, unrelated to the space
       actually available. Once the command bar shrank .environment-menu below
       the button's content width, the button kept its content width and painted
       outside its own parent — measured at a 1200px viewport as an 11px overlap
       with "Cookies" ("Development" ended at x=666, "Cookies" started at 655).
       The inner span already ellipsizes, but it never got the chance, because
       nothing ever forced the button narrower.

       min(170px, 100%) keeps the original cap and adds the missing one: never
       wider than the container. Flex can then shrink the button, which makes
       the span ellipsize as it was always meant to. */
    max-width: min(170px, 100%);
    min-height: 30px;
    padding: 4px 7px;
    border-color: transparent;
    background: transparent;
  }
  .environment-menu > button:hover,
  .environment-menu > button[aria-expanded="true"] { border-color: var(--border); background: var(--surface-soft); }
  .environment-menu > button span:nth-child(2) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  svg { width: 16px; height: 16px; flex: 0 0 auto; fill: none; stroke: currentColor; stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
  .chevron { color: var(--muted); font-size: 10px; }
  .environment-panel {
    position: absolute;
    z-index: 80;
    top: calc(100% + 6px);
    left: 0;
    display: grid;
    gap: 10px;
    width: min(320px, calc(100vw - 24px));
    padding: 10px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    box-shadow: 0 16px 42px var(--shadow-medium);
  }
  label { display: grid; gap: 5px; color: var(--muted); font-size: 11px; font-weight: 750; }
  select { width: 100%; }
  .environment-panel > button { justify-self: start; border-color: transparent; background: transparent; color: var(--accent-strong); }
</style>
