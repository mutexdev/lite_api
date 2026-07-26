<script lang="ts">
  import { rowsToBulkText, parseBulkText, bulkTextIsLossy } from './bulkEdit'
  import VariableTextOverlay from './VariableTextOverlay.svelte'

  type KeyValueRow = { name: string; value: string; enabled: boolean; secret?: boolean; description?: string }
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

  // US-027 — runes. variableTooltipDraft is the only $bindable prop: App.svelte
  // binds it in two places and MultipartTable in a third, and without $bindable
  // the parent would stop tracking what the user types in the tooltip editor,
  // so the edit would be silently discarded on save.
  type Props = {
    rows?: KeyValueRow[]
    readonly?: boolean
    readonlyNames?: boolean
    showEnabled?: boolean
    showActions?: boolean
    showAddRow?: boolean
    showMove?: boolean
    showBulkEdit?: boolean
    bulkLabel?: string
    variableOverlay?: boolean
    multilineValues?: boolean
    activeVariableTooltip?: string
    editingVariableTooltip?: string
    variableTooltipDraft?: string
    revealedVariableTooltips?: Record<string, boolean>
    copiedVariableTooltips?: Record<string, boolean>
    busy?: string
    onAdd?: () => void
    onChange?: (index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) => void
    onMove?: (index: number, direction: -1 | 1) => void
    onReorder?: (from: number, to: number) => void
    onBulkChange?: (rows: KeyValueRow[]) => void
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
    readonlyNames = false,
    showEnabled = true,
    showActions = true,
    showAddRow = true,
    showMove = false,
    showBulkEdit = false,
    bulkLabel = 'Bulk edit rows',
    variableOverlay = false,
    multilineValues = false,
    activeVariableTooltip = '',
    editingVariableTooltip = '',
    variableTooltipDraft = $bindable(''),
    revealedVariableTooltips = {},
    copiedVariableTooltips = {},
    busy = '',
    onAdd = () => {},
    onChange = () => {},
    onMove = () => {},
    onReorder = () => {},
    onBulkChange = () => {},
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
  // scroll sync, the bulk-edit toggle and the drag highlight would all silently
  // stop updating while the component still compiled and rendered.
  let valueScrollLeft = $state<Record<number, number>>({})
  let valueScrollTop = $state<Record<number, number>>({})
  let bulkMode = $state(false)
  // US-056. The text shown while editing is held locally rather than recomputed
  // from `rows` on every keystroke. Rendering rowsToBulkText(rows) live would
  // reformat the user's text under the cursor mid-edit — a half-typed line
  // becomes a name with an empty value, gets re-rendered as "name: ", and the
  // caret jumps.
  let bulkDraft = $state('')

  function enterBulkMode() {
    bulkDraft = rowsToBulkText(rows)
    bulkMode = true
  }

  function applyBulkDraft(text: string) {
    bulkDraft = text
    // `rows` is passed as the previous state so secret and description, which
    // the text format cannot express, are carried over rather than reset.
    onBulkChange(parseBulkText(text, rows) as KeyValueRow[])
  }
  let draggingIndex = $state<number | null>(null)
  let dragOverIndex = $state<number | null>(null)

	  function syncValueScroll(index: number, event: Event) {
	    const target = event.currentTarget as HTMLInputElement | HTMLTextAreaElement
	    valueScrollLeft = { ...valueScrollLeft, [index]: target.scrollLeft }
	    valueScrollTop = { ...valueScrollTop, [index]: target.scrollTop }
	  }

	  function changeValue(index: number, event: Event) {
	    syncValueScroll(index, event)
	    onChange(index, 'value', (event.currentTarget as HTMLInputElement | HTMLTextAreaElement).value)
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

{#if showBulkEdit && !readonly}
  <div class="kv-bulk-toggle">
    <button type="button" data-testid="kv-mode-rows" class:active={!bulkMode} onclick={() => (bulkMode = false)}>Key/Value Edit</button>
    <button type="button" data-testid="kv-mode-bulk" class:active={bulkMode} onclick={enterBulkMode}>Bulk Edit</button>
  </div>
  {#if bulkMode && bulkTextIsLossy(rows)}
    <p class="muted" data-testid="kv-bulk-warning">
      A name in this table contains <code>:</code>, <code>=</code> or a leading disabled marker, which bulk text cannot represent. Editing here will rewrite it.
    </p>
  {/if}
{/if}

{#if showBulkEdit && bulkMode}
  <textarea
    class="kv-bulk-textarea"
    aria-label={bulkLabel}
    spellcheck="false"
    value={bulkDraft}
    oninput={(event) => applyBulkDraft(event.currentTarget.value)}
  ></textarea>
{:else}
	<table class="kv-table">
	  <thead>
	    <tr>
	      {#if showEnabled}
	        <th></th>
	      {/if}
	      <th>Name</th>
	      <th>Value</th>
	      {#if showActions}
	        <th></th>
	      {/if}
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
	        {#if showEnabled}
	          <td>
	            <input
	              type="checkbox"
	              checked={row.enabled}
	              disabled={readonly}
	              onchange={(event) => onChange(index, 'enabled', event.currentTarget.checked)}
	            />
	          </td>
	        {/if}
	        <td>
	          <input
	            value={row.name}
		            disabled={readonly || readonlyNames}
		            placeholder="name"
		            oninput={(event) => onChange(index, 'name', event.currentTarget.value)}
		          />
        </td>
        <td>
          {#if variableOverlay && !row.secret}
	            <div class="kv-variable-editor" class:multiline={multilineValues}>
	              {#if multilineValues}
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
	              {:else}
	                <input
	                  class="kv-variable-input"
	                  type="text"
	                  value={row.value}
	                  disabled={readonly}
	                  placeholder="value"
	                  oninput={(event) => changeValue(index, event)}
	                  onscroll={(event) => syncValueScroll(index, event)}
	                  onkeyup={(event) => syncValueScroll(index, event)}
	                  onmouseup={(event) => syncValueScroll(index, event)}
	                />
	              {/if}
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
          {:else}
            {#if multilineValues && !row.secret}
              <textarea
                value={row.value}
                disabled={readonly}
                placeholder="value"
                rows="3"
                oninput={(event) => onChange(index, 'value', event.currentTarget.value)}
              ></textarea>
            {:else}
              <input
                type={row.secret ? 'password' : 'text'}
                value={row.value}
                disabled={readonly}
                placeholder="value"
                oninput={(event) => onChange(index, 'value', event.currentTarget.value)}
              />
            {/if}
          {/if}
        </td>
	        {#if showActions}
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
	        {/if}
	      </tr>
	    {/each}
	  </tbody>
	</table>
{/if}

{#if !readonly && showAddRow && !(showBulkEdit && bulkMode)}
	  <button onclick={onAdd}>Add row</button>
{/if}
