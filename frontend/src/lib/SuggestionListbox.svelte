<script lang="ts">
  // The completion popup for a key/value cell (US-056).
  //
  // Keyboard ownership stays with the input: this listbox never takes focus, and
  // announces the active option through aria-activedescendant on the input
  // instead. A popup that stole focus would make Tab and typing behave
  // differently from every other cell in the table.
  //
  // It is positioned against the viewport rather than the cell because the
  // key/value table scrolls, and a popup laid out inside the row is clipped by
  // that scroll container the moment it is longer than a row is tall.

  type Props = {
    items: string[]
    activeIndex: number
    /** The input the list belongs to; the popup is placed under it. */
    anchor: HTMLElement | undefined
    /** Prefix for option element ids, so the input can point at the active one. */
    idPrefix: string
    onPick: (value: string) => void
    onClose: () => void
  }

  let { items, activeIndex, anchor, idPrefix, onPick, onClose }: Props = $props()

  let panel = $state<HTMLElement | undefined>(undefined)
  let position = $state({ top: 0, left: 0, width: 0 })

  function place() {
    if (!anchor) return
    const box = anchor.getBoundingClientRect()
    position = { top: box.bottom, left: box.left, width: box.width }
  }

  $effect(() => {
    // Re-read on every render: the row can move while the list is open, as the
    // table reflows around a value being typed.
    void items
    void anchor
    place()
  })

  $effect(() => {
    const reposition = () => place()
    window.addEventListener('scroll', reposition, true)
    window.addEventListener('resize', reposition)
    return () => {
      window.removeEventListener('scroll', reposition, true)
      window.removeEventListener('resize', reposition)
    }
  })

  // Dismiss on pointerdown outside rather than on blur: the pointerdown that
  // picks an option would otherwise blur the input and close the list before
  // the click landed.
  $effect(() => {
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Node | null
      if (!target) return
      if (panel?.contains(target) || anchor?.contains(target)) return
      onClose()
    }
    window.addEventListener('pointerdown', dismiss, true)
    return () => window.removeEventListener('pointerdown', dismiss, true)
  })

  // Keep the active option in view when the arrow keys walk past the edge.
  $effect(() => {
    if (activeIndex < 0) return
    panel?.querySelectorAll<HTMLElement>('[role="option"]')[activeIndex]?.scrollIntoView({ block: 'nearest' })
  })
</script>

{#if items.length > 0}
  <ul
    bind:this={panel}
    class="suggestion-listbox"
    role="listbox"
    id={`${idPrefix}-listbox`}
    style={`top:${position.top}px;left:${position.left}px;min-width:${position.width}px`}
  >
    {#each items as item, index (item)}
      <li
        id={`${idPrefix}-option-${index}`}
        role="option"
        class:active={index === activeIndex}
        aria-selected={index === activeIndex}
        onpointerdown={(event) => {
          event.preventDefault()
          onPick(item)
        }}
      >
        {item}
      </li>
    {/each}
  </ul>
{/if}

<style>
  .suggestion-listbox {
    position: fixed;
    z-index: 60;
    margin: 2px 0 0;
    padding: 4px;
    list-style: none;
    max-height: 15rem;
    overflow-y: auto;
    background: var(--surface-raised, var(--surface, #fff));
    border: 1px solid var(--border, rgba(0, 0, 0, 0.15));
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.18);
    font-size: 0.85rem;
  }

  .suggestion-listbox li {
    padding: 4px 8px;
    border-radius: 4px;
    cursor: pointer;
    white-space: nowrap;
  }

  .suggestion-listbox li.active,
  .suggestion-listbox li:hover {
    background: var(--accent-soft, rgba(0, 0, 0, 0.08));
  }
</style>
