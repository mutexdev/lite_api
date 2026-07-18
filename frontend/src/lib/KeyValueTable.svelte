<script lang="ts">
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

	  export let rows: KeyValueRow[] = []
	  export let readonly = false
	  export let readonlyNames = false
	  export let showEnabled = true
	  export let showActions = true
	  export let showAddRow = true
  export let showMove = false
  export let showBulkEdit = false
  export let bulkLabel = 'Bulk edit rows'
		  export let variableOverlay = false
		  export let multilineValues = false
  export let activeVariableTooltip = ''
  export let editingVariableTooltip = ''
  export let variableTooltipDraft = ''
  export let revealedVariableTooltips: Record<string, boolean> = {}
  export let copiedVariableTooltips: Record<string, boolean> = {}
  export let busy = ''
  export let onAdd: () => void = () => {}
  export let onChange: (index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) => void = () => {}
  export let onMove: (index: number, direction: -1 | 1) => void = () => {}
  export let onReorder: (from: number, to: number) => void = () => {}
  export let onBulkChange: (rows: KeyValueRow[]) => void = () => {}
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
  let bulkMode = false
  let draggingIndex: number | null = null
  let dragOverIndex: number | null = null

	  function syncValueScroll(index: number, event: Event) {
	    const target = event.currentTarget as HTMLInputElement | HTMLTextAreaElement
	    valueScrollLeft = { ...valueScrollLeft, [index]: target.scrollLeft }
	    valueScrollTop = { ...valueScrollTop, [index]: target.scrollTop }
	  }

	  function changeValue(index: number, event: Event) {
	    syncValueScroll(index, event)
	    onChange(index, 'value', (event.currentTarget as HTMLInputElement | HTMLTextAreaElement).value)
	  }

  function rowsToBulkText(value: KeyValueRow[]) {
    return (value ?? []).map((row) => `${row.enabled === false ? '~' : ''}${row.name ?? ''}: ${row.value ?? ''}`).join('\n')
  }

  function parseBulkText(value: string): KeyValueRow[] {
    return value
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => {
        const enabled = !line.startsWith('~')
        const normalized = enabled ? line : line.slice(1).trimStart()
        let separator = normalized.indexOf(':')
        if (separator < 0) separator = normalized.indexOf('=')
        const name = separator >= 0 ? normalized.slice(0, separator).trim() : normalized.trim()
        const rowValue = separator >= 0 ? normalized.slice(separator + 1).trimStart() : ''
        return { name, value: rowValue, enabled, secret: false, description: '' }
      })
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
    <button type="button" class:active={!bulkMode} on:click={() => (bulkMode = false)}>Key/Value Edit</button>
    <button type="button" class:active={bulkMode} on:click={() => (bulkMode = true)}>Bulk Edit</button>
  </div>
{/if}

{#if showBulkEdit && bulkMode}
  <textarea
    class="kv-bulk-textarea"
    aria-label={bulkLabel}
    spellcheck="false"
    value={rowsToBulkText(rows)}
    on:input={(event) => onBulkChange(parseBulkText(event.currentTarget.value))}
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
	    {#each rows ?? [] as row, index}
	      <tr
	        class:dragging={draggingIndex === index}
	        class:drag-over={dragOverIndex === index && draggingIndex !== index}
	        draggable={showMove && !readonly}
	        on:dragstart={(event) => handleDragStart(index, event)}
	        on:dragover={(event) => handleDragOver(index, event)}
	        on:drop={(event) => handleDrop(index, event)}
	        on:dragend={clearDragState}
	      >
	        {#if showEnabled}
	          <td>
	            <input
	              type="checkbox"
	              checked={row.enabled}
	              disabled={readonly}
	              on:change={(event) => onChange(index, 'enabled', event.currentTarget.checked)}
	            />
	          </td>
	        {/if}
	        <td>
	          <input
	            value={row.name}
		            disabled={readonly || readonlyNames}
		            placeholder="name"
		            on:input={(event) => onChange(index, 'name', event.currentTarget.value)}
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
	                  on:input={(event) => changeValue(index, event)}
	                  on:scroll={(event) => syncValueScroll(index, event)}
	                  on:keyup={(event) => syncValueScroll(index, event)}
	                  on:mouseup={(event) => syncValueScroll(index, event)}
	                ></textarea>
	              {:else}
	                <input
	                  class="kv-variable-input"
	                  type="text"
	                  value={row.value}
	                  disabled={readonly}
	                  placeholder="value"
	                  on:input={(event) => changeValue(index, event)}
	                  on:scroll={(event) => syncValueScroll(index, event)}
	                  on:keyup={(event) => syncValueScroll(index, event)}
	                  on:mouseup={(event) => syncValueScroll(index, event)}
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
                on:input={(event) => onChange(index, 'value', event.currentTarget.value)}
              ></textarea>
            {:else}
              <input
                type={row.secret ? 'password' : 'text'}
                value={row.value}
                disabled={readonly}
                placeholder="value"
                on:input={(event) => onChange(index, 'value', event.currentTarget.value)}
              />
            {/if}
          {/if}
        </td>
	        {#if showActions}
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
	        {/if}
	      </tr>
	    {/each}
	  </tbody>
	</table>
{/if}

{#if !readonly && showAddRow && !(showBulkEdit && bulkMode)}
	  <button on:click={onAdd}>Add row</button>
{/if}
