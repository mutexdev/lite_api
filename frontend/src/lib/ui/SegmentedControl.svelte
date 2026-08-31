<script lang="ts">
  /**
   * One-of-N, shown as a row rather than hidden in a dropdown.
   *
   * The app already had `.segmented` in style.css, used correctly in three
   * places — and then reinvented it a fourth time as a bare pill group in
   * AppearanceSection, while the two choices users make most often (request
   * body mode, response view) stayed `<select>` elements. A `<select>` hides
   * every option until it is opened, which is the wrong trade for a choice the
   * user makes constantly and needs to SEE the current value of at a glance.
   *
   * This wraps the existing class rather than replacing it, so the three
   * correct callers keep their exact appearance and the new ones join them.
   *
   * Keyboard behaviour is the WAI-ARIA radio group, not a row of buttons: one
   * tab stop for the whole control, arrows to move between options. A row of
   * eight plain buttons costs eight tab presses to walk past, which is how the
   * body-mode picker would have regressed keyboard users had it simply become
   * eight `<button>`s.
   */
  type Option = { value: string; label: string; title?: string; disabled?: boolean }
  type Props = {
    options: Option[]
    value: string
    onChange: (value: string) => void
    ariaLabel: string
    compact?: boolean
    testId?: string
  }

  let { options, value, onChange, ariaLabel, compact = true, testId = undefined }: Props = $props()

  let container = $state<HTMLDivElement | null>(null)

  const enabled = $derived(options.filter((option) => !option.disabled))

  /**
   * Which option carries the group's single tab stop.
   *
   * Normally the checked one. But if `value` matches NO option — a stored value
   * from a newer version, or one frame during a change — every button would get
   * `tabindex="-1"` and the group would have no tab stop at all: reachable by
   * mouse, unreachable by keyboard, with nothing on screen saying so. Review
   * flagged this as latent rather than live (the body picker's `modeOf` always
   * resolves to a real segment), which is exactly the kind of thing that stops
   * being latent the second there is a second caller.
   */
  const tabStop = $derived(
    options.some((option) => option.value === value) ? value : enabled[0]?.value
  )

  function select(next: string) {
    if (next !== value) onChange(next)
  }

  /**
   * Arrow keys move the selection AND the focus, which is what makes this a
   * radio group rather than a toolbar. Home/End jump to the ends. Disabled
   * options are skipped rather than landed on and refused.
   */
  function keydown(event: KeyboardEvent) {
    const keys = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp', 'Home', 'End']
    if (!keys.includes(event.key)) return
    event.preventDefault()
    if (enabled.length === 0) return
    const current = Math.max(0, enabled.findIndex((option) => option.value === value))
    const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : -1
    const index = event.key === 'Home' ? 0
      : event.key === 'End' ? enabled.length - 1
      : (current + step + enabled.length) % enabled.length
    const next = enabled[index]
    select(next.value)
    // The focused element must follow the selection, or a screen reader
    // announces the old option while the new one is checked.
    container?.querySelector<HTMLButtonElement>(`[data-value="${CSS.escape(next.value)}"]`)?.focus()
  }
</script>

<!--
  tabindex="-1" on the group, not 0: the checked option is the real tab stop
  (see the buttons' own tabindex below). The container carries one only to
  satisfy the rule that an interactive role be focusable; making it 0 would put
  a second, empty stop in front of every picker.
-->
<div
  bind:this={container}
  class="segmented ui-segmented"
  class:compact
  role="radiogroup"
  tabindex="-1"
  aria-label={ariaLabel}
  data-testid={testId}
  onkeydown={keydown}
>
  <!--
    tabindex below gives the group ONE tab stop: only the checked option is
    reachable by Tab and the arrows do the rest, which is what stops an
    eight-option body-mode picker from costing eight tab presses to walk past.
  -->
  {#each options as option (option.value)}
    <button
      type="button"
      role="radio"
      data-value={option.value}
      class:active={option.value === value}
      aria-checked={option.value === value}
      disabled={option.disabled}
      title={option.title}
      tabindex={option.value === tabStop ? 0 : -1}
      onclick={() => select(option.value)}
    >{option.label}</button>
  {/each}
</div>

<style>
  .ui-segmented { flex: none; }
  /* The row is a control, not a paragraph — it must never rewrap mid-choice. */
  .ui-segmented button { white-space: nowrap; }
  .ui-segmented button:disabled { opacity: 0.45; }
</style>
