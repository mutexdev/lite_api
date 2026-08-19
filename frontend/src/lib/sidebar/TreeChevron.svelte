<script lang="ts">
  /**
   * The expand/collapse control for a collection or folder row.
   *
   * IT USED TO BE THE CHARACTER "▾" set at the smallest size in the type scale
   * and painted in the muted rail colour. At that size and contrast it read as
   * a speck rather than a control: nothing about it suggested it could be
   * clicked, it carried no hover state at all (the hover rule set the same
   * transparent background it already had), and it was the ONLY way to open a
   * folder, because clicking a folder's name opens that folder's settings
   * instead of expanding it.
   *
   * Three things changed. The glyph is now a drawn path, so it has the same
   * weight and alignment on every platform instead of depending on which font
   * happens to supply the arrow. The control has a hover and active background,
   * so it looks like something that responds. And the hit area is larger than
   * the mark inside it, which is what makes it reachable without aiming.
   *
   * The rotation still carries the state — pointing down when open, right when
   * closed — because that is the convention every file tree uses, and it
   * survives for people who cannot distinguish the colour change.
   */
  interface Props {
    /** Open when true; the glyph points down. */
    expanded: boolean
    /** Spoken name of the thing being toggled, e.g. "folder auth". */
    label: string
    onToggle: () => void
  }

  const { expanded, label, onToggle }: Props = $props()
</script>

<button
  class="tree-chevron"
  class:collapsed={!expanded}
  type="button"
  aria-expanded={expanded}
  aria-label={`${expanded ? 'Collapse' : 'Expand'} ${label}`}
  title={`${expanded ? 'Collapse' : 'Expand'} ${label}`}
  onclick={(event) => { event.stopPropagation(); onToggle() }}
>
  <svg viewBox="0 0 12 12" aria-hidden="true" focusable="false">
    <path d="M3 4.5 L6 8 L9 4.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
  </svg>
</button>
