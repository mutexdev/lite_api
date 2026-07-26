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

  // US-027 — runes. variableTooltipDraft is $bindable because App.svelte binds
  // it; without that the parent would stop tracking what the user types in the
  // tooltip editor and the edit would be discarded on save. Every other prop is
  // passed by value and mutated through the callbacks.
  type Props = {
    rows?: MultipartRow[]
    readonly?: boolean
    activeVariableTooltip?: string
    editingVariableTooltip?: string
    variableTooltipDraft?: string
    revealedVariableTooltips?: Record<string, boolean>
    copiedVariableTooltips?: Record<string, boolean>
    busy?: string
    showMove?: boolean
    onAdd?: () => void
    onChange?: (index: number, field: keyof MultipartRow, value: string | boolean) => void
    onMove?: (index: number, direction: -1 | 1) => void
    onReorder?: (from: number, to: number) => void
    onRemove?: (index: number) => void
    valueVariableSegments?: (value: string, index: number) => VariableTextSegment[]
    displayTooltipValue?: (info: VariableTooltipInfo, revealed: boolean) => string
    onToggleActive?: (name: string) => void
    onBeginEdit?: (info: VariableTooltipInfo) => void
    onEditorKey?: (event: KeyboardEvent, info: VariableTooltipInfo) => void
    onEditorBlur?: (event: FocusEvent, info: VariableTooltipInfo) => void
    onSave?: (info: VariableTooltipInfo) => void | Promise<void>
    onCancel?: () => void
    onCopy?: (info: VariableTooltipInfo) => void | Promise<void>
    onToggleSecret?: (name: string) => void
  }

  let {
    rows = [],
    readonly = false,
    activeVariableTooltip = '',
    editingVariableTooltip = '',
    variableTooltipDraft = $bindable(''),
    revealedVariableTooltips = {},
    copiedVariableTooltips = {},
    busy = '',
    showMove = false,
    onAdd = () => {},
    onChange = () => {},
    onMove = () => {},
    onReorder = () => {},
    onRemove = () => {},
    valueVariableSegments = () => [],
    displayTooltipValue = (info) => info.resolvedValue,
    onToggleActive = () => {},
    onBeginEdit = () => {},
    onEditorKey = () => {},
    onEditorBlur = () => {},
    onSave = () => {},
    onCancel = () => {},
    onCopy = () => {},
    onToggleSecret = () => {}
  }: Props = $props()

  // $state, not a bare let: in runes mode a plain let is not reactive, so the
  // scroll sync and drag highlight would silently stop updating.
  let valueScrollLeft = $state<Record<number, number>>({})
  let valueScrollTop = $state<Record<number, number>>({})
  let draggingIndex = $state<number | null>(null)
  let dragOverIndex = $state<number | null>(null)

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
        ondragstart={(event) => handleDragStart(index, event)}
        ondragover={(event) => handleDragOver(index, event)}
        ondrop={(event) => handleDrop(index, event)}
        ondragend={clearDragState}
      >
        <td>
          <input
            type="checkbox"
            checked={row.enabled}
            disabled={readonly}
            onchange={(event) => onChange(index, 'enabled', event.currentTarget.checked)}
          />
        </td>
        <td>
          <input
            value={row.name}
            disabled={readonly}
            placeholder="name"
            oninput={(event) => onChange(index, 'name', event.currentTarget.value)}
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
              oninput={(event) => changeValue(index, event)}
              onscroll={(event) => syncValueScroll(index, event)}
              onkeyup={(event) => syncValueScroll(index, event)}
              onmouseup={(event) => syncValueScroll(index, event)}
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
          {#if !readonly}
            {#if showMove}
              <button class="icon-button drag-handle" title="Drag row" aria-label="Drag row to reorder">::</button>
              <button class="icon-button" title="Move row up" aria-label="Move row up" disabled={index === 0} onclick={() => onMove(index, -1)}>^</button>
              <button class="icon-button" title="Move row down" aria-label="Move row down" disabled={index === rows.length - 1} onclick={() => onMove(index, 1)}>v</button>
            {/if}
            <button class="icon-button" title="Remove row" aria-label="Remove row" onclick={() => onRemove(index)}>x</button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>

{#if !readonly}
  <button onclick={onAdd}>Add row</button>
{/if}
