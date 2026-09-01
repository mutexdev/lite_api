<script lang="ts">
  import VariableTextOverlay from './VariableTextOverlay.svelte'
  import RowActions from './RowActions.svelte'

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
    /** Accessible name for the table itself; see KeyValueTable's `label`. */
    label?: string
    readonly?: boolean
    busy?: string
    showMove?: boolean
    onAdd?: () => void
    onChange?: (index: number, field: keyof MultipartRow, value: string | boolean) => void
    onMove?: (index: number, direction: -1 | 1) => void
    onReorder?: (from: number, to: number) => void
    onRemove?: (index: number) => void
    valueVariableSegments?: (value: string, index: number) => VariableTextSegment[]
    displayTooltipValue?: (info: VariableTooltipInfo, revealed: boolean) => string
    onEditorKey?: (event: KeyboardEvent, info: VariableTooltipInfo) => void
    onEditorBlur?: (event: FocusEvent, info: VariableTooltipInfo) => void
    onSave?: (info: VariableTooltipInfo) => void | Promise<void>
    onCopy?: (info: VariableTooltipInfo) => void | Promise<void>
  }

  let {
    rows = [],
    label = undefined,
    readonly = false,
    busy = '',
    showMove = false,
    onAdd = () => {},
    onChange = () => {},
    onMove = () => {},
    onReorder = () => {},
    onRemove = () => {},
    valueVariableSegments = () => [],
    displayTooltipValue = (info) => info.resolvedValue,
    onEditorKey = () => {},
    onEditorBlur = () => {},
    onSave = () => {},
    onCopy = () => {}
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

<table class="kv-table multipart-table" aria-label={label}>
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
              {busy}
              scrollLeft={valueScrollLeft[index] ?? 0}
              scrollTop={valueScrollTop[index] ?? 0}
              {displayTooltipValue}
              {onEditorKey}
              {onEditorBlur}
              {onSave}
              {onCopy}
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
            <RowActions {index} count={rows.length} {showMove} {onMove} {onRemove} />
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>

{#if !readonly}
  <button type="button" onclick={onAdd}>Add row</button>
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
