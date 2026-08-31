<script lang="ts">
  // The sidebar's search box — now the shared FindBar rather than a ninth
  // hand-rolled one.
  //
  // WHAT IT USED TO BE: a bare `<input>` in a two-column grid, a clear button
  // that was the literal letter `x` inside something whose class already said
  // `icon-button`, the placeholder "Find requests" (the only surface in the app
  // saying "Find" rather than "Search"), a count rendered as a separate `<small>`
  // below the box, and no Escape handling at all. Every one of those is a small
  // divergence, and together they are the reason a user who learns search here
  // learns nothing that transfers to the pane next door.
  //
  // `value` is $bindable because the parent both reads it (to filter the tree)
  // and writes it (the command palette and the ⌘F shortcut focus this box and
  // sometimes pre-fill it). Without $bindable the clear button and typing would
  // update a local copy and the tree would keep showing the old filter — the
  // same failure KeyValueTable's variableTooltipDraft prop exists to avoid.
  import FindBar from './ui/FindBar.svelte'

  type Props = {
    value: string
    input?: HTMLInputElement | undefined
    matchCount: number
  }

  let { value = $bindable(), input = $bindable(undefined), matchCount }: Props = $props()

  let host = $state<HTMLElement | undefined>(undefined)

  /**
   * Hands the parent the actual `<input>` FindBar rendered.
   *
   * FindBar keeps its input private and exposes `focus()` instead, which is the
   * better contract — but App.svelte's ⌘F handler holds an `HTMLInputElement`
   * and calls `.focus()` and `.select()` on it directly, and App.svelte is a
   * single-owner file nobody in this wave may edit. Reaching for the element
   * through the wrapper is the smaller of the two lies available: the alternative
   * was keeping a second, hand-rolled search box in the app purely so one call
   * site could keep its type. The handoff carries the four-line App.svelte change
   * that retires this.
   */
  $effect(() => {
    input = host?.querySelector('input') ?? undefined
  })
</script>

<section class="rail-section search-section">
  <span class="field-label">Search</span>
  <!--
    "Search requests", not "Find requests". The audit found "Search" on six
    surfaces, "Find" here and "Filter" on history; one of the three had to win
    and it was never going to be the one used once.

    `total` is the tree's live match count, so the counter and the empty state
    are the same number said two ways — and FindBar shows nothing at all while
    the box is blank, which is what the old `showCount` guard was for.
  -->
  <div bind:this={host}>
    <FindBar
      {value}
      onChange={(next) => (value = next)}
      ariaLabel="Search requests"
      placeholder="Search requests"
      total={matchCount}
      noun="requests"
      testId="sidebar-find-bar"
    />
  </div>
</section>
