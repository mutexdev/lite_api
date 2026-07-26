<script lang="ts">
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

  // US-027 — runes.
  //
  // variableTooltipDraft is the ONLY $bindable prop here, and it has to be:
  // App.svelte binds it in two places and KeyValueTable in a third, so without
  // $bindable the parent's value would stop tracking what the user types in
  // the tooltip editor — the field would appear to work and the edit would be
  // discarded on save. Every other prop is passed by value.
  type Props = {
    segments?: VariableTextSegment[]
    activeVariableTooltip?: string
    editingVariableTooltip?: string
    variableTooltipDraft?: string
    revealedVariableTooltips?: Record<string, boolean>
    copiedVariableTooltips?: Record<string, boolean>
    busy?: string
    scrollLeft?: number
    scrollTop?: number
    displayTooltipValue?: (info: VariableTooltipInfo, revealed: boolean) => string
    onToggleActive?: (name: string) => void
    onBeginEdit?: (info: VariableTooltipInfo) => void
    onEditorKey?: (event: KeyboardEvent, info: VariableTooltipInfo) => void
    onEditorBlur?: (event: FocusEvent, info: VariableTooltipInfo) => void
    onSave?: (info: VariableTooltipInfo) => void
    onCancel?: () => void
    onCopy?: (info: VariableTooltipInfo) => void
    onToggleSecret?: (name: string) => void
  }

  let {
    segments = [],
    activeVariableTooltip = '',
    editingVariableTooltip = '',
    variableTooltipDraft = $bindable(''),
    revealedVariableTooltips = {},
    copiedVariableTooltips = {},
    busy = '',
    scrollLeft = 0,
    scrollTop = 0,
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

  const invalidVariableWarning = 'Invalid variable name! Variables must only contain alpha-numeric characters, "-", "_", "."'

  function isValidVariableSegment(segment: VariableTextSegment) {
    return segment.variable && segment.info.found && segment.info.validName
  }

  function handleTokenKey(event: KeyboardEvent, name: string) {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      onToggleActive(name)
    }
  }
</script>

<div class="variable-textarea-overlay">
  <span
    class="variable-textarea-overlay-content"
    style={`transform: translate(${-scrollLeft}px, ${-scrollTop}px);`}
  >
	    {#each segments as segment (segment.key)}
	      {#if segment.prompt}
	        <span class="cm-variable-prompt">{segment.text}</span>
	      {:else if segment.variable}
	        <span class="inline-variable-token-wrapper" class:open={activeVariableTooltip === segment.name}>
          <span
            role="button"
            tabindex="0"
            class:cm-variable-valid={isValidVariableSegment(segment)}
            class:cm-variable-invalid={!isValidVariableSegment(segment)}
            onclick={() => onToggleActive(segment.name)}
            onkeydown={(event) => handleTokenKey(event, segment.name)}
          >{segment.text}</span>
          <div class="CodeMirror-brunoVarInfo inline-var-tooltip" role="tooltip">
            <div class="variable-tooltip-title">
              <strong class="var-name">{segment.info.name}</strong>
              <span class="var-scope-badge">{segment.info.scope}</span>
            </div>
            {#if !segment.info.validName}
              <small class="var-warning-note">{invalidVariableWarning}</small>
            {:else if editingVariableTooltip === segment.info.name}
              <textarea
                class="var-value-editor"
                aria-label={'Edit variable ' + segment.info.name}
                bind:value={variableTooltipDraft}
                onkeydown={(event) => onEditorKey(event, segment.info)}
                onblur={(event) => onEditorBlur(event, segment.info)}
              ></textarea>
              <div class="button-row compact">
                <button class="var-save-button" onclick={(event) => { event.stopPropagation(); onSave(segment.info) }} disabled={busy !== ''}>Save</button>
                <button onclick={(event) => { event.stopPropagation(); onCancel() }}>Cancel</button>
              </div>
            {:else if segment.info.editable}
              <button type="button" class="var-value-editable-display" onclick={(event) => { event.stopPropagation(); onBeginEdit(segment.info) }}>
                {displayTooltipValue(segment.info, Boolean(revealedVariableTooltips[segment.info.name]))}
              </button>
            {:else}
              <div class="var-value-editable-display">{displayTooltipValue(segment.info, Boolean(revealedVariableTooltips[segment.info.name]))}</div>
            {/if}
            {#if segment.info.readOnly}
              <small class="var-readonly-note">read-only</small>
            {/if}
            <div class="button-row compact">
              <button
                class="copy-button"
                class:copy-success={copiedVariableTooltips[segment.info.name]}
                onclick={(event) => { event.stopPropagation(); onCopy(segment.info) }}
                disabled={!segment.info.found || !segment.info.validName || copiedVariableTooltips[segment.info.name]}
              >
                {copiedVariableTooltips[segment.info.name] ? 'Copied' : 'Copy'}
              </button>
              {#if segment.info.secret}
                <button class="secret-toggle-button" onclick={(event) => { event.stopPropagation(); onToggleSecret(segment.info.name) }}>
                  {revealedVariableTooltips[segment.info.name] ? 'Hide' : 'Show'}
                </button>
              {/if}
            </div>
          </div>
        </span>
      {:else}
        <span>{segment.text}</span>
      {/if}
    {/each}
  </span>
</div>
