<script lang="ts">
  /**
   * What the collections tree says when it draws no rows.
   *
   * IT USED TO SAY "No matching requests" FOR TWO COMPLETELY DIFFERENT
   * SITUATIONS. `sidebarCollections(workspace, query)` returns the workspace's
   * collections untouched when the query is empty, so an empty tree means
   * either "your filter excluded everything" or "you have never created a
   * collection" — and the markup had one branch for both. The consequence is
   * that the FIRST screen a new user sees reports a failed search they never
   * ran, at the exact moment the app should be telling them what to do.
   *
   * Two causes, two states. The filtered case is one muted line, because the
   * user knows what they typed and the fix is to type something else. The
   * first-run case gets a second line naming the control that starts things,
   * because there is nothing else on that screen to look at.
   *
   * NO SECOND "+ New" BUTTON HERE, on purpose. SidebarHeader's primary button
   * sits about forty pixels above this text and does exactly this job; a
   * duplicate would be a second primary action in one 280px column, and the
   * two would then have to be kept in step. The hint points at the real one
   * instead — which is also why it names the button's own label verbatim.
   */
  import { emptyStateMessage, isFilteredEmpty } from './emptyState'

  type Props = {
    /** The live sidebar filter. Its emptiness is what picks the state. */
    query: string
    /**
     * Plural noun for what the tree holds when nothing is filtered. The tree
     * lists collections, so a first-run message about "requests" would name a
     * thing the user cannot create until a collection exists.
     */
    noun?: string
  }

  let { query, noun = 'collections' }: Props = $props()

  const filtered = $derived(isFilteredEmpty(query))
  const message = $derived(emptyStateMessage({ query, noun }))
</script>

<div class="sidebar-empty" data-testid="sidebar-empty">
  <p class="sidebar-empty-message">{message}</p>
  {#if !filtered}
    <!--
      A tabindex-free, control-free hint. Screen readers reach it through the
      tree's own container, and it deliberately names the two ways in that
      already exist rather than inventing a third.
    -->
    <p class="sidebar-empty-hint">Create one with <strong>+ New</strong> above, or import an existing collection.</p>
  {/if}
</div>

<style>
  /*
    `.sidebar-empty` itself is styled in style.css — padding, muted colour,
    12px — and that rule still applies, because this element carries the class.
    Only the two-line shape is new, so only the two-line shape is here.
  */
  .sidebar-empty-message {
    margin: 0;
  }

  .sidebar-empty-hint {
    margin: var(--space-6) 0 0;
    font-size: var(--font-size-11);
    /* Quieter than the message above it: the sentence is the answer, the hint
       is where to go next, and they must not read as equally important. */
    opacity: 0.8;
    line-height: 1.45;
  }

  .sidebar-empty-hint strong {
    color: var(--rail-text);
    font-weight: 700;
  }
</style>
