<script lang="ts">
  import VariableTextOverlay from './VariableTextOverlay.svelte'

  type VariableTooltipSource = 'global' | 'collection' | 'environment' | 'folder' | 'request' | 'runtime' | 'process' | 'path' | 'missing' | 'invalid'
  type VariableTooltipInfo = {
    name: string
    scope: string
    rawValue: string
    resolvedValue: string
    secret: boolean
    readOnly: boolean
    found: boolean
    editable: boolean
    validName: boolean
    source: VariableTooltipSource
    index: number
  }
  type VariableTextSegment =
    | {
      key: string
      text: string
      variable: false
      prompt: false
    }
    | {
      key: string
      text: string
      variable: false
      prompt: true
      name: string
    }
    | {
      key: string
      text: string
      variable: true
      prompt: false
      name: string
      info: VariableTooltipInfo
    }

  type MultipartRow = {
    name: string
    value: string
    filePath: string
    contentType: string
    enabled: boolean
  }

  export let rows: MultipartRow[] = []
  export let readonly = false
  export let activeVariableTooltip = ''
  export let editingVariableTooltip = ''
  export let variableTooltipDraft = ''
  export let revealedVariableTooltips: Record<string, boolean> = {}
  export let copiedVariableTooltips: Record<string, boolean> = {}
  export let busy = ''
  export let showMove = false
  export let onAdd: () => void = () => {}
  export let onChange: (index: number, field: keyof MultipartRow, value: string | boolean) => void = () => {}
  export let onMove: (index: number, direction: -1 | 1) => void = () => {}
  export let onReorder: (from: number, to: number) => void = () => {}
  export let onRemove: (index: number) => void = () => {}
  export let valueVariableSegments: (value: string, index: number) => VariableTextSegment[] = () => []
  export let displayTooltipValue: (info: VariableTooltipInfo, revealed: boolean) => string = (info) => info.resolvedValue
  export let onToggleActive: (name: string) => void = () => {}
  export let onBeginEdit: (info: VariableTooltipInfo) => void = () => {}
  export let onEditorKey: (event: KeyboardEvent, info: VariableTooltipInfo) => void = () => {}
  export let onEditorBlur: (event: FocusEvent, info: VariableTooltipInfo) => void = () => {}
  export let onSave: (info: VariableTooltipInfo) => void | Promise<void> = () => {}
  export let onCancel: () => void = () => {}
  export let onCopy: (info: VariableTooltipInfo) => void | Promise<void> = () => {}
  export let onToggleSecret: (name: string) => void = () => {}

  let valueScrollLeft: Record<number, number> = {}
  let valueScrollTop: Record<number, number> = {}
  let draggingIndex: number | null = null
  let dragOverIndex: number | null = null

  function syncValueScroll(index: number, event: Event) {
    const target = event.currentTarget as HTMLTextAreaElement
    valueScrollLeft = { ...valueScrollLeft, [index]: target.scrollLeft }
    valueScrollTop = { ...valueScrollTop, [index]: target.scrollTop }
  }

  function changeValue(index: number, event: Event) {
    syncValueScroll(index, event)
    onChange(index, 'value', (event.currentTarget as HTMLTextAreaElement).value)
  }

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

<table class="kv-table multipart-table">
  <thead>
    <tr>
      <th></th>
      <th>Name</th>
      <th>Value</th>
      <th>File path</th>
      <th>Content-Type</th>
      <th></th>
    </tr>
  </thead>
  <tbody>
    {#each rows ?? [] as row, index (index)}
      <tr
        class:dragging={draggingIndex === index}
        class:drag-over={dragOverIndex === index && draggingIndex !== index}
        draggable={showMove && !readonly}
        on:dragstart={(event) => handleDragStart(index, event)}
        on:dragover={(event) => handleDragOver(index, event)}
        on:drop={(event) => handleDrop(index, event)}
        on:dragend={clearDragState}
      >
        <td>
          <input
            type="checkbox"
            checked={row.enabled}
            disabled={readonly}
            on:change={(event) => onChange(index, 'enabled', event.currentTarget.checked)}
          />
        </td>
        <td>
          <input
            value={row.name}
            disabled={readonly}
            placeholder="name"
            on:input={(event) => onChange(index, 'name', event.currentTarget.value)}
          />
        </td>
        <td>
          <div class="kv-variable-editor multiline">
            <textarea
              class="kv-variable-input kv-variable-textarea"
              value={row.value}
              disabled={readonly}
              placeholder="value"
              rows="3"
              on:input={(event) => changeValue(index, event)}
              on:scroll={(event) => syncValueScroll(index, event)}
              on:keyup={(event) => syncValueScroll(index, event)}
              on:mouseup={(event) => syncValueScroll(index, event)}
            ></textarea>
            <VariableTextOverlay
              segments={valueVariableSegments(row.value ?? '', index)}
              {activeVariableTooltip}
              {editingVariableTooltip}
              bind:variableTooltipDraft
              {revealedVariableTooltips}
              {copiedVariableTooltips}
              {busy}
              scrollLeft={valueScrollLeft[index] ?? 0}
              scrollTop={valueScrollTop[index] ?? 0}
              {displayTooltipValue}
              {onToggleActive}
              {onBeginEdit}
              {onEditorKey}
              {onEditorBlur}
              {onSave}
              {onCancel}
              {onCopy}
              {onToggleSecret}
            />
          </div>
        </td>
        <td>
          <input
            value={row.filePath}
            disabled={readonly}
            placeholder="/path/to/file"
            on:input={(event) => onChange(index, 'filePath', event.currentTarget.value)}
          />
        </td>
        <td>
          <input
            value={row.contentType}
            disabled={readonly}
            placeholder="Auto"
            on:input={(event) => onChange(index, 'contentType', event.currentTarget.value)}
          />
        </td>
        <td>
          {#if !readonly}
            {#if showMove}
              <button class="icon-button drag-handle" title="Drag row" aria-label="Drag row to reorder">::</button>
              <button class="icon-button" title="Move row up" aria-label="Move row up" disabled={index === 0} on:click={() => onMove(index, -1)}>^</button>
              <button class="icon-button" title="Move row down" aria-label="Move row down" disabled={index === rows.length - 1} on:click={() => onMove(index, 1)}>v</button>
            {/if}
            <button class="icon-button" title="Remove row" aria-label="Remove row" on:click={() => onRemove(index)}>x</button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>

{#if !readonly}
  <button on:click={onAdd}>Add row</button>
{/if}
