<script lang="ts">
  // The sidebar's brand block and "New" button.
  //
  // A callback prop rather than a bindable one, following the convention the
  // rest of lib/ uses (CommandOverflowMenu's onSelect, KeyValueTable's
  // handlers): the child owns no state here, it just reports a click. The
  // invoker element is passed up because the creation flow returns focus to
  // whatever opened it, and only the child knows which element that was.
  import BrandMark from './BrandMark.svelte'

  type Props = {
    onNew: (invoker: HTMLElement | null) => void
  }

  let { onNew }: Props = $props()
</script>

<div class="brand">
  <div class="brand-mark"><BrandMark /></div>
  <div>
    <h1>LiteAPI</h1>
    <p>Local-first API workbench</p>
  </div>
</div>

<section class="rail-section rail-create">
  <button
    class="primary new-request-button"
    type="button"
    onclick={(event) => onNew(event.currentTarget as HTMLElement)}
    aria-haspopup="dialog"
  >
    <span aria-hidden="true">+</span> New
    <kbd>⌘N</kbd>
  </button>
  <small>Create a scratch request in the active collection.</small>
</section>
