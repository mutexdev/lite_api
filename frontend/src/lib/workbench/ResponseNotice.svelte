<script lang="ts">
  import type { Snippet } from 'svelte'

  /**
   * "The pane cannot show you a body right now — here is why, and what to do."
   *
   * The audit counted four visually distinct containers saying exactly that
   * inside this one component: a plain centred block for the resting state, a
   * bordered `role="alert"` box for a TLS failure, a bordered strip with no
   * card treatment for a send error or an oversized preview, and a
   * radius-and-background card for a binary body. Four answers to one question,
   * on four different screens of the same pane, so nothing on screen taught the
   * reader what any of the treatments MEANT — the differences were an accident
   * of the order they were written in.
   *
   * The rule this file encodes, and the whole rule:
   *
   *   NOTHING HAS HAPPENED YET  → the app's shared `.empty-state`. No tone, no
   *   remedy, nothing wrong. "Ready for a response", "No console output".
   *
   *   SOMETHING HAPPENED AND THE BODY IS NOT WHAT YOU EXPECTED → this, with a
   *   tone. It replaces the body, states the cause in the title, and holds
   *   whatever action fixes it.
   *
   *   THE BODY IS FINE BUT PARTIAL → neither. That is the truncation strip
   *   under the toolbar, which ACCOMPANIES a body rather than replacing one,
   *   and a card there would look like a fault.
   *
   * The three tones are the only variation, and each is a claim about the
   * response rather than about the severity of the prose: `error` means the
   * request did not produce a body, `warning` means it did and the pane will
   * not render it, `info` means it did and the pane rendered something else
   * about it instead.
   */
  type Props = {
    tone?: 'info' | 'warning' | 'error'
    /** The cause, in a sentence. Shown as the notice's heading line. */
    title?: string
    /**
     * Announced by assistive technology when the notice appears.
     *
     * Only for notices that appear in REACTION to something the user did —
     * an error, a cancellation. A binary body is a property of the response,
     * not an event, and announcing it would talk over the status the shell
     * already announced.
     */
    role?: 'alert' | 'status' | undefined
    ariaLabel?: string
    testId?: string
    children?: Snippet
  }

  let { tone = 'info', title = '', role = undefined, ariaLabel = undefined, testId = undefined, children }: Props = $props()
</script>

<section
  class="response-notice"
  class:warning={tone === 'warning'}
  class:error={tone === 'error'}
  {role}
  aria-label={ariaLabel}
  data-testid={testId}
>
  {#if title}<strong class="response-notice-title">{title}</strong>{/if}
  {@render children?.()}
</section>

<style>
  .response-notice {
    display: grid;
    gap: var(--space-8);
    margin: var(--space-10);
    padding: var(--space-10) var(--space-12);
    border: 1px solid var(--border);
    border-radius: var(--radius-7);
    background: var(--surface-raised);
    color: var(--muted);
    font-size: var(--font-size-12);
  }
  /*
    Tone is carried by the border and the heading, not by the background alone.
    A tinted background is the first thing lost to a high-contrast setting, and
    it is the only signal three of the four containers this replaces had.
  */
  .response-notice.warning { border-color: var(--warning-border); background: var(--warning-bg-soft); }
  .response-notice.error { border-color: var(--danger-border); background: var(--danger-bg); }
  .response-notice-title { color: var(--text); font-size: var(--font-size-13); }
  .response-notice.warning .response-notice-title { color: var(--warning-strong); }
  .response-notice.error .response-notice-title { color: var(--danger); }
  .response-notice :global(p) { margin: 0; overflow-wrap: anywhere; }
</style>
