<script lang="ts">
  type FileBodyRow = {
    filePath: string
    contentType: string
    selected: boolean
  }

  // US-027 — runes. Nothing here is bound by a parent; every instance passes
  // rows by value and mutates through the callbacks, so no $bindable.
  type Props = {
    rows?: FileBodyRow[]
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

<table class="kv-table file-body-table">
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
            {#if showMove}
              <button class="icon-button drag-handle" title="Drag file" aria-label="Drag file to reorder">::</button>
              <button class="icon-button" title="Move file up" aria-label="Move file up" disabled={index === 0} onclick={() => onMove(index, -1)}>^</button>
              <button class="icon-button" title="Move file down" aria-label="Move file down" disabled={index === rows.length - 1} onclick={() => onMove(index, 1)}>v</button>
            {/if}
            <button class="icon-button" title="Remove file" aria-label="Remove file" onclick={() => onRemove(index)}>x</button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>

{#if !readonly}
  <button onclick={onAdd}>Add File</button>
{/if}
