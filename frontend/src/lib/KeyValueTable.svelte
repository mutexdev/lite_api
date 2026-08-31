<script lang="ts">
  import { rowsToBulkText, parseBulkText, bulkTextIsLossy } from './bulkEdit'
  import VariableTextOverlay from './VariableTextOverlay.svelte'
  import SuggestionListbox from './SuggestionListbox.svelte'
  import RowActions from './RowActions.svelte'
  import SegmentedControl from './ui/SegmentedControl.svelte'
  import { moveSuggestionIndex } from './httpHeaders'

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
    /**
     * Accessible name for the table itself.
     *
     * Not one `<table>` in the app has ever had one, so a screen reader
     * announces eleven structurally identical name/value grids — params,
     * headers, folder headers, collection headers, request vars — with nothing
     * to tell them apart but the surrounding heading, which table navigation
     * skips past. Optional rather than required only because App.svelte owns
     * the call sites and cannot be edited from here; every one of them should
     * pass it.
     */
    label?: string
    readonly?: boolean
    readonlyNames?: boolean
    showEnabled?: boolean
    showActions?: boolean
    showAddRow?: boolean
    showMove?: boolean
    showBulkEdit?: boolean
    /**
     * The bulk textarea's accessible name.
     *
     * Optional now rather than defaulted to a constant: five of the eleven call
     * sites spell out "Request headers bulk edit", "Example request params bulk
     * edit" and so on, and the rest fall back to the same literal string
     * "Bulk edit rows" — so a page with two bulk-editable tables on it announces
     * two textareas with one name. Given `label`, the name is derived from it
     * and the call site has nothing to keep in step.
     */
    bulkLabel?: string
    /**
     * A1-08. Renders `row.description` as a trailing read-only column.
     *
     * The Vars tab has been computing `description: v.dataType` per row since
     * the variable data-type feature landed, and this component has never had a
     * rendering path for `description` anywhere in its template — the value was
     * mapped on every keystroke and dropped on the floor. It is read-only
     * because it is DERIVED: the data type is a fact about the value the user
     * typed, not a fifth thing to type, and making it an input would invite
     * edits that the next recomputation silently discards.
     *
     * Off by default, because switching it on changes the column count and
     * eleven call sites share this table.
     */
    showDescription?: boolean
    descriptionLabel?: string
    variableOverlay?: boolean
    multilineValues?: boolean
    busy?: string
    /**
     * US-056. Completion for the name cell. Given what has been typed and the
     * names on the other rows, returns the names to offer. Left undefined by
     * every table that is not a header table, so query params and form rows are
     * unchanged.
     */
    nameSuggestions?: (query: string, otherNames: string[]) => string[]
    /** Completion for the value cell, given the row's name. */
    valueSuggestions?: (name: string, query: string) => string[]
    onAdd?: () => void
    onChange?: (index: number, field: 'name' | 'value' | 'enabled', value: string | boolean) => void
    onMove?: (index: number, direction: -1 | 1) => void
    onReorder?: (from: number, to: number) => void
    onBulkChange?: (rows: KeyValueRow[]) => void
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
    readonlyNames = false,
    showEnabled = true,
    showActions = true,
    showAddRow = true,
    showMove = false,
    showBulkEdit = false,
    bulkLabel = undefined,
    showDescription = false,
    descriptionLabel = 'Description',
    variableOverlay = false,
    multilineValues = false,
    busy = '',
    nameSuggestions = undefined,
    valueSuggestions = undefined,
    onAdd = () => {},
    onChange = () => {},
    onMove = () => {},
    onReorder = () => {},
    onBulkChange = () => {},
    onRemove = () => {},
    valueVariableSegments = () => [],
    displayTooltipValue = (info) => info.resolvedValue,
    onEditorKey = () => {},
    onEditorBlur = () => {},
    onSave = () => {},
    onCopy = () => {}
  }: Props = $props()

  // $state, not a bare let: in runes mode a plain let is not reactive, so the
  // scroll sync, the bulk-edit toggle and the drag highlight would all silently
  // stop updating while the component still compiled and rendered.
  let valueScrollLeft = $state<Record<number, number>>({})
  let valueScrollTop = $state<Record<number, number>>({})
  let bulkMode = $state(false)

  const bulkEditLabel = $derived(bulkLabel ?? (label ? `${label} bulk edit` : 'Bulk edit rows'))

  /**
   * A9-10, and the part of it the audit read as a style inconsistency.
   *
   * Bulk edit is not a view of the rows, it is a REWRITE of them: the textarea
   * is parsed back into a whole new row array, so it can rename a row, delete
   * one and add one. Which means it is only sound where all three of those are
   * things the table already permits.
   *
   * Path Params is where that mattered. It passes showBulkEdit alongside
   * readonlyNames, showAddRow={false} and showActions={false} — the names are
   * derived from the URL and the row set with them — and got a Bulk Edit tab
   * that let a user rename `:id`, delete it, and invent a path parameter the
   * URL does not contain, in a table whose entire grid is otherwise locked
   * against exactly that. The audit filed this as "odd: bulk edit shown on a
   * table with no per-row actions". It was not odd, it was a hole.
   *
   * Deriving the condition here rather than fixing the one call site is the
   * point: this is now a property of what the table permits, so no future
   * caller can reopen the hole by passing the same pair of props again.
   */
  const bulkEditAvailable = $derived(showBulkEdit && !readonly && !readonlyNames && showAddRow)

  // US-056. One popup at a time, identified by the row and which cell it
  // belongs to. Held here rather than per-cell so that opening one closes any
  // other, which is what a user expects from a completion list.
  let suggestionCell = $state<{ index: number; field: 'name' | 'value' } | undefined>(undefined)
  let suggestionItems = $state<string[]>([])
  let suggestionActive = $state(-1)
  let suggestionAnchor = $state<HTMLElement | undefined>(undefined)

  function otherRowNames(index: number): string[] {
    return rows.filter((_, position) => position !== index).map((row) => row.name ?? '')
  }

  function suggestionsFor(index: number, field: 'name' | 'value', query: string): string[] {
    if (readonly) return []
    if (field === 'name') {
      if (readonlyNames || !nameSuggestions) return []
      return nameSuggestions(query, otherRowNames(index))
    }
    if (!valueSuggestions) return []
    return valueSuggestions(rows[index]?.name ?? '', query)
  }

  function openSuggestions(index: number, field: 'name' | 'value', query: string, anchor: HTMLElement) {
    const items = suggestionsFor(index, field, query)
    suggestionItems = items
    suggestionAnchor = anchor
    suggestionCell = items.length > 0 ? { index, field } : undefined
    suggestionActive = -1
  }

  function closeSuggestions() {
    suggestionCell = undefined
    suggestionItems = []
    suggestionActive = -1
  }

  function suggestionsOpenFor(index: number, field: 'name' | 'value') {
    return suggestionCell?.index === index && suggestionCell.field === field && suggestionItems.length > 0
  }

  function applySuggestion(index: number, field: 'name' | 'value', value: string, input: HTMLElement | undefined) {
    onChange(index, field, value)
    closeSuggestions()
    const element = input as HTMLInputElement | undefined
    if (element) {
      element.value = value
      element.focus()
      // Leaves the caret after a value that is deliberately unfinished, such as
      // the trailing space in "Bearer ".
      element.setSelectionRange?.(value.length, value.length)
    }
    // A completed header name usually wants a value next, so offer one.
    if (field === 'name') {
      const values = suggestionsFor(index, 'value', '')
      if (values.length > 0) {
        const valueInput = element?.closest('tr')?.querySelector<HTMLInputElement>('[data-kv-value]')
        if (valueInput) {
          valueInput.focus()
          openSuggestions(index, 'value', '', valueInput)
        }
      }
    }
  }

  function suggestionKeydown(event: KeyboardEvent, index: number, field: 'name' | 'value') {
    const input = event.currentTarget as HTMLInputElement
    if (!suggestionsOpenFor(index, field)) {
      if (event.key === 'ArrowDown' && !event.altKey) {
        const items = suggestionsFor(index, field, input.value)
        if (items.length > 0) {
          event.preventDefault()
          openSuggestions(index, field, input.value, input)
          suggestionActive = 0
        }
      }
      return
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      suggestionActive = moveSuggestionIndex(suggestionActive, event.key === 'ArrowDown' ? 1 : -1, suggestionItems.length)
      return
    }
    if (event.key === 'Escape') {
      // Stopped here: Escape inside an open completion list means "close the
      // list", not "close the dialog this table happens to be inside".
      event.preventDefault()
      event.stopPropagation()
      closeSuggestions()
      return
    }
    if ((event.key === 'Enter' || event.key === 'Tab') && suggestionActive >= 0) {
      event.preventDefault()
      applySuggestion(index, field, suggestionItems[suggestionActive], input)
    }
  }
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

{#if bulkEditAvailable}
  <!--
    The two-button pill group this replaces was one of the hand-rolled
    segmented controls the primitive exists for: plain buttons with a `.active`
    class, no role, no grouping, and two tab stops for a one-of-two choice.
    SegmentedControl is a real radiogroup — one tab stop, arrows to switch — and
    it is what the body-mode and response-view pickers already are, so the same
    gesture now works on all three.

    `data-testid="kv-mode"` sits on the group; the old per-button ids are gone
    and the buttons are addressable by `data-value` instead, which is the
    primitive's convention.
  -->
  <SegmentedControl
    testId="kv-mode"
    ariaLabel={label ? `${label} edit mode` : 'Edit mode'}
    options={[
      { value: 'rows', label: 'Key/Value Edit' },
      { value: 'bulk', label: 'Bulk Edit' }
    ]}
    value={bulkMode ? 'bulk' : 'rows'}
    onChange={(next) => (next === 'bulk' ? enterBulkMode() : (bulkMode = false))}
  />
  {#if bulkMode && bulkTextIsLossy(rows)}
    <p class="muted" data-testid="kv-bulk-warning">
      A name in this table contains <code>:</code>, <code>=</code> or a leading disabled marker, which bulk text cannot represent. Editing here will rewrite it.
    </p>
  {/if}
{/if}

{#if bulkEditAvailable && bulkMode}
  <textarea
    class="kv-bulk-textarea"
    aria-label={bulkEditLabel}
    spellcheck="false"
    value={bulkDraft}
    oninput={(event) => applyBulkDraft(event.currentTarget.value)}
  ></textarea>
{:else}
	<table class="kv-table" aria-label={label}>
	  <thead>
	    <tr>
	      {#if showEnabled}
	        <th></th>
	      {/if}
	      <th>Name</th>
	      <th>Value</th>
	      {#if showDescription}
	        <th>{descriptionLabel}</th>
	      {/if}
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
		            autocomplete="off"
		            role={nameSuggestions ? 'combobox' : undefined}
		            aria-autocomplete={nameSuggestions ? 'list' : undefined}
		            aria-expanded={nameSuggestions ? suggestionsOpenFor(index, 'name') : undefined}
		            aria-controls={nameSuggestions ? `kv-${index}-name-listbox` : undefined}
		            aria-activedescendant={suggestionsOpenFor(index, 'name') && suggestionActive >= 0 ? `kv-${index}-name-option-${suggestionActive}` : undefined}
		            oninput={(event) => { onChange(index, 'name', event.currentTarget.value); openSuggestions(index, 'name', event.currentTarget.value, event.currentTarget) }}
		            onfocus={(event) => openSuggestions(index, 'name', event.currentTarget.value, event.currentTarget)}
		            onkeydown={(event) => suggestionKeydown(event, index, 'name')}
		          />
	          {#if suggestionsOpenFor(index, 'name')}
	            <SuggestionListbox
	              items={suggestionItems}
	              activeIndex={suggestionActive}
	              anchor={suggestionAnchor}
	              idPrefix={`kv-${index}-name`}
	              onPick={(value) => applySuggestion(index, 'name', value, suggestionAnchor)}
	              onClose={closeSuggestions}
	            />
	          {/if}
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
	                  data-kv-value
	                  value={row.value}
	                  disabled={readonly}
	                  placeholder="value"
	                  autocomplete="off"
	                  role={valueSuggestions ? 'combobox' : undefined}
	                  aria-autocomplete={valueSuggestions ? 'list' : undefined}
	                  aria-expanded={valueSuggestions ? suggestionsOpenFor(index, 'value') : undefined}
	                  aria-controls={valueSuggestions ? `kv-${index}-value-listbox` : undefined}
	                  aria-activedescendant={suggestionsOpenFor(index, 'value') && suggestionActive >= 0 ? `kv-${index}-value-option-${suggestionActive}` : undefined}
	                  oninput={(event) => { changeValue(index, event); openSuggestions(index, 'value', event.currentTarget.value, event.currentTarget) }}
	                  onkeydown={(event) => suggestionKeydown(event, index, 'value')}
	                  onscroll={(event) => syncValueScroll(index, event)}
	                  onkeyup={(event) => syncValueScroll(index, event)}
	                  onmouseup={(event) => syncValueScroll(index, event)}
	                />
	                {#if suggestionsOpenFor(index, 'value')}
	                  <SuggestionListbox
	                    items={suggestionItems}
	                    activeIndex={suggestionActive}
	                    anchor={suggestionAnchor}
	                    idPrefix={`kv-${index}-value`}
	                    onPick={(value) => applySuggestion(index, 'value', value, suggestionAnchor)}
	                    onClose={closeSuggestions}
	                  />
	                {/if}
	              {/if}
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
                data-kv-value
                value={row.value}
                disabled={readonly}
                placeholder="value"
                autocomplete={valueSuggestions ? 'off' : undefined}
                role={valueSuggestions && !row.secret ? 'combobox' : undefined}
                aria-autocomplete={valueSuggestions && !row.secret ? 'list' : undefined}
                aria-expanded={valueSuggestions && !row.secret ? suggestionsOpenFor(index, 'value') : undefined}
                aria-controls={valueSuggestions && !row.secret ? `kv-${index}-value-listbox` : undefined}
                aria-activedescendant={suggestionsOpenFor(index, 'value') && suggestionActive >= 0 ? `kv-${index}-value-option-${suggestionActive}` : undefined}
                oninput={(event) => { onChange(index, 'value', event.currentTarget.value); if (!row.secret) openSuggestions(index, 'value', event.currentTarget.value, event.currentTarget) }}
                onkeydown={(event) => { if (!row.secret) suggestionKeydown(event, index, 'value') }}
              />
              {#if suggestionsOpenFor(index, 'value')}
                <SuggestionListbox
                  items={suggestionItems}
                  activeIndex={suggestionActive}
                  anchor={suggestionAnchor}
                  idPrefix={`kv-${index}-value`}
                  onPick={(value) => applySuggestion(index, 'value', value, suggestionAnchor)}
                  onClose={closeSuggestions}
                />
              {/if}
            {/if}
          {/if}
        </td>
	        {#if showDescription}
	          <!--
	            A `<code>` cell, not an input. The value is a computed data type —
	            the same category of string as a header value, and the app's rule
	            is that those are monospaced. Falls back to an em dash rather than
	            an empty cell so a row whose type has not been derived yet reads
	            as "not known", not as a cell that failed to render.
	          -->
	          <td class="kv-description"><code>{row.description || '—'}</code></td>
	        {/if}
	        {#if showActions}
	          <td>
	            {#if !readonly}
	              <RowActions {index} count={rows.length} {showMove} {onMove} {onRemove} />
	            {/if}
	          </td>
	        {/if}
	      </tr>
	    {/each}
	  </tbody>
	</table>
{/if}

{#if !readonly && showAddRow && !(bulkEditAvailable && bulkMode)}
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

  /* Not part of the shared pair above: only this table has the column. */
  .kv-description {
    color: var(--muted-strong);
    white-space: nowrap;
  }
</style>
