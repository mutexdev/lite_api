<script lang="ts">
  /**
   * A field holding a credential, with one way to look at it.
   *
   * A5-08. The app had two postures on the same concept. A `{{secretVar}}`
   * reference got a tooltip with an explicit Show/Hide button. A literal secret
   * typed straight into a field — Bearer token, Client secret, AWS secret key,
   * API key value — got a bare `type="password"` with no reveal at all, so the
   * only way to check what you had pasted was to clear it and paste again.
   *
   * That is backwards: most Auth-tab secrets are literal text, so the case with
   * no affordance was the common one.
   *
   * The button says "Show" and "Hide" rather than carrying an eye glyph, and
   * that is deliberate rather than a shortfall: those are the exact words the
   * variable tooltip already uses for this exact action. One vocabulary for one
   * gesture was the point of the exercise; a second, wordless idiom for the same
   * thing would have been a fourth widget where the audit found three.
   *
   * Revealing is per-field and resets on every remount. Nothing about "I looked
   * at this secret" is worth persisting, and a reveal that survived a tab switch
   * would put a credential back on screen without anyone asking.
   */
  type Props = {
    value: string
    onChange: (value: string) => void
    ariaLabel: string
    placeholder?: string
    /** Fires on every keystroke rather than on commit. Matches the field it replaces. */
    live?: boolean
    id?: string
    disabled?: boolean
    testId?: string
  }

  let {
    value,
    onChange,
    ariaLabel,
    placeholder = undefined,
    live = false,
    id = undefined,
    disabled = false,
    testId = undefined
  }: Props = $props()

  let revealed = $state(false)

  // An empty field has nothing to reveal, and offering the toggle anyway is a
  // control that visibly does nothing the first time anyone tries it.
  const hasValue = $derived(String(value ?? '') !== '')
</script>

<span class="secret-field">
  <input
    {id}
    {placeholder}
    {disabled}
    data-testid={testId}
    aria-label={ariaLabel}
    type={revealed ? 'text' : 'password'}
    autocomplete="off"
    spellcheck="false"
    value={value ?? ''}
    oninput={live ? (event) => onChange(event.currentTarget.value) : undefined}
    onchange={live ? undefined : (event) => onChange(event.currentTarget.value)}
  />
  {#if hasValue}
    <button
      type="button"
      class="secret-toggle-button"
      aria-pressed={revealed}
      onclick={() => (revealed = !revealed)}
    >{revealed ? 'Hide' : 'Show'}</button>
  {/if}
</span>

<style>
  /*
    The toggle sits beside the input rather than inside it. Overlaying it would
    have meant reserving padding on an input whose width is set by the shared
    .field-grid, and a credential long enough to matter is exactly the one that
    would then run under the button.
  */
  .secret-field {
    display: flex;
    align-items: center;
    gap: var(--space-6);
    min-width: 0;
  }

  .secret-field input {
    flex: 1 1 auto;
    min-width: 0;
  }

  .secret-field .secret-toggle-button {
    flex: none;
  }
</style>
