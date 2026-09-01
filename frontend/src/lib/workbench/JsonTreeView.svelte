<script lang="ts">
  import { findMatches } from './response'
  import { highlightSegments } from './bodyHighlight'
  import { JSON_TREE_BUDGET, JSON_TREE_MAX_ENTRIES, jsonTreeNodeMatches, type JsonTree, type JsonTreeEntry } from './jsonTree'

  /**
   * The response body as an expandable tree.
   *
   * What was here before was a list of the ROOT keys, each holding one
   * `<pre>{JSON.stringify(value, null, 2)}</pre>`. A control labelled "JSON
   * tree" therefore offered one level of accordion over the same flat text the
   * body view already showed, in the same absence of colour — so opening a
   * field bought indentation and nothing else, while the request editor one
   * pane over rendered the identical document fully coloured and structured.
   *
   * Two things make this a tree rather than a second flat list:
   *
   *   It recurses. `jsonTree.ts` walks the whole value under four explicit
   *   bounds, so a 100 MB response yields a bounded tree that SAYS it is
   *   bounded rather than a frozen window — the failure mode that makes "just
   *   expand everything" the wrong fix.
   *
   *   It is painted by `bodyHighlight.ts`, the same scanner and therefore the
   *   same `--syntax-*` tokens as the body view directly above it. Each leaf's
   *   text is already JSON (`"Ada"`, `7`, `null`), so it is handed to the JSON
   *   scanner verbatim and lands in the identical colour it has in the body.
   *   There is no second palette here to keep in step, which is the whole point.
   */
  type Props = {
    tree: JsonTree
    /** The find bar's current query. Opens matching branches and paints hits. */
    query?: string
    testId?: string
  }

  let { tree, query = '', testId = undefined }: Props = $props()

  const needle = $derived(query.trim().toLowerCase())

  /**
   * The branches a search has forced open.
   *
   * Computed in ONE depth-first pass rather than asked per row. "Does this
   * subtree contain a hit?" answered per node is quadratic, and at the four
   * thousand nodes the tree is allowed to reach that is sixteen million
   * comparisons on every keystroke — a search box that stutters as you type.
   */
  const openPaths = $derived.by(() => {
    const paths = new Set<string>()
    if (needle) collectOpenPaths(tree.entries, needle, paths)
    return paths
  })

  function collectOpenPaths(nodes: JsonTreeEntry[], search: string, paths: Set<string>): boolean {
    let found = false
    for (const node of nodes) {
      const inside = collectOpenPaths(node.children, search, paths)
      if (jsonTreeNodeMatches(node, search) || inside) {
        paths.add(node.path)
        found = true
      }
    }
    return found
  }

  /**
   * A leaf's value, coloured and search-marked in one pass.
   *
   * Same call the body view makes, with the per-leaf match offsets rather than
   * the document's: a hit lands inside a string exactly as often here, and
   * merging the two features into one segment list is what keeps the colour on
   * a highlighted character. See bodyHighlight.ts.
   */
  function valueSegments(text: string) {
    return highlightSegments(text, 'json', needle ? findMatches(text, needle) : [], needle.length)
  }

  /** A field name, marked where the query hits it. Never coloured as data. */
  function nameSegments(name: string) {
    return highlightSegments(name, 'plain', needle ? findMatches(name, needle) : [], needle.length)
  }
</script>

<!--
  The row snippet calls itself for its children. A recursive snippet rather than
  a self-importing component because the recursion is the only thing being
  reused — a second component file would add a props boundary and a second place
  for the depth accounting to be wrong.
-->
{#snippet row(entry: JsonTreeEntry, depth: number)}
  {#if entry.children.length > 0}
    <details class="json-node" open={depth === 0 || openPaths.has(entry.path)}>
      <summary class="json-row">
        <span class="json-name">{#each nameSegments(entry.name) as segment, index (index)}{#if segment.match}<mark>{segment.text}</mark>{:else}{segment.text}{/if}{/each}</span>
        <span class="json-summary">{entry.summary}</span>
      </summary>
      <div class="json-children">
        {#each entry.children as child (child.path)}{@render row(child, depth + 1)}{/each}
        <!--
          A container whose children were cut short says so ON the container.
          Stated once at the bottom of the panel instead, a reader had no way to
          tell WHICH branch had stopped — so a list that ended at item 100 read
          as a list with 100 items.
        -->
        {#if entry.collapsed}<p class="json-note">Showing {entry.children.length} of {entry.childCount}.</p>{/if}
      </div>
    </details>
  {:else}
    <div class="json-node json-row json-leaf">
      <span class="json-name">{#each nameSegments(entry.name) as segment, index (index)}{#if segment.match}<mark>{segment.text}</mark>{:else}{segment.text}{/if}{/each}</span>
      {#if entry.text}
        <span class="json-value">{#each valueSegments(entry.text) as segment, index (index)}{#if segment.match}<span class={segment.kind === 'plain' ? undefined : `response-token-${segment.kind}`}><mark>{segment.text}</mark></span>{:else if segment.kind === 'plain'}{segment.text}{:else}<span class={`response-token-${segment.kind}`}>{segment.text}</span>{/if}{/each}</span>
      {:else}
        <span class="json-summary">{entry.summary}{entry.collapsed ? ' · not expanded' : ''}</span>
      {/if}
    </div>
  {/if}
{/snippet}

<div class="json-tree" aria-label="Bounded JSON tree" data-testid={testId}>
  {#each tree.entries as entry (entry.path)}{@render row(entry, 0)}{/each}
  {#if tree.entries.length === 0 && tree.truncated}
    <p class="json-note">The first field alone is larger than the {Math.round(JSON_TREE_BUDGET / 1024)} KB this view renders. Use the Pretty or Raw view to read it.</p>
  {:else if tree.entries.length === 0}
    <p class="json-note">This response has no fields to expand.</p>
  {:else if tree.truncated}
    <p class="json-note">Tree render is bounded to {JSON_TREE_MAX_ENTRIES} children per field and {Math.round(JSON_TREE_BUDGET / 1024)} KB of values.</p>
  {/if}
</div>

<style>
  .json-tree {
    max-height: clamp(360px, 54vh, 480px);
    overflow: auto;
    padding: var(--space-10);
    font-family: var(--code-font-family);
    font-size: var(--code-font-size);
    line-height: 1.5;
  }
  /*
    Every row is one line that scrolls, not a paragraph that wraps. A tree reads
    by scanning the left edge for names, and a long string value that rewraps
    under its own name puts the next name three lines below where the eye is.
  */
  .json-row {
    display: flex;
    align-items: baseline;
    gap: var(--space-6);
    padding: var(--space-2) var(--space-4);
    border-radius: var(--radius-4);
    min-width: 0;
  }
  .json-row:hover { background: color-mix(in srgb, var(--selected-bg) 55%, transparent); }
  summary.json-row { cursor: pointer; }
  summary.json-row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .json-name { color: var(--syntax-key); white-space: nowrap; }
  .json-summary { color: var(--muted); font-size: var(--font-size-11); }
  .json-value { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: pre; }
  /*
    The guide line is what makes depth readable once a tree is more than two
    levels deep; indentation alone leaves the reader counting pixels.
  */
  .json-children {
    margin-left: var(--space-7);
    padding-left: var(--space-8);
    border-left: 1px solid var(--border-subtle);
  }
  .json-note { margin: var(--space-4) 0 0; color: var(--muted); font-size: var(--font-size-11); }
  /*
    The find highlight, in the app's own tokens.

    A bare `<mark>` is browser-yellow with black text, which overrides the
    syntax colour underneath it and is the one thing on screen belonging to no
    theme. These are the same three values the body view and the request
    editor's find bar use for a non-current hit — the tree has no current hit
    to distinguish, because there is no linear order to step through.
  */
  @media (min-width: 1200px) and (min-height: 820px) {
    .json-tree { max-height: clamp(480px, 65vh, 720px); }
  }
</style>
