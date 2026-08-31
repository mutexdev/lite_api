<script lang="ts">
  import RowActions from './RowActions.svelte'

  type FileBodyRow = {
    filePath: string
    contentType: string
    selected: boolean
  }

  // US-027 — runes. Nothing here is bound by a parent; every instance passes
  // rows by value and mutates through the callbacks, so no $bindable.
  type Props = {
    rows?: FileBodyRow[]
    /** Accessible name for the table itself; see KeyValueTable's `label`. */
    label?: string
    readonly?: boolean
    onAdd?: () => void
    onChange?: (index: number, field: keyof FileBodyRow, value: string | boolean) => void
    onMove?: (index: number, direction: -1 | 1) => void
    onReorder?: (from: number, to: number) => void
    onRemove?: (index: number) => void
    showMove?: boolean
  }

  let {
    rows = [],
    label = undefined,
    readonly = false,
    onAdd = () => {},
    onChange = () => {},
    onMove = () => {},
    onReorder = () => {},
    onRemove = () => {},
    showMove = false
  }: Props = $props()

  // US-027. $state, not a bare let: in runes mode a plain let is NOT reactive,
  // so the drag-over highlight would silently stop updating — the component
  // compiles, typechecks and renders, and only the visual feedback is gone.
  let draggingIndex = $state<number | null>(null)
  let dragOverIndex = $state<number | null>(null)

  function handleDragStart(index: number, event: DragEvent) {
    if (readonly || !showMove) return
    draggingIndex = index
    dragOverIndex = index
    event.dataTransfer?.setData('text/plain', String(index))
    if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
  }

  function handleDragOver(index: number, event: DragEvent) {
    if (readonly || !showMove || draggingIndex === null) return
    event.preventDefault()
    dragOverIndex = index
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
  }

  function handleDrop(index: number, event: DragEvent) {
    if (readonly || !showMove) return
    event.preventDefault()
    const raw = event.dataTransfer?.getData('text/plain') ?? ''
    const from = draggingIndex ?? Number.parseInt(raw, 10)
    draggingIndex = null
    dragOverIndex = null
    if (!Number.isInteger(from) || from < 0 || from === index) return
    onReorder(from, index)
  }

  function clearDragState() {
    draggingIndex = null
    dragOverIndex = null
  }
</script>

<table class="kv-table file-body-table" aria-label={label}>
  <thead>
    <tr>
      <th>File</th>
      <th>Content-Type</th>
      <th>Selected</th>
      <th></th>
    </tr>
  </thead>
  <tbody>
    {#each rows ?? [] as row, index (index)}
      <tr
        class:dragging={draggingIndex === index}
        class:drag-over={dragOverIndex === index && draggingIndex !== index}
        draggable={showMove && !readonly}
        ondragstart={(event) => handleDragStart(index, event)}
        ondragover={(event) => handleDragOver(index, event)}
        ondrop={(event) => handleDrop(index, event)}
        ondragend={clearDragState}
      >
        <td>
          <input
            value={row.filePath}
            disabled={readonly}
            placeholder="/path/to/file"
            oninput={(event) => onChange(index, 'filePath', event.currentTarget.value)}
          />
        </td>
        <td>
          <input
            value={row.contentType}
            disabled={readonly}
            placeholder="Auto"
            oninput={(event) => onChange(index, 'contentType', event.currentTarget.value)}
          />
        </td>
        <td>
          <input
            type="radio"
            name="file-body-selected"
            checked={row.selected}
            disabled={readonly}
            onchange={(event) => onChange(index, 'selected', event.currentTarget.checked)}
          />
        </td>
        <td>
          {#if !readonly}
            <RowActions {index} count={rows.length} noun="file" {showMove} {onMove} {onRemove} />
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>

<!--
  "Add file", not "Add File". The three body tables render one below the other
  as the body mode changes, and this one was the only one title-casing its
  second word — the same drift that made the row-delete control two different
  controls. The noun still differs because the rows differ; the casing must not.
-->
{#if !readonly}
  <button type="button" onclick={onAdd}>Add file</button>
{/if}

<!--
  A9-12. Row feedback, and what it means on a table you can type into.

  Until now exactly one table in the app — the DevTools network log — showed
  anything on hover or selection, and the audit was right that the rest reads as
  "one table got more attention" rather than as a rule. The rule it proposed
  ("hover only where clicking a row does something") would have left every
  editable table with nothing, which is the wrong answer for a different reason:
  these grids are rows of near-identical inputs, and the mistake they invite is
  editing the wrong row, not failing to click one.

  So the two states are kept and remapped rather than copied. Hover is the same
  55% tint of --selected-bg the network table uses, and means the same thing
  there as here: this is the row under the pointer. What the network table calls
  "selected" is, in a table with no selection, the row that has the caret — so
  :focus-within carries the full --selected-bg, and the row being typed into is
  marked as plainly as the row being read.

  focus-within is written after hover deliberately: they have equal specificity,
  so source order decides, and a row that is both focused and hovered should
  read as focused. No cursor: pointer and no focus ring on the <tr> — the row is
  not a control here, its cells are.

  This block is byte-identical in KeyValueTable, MultipartTable and
  FileBodyTable, and tableRowActions.test.mts asserts that it stays that way.
  style.css would be the one right home for it; that file belongs to another
  owner this wave, and the paste is in the handoff.
-->
<style>
  tbody tr:hover td {
    background: color-mix(in srgb, var(--selected-bg) 55%, transparent);
  }

  tbody tr:focus-within td {
    background: var(--selected-bg);
  }
</style>
