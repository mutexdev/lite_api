<script lang="ts">
  // The sidebar's search box, lifted out of App.svelte's markup.
  //
  // `value` is $bindable because the parent both reads it (to filter the tree)
  // and writes it (the command palette and the ⌘F shortcut focus this box and
  // sometimes pre-fill it). Without $bindable the clear button and typing would
  // update a local copy and the tree would keep showing the old filter — the
  // same failure KeyValueTable's variableTooltipDraft prop exists to avoid.
  //
  // `input` is bindable too, because the parent focuses this element from a
  // keyboard shortcut handler that lives outside this component.
  type Props = {
    value: string
    input?: HTMLInputElement | undefined
    matchCount: number
  }

  let { value = $bindable(), input = $bindable(undefined), matchCount }: Props = $props()

  // Only shown once the query is non-blank: a count beside an empty box would
  // read as "0 matches" for a search nobody has started.
  const showCount = $derived(value.trim().length > 0)
</script>

<section class="rail-section search-section">
  <span class="field-label">Search</span>
  <div class="search-box">
    <input aria-label="Search requests" placeholder="Find requests" bind:this={input} bind:value={value} />
    {#if value}
      <button class="icon-button ghost" title="Clear search" onclick={() => (value = '')}>x</button>
    {/if}
  </div>
  {#if showCount}
    <small>{matchCount} matching requests</small>
  {/if}
</section>
