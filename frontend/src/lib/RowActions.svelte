<script lang="ts">
  import Icon from './ui/Icon.svelte'
  import IconButton from './ui/IconButton.svelte'

  /**
   * The trailing action cell of every editable table.
   *
   * The audit counted fifteen tables that delete a row and found two unrelated
   * controls doing it: a 32px square holding the letter `x` in seven of them, a
   * full-width text button reading "Remove" in the other eight, with nothing
   * about the data deciding which a table got. The move and drag affordances
   * were in the same state — `^`, `v` and `::` typed as literal characters into
   * elements whose class already claimed they held an icon.
   *
   * The fix is not "replace the glyphs"; that would leave three copies of the
   * same cell in three files, free to drift again the next time one of them is
   * touched. There is one cell now, and the three table primitives render it.
   * A table cannot end up with a different delete control than its neighbour
   * because there is no second control to reach for.
   *
   * `noun` exists because these rows are not all rows: the file-body table's
   * are files, and "Remove row" would be wrong there. It changes the wording of
   * the labels and nothing about the control, which is the distinction the old
   * `x`-versus-`Remove` split failed to make.
   */
  type Props = {
    index: number
    /** Row count, so the last row's Move down is disabled rather than a no-op. */
    count: number
    noun?: string
    showMove?: boolean
    onMove?: (index: number, direction: -1 | 1) => void
    onRemove?: (index: number) => void
  }

  let { index, count, noun = 'row', showMove = false, onMove = () => {}, onRemove = () => {} }: Props = $props()
</script>

<div class="row-actions">
  <!--
    The handle is a span, not a button, and that is a deliberate change from the
    markup it replaces.

    The drag itself has always lived on the `<tr>` (`draggable` plus the
    dragstart/dragover/drop handlers), so this element never had a click
    behaviour to lose — it was a focusable control that did nothing when
    activated, sitting in the tab order in front of two Move buttons that do the
    same job properly for keyboard users. Dragging is unchanged; one dead tab
    stop per row is gone.

    The three-bar mark is the reorder handle the icon set has. A dotted grip
    would be the conventional drawing, but Icon.svelte owns the set and adding
    to it belongs with whoever owns that file — see the handoff.
  -->
  {#if showMove}
    <span class="row-drag-handle drag-handle" aria-hidden="true"><Icon name="list" /></span>
    <IconButton icon="chevron-up" label={`Move ${noun} up`} disabled={index === 0} onclick={() => onMove(index, -1)} />
    <IconButton icon="chevron-down" label={`Move ${noun} down`} disabled={index === count - 1} onclick={() => onMove(index, 1)} />
  {/if}
  <IconButton icon="trash" label={`Remove ${noun}`} tone="danger" onclick={() => onRemove(index)} />
</div>

<style>
  /*
    Flex rather than the inline-block row of buttons this replaces: the old cell
    relied on whitespace between the tags for its spacing, so the gap changed
    with the font and disappeared entirely when the markup was reformatted onto
    one line.
  */
  .row-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }
  /*
    Matches IconButton's box exactly. The handle is not a button, so it inherits
    none of that sizing, and a 16px icon left to size itself sits half a row
    higher than the controls beside it.
  */
  .row-drag-handle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    height: 28px;
    width: 28px;
    min-width: 28px;
    color: var(--muted-strong);
  }
</style>
