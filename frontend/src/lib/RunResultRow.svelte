<script lang="ts">
  import type { Snippet } from 'svelte'
  import { toneClass, toneLabel, type StatusTone } from './statusTone'

  /**
   * One row of "a request ran, here is what came back".
   *
   * THE ANATOMY IS THE RESPONSE TIMELINE'S, on purpose. A8-03 found four
   * independent widgets drawing this same event; the Timeline is the one that
   * cannot be changed (it lives in the workbench, not here), so it is the
   * pattern the other three move onto rather than a fifth being invented:
   *
   *     [badge] [ status ] [method] [ what was called ..... ] [ metrics ]
   *     └─ one button, the whole width, aria-expanded ────────────────────┘
   *     [ detail, when open ]
   *
   * THE BADGE IS FLOW'S CHIP, PROMOTED. Flow already had the one idiom the
   * other two needed and nobody else could reach — a pill saying pending /
   * running / passed / failed — because a run has something a single response
   * does not: a row for a step that has not happened yet, and a verdict that
   * can disagree with the status code (a 200 that failed an assertion). It
   * lives here now so the Runner's four verdict words get the same pill rather
   * than a fifth grammar.
   *
   * The badge column is rendered even when empty. Two rows whose columns start
   * at different x-positions stop being scannable as a list, which is the whole
   * reason to have a shared row at all.
   *
   * ONE BUTTON, NOT A ROW OF THEM. The summary is a single control because the
   * whole row is one target — a row split into three focusable pieces costs
   * three tab presses per result, and a forty-request run becomes a hundred and
   * twenty. Per-row actions (Open in tab, Save to collection) live in the
   * detail region, where they are reachable only once the row is open and so
   * cost nothing to walk past.
   *
   * COLOUR IS NEVER THE ONLY SIGNAL. The status cell carries a visually hidden
   * word from toneLabel() beside the code, so "302" is announced as "302, needs
   * attention" rather than as a number whose amber a screen reader cannot see.
   *
   * A row with no detail renders as a plain div rather than a dead button:
   * an expander that expands to nothing is worse than no expander, and the
   * Runner's skipped rows genuinely have nothing to show.
   */
  type Props = {
    /** The grade of the STATUS CELL. Drives its colour class and spoken word. */
    tone: StatusTone
    /** The status column: a code, a verdict word, or an em dash. */
    status: string
    /**
     * The run's own verdict, which can disagree with the status code.
     * 'active' is the in-flight state — not a grade, so it is not a StatusTone.
     */
    badge?: { label: string; tone: StatusTone | 'active' } | undefined
    /** Surface-specific test ids, so an existing hook survives the move here. */
    statusTestId?: string
    badgeTestId?: string
    /** Optional HTTP method badge, styled by the app's shared `.method` rule. */
    method?: string
    /** The main column — a request name, a URL, a step id. */
    title: string
    /** Tooltip for a title that will be truncated. */
    titleTitle?: string
    /** Secondary line under the title, for a name or a note. */
    subtitle?: string
    /** Right-hand metrics, already formatted. Joined with a middle dot. */
    metrics?: readonly string[]
    expanded?: boolean
    onToggle?: () => void
    /** Marks the one row that stopped a run. */
    emphasis?: 'none' | 'danger'
    detail?: Snippet
    testId?: string
  }

  let {
    tone,
    status,
    badge = undefined,
    statusTestId = undefined,
    badgeTestId = undefined,
    method = '',
    title,
    titleTitle = undefined,
    subtitle = '',
    metrics = [],
    expanded = false,
    onToggle = undefined,
    emphasis = 'none',
    detail = undefined,
    testId = undefined,
  }: Props = $props()

  const expandable = $derived(Boolean(detail && onToggle))
  const announced = $derived(toneLabel(tone))
</script>

<!--
  Declared once and rendered into either wrapper below. Written out twice, the
  expandable and the static row would be free to drift into two column
  templates, which is precisely the failure this component exists to end.
-->
{#snippet summary()}
  <span class="run-result-badge-cell">
    {#if badge}<span class={`run-result-badge tone-${badge.tone}`} data-testid={badgeTestId}>{badge.label}</span>{/if}
  </span>
  <span class={`run-result-status ${toneClass(tone)}`} data-testid={statusTestId}>
    {status}{#if announced}<span class="sr-only"> {announced}</span>{/if}
  </span>
  <span class="run-result-method">{#if method}<span class="method" data-method={method}>{method}</span>{/if}</span>
  <span class="run-result-title" title={titleTitle ?? title}>{title}</span>
  <span class="run-result-metrics">{metrics.join(' · ')}</span>
  {#if subtitle}<small class="run-result-subtitle muted">{subtitle}</small>{/if}
{/snippet}

<li class="run-result-row" class:danger={emphasis === 'danger'} data-testid={testId}>
  {#if expandable}
    <button
      type="button"
      class="run-result-summary"
      aria-expanded={expanded}
      onclick={() => onToggle?.()}
    >{@render summary()}</button>
  {:else}
    <!--
      Same grid, same columns, no control. A row with nothing behind it must
      still line its status and metrics up with the rows above and below it —
      the moment one row's columns shift, the list stops being scannable.
    -->
    <div class="run-result-summary">{@render summary()}</div>
  {/if}

  {#if expandable && expanded}
    <div class="run-result-detail">{@render detail?.()}</div>
  {/if}
</li>

<style>
  .run-result-row {
    display: grid;
    gap: var(--space-6);
    padding: var(--space-8) var(--space-10);
    border: 1px solid var(--border);
    border-radius: var(--radius-6);
    background: var(--surface);
    min-width: 0;
  }

  /* The one row the eye should land on in a failed run. A left rule rather than
     a red fill, matching FlowRunPanel's stopper card: the row still has to be
     readable, and a tinted block of error text is not. */
  .run-result-row.danger {
    border-color: var(--danger-border);
    border-left: 3px solid var(--danger-strong);
    background: var(--danger-bg-soft);
  }

  /*
    The Timeline's column template, kept to the pixel: a status wide enough for
    "cancelled", a method slot, the elastic title, and metrics that never wrap.
    Two lists side by side with different templates do not read as one app.
  */
  .run-result-summary {
    display: grid;
    grid-template-columns: auto minmax(60px, auto) minmax(0, 52px) minmax(0, 1fr) auto;
    gap: var(--space-8);
    align-items: baseline;
    width: 100%;
    padding: 0;
    border: none;
    background: none;
    text-align: left;
    min-width: 0;
  }

  .run-result-badge-cell,
  .run-result-method {
    display: inline-flex;
    align-items: center;
    min-width: 0;
  }

  /* Flow's four chips, one grammar, now available to all three surfaces.
     Pending — the toneless default — is deliberately the quietest: in a long
     run most rows are pending most of the time, and a tray of loud grey badges
     would drown the one row that is moving. */
  .run-result-badge {
    display: inline-flex;
    align-items: center;
    padding: 1px var(--space-8);
    border: 1px solid var(--border);
    border-radius: var(--radius-pill);
    background: var(--surface-alt);
    color: var(--muted-strong);
    font-size: var(--font-size-11);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.02em;
    white-space: nowrap;
  }

  .run-result-badge.tone-active {
    border-color: var(--accent-border);
    background: var(--accent-soft);
    color: var(--accent-strong);
  }

  .run-result-badge.tone-success {
    border-color: color-mix(in srgb, var(--accent) 40%, transparent);
    background: var(--success-bg);
    color: var(--accent-strong);
  }

  .run-result-badge.tone-warning {
    border-color: var(--warning-border);
    background: var(--warning-bg-soft);
    color: var(--warning-strong);
  }

  .run-result-badge.tone-danger {
    border-color: var(--danger-border);
    background: var(--danger-bg);
    color: var(--danger-strong);
  }

  .run-result-status {
    font-family: var(--code-font-family);
    font-size: var(--font-size-12);
    font-weight: 800;
  }

  /* The title is the one field that can be arbitrarily long — a URL with a
     query string forces the whole panel to scroll horizontally without this. */
  .run-result-title,
  .run-result-metrics {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .run-result-metrics {
    color: var(--muted-strong);
    font-family: var(--code-font-family);
    font-size: var(--font-size-11);
  }

  .run-result-subtitle {
    grid-column: 1 / -1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .run-result-detail {
    display: grid;
    gap: var(--space-6);
    padding-top: var(--space-6);
    border-top: 1px solid var(--border-subtle);
    min-width: 0;
  }

  /* The status word paired with the colour. See the note in the script block:
     the tone is otherwise invisible to anyone who cannot see it. */

  /* Below this the four columns cannot all hold a readable minimum, so the
     title takes its own line rather than every column being squeezed to an
     ellipsis at once. */
  @media (max-width: 720px) {
    .run-result-summary {
      grid-template-columns: auto minmax(60px, auto) minmax(0, 1fr);
    }
    .run-result-title,
    .run-result-metrics {
      grid-column: 1 / -1;
    }
  }
</style>
