<script lang="ts">
  import type { Snippet } from 'svelte'

  /**
   * The one bar that sits above a pane's content.
   *
   * Four auditors independently reported the same thing: every pane hand-rolls
   * its own toolbar, so the response body, response headers, the timeline, the
   * request body, the flow run panel and the devtools tabs each arrange their
   * controls differently. There is no component to blame — there was no
   * component at all.
   *
   * The rule this encodes, taken from what Postman, Insomnia and every editor
   * converge on: WHAT AM I LOOKING AT on the left, WHAT CAN I DO TO IT on the
   * right. Mode pickers, format selectors and view toggles are left; copy,
   * search, download and overflow are right, as icons.
   *
   * The middle slot is for status that must sit beside the mode — a byte count,
   * a validity message — and is deliberately allowed to shrink and truncate,
   * because it is the only part of the bar that may be sacrificed when the pane
   * gets narrow. The two ends never wrap: a toolbar that reflows into two rows
   * moves the buttons out from under the pointer that was heading for them.
   */
  type Props = {
    left?: Snippet
    middle?: Snippet
    right?: Snippet
    ariaLabel?: string
    /** Drops the bottom border, for a toolbar stacked directly on another. */
    seamless?: boolean
    testId?: string
  }

  let { left, middle, right, ariaLabel = undefined, seamless = false, testId = undefined }: Props = $props()
</script>

<div class="pane-toolbar" class:seamless role="toolbar" aria-label={ariaLabel} data-testid={testId}>
  <div class="pane-toolbar-group start">{@render left?.()}</div>
  <div class="pane-toolbar-status">{@render middle?.()}</div>
  <div class="pane-toolbar-group end">{@render right?.()}</div>
</div>

<style>
  .pane-toolbar {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    padding: var(--space-6) var(--space-10);
    border-bottom: 1px solid var(--border-subtle);
    background: var(--surface-alt);
    min-height: 38px;
  }
  .pane-toolbar.seamless { border-bottom: none; }
  .pane-toolbar-group { display: flex; align-items: center; gap: var(--space-6); flex: none; }
  .pane-toolbar-group.end { margin-left: auto; }
  /*
    min-width:0 is what actually lets the status truncate. Without it the flex
    item refuses to shrink below its content and the right-hand icon group gets
    pushed off the edge instead — the failure mode the audit found in the
    response toolbar, where a long byte count crowded out Download.
  */
  .pane-toolbar-status {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    min-width: 0;
    flex: 1 1 auto;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
    color: var(--muted);
    font-size: var(--font-size-11);
  }
</style>
