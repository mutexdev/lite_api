<script lang="ts">
  /**
   * The one header every full-pane view uses.
   *
   * Before this, each view built its own: Environments put two "create"
   * forms inside its header, Import carried an "Export active" button that
   * belongs to a collection, Dev Tools had a decorative subtitle repeating
   * its own tab names, and Runner, Collection and Preferences each aligned
   * their actions differently. The user learned nothing from one that
   * transferred to the next.
   *
   * The contract mirrors PaneToolbar's: what am I looking at (title) on the
   * left, what can I do to it (actions) on the right, live facts (meta) in
   * between. A subtitle is allowed only when it carries information the
   * title does not — a path, a count, a state — never a description of the
   * view.
   */
  import type { Snippet } from 'svelte'

  type Props = {
    title: string
    /** Information, not decoration: "bru · 2 requests", not "Console · Network". */
    subtitle?: string
    /** Live facts about the view: counts, running state. Truncates first. */
    meta?: Snippet
    /** Buttons. Primary action last. */
    actions?: Snippet
    testId?: string
    /**
     * Names the heading so the pane can point `aria-labelledby` at it, and
     * hands the element back so the view can move focus there.
     *
     * Both exist for the same case and it is not decorative: the Git workbench
     * opens from a command, with no click to carry focus, and parks the caret
     * on its heading so a screen reader announces which pane just replaced the
     * request. Without these the conversion to PageHeader would have moved that
     * pane's focus target out of reach and lost the behaviour silently.
     */
    titleId?: string
    titleRef?: HTMLHeadingElement | null
  }

  let {
    title,
    subtitle = '',
    meta,
    actions,
    testId = undefined,
    titleId = undefined,
    titleRef = $bindable(null),
  }: Props = $props()
</script>

<header class="page-header" data-testid={testId}>
  <div class="page-header-title">
    <!-- tabindex is the literal -1 rather than a conditional one because a
         computed value defeats svelte-check's a11y rule, which cannot see that
         the number is negative and reports a heading in the tab order. -1 only
         makes the heading a legal focus() target; it stays out of tab order. -->
    <h2 id={titleId} tabindex="-1" bind:this={titleRef}>{title}</h2>
    {#if subtitle}<p>{subtitle}</p>{/if}
  </div>
  {#if meta}<div class="page-header-meta">{@render meta()}</div>{/if}
  {#if actions}<div class="page-header-actions">{@render actions()}</div>{/if}
</header>

<style>
  .page-header {
    display: flex;
    align-items: center;
    gap: var(--space-12);
    min-height: 36px;
    margin-bottom: var(--space-14);
  }

  .page-header-title {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    min-width: 0;
  }

  .page-header-title h2 {
    margin: 0;
    font-size: var(--font-size-18);
    line-height: 1.2;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .page-header-title p {
    margin: 0;
    color: var(--muted);
    font-size: var(--font-size-12);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .page-header-meta {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
    flex: 1 1 auto;
    overflow: hidden;
    white-space: nowrap;
    color: var(--muted);
    font-size: var(--font-size-12);
  }

  .page-header-actions {
    display: flex;
    align-items: center;
    gap: var(--space-6);
    flex: none;
    margin-left: auto;
    /* An action label must never wrap. The Cookies header put a search box and
       "Clear all" here and the label broke across two lines, which made the
       button 40px tall in a 36px header and read as two stacked controls. The
       meta slot beside it is the part that is allowed to give way. */
    white-space: nowrap;
  }
</style>
