<script lang="ts">
  import { variableTooltips } from './stores/variableTooltipStore.svelte'
  import VariableChip from './VariableChip.svelte'
  import VariableTooltip from './VariableTooltip.svelte'
  import type { VariableTooltipInfo } from './variableResolution'

  // The local structural copy of VariableTooltipInfo that used to live here was
  // a fourth definition of the same shape, kept in step by hand and already one
  // field behind (it had no environmentId). It is now the real type — this
  // component and App.svelte pass the SAME objects, so describing them twice
  // could only ever be a way to disagree about them.

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
    busy?: string
    scrollLeft?: number
    scrollTop?: number
    displayTooltipValue?: (info: VariableTooltipInfo, revealed: boolean) => string
    onEditorKey?: (event: KeyboardEvent, info: VariableTooltipInfo) => void
    onEditorBlur?: (event: FocusEvent, info: VariableTooltipInfo) => void
    onSave?: (info: VariableTooltipInfo) => void
    onCopy?: (info: VariableTooltipInfo) => void
  }

  let {
    segments = [],
    busy = '',
    scrollLeft = 0,
    scrollTop = 0,
    displayTooltipValue = (info) => info.resolvedValue,
    onEditorKey = () => {},
    onEditorBlur = () => {},
    onSave = () => {},
    onCopy = () => {},
  }: Props = $props()

  const invalidVariableWarning = 'Invalid variable name! Variables must only contain alpha-numeric characters, "-", "_", "."'
</script>

<div class="variable-textarea-overlay">
  <span
    class="variable-textarea-overlay-content"
    style={`transform: translate(${-scrollLeft}px, ${-scrollTop}px);`}
  >
	    {#each segments as segment (segment.key)}
	      {#if segment.prompt}
	        <VariableChip
	          text={segment.text}
	          name={segment.name}
	          prompt
	        />
	      {:else if segment.variable}
	        <span class="inline-variable-token-wrapper" class:open={variableTooltips.active === segment.name}>
          <VariableChip
            text={segment.text}
            name={segment.name}
            info={segment.info}
            scope={segment.info.scope}
            onActivate={() => variableTooltips.toggleActive(segment.name)}
            onDismiss={() => variableTooltips.close()}
          />
          <VariableTooltip
            panelClass="CodeMirror-brunoVarInfo inline-var-tooltip"
            info={segment.info}
            {busy}
            displayValue={displayTooltipValue}
            invalidWarning={invalidVariableWarning}
            onEditorKey={onEditorKey}
            onEditorBlur={onEditorBlur}
            onSave={onSave}
            onBeginEdit={(info) => variableTooltips.beginEdit(info.name, info.rawValue, info.found, info.editable)}
            onCancelEdit={() => variableTooltips.cancelEdit()}
            onCopy={onCopy}
            onToggleReveal={(name) => variableTooltips.toggleRevealed(name)}
          />
        </span>
      {:else}
        <span>{segment.text}</span>
      {/if}
    {/each}
  </span>
</div>
