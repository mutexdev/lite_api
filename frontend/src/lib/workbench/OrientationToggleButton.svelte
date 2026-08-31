<script lang="ts">
  import type { ResponsePaneOrientation } from '../preferences'

  /**
   * "Change response orientation", as ONE control.
   *
   * WHAT WENT WRONG. The identical command — the same `⌘J`, the same
   * `toggleResponsePaneOrientation` handler — was drawn twice, a few hundred
   * pixels apart, in two different icon languages. The command bar rendered a
   * stroke SVG on the app's 20×20 icon grid; the request strip rendered the
   * Unicode characters `⇄` and `⇅` inside a 30px box. The audit called this the
   * single clearest instance of "it looks like a different application in each
   * section", and it is: two vocabularies for one action, on one screen, with
   * only one of the two advertising the shortcut that performs it.
   *
   * A text glyph is not a cheap icon, for the reasons `ui/Icon.svelte` sets out
   * — it inherits the text font, so it changes shape with the user's font
   * settings, it is not stroke-aligned with the SVGs beside it, and it is a
   * letter to a screen reader. So the SVG wins, and it is the command bar's
   * existing one rather than a new drawing.
   *
   * WHY THIS IS NOT `ui/IconButton.svelte`. It should be, and one day it will
   * be: `IconButton` takes an `IconName`, and the shared icon set has no
   * split-pane glyph — its nineteen names are search/copy/format/chevrons and
   * so on, none of which means "the panes are side by side". Adding one is an
   * edit to `lib/ui/`, which belongs to another implementer this wave. Rather
   * than invent a second inline SVG to sit beside the one that already exists,
   * this component holds the ONE copy of that mark and both call sites render
   * it, so there is exactly one drawing to move into `Icon.svelte` when the icon
   * set can take it. The handoff carries the paste.
   *
   * WHY THE MARK CHANGES WITH THE STATE. The frame with a divider down the
   * middle is the layout you are looking at now, not the one the button will
   * produce. That reads correctly next to the pane it describes and, more
   * importantly, gives the control a visible state at all — the old strip glyph
   * flipped between two arrows while the command bar's icon never changed, so
   * the same command looked stateful in one place and stateless in the other.
   */
  type Props = {
    /** The orientation in effect right now, which is what the mark draws. */
    orientation?: ResponsePaneOrientation
    onclick: () => void
    /**
     * Preserved verbatim per call site. `App.svelte` mounts both of these and
     * cannot be edited this wave, so a testid that moved would be a silent
     * break with no compile error.
     */
    testId?: string
    /**
     * `bar` is the 30px command-bar cell, `strip` the request toolbar's. Only
     * the box differs; the mark, the label and the shortcut hint do not.
     */
    variant?: 'bar' | 'strip'
  }

  let { orientation = 'horizontal', onclick, testId = undefined, variant = 'bar' }: Props = $props()

  /*
   * ONE STRING, BOTH PLACES. The command bar showed "(⌘J)" and the strip showed
   * nothing, so the same command taught the user its shortcut in one toolbar
   * and hid it in the other. The tooltip and the accessible name are built from
   * the same source here for the same reason `IconButton` requires them to
   * match: a tooltip that says something the screen reader does not is a
   * second, invisible vocabulary.
   */
  const action = $derived(orientation === 'horizontal' ? 'Stack the response below' : 'Put the response beside')
  const label = $derived(`Change response orientation — ${action}`)
</script>

<button
  type="button"
  class="orientation-toggle"
  class:strip={variant === 'strip'}
  data-testid={testId}
  title={`${label} (⌘J)`}
  aria-label={`${label} (⌘J)`}
  onclick={() => onclick()}
>
  <svg viewBox="0 0 20 20" aria-hidden="true" focusable="false">
    <rect x="2.5" y="3" width="15" height="14" rx="2" />
    {#if orientation === 'horizontal'}
      <path d="M10 3v14" />
    {:else}
      <path d="M2.5 10h15" />
    {/if}
  </svg>
</button>

<style>
  /*
    Geometry copied exactly from the command bar's `.command-icon` rule rather
    than approximated, so moving the second call site onto this component is a
    no-op there and a repaint only in the request strip — which is the half that
    was wrong.
  */
  .orientation-toggle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 30px;
    min-height: 30px;
    padding: var(--space-4) var(--space-7);
    border-color: transparent;
    background: transparent;
  }

  .orientation-toggle:hover {
    border-color: var(--border);
    background: var(--surface-soft);
  }

  /*
    The request strip's rows are 28px, not 30px — its buttons all declare
    `min-height: 28px` — so the one thing the variant changes is the box, not
    the mark inside it.
  */
  .orientation-toggle.strip {
    min-width: 28px;
    min-height: 28px;
  }

  .orientation-toggle svg {
    width: 16px;
    height: 16px;
    flex: 0 0 auto;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
</style>
