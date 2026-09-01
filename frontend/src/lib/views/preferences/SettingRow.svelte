<script lang="ts">
  import type { Snippet } from 'svelte'

  /**
   * One setting. All of them, everywhere in Preferences.
   *
   * WHAT WENT WRONG. There was no settings-row component, so the eight sections
   * grew four incompatible row anatomies between them: General put a checkbox
   * and its text in an `.inline-toggle`, Cache presented the same kind of
   * boolean as a bordered card with a bold title and a paragraph, Proxy laid
   * label/value pairs into a `.field-grid` — and dropped a bare checkbox into
   * the value column of one — and Keybindings abandoned rows for a spreadsheet
   * table. Scrolling from General to Cache to Keybindings crosses three visual
   * grammars in a few hundred pixels, which is the literal mechanism behind the
   * "every section looks like a different app" complaint. Nothing told the eye
   * these were all settings, because nothing in the code said so either.
   *
   * THREE PRESENTATIONS, ONE ROW. They are presentations of the same component
   * on purpose — a section cannot pick a fourth:
   *
   *   boolean   `onCheckedChange` is supplied, so the row owns the checkbox and
   *             renders it inline with its text, the app's one boolean control.
   *   field     the caller supplies the control; the label sits left of it.
   *   stacked   `stacked`, for a control too wide for a column — the theme
   *             variant cards, the keybindings table.
   *
   * The description is NEVER beside the control. A sentence competing with an
   * input for the same column forces one of them narrow, and it is always the
   * sentence that loses; below the row it can run to a comfortable measure
   * without touching the control's width at all.
   *
   * The row does not cap its own width — SettingSection does that once for the
   * whole panel. A row that knew its own maximum is how the panel ended up with
   * five different ones.
   */
  type Props = {
    /** Always visible. For a boolean row this is the text beside the checkbox. */
    label: string
    /** Optional help text, rendered under the whole row at reduced size. */
    description?: string
    /**
     * A one-word qualifier attached to the name — "Beta" today. It is a prop
     * rather than markup the caller smuggles into `label` because the moment a
     * label can contain arbitrary markup, one row's name stops looking like
     * every other row's.
     */
    badge?: string
    /** Dims the row. The caller still has to disable the control itself. */
    disabled?: boolean
    /**
     * The row is waiting on a write that has not round-tripped yet.
     *
     * WHY A PREFERENCES ROW NEEDS THIS AT ALL. The panel writes instantly and
     * has no Save button, which is correct — but "instant" is the intent, not
     * the timing. Several of these settings go to Go and back: enabling the
     * file cache saves and then re-measures the cache, clearing it is a disk
     * walk, and turning AI access on binds a socket. Until the round trip lands
     * the state the control renders is still the OLD state, so the checkbox
     * the user just clicked springs back under their finger and nothing on
     * screen says why. That is the same finding as A6-11 on the response side:
     * an async action whose only feedback is that, eventually, something
     * changes.
     *
     * A word rather than a spinner, and inside the label rather than beside the
     * control, so it works identically for all three row presentations without
     * any of them needing a grid cell only some rows have.
     */
    busy?: boolean
    /** What the wait is, in the user's words. */
    busyLabel?: string
    /**
     * The id of the caller's control. Present, the label is a real `<label
     * for=...>`; absent, it degrades to a `<span>` rather than emitting a
     * `<label>` that points at nothing.
     */
    labelFor?: string
    /** Boolean rows: supplying this handler is what makes the row a boolean. */
    onCheckedChange?: (checked: boolean) => void
    checked?: boolean
    checkboxId?: string
    /** Only when the visible text is not the name the control should announce. */
    checkboxAriaLabel?: string
    /** Exactly one interactive element, or one tightly-coupled group. */
    control?: Snippet
    /** Puts the control on its own full-width line beneath the label. */
    stacked?: boolean
    /**
     * Spelled as the attribute rather than as `testId` so a call site still
     * reads `data-testid="ssl-verification-toggle"`. Several suites grep the
     * .svelte sources for that literal string — the repo has no render harness
     * — and a prop named anything else would have silently emptied them while
     * every test still passed.
     *
     * It lands on the checkbox for a boolean row and on the row itself
     * otherwise, because on a boolean row the checkbox is the only thing worth
     * selecting.
     */
    'data-testid'?: string
  }

  let {
    label,
    description = undefined,
    badge = undefined,
    disabled = false,
    busy = false,
    busyLabel = 'Saving…',
    labelFor = undefined,
    onCheckedChange = undefined,
    checked = false,
    checkboxId = undefined,
    checkboxAriaLabel = undefined,
    control = undefined,
    stacked = false,
    'data-testid': testId = undefined,
  }: Props = $props()

  const isBoolean = $derived(typeof onCheckedChange === 'function')
</script>

<div class="setting-row" class:stacked class:settings-disabled={disabled} aria-busy={busy ? 'true' : undefined} data-testid={isBoolean ? undefined : testId}>
  {#if isBoolean}
    <!--
      The checkbox lives inside its own label so the text is part of the hit
      target. Making the text a separate cell would halve the click area of
      every boolean in Preferences.
    -->
    <label class="inline-toggle setting-row-label" class:full={!control}>
      <input
        type="checkbox"
        id={checkboxId}
        data-testid={testId}
        aria-label={checkboxAriaLabel}
        {checked}
        {disabled}
        onchange={(event) => onCheckedChange?.(event.currentTarget.checked)}
      />
      {label}{#if badge}<span class="beta-badge">{badge}</span>{/if}{#if busy}<span class="setting-row-busy" role="status">{busyLabel}</span>{/if}
    </label>
  {:else if labelFor}
    <label class="setting-row-label" class:full={stacked} for={labelFor}>
      {label}{#if badge}<span class="beta-badge">{badge}</span>{/if}{#if busy}<span class="setting-row-busy" role="status">{busyLabel}</span>{/if}
    </label>
  {:else}
    <span class="setting-row-label" class:full={stacked}>
      {label}{#if badge}<span class="beta-badge">{badge}</span>{/if}{#if busy}<span class="setting-row-busy" role="status">{busyLabel}</span>{/if}
    </span>
  {/if}

  {#if control}
    <div class="setting-row-control" class:full={stacked}>{@render control()}</div>
  {/if}

  {#if description}
    <p class="setting-row-description">{description}</p>
  {/if}
</div>

<style>
  /*
    ONE LABEL COLUMN WIDTH for the whole panel. The sections previously used
    140px, 180px and 120px depending on which grid class they had reached for,
    so labels that sit in one vertical list on screen did not share a right
    edge. 200px is the width at which the longest existing label — "SSL/TLS
    Certificate Verification" — still wraps to two lines, which is fine; what is
    not fine is each section picking its own.
  */
  .setting-row {
    display: grid;
    grid-template-columns: 200px minmax(0, 1fr);
    column-gap: var(--space-12);
    row-gap: var(--space-4);
    align-items: center;
    min-width: 0;
  }

  /* Label above, control below — the row still exists, it just wraps. */
  .setting-row.stacked {
    grid-template-columns: minmax(0, 1fr);
  }

  /*
    ONE LABEL VOICE. A boolean's text came from `.inline-toggle` — 13px, weight
    800, --text-muted — while a field's label came from `.field-grid
    .field-label` at 12px, weight 700, --muted. Two settings one line apart were
    named in two different typefaces of the same font. Worse, `.field-label`
    only inherits that rule INSIDE a `.field-grid`, so Display's and
    Appearance's labels, which sat in bespoke grids, were styled by nothing at
    all and rendered as plain body text.

    The toggle's treatment wins because it is the one this component cannot
    override — `.inline-toggle` is a global class — so the field labels are
    matched to it rather than the other way round.
  */
  .setting-row-label {
    min-width: 0;
    color: var(--text-muted);
    font-size: var(--font-size-13);
    font-weight: 800;
  }

  .setting-row-label.full,
  .setting-row-control.full {
    grid-column: 1 / -1;
  }

  .setting-row-control {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
  }

  /*
    THE CONTROL CAPS, DEFINED ONCE. Written with :global because the control is
    the caller's markup and carries the caller's scope class, not this file's —
    without it these rules compile to selectors that match nothing and every
    number input goes back to being as wide as its section chose to make it.

    A port or a font size is at most five characters; letting it stretch to the
    full column says the field expects a sentence. A select is sized by its
    longest option, and none of them here is long.
  */
  .setting-row-control :global(input[type='number']) {
    max-width: 120px;
  }

  .setting-row-control :global(select) {
    max-width: 260px;
  }

  /* Free text — a font name, a host, a path — takes the rest of the column. */
  .setting-row-control :global(input:not([type])),
  .setting-row-control :global(input[type='text']),
  .setting-row-control :global(input[type='password']) {
    flex: 1 1 auto;
    min-width: 0;
  }

  /*
    Sits with the name, not with the control, and reads as a status rather than
    as a second label: lighter than the row's own 800 weight so it cannot be
    mistaken for part of the setting's name.
  */
  .setting-row-busy {
    margin-left: var(--space-6);
    color: var(--muted);
    font-size: var(--font-size-11);
    font-weight: 700;
  }

  /*
    Full row width and its own measure, under the control rather than beside it.
    62ch is the same measure the hint paragraphs used before this component
    existed, kept so the migration changed no line breaks.
  */
  .setting-row-description {
    grid-column: 1 / -1;
    max-width: 62ch;
    margin: 0;
    color: var(--muted);
    font-size: var(--font-size-12);
    line-height: 1.5;
  }

  /*
    Below the width at which a 200px label column and a control still fit side
    by side, every row becomes stacked.

    This was 720px, matched to the 720px block style.css
    used to re-stack the settings grids — but every rule in that block targets
    one of the anatomies this component retired (`.default-location-control`,
    `.cache-preference-card`, `.keybindings-preference-section`,
    `.keybindings-section-actions`, `.keybindings-disclosure summary`), so the
    block is dead and 720 was a breakpoint kept in step with nothing. It is now
    the shell's own compact step from `layout.ts` — 680, the width `style.css`
    already re-pads `.request-command-meta` and re-stacks the git grids at — so
    the panel changes shape when the shell around it does. `layout.test.mts`
    fails if this number leaves that scale.
  */
  @media (max-width: 680px) {
    .setting-row {
      grid-template-columns: minmax(0, 1fr);
    }

    .setting-row-control {
      grid-column: 1 / -1;
    }
  }
</style>
