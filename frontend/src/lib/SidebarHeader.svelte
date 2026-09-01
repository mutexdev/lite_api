<script lang="ts">
  // The sidebar's one header row: the mark, the name, and the two things
  // anybody does from the top of a sidebar — find something, or make something.
  //
  // WHAT WAS HERE: 130px and five elements before the first collection — a
  // brand block with a tagline, a full-width "+ New ⌘N" button, and a sentence
  // under it explaining what that button did. Yaak, Bruno and Postman all spend
  // 36–44px and a single row on this. The tagline told a returning user nothing
  // they did not already know; the helper sentence explained a control whose own
  // tooltip could say it; and the New button was the SECOND place to start a
  // request, the top bar's `+ New` being the first. Two New menus is one menu
  // too many, so this row opens the top bar's list and the top bar's copy goes.
  //
  // Callback props rather than bindable ones, following the convention the rest
  // of lib/ uses (CommandOverflowMenu's onSelect, KeyValueTable's handlers): the
  // child owns no state, it reports an intent. The invoker element travels with
  // the command because the creation flow returns focus to whatever opened it,
  // and only the child knows which element that was.
  import BrandMark from './BrandMark.svelte'
  import IconButton from './ui/IconButton.svelte'
  import CommandOverflowMenu from './workbench/CommandOverflowMenu.svelte'
  import { newItems, type WorkbenchCommandID } from './workbench/workbenchCommands'
  import { workspaceStore } from './stores/workspaceStore.svelte'

  type Props = {
    onCommand: (id: WorkbenchCommandID, invoker: HTMLElement | null) => void | Promise<void>
    onToggleSearch: () => void
    searchOpen: boolean
  }

  let { onCommand, onToggleSearch, searchOpen }: Props = $props()

  // The SAME list the top bar's `+ New` built, imported rather than rebuilt:
  // two surfaces opening one menu is the whole point of D1, and a second copy
  // is how the sidebar ends up offering WebSocket six months after the top bar
  // stopped. The store is passed whole because it satisfies WorkbenchMenuScope
  // structurally, and the getters are read inside the $derived, so the disabled
  // states track the active collection.
  const items = $derived(newItems(workspaceStore))
</script>

<div class="sidebar-header">
  <div class="brand-mark"><BrandMark /></div>
  <h1>LiteAPI</h1>

  <div class="sidebar-header-actions">
    <!--
      The shortcut lives in the label, which IconButton uses for both the
      accessible name and the tooltip. That is what lets the `<kbd>⌘N</kbd>` and
      the `<small>` under the old button go without taking the hint with them.
    -->
    <IconButton
      icon="search"
      label="Search requests (⌘F)"
      pressed={searchOpen}
      onclick={onToggleSearch}
      testId="sidebar-search-toggle"
    />
    <!--
      align="right", not left: the trigger sits at the sidebar's right edge, so
      a left-anchored 250px panel would hang almost entirely over the request
      pane. Anchoring the panel's right edge to the trigger's keeps it under the
      sidebar it belongs to.
    -->
    <CommandOverflowMenu
      label="New (⌘N)"
      icon="add"
      align="right"
      items={items}
      onSelect={onCommand}
      testId="sidebar-new-menu"
    />
  </div>
</div>

<style>
  .sidebar-header {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    height: 36px;
  }

  /* 22px, against the 38px the shared .brand-mark rule sizes for the old
     two-line brand block. The row is 36px tall now and a 38px mark does not
     fit in it. */
  .sidebar-header .brand-mark {
    width: 22px;
    height: 22px;
  }

  .sidebar-header h1 {
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--font-size-13);
    font-weight: 700;
  }

  .sidebar-header-actions {
    display: flex;
    flex: none;
    align-items: center;
    gap: var(--space-2);
  }
</style>
