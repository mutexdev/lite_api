<script lang="ts">
  import type { Snippet } from 'svelte'

  /**
   * The wrapper every Preferences section sits in.
   *
   * WHAT WENT WRONG. Each of the eight sections was extracted from App.svelte
   * with a style block of its own, and each independently decided how wide its
   * content should be: 620px for Proxy's field grid, 600px for Display's font
   * row, 440px for the Zoom row directly beneath it, 780px for General and
   * Cache, 920px for the theme variant cards, and nothing at all for the
   * keybindings table. Nobody chose those numbers against each other, so on a
   * wide window the right-hand edge of the settings column zig-zags from
   * section to section — the reason the panel reads as several small apps
   * stacked rather than one page.
   *
   * There is exactly one width declaration in this component and none in any
   * section, which is the whole point: the number below is the settings content
   * column, and changing it changes every section at once. It is not a `--`
   * custom property because a new token has to be declared in :root to mean
   * anything, and style.css is not this component's to edit.
   *
   * `title` is optional so Keybindings — whose heading is the `<summary>` of a
   * disclosure that App.svelte's openKeyboardShortcuts() reaches into by
   * selector — can still take the shared width without growing a second
   * heading above its own.
   */
  type Props = {
    /** Rendered as the section's `<h3>`. Omitted only by Keybindings; see above. */
    title?: string
    /**
     * The right-hand end of the header row: one readout or one action about the
     * section as a whole — Display's live zoom percentage, the activity log's
     * Refresh. Anything that acts on a single setting belongs in that setting's
     * row, not up here where it loses the thing it applies to.
     */
    status?: Snippet
    /** Explains the section as a whole, rather than any one row in it. */
    note?: string
    children: Snippet
  }

  let { title = undefined, status = undefined, note = undefined, children }: Props = $props()
</script>

<section class="setting-section">
  {#if title}
    <div class="settings-section-header">
      <h3>{title}</h3>
      {@render status?.()}
    </div>
  {/if}
  <div class="setting-section-body" class:headed={title}>
    {#if note}
      <p class="setting-section-note">{note}</p>
    {/if}
    {@render children()}
  </div>
</section>

<style>
  /*
    THE ONE WIDTH. Every section's content is capped here and nowhere else, so
    the eight sections end at the same right edge. Rows inside stay at
    min-width: 0 so a long path or pairing command wraps inside the column
    instead of widening it and reintroducing the horizontal scroll this panel
    was already fixed for once.
  */
  .setting-section-body {
    display: grid;
    gap: var(--space-10);
    width: 100%;
    min-width: 0;
    max-width: 780px;
  }

  /* Only a section that actually drew a heading needs to be pushed off it. */
  .setting-section-body.headed {
    margin-top: var(--space-10);
  }

  /*
    Section-level prose, matching a row's description type exactly. The two are
    the same voice at two altitudes — "what this whole section is for" and
    "what this one control does" — and a reader should not be able to tell them
    apart by weight or size.
  */
  .setting-section-note {
    max-width: 62ch;
    margin: 0;
    color: var(--muted);
    font-size: var(--font-size-12);
    line-height: 1.5;
  }
</style>
