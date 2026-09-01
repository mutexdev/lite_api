<script module lang="ts">
  /**
   * The one "what kind of row is this, and what state is it in" mark.
   *
   * THE SIDEBAR HAD FOUR CONVENTIONS FOR ONE IDEA, and they were four different
   * HTML elements, not four different styles:
   *
   *   <small>Scratch</small> <small>Git</small>   a collection's status pills
   *   <em>temp</em> <em>draft</em>                a request's state
   *   <span class="example-glyph">Ex</span>       a saved example's type
   *   <span class="flow-glyph">Flow</span>        a flow's type
   *
   * The last two are the tell: `.example-glyph` and `.flow-glyph` are BYTE FOR
   * BYTE the same four declarations in style.css, written twice because there
   * was no component to write them in once. Nothing was broken — this is the
   * "different app in each section" complaint reproduced inside a single 280px
   * column, which is exactly where it is least defensible.
   *
   * One element now, `<span data-tone>`, with the styling in this file rather
   * than in style.css. That is not a stylistic preference: style.css belongs to
   * another owner during this wave, and a component that carries its own
   * appearance is one that cannot be half-adopted.
   *
   * THE METHOD CHIP IS DELIBERATELY NOT ONE OF THESE. `<span class="method"
   * data-method="POST">` looks like a fifth convention and is not: it renders a
   * VALUE, not a type, and it does so through `--method-color`, a channel each
   * of the thirteen themes sets per HTTP verb. Folding it in here would mean
   * re-encoding that palette inside a component whose entire job is a word and
   * a colour role. The rule is therefore "every TYPE or STATE mark is a
   * RowBadge; the method chip stays what it is", which is a line that can be
   * held, rather than "everything is a RowBadge", which cannot.
   */
  export type RowBadgeTone =
    /** A container's provenance or format: Scratch, Git, Not cloned, bru. */
    | 'status'
    /** A transient condition of the row's own object: temp, draft. */
    | 'state'
    /** A two-to-four letter type mark standing where an icon would: Ex, Flow. */
    | 'glyph'
</script>

<script lang="ts">
  type Props = {
    tone: RowBadgeTone
    /** The word itself. Cased for reading; `glyph` and `status` uppercase it. */
    text: string
  }

  let { tone, text }: Props = $props()
</script>

<span class="row-badge" data-tone={tone}>{text}</span>

<style>
  /*
    `flex: none` and the nowrap are the load-bearing pair.
    The request row's badges live in a flex cell whose neighbour is an
    ellipsised name, and a badge that agrees to shrink is a badge that renders
    as "d…" the first time a request has a long title — which is how the old
    markup lost the `draft` mark on exactly the rows where it mattered.
  */
  .row-badge {
    flex: none;
    line-height: 1;
    white-space: nowrap;
  }

  /* Uppercased, muted, and the smallest step in the scale: a provenance mark
     is a label on the collection, never a competitor to its name. */
  .row-badge[data-tone='status'] {
    color: var(--rail-muted);
    font-size: var(--font-size-10);
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  /* NOT uppercased, and warning-coloured, because `temp` and `draft` say
     something is unsaved rather than something is categorised. It keeps the
     colour and size the `em` rule in style.css already gave it, so the one
     tone that carried real meaning does not change appearance. */
  .row-badge[data-tone='state'] {
    color: var(--warning);
    font-size: var(--font-size-11);
    font-style: normal;
    font-weight: 600;
  }

  /* The accent-coloured type mark. Same four declarations `.example-glyph` and
     `.flow-glyph` each carried separately in style.css. */
  .row-badge[data-tone='glyph'] {
    color: var(--rail-accent);
    font-size: var(--font-size-10);
    font-weight: 800;
    letter-spacing: 0;
    text-transform: uppercase;
  }
</style>
