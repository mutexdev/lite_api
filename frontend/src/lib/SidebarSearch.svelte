<script lang="ts">
  // The sidebar's search box — the shared FindBar rather than a ninth
  // hand-rolled one, and now hidden until it is asked for.
  //
  // WHAT IT USED TO BE: a bare `<input>` in a two-column grid, a clear button
  // that was the literal letter `x` inside something whose class already said
  // `icon-button`, the placeholder "Find requests" (the only surface in the app
  // saying "Find" rather than "Search"), a count rendered as a separate `<small>`
  // below the box, and no Escape handling at all. Every one of those is a small
  // divergence, and together they are the reason a user who learns search here
  // learns nothing that transfers to the pane next door.
  //
  // WHY IT IS NOW HIDDEN AT REST: a permanently-parked search field is a row of
  // chrome charged to every session, and none of Yaak, Bruno or Postman spends
  // one. The box costs a row only while it is in use — the header's magnifier
  // and ⌘F open it, a query holds it open, and Escape on an empty query gives
  // the row back.
  //
  // `value` is $bindable because the parent both reads it (to filter the tree)
  // and writes it (the command palette and the ⌘F shortcut focus this box and
  // sometimes pre-fill it). Without $bindable the clear button and typing would
  // update a local copy and the tree would keep showing the old filter — the
  // same failure KeyValueTable's variableTooltipDraft prop exists to avoid.
  // `open` is $bindable for the same reason: the header toggles it, ⌘F opens
  // it, and Escape in here closes it.
  import FindBar from './ui/FindBar.svelte'

  type Props = {
    value: string
    open: boolean
    input?: HTMLInputElement | undefined
    matchCount: number
    /** Called after Escape closes the bar, so focus lands somewhere real. */
    onClose?: (() => void) | undefined
  }

  let {
    value = $bindable(),
    open = $bindable(),
    input = $bindable(undefined),
    matchCount,
    onClose = undefined
  }: Props = $props()

  let host = $state<HTMLElement | undefined>(undefined)

  /**
   * A query keeps the bar on screen even after the toggle is switched off.
   *
   * Without this, clicking the magnifier a second time (or Escape arriving from
   * somewhere else) would hide a bar that is still filtering the tree, and the
   * user would be left looking at a partial collection list with nothing on
   * screen explaining why.
   */
  const visible = $derived(open || value !== '')

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
   *
   * It reads `visible` so the effect re-runs when the bar is torn down, leaving
   * the parent holding `undefined` rather than a detached input it would then
   * focus into nothing.
   */
  $effect(() => {
    input = visible ? (host?.querySelector('input') ?? undefined) : undefined
  })

  /**
   * Escape's second step, in the capture phase because FindBar stops the first.
   *
   * FindBar calls stopPropagation() on every Escape before deciding what to do
   * with it, so a bubbling listener here would never fire. Capture runs before
   * the input's own handler, and the empty-query guard is what keeps the two
   * steps in the right order: a non-empty query is left to FindBar to clear,
   * and only an already-empty one closes the bar.
   */
  function escapeToClose(event: KeyboardEvent) {
    if (event.key !== 'Escape' || value.trim() !== '') return
    event.stopPropagation()
    open = false
    onClose?.()
  }
</script>

{#if visible}
  <!--
    "Search requests", not "Find requests". The audit found "Search" on six
    surfaces, "Find" here and "Filter" on history; one of the three had to win
    and it was never going to be the one used once.

    `total` is the tree's live match count, so the counter and the empty state
    are the same number said two ways — and FindBar shows nothing at all while
    the box is blank, which is what the old `showCount` guard was for. The
    "Search" field label above it is gone with it: a labelled box whose
    placeholder repeats the label is one word said twice.
  -->
  <section class="rail-section search-section">
    <div bind:this={host} onkeydowncapture={escapeToClose}>
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
{/if}
