<script lang="ts">
  import Icon from './Icon.svelte'
  import IconButton from './IconButton.svelte'

  /**
   * The one in-pane find bar.
   *
   * The audit counted six of these — response body, response headers, timeline,
   * history, cookies, environment variables — plus CodeMirror's own floating
   * panel, which is themed by CodeMirror rather than by this app and binds
   * Mod-f on top of the app's global ⌘F. Seven search experiences, no two
   * alike: different placeholders, different counter wording, some with
   * Previous/Next as full-width text buttons, some with no way to clear.
   *
   * ⌘K global search and ⌘⇧P the command palette stay modals — they search the
   * WORKSPACE, and they are a deliberate pair. This is for searching the thing
   * currently on screen, and it stays in the pane with it.
   *
   * `matches`/`activeMatch` are optional: a bar that filters a list (headers,
   * cookies) has no cursor to step through, so it passes `total` only and gets
   * no Previous/Next. One component, both shapes — that is the point.
   */
  type Props = {
    value: string
    onChange: (value: string) => void
    placeholder?: string
    ariaLabel: string
    /** Number of hits. Pass `undefined` while there is nothing to count. */
    total?: number | undefined
    /** 0-based cursor. Present only for steppable searches. */
    activeMatch?: number | undefined
    onStep?: (direction: 1 | -1) => void
    /** Wording for a filter bar that found nothing, e.g. "headers". */
    noun?: string
    testId?: string
  }

  let {
    value,
    onChange,
    placeholder = 'Search',
    ariaLabel,
    total = undefined,
    activeMatch = undefined,
    onStep = undefined,
    noun = 'results',
    testId = undefined
  }: Props = $props()

  let input = $state<HTMLInputElement | null>(null)

  const query = $derived(value.trim())
  const steppable = $derived(Boolean(onStep) && activeMatch !== undefined)

  /**
   * One counter format for the whole app.
   *
   * The audit found "3 of 12", "12 of 40 headers", "No matches", "No matching
   * headers" and a bare count all in use at once. The rule here: a steppable
   * search says where the cursor is, a filter says how much survived, an empty
   * query says nothing at all — a counter beside an empty box is noise.
   */
  const status = $derived(
    !query ? ''
      : total === undefined ? ''
      : total === 0 ? `No ${noun}`
      : steppable ? `${(activeMatch ?? 0) + 1} of ${total}`
      : `${total} ${total === 1 ? noun.replace(/s$/, '') : noun}`
  )

  export function focus() {
    input?.focus()
    input?.select()
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      // Escape CLEARS a non-empty query and only leaves the bar once it is
      // already empty, so a mistyped search does not require reaching for the
      // mouse and a second Escape still gets you out of the pane.
      event.stopPropagation()
      if (query) {
        onChange('')
        return
      }
      input?.blur()
      return
    }
    if (event.key === 'Enter' && onStep) {
      event.preventDefault()
      onStep(event.shiftKey ? -1 : 1)
    }
  }
</script>

<div class="find-bar" data-testid={testId}>
  <span class="find-bar-icon" aria-hidden="true"><Icon name="search" size={14} /></span>
  <input
    bind:this={input}
    class="find-bar-input"
    type="search"
    aria-label={ariaLabel}
    {placeholder}
    {value}
    oninput={(event) => onChange(event.currentTarget.value)}
    onkeydown={keydown}
  />
  <!--
    aria-live is on a container that exists from first render and is empty
    until there is a query. Three of the bars this replaces put aria-live on
    the count itself, so every keystroke re-announced a number — the reason the
    audit flagged them as screen-reader noise.
  -->
  <span class="find-bar-status" aria-live="polite">{status}</span>
  {#if steppable}
    <IconButton icon="chevron-up" label="Previous match" size="small" disabled={!query || !total} onclick={() => onStep?.(-1)} />
    <IconButton icon="chevron-down" label="Next match" size="small" disabled={!query || !total} onclick={() => onStep?.(1)} />
  {/if}
  {#if query}
    <IconButton icon="close" label="Clear search" size="small" onclick={() => { onChange(''); focus() }} />
  {/if}
</div>

<style>
  .find-bar {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    padding: var(--space-4) var(--space-8);
    border: 1px solid var(--border);
    border-radius: var(--radius-6);
    background: var(--surface);
    min-width: 0;
  }
  .find-bar:focus-within { border-color: var(--accent-border); box-shadow: 0 0 0 2px var(--focus-ring); }
  .find-bar-icon { display: flex; color: var(--muted); flex: none; }
  .find-bar-input {
    flex: 1 1 auto;
    min-width: 80px;
    border: none;
    background: transparent;
    padding: 0;
    font-size: var(--font-size-12);
    color: var(--text);
  }
  .find-bar-input:focus { outline: none; }
  /* The browser's own search affordance duplicates the Clear button. */
  .find-bar-input::-webkit-search-cancel-button { display: none; }
  .find-bar-status { flex: none; color: var(--muted); font-size: var(--font-size-11); white-space: nowrap; }
</style>
