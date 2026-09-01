<script module lang="ts">
  /**
   * The app's icon set, in one file.
   *
   * The audit found the same idea drawn three different ways: a real SVG in
   * CommandOverflowMenu, a Unicode arrow glyph in RequestCommandStrip for the
   * identical "change response orientation" command, and bare text characters
   * (`x`, `^`, `v`, `::`) as the row actions in every editable table — several
   * of them inside elements whose class already said `icon-button`.
   *
   * A glyph is not a cheap icon. It inherits the text font, so it changes shape
   * with the user's font settings, it is not stroke-aligned with the SVGs
   * beside it, and a screen reader reads it out as a letter. Hence one set,
   * one grid, one stroke weight.
   *
   * The geometry follows what CommandOverflowMenu already established, because
   * that is the set already on screen: a 20×20 viewBox, no fill, a 1.7 stroke
   * in currentColor, round caps. Anything added later must be drawn on the same
   * grid or it will look bolder or lighter than its neighbours at 16px.
   */
  export type IconName =
    | 'search'
    | 'copy'
    | 'download'
    | 'format'
    | 'minify'
    | 'tree'
    | 'list'
    | 'wrap'
    | 'close'
    | 'check'
    | 'chevron-up'
    | 'chevron-down'
    | 'chevron-left'
    | 'chevron-right'
    | 'plus'
    | 'trash'
    | 'more'
    | 'filter'
    | 'external'
    | 'layout-split'
    | 'sidebar'
    | 'command'
    | 'save'
    | 'bell'
    | 'play'
    | 'stop'
    | 'restore'
    | 'cookie'
    | 'bookmark'

  export const iconNames: IconName[] = [
    'search', 'copy', 'download', 'format', 'minify', 'tree', 'list', 'wrap',
    'close', 'check', 'chevron-up', 'chevron-down', 'chevron-left', 'chevron-right',
    'plus', 'trash', 'more', 'filter', 'external', 'layout-split',
    'sidebar', 'command', 'save', 'bell', 'play', 'stop', 'restore', 'cookie', 'bookmark'
  ]
</script>

<script lang="ts">
  type Props = { name: IconName; size?: number }
  let { name, size = 16 }: Props = $props()
</script>

<!--
  aria-hidden on every icon, without exception.

  An icon in this app is never the accessible name of anything: IconButton
  carries the label, and a decorative icon inside a labelled control that also
  announced itself would make every button read its purpose twice.
-->
<svg class="ui-icon" viewBox="0 0 20 20" width={size} height={size} aria-hidden="true" focusable="false">
  {#if name === 'search'}
    <circle cx="9" cy="9" r="5.2" /><path d="M12.8 12.8 17 17" />
  {:else if name === 'copy'}
    <rect x="7" y="7" width="9" height="9" rx="1.6" /><path d="M13 4.5H5.6A1.6 1.6 0 0 0 4 6.1v7.4" />
  {:else if name === 'download'}
    <path d="M10 3v9" /><path d="m6.2 8.6 3.8 3.8 3.8-3.8" /><path d="M3.6 15.4h12.8" />
  {:else if name === 'format'}
    <!-- Indented lines: what "pretty print" does, rather than a magic wand. -->
    <path d="M3.4 5h13.2" /><path d="M7 9h9.6" /><path d="M7 13h9.6" /><path d="M3.4 17h13.2" />
  {:else if name === 'minify'}
    <!-- The mirror of format: two edges collapsing toward one line. -->
    <path d="M3.4 10h13.2" /><path d="m7.2 6.4 2.8-2.8 2.8 2.8" /><path d="m7.2 13.6 2.8 2.8 2.8-2.8" />
  {:else if name === 'tree'}
    <path d="M4.4 4.6h3" /><path d="M8.6 9.4h7" /><path d="M8.6 14.2h7" /><path d="M5.9 4.6v9.6" /><path d="M5.9 9.4h1.4" /><path d="M5.9 14.2h1.4" />
  {:else if name === 'list'}
    <path d="M4 5.4h12" /><path d="M4 10h12" /><path d="M4 14.6h12" />
  {:else if name === 'wrap'}
    <path d="M3.6 5.4h12.8" /><path d="M3.6 10h9.6a2.6 2.6 0 0 1 0 5.2h-2.4" /><path d="m12 12.8-1.8 2.4 1.8 2.4" />
  {:else if name === 'close'}
    <path d="m5.4 5.4 9.2 9.2" /><path d="m14.6 5.4-9.2 9.2" />
  {:else if name === 'check'}
    <path d="m4.4 10.4 3.6 3.6 7.6-8" />
  {:else if name === 'chevron-up'}
    <path d="m5.4 12.2 4.6-4.6 4.6 4.6" />
  {:else if name === 'chevron-down'}
    <path d="m5.4 7.8 4.6 4.6 4.6-4.6" />
  {:else if name === 'chevron-left'}
    <path d="m12.2 5.4-4.6 4.6 4.6 4.6" />
  {:else if name === 'chevron-right'}
    <path d="m7.8 5.4 4.6 4.6-4.6 4.6" />
  {:else if name === 'plus'}
    <path d="M10 4v12" /><path d="M4 10h12" />
  {:else if name === 'trash'}
    <path d="M4.2 6.2h11.6" /><path d="M8.2 6.2V4.6h3.6v1.6" /><path d="M5.8 6.2 6.5 16h7l.7-9.8" />
  {:else if name === 'more'}
    <circle cx="4.4" cy="10" r="1.3" /><circle cx="10" cy="10" r="1.3" /><circle cx="15.6" cy="10" r="1.3" />
  {:else if name === 'filter'}
    <path d="M3.6 5h12.8l-4.9 5.6v4.6l-3-1.6v-3z" />
  {:else if name === 'external'}
    <path d="M11 4h5v5" /><path d="M16 4l-7.2 7.2" /><path d="M13.4 11.6V16H4V6.6h4.4" />
  {:else if name === 'layout-split'}
    <!-- A frame with one divider: the pane arrangement itself, rather than an
         arrow suggesting movement. Used by the response-orientation toggle,
         which the audit found drawn as a stroke SVG in one toolbar and as a
         `⇄`/`⇅` text glyph in another, for the same command.

         Redrawn on this set's 1.7 stroke rather than carried over at the
         command bar's 1.6 — the difference at 16px is invisible and the
         exception would not be. -->
    <rect x="2.5" y="3" width="15" height="14" rx="2" /><path d="M10 3v14" />
  {:else if name === 'sidebar'}
    <!-- The same frame as layout-split with the divider at the sidebar's edge:
         the thing the button shows or hides, not an arrow. -->
    <rect x="2.5" y="3" width="15" height="14" rx="2" /><path d="M7 3v14" />
  {:else if name === 'command'}
    <!-- A prompt: "type a command". Distinct from the magnifier on purpose —
         the command palette runs things, ⌘K search finds them. -->
    <path d="m4.6 5.6 4.8 4.4-4.8 4.4" /><path d="M11 15h4.4" />
  {:else if name === 'save'}
    <path d="M4 4.5h9.4L16 7.1v8.4a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1z" /><path d="M7 4.5v3.6h5.6V4.5" /><path d="M7 16.5v-4.4h6v4.4" />
  {:else if name === 'bell'}
    <path d="M5.2 14h9.6l-1.2-1.7V8.4a3.6 3.6 0 0 0-7.2 0v3.9z" /><path d="M8.4 16.2h3.2" />
  {:else if name === 'play'}
    <path d="M6.5 4.5v11l9-5.5z" />
  {:else if name === 'stop'}
    <rect x="5.5" y="5.5" width="9" height="9" rx="1.5" />
  {:else if name === 'restore'}
    <!-- A clock turning back: recover something that was removed. -->
    <path d="M4.2 10a5.9 5.9 0 1 0 1.7-4.2" /><path d="M4 4.4v3.2h3.2" /><path d="M10 7v3.2l2.2 1.4" />
  {:else if name === 'cookie'}
    <!-- The chips are stubs with round caps, not circles: the `more` rule below
         fills any icon with two circles, which would erase this outline. -->
    <path d="M16.7 10.2A7 7 0 1 1 9.8 3.3a3.2 3.2 0 0 0 3.5 3.5 3.2 3.2 0 0 0 3.4 3.4Z" /><path d="M7 8h.01" /><path d="M9.5 13h.01" /><path d="M5.8 12.5h.01" />
  {:else if name === 'bookmark'}
    <path d="M5.5 3.5h9v13l-4.5-3.2-4.5 3.2z" />
  {/if}
</svg>

<style>
  /*
    Matches CommandOverflowMenu's existing stroke exactly. The `more` icon is
    the one dot-based glyph and needs the inverse treatment, the same exception
    that file already carries for it.
  */
  .ui-icon { display: block; flex: none; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
  .ui-icon circle:only-of-type { fill: none; }
  .ui-icon:has(circle + circle) { fill: currentColor; stroke: none; }
</style>
