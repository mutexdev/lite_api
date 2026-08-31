// The sidebar as a flat list of rows, in the order they are drawn.
//
// THE SIDEBAR IS VIRTUALISED, AND THAT IS WHY THIS EXISTS. sidebarGroupWindow
// renders only `group.items.slice(start, end)` and replaces everything else
// with two spacer divs, so a row is frequently absent from the document even
// though the user can see where it would be and can scroll to it. The usual
// keyboard-navigation pattern — put tabindex on every item and focus the next
// sibling — assumes every item is in the DOM, so it cannot be used here.
// Navigation instead moves an index in this array, scrolls the container with
// keepIndexVisible, and focuses whichever element rendered afterwards.
//
// ONE WALK, TWO CONSUMERS. The window arithmetic needs to know how many rows
// precede a group; keyboard navigation needs the rows themselves. Those were
// two separate walks over the same structure, each re-encoding the same three
// rules about what is drawn:
//
//   * a COLLAPSED collection contributes its header and nothing more
//   * a collection whose directory is MISSING does the same
//   * a SEARCH overrides every collapse, since a result inside a collapsed
//     folder has to be reachable
//
// Two walks that must agree is a bad trade when disagreement is silent. It does
// not throw and it does not look wrong on load: the offsets drift, and the list
// only misdraws once somebody scrolls — the exact failure virtualList.ts warns
// about. walkSidebar produces both outputs from one pass, and the equivalence
// test in sidebarRows.test.mts holds the replacement to the original's
// behaviour on every combination of the three rules.

/** A saved response example, already reduced to its stable identifier. */
export type SidebarRowExample = {
  id: string
  name: string
}

export type SidebarRowItem = {
  id: string
  name: string
  /**
   * Loosely typed on purpose: the app's own RequestItem carries examples whose
   * `id` is optional, because an example saved before ids existed is known only
   * by its name. Narrowing this to a required id would make the real type
   * unassignable and force a cast at the one call site that matters.
   */
  examples?: readonly { id?: string; name: string }[]
}

export type SidebarRowGroup = {
  /** Empty string for the collection's root group. */
  folder: string
  items: readonly SidebarRowItem[]
}

/** One flow, reduced to what a row needs. */
export type SidebarRowFlow = {
  id: string
  name: string
}

export type SidebarRowCollection = {
  id: string
  name: string
  /** A collection whose directory is missing renders its header and nothing else. */
  notFoundLocally?: boolean
}

export type SidebarRowsInput = {
  collections: readonly SidebarRowCollection[]
  groupsFor: (collectionId: string) => readonly SidebarRowGroup[]
  collapsedCollections: Readonly<Record<string, boolean>>
  collapsedFolders: Readonly<Record<string, boolean>>
  /** Any non-empty query overrides every collapse. */
  searchQuery: string
  folderKey: (collectionId: string, folder: string) => string
  /**
   * A request's saved response examples, reduced to id and name.
   *
   * Supplied as a callback rather than read off the item so the caller can
   * resolve each example's stable identifier — which lives in
   * workbench/tabPresentation and is not a plain field — without mapping the
   * entire tree before every walk. Defaults to the item's own `examples`,
   * which is what the tests use.
   */
  examplesFor?: (item: SidebarRowItem) => readonly SidebarRowExample[]
  /**
   * A collection's flows, drawn after all of its folders and requests.
   *
   * FLOW ROWS WERE THE ONE DRAWN ROW THIS WALK DID NOT KNOW ABOUT, and the
   * markup carried a comment saying so: the keyboard cursor was "deliberately
   * NOT moved onto a flow row", because a cursor naming a row the walk never
   * emitted resolves to index -1, and the next arrow key jumps to the top of
   * the tree instead of moving by one. That was the honest behaviour while the
   * walk was blind — but the cost is that a flow, which is a first-class object
   * one click from every other row, is unreachable by keyboard, invisible to a
   * screen reader's tree navigation, and has no ⋯ menu, while its siblings have
   * all three.
   *
   * Supplied as a callback for the same reason `groupsFor` is: the caller reads
   * flows off its own collection type, and the walk should not know that type.
   * Omitted entirely, no flow rows are emitted and every existing caller and
   * test behaves exactly as before.
   *
   * NOT FILTERED BY THE SEARCH QUERY, because the markup does not filter them
   * either — a collection's flows are drawn whenever the collection is drawn.
   * The walk's contract is to match what is on screen row for row; making the
   * two disagree here is precisely the silent-drift failure this file exists to
   * prevent. Filtering flows is a change to the markup and to this callback
   * together, in that order.
   */
  flowsFor?: (collectionId: string) => readonly SidebarRowFlow[]
}

export type SidebarRowKind = 'collection' | 'folder' | 'request' | 'example' | 'flow'

/**
 * One drawn row.
 *
 * The absent fields carry '' rather than undefined so that `key` is always
 * total and two rows can be compared without narrowing on `kind` first. That
 * matters because the navigation code handles rows generically — it moves an
 * index and asks the row what it is only at the moment it acts on one.
 */
export type SidebarRow = {
  kind: SidebarRowKind
  /**
   * Stable across a re-walk, which is what makes focus survive a rename, a
   * filter keystroke or a collapse. Focus is restored by key, not by index:
   * indices shift the instant a folder above the cursor collapses.
   */
  key: string
  collectionId: string
  folder: string
  /**
   * The leaf this row names: a request's id on a request or example row, and a
   * FLOW's id on a flow row.
   *
   * Sharing the field rather than adding `flowId` beside it is what lets
   * sidebarObjectForRow stay a single mapping — and what lets every consumer
   * that already narrows on `kind` before reading this keep working untouched.
   * '' on a collection or folder row.
   */
  itemId: string
  exampleId: string
  /** 0 for a collection, 1 for a folder or a root request, and so on down. */
  depth: number
  label: string
  /**
   * The row's position in the coordinate the SCROLL arithmetic uses, which is
   * not its index in `rows`.
   *
   * keepIndexVisible converts an index to a pixel offset by multiplying by one
   * uniform row height, and example rows are not that height — so they are not
   * counted, exactly as they are not counted in `groupOffsets`. An example row
   * therefore reports its parent request's position: scrolling to an example
   * scrolls to the request it hangs off, which is on screen together with it.
   */
  windowIndex: number
}

export type SidebarWalk = {
  rows: SidebarRow[]
  /**
   * Flat row index of each group's first request, EXAMPLES EXCLUDED.
   *
   * Keyed by collection and folder through a separator that cannot occur in
   * either — rather than through the caller's folderKey, whose job is to name a
   * collapse-state entry and which nothing promises is injective across
   * collections.
   */
  groupOffsets: Map<string, number>
  /** The offset an unknown group falls back to: the total row count. */
  totalOffset: number
}

const KEY_SEPARATOR = '\u0000'

const groupOffsetKey = (collectionId: string, folder: string) =>
  `${collectionId}${KEY_SEPARATOR}${folder}`

/**
 * Walks the sidebar once, producing every drawn row and every group offset.
 *
 * EXAMPLE ROWS ARE COUNTED IN `rows` BUT NOT IN `groupOffsets`, and the
 * asymmetry is deliberate. The window arithmetic turns a scrollTop into a row
 * index by dividing by ONE measured row height, sampled from
 * `.request-row-shell`. Example rows are not that height — `.request-row` sets
 * `min-height: 28px` while `.sidebar-example-row` sets no height at all — so
 * counting them in the offsets would not make the arithmetic correct, only
 * differently incorrect. Keyboard navigation has no such constraint: it needs
 * every focusable row, whatever it measures.
 *
 * The consequence is a real, pre-existing imprecision: a group below a request
 * that has saved examples gets an offset short by one row per example, so its
 * window is computed slightly off. Fixing that means variable-height
 * windowing, which is a change to virtualList.ts and not to this walk.
 */
export function walkSidebar(input: SidebarRowsInput): SidebarWalk {
  const { collections, groupsFor, collapsedCollections, collapsedFolders, searchQuery, folderKey } = input
  // No flows unless the caller supplies them, so every existing caller — and
  // every existing test — walks exactly the tree it walked before.
  const flowsFor = input.flowsFor ?? (() => [])
  // The fallback resolves a missing id to the name, which is the same rule
  // workbench/tabPresentation applies — an example with no id is identified by
  // what it is called.
  const examplesFor =
    input.examplesFor ??
    ((item: SidebarRowItem) =>
      (item.examples ?? []).map((example) => ({ id: example.id ?? example.name, name: example.name })))
  const searching = Boolean(searchQuery)
  const rows: SidebarRow[] = []
  const groupOffsets = new Map<string, number>()

  // Tracked separately from rows.length because examples are rows but are not
  // offsets. Conflating the two is precisely the bug this file exists to avoid.
  let offset = 0

  for (const collection of collections) {
    rows.push({
      kind: 'collection',
      key: `c:${collection.id}`,
      collectionId: collection.id,
      folder: '',
      itemId: '',
      exampleId: '',
      depth: 0,
      label: collection.name,
      windowIndex: offset
    })
    offset += 1

    const collapsed = !searching && Boolean(collapsedCollections[collection.id])
    // No offsets are recorded for a collection that draws nothing, which is how
    // a lookup into it falls through to totalOffset — the original returned its
    // running total for the same case.
    if (collapsed || collection.notFoundLocally) continue

    for (const group of groupsFor(collection.id)) {
      const inFolder = Boolean(group.folder)
      // A folder path is a PATH, so its depth is how many segments it has:
      // "api" nests one deep, "api/v2" two. Hardcoding 1 here made every folder
      // a sibling of every other, which left Left-arrow unable to walk out of a
      // nested folder into the one containing it, and left the tree flat.
      const segments = inFolder ? group.folder.split('/') : []
      const folderDepth = segments.length
      if (inFolder) {
        rows.push({
          kind: 'folder',
          key: `f:${collection.id}:${group.folder}`,
          collectionId: collection.id,
          folder: group.folder,
          itemId: '',
          exampleId: '',
          depth: folderDepth,
          // The row shows its own name; the path is what indentation conveys.
          label: segments[segments.length - 1],
          windowIndex: offset
        })
        offset += 1
      }

      // Recorded BEFORE the collapse check and before the items are counted, so
      // that a collapsed or empty group still reports where its rows would
      // begin. The original returned at exactly this point.
      groupOffsets.set(groupOffsetKey(collection.id, group.folder), offset)

      const folderCollapsed =
        inFolder && !searching && Boolean(collapsedFolders[folderKey(collection.id, group.folder)])
      if (folderCollapsed) continue

      const itemDepth = folderDepth + 1
      for (const item of group.items) {
        rows.push({
          kind: 'request',
          key: `r:${collection.id}:${item.id}`,
          collectionId: collection.id,
          folder: group.folder,
          itemId: item.id,
          exampleId: '',
          depth: itemDepth,
          label: item.name,
          windowIndex: offset
        })
        const requestWindowIndex = offset
        offset += 1

        for (const example of examplesFor(item)) {
          rows.push({
            kind: 'example',
            key: `e:${collection.id}:${item.id}:${example.id}`,
            collectionId: collection.id,
            folder: group.folder,
            itemId: item.id,
            exampleId: example.id,
            depth: itemDepth + 1,
            label: example.name,
            windowIndex: requestWindowIndex
          })
          // Deliberately not `offset += 1`. See the note above.
        }
      }
    }

    // Flows come last, after every folder and request, because that is where
    // the markup draws them — and a walk that agrees with the DOM about
    // ordering is the entire contract here.
    //
    // COUNTED IN `offset`, unlike examples, and the difference is height. The
    // window arithmetic multiplies ONE measured row height, sampled from
    // `.request-row-shell`; `.sidebar-flow-row` sets the same `min-height:
    // 28px` that `.request-row` does, so a flow row IS that height and
    // counting it makes the offsets of every later collection more correct
    // rather than differently wrong. An example row sets no height at all,
    // which is why it stays excluded.
    //
    // The residual imprecision is the `Flows` heading, which occupies height
    // and is not a row. It is one heading per collection that has flows, it is
    // the same class of error the examples note already describes, and closing
    // it means variable-height windowing in virtualList.ts rather than a
    // different sum here.
    for (const flow of flowsFor(collection.id)) {
      rows.push({
        kind: 'flow',
        // The prefix matches flowView.ts's flowRowKey exactly. The markup
        // already builds its DOM ids from that function, so a walk that
        // invented its own prefix would emit rows whose keys never resolve to
        // an element — aria-activedescendant pointing at nothing, and focus
        // landing nowhere.
        key: `fl:${collection.id}:${flow.id}`,
        collectionId: collection.id,
        folder: '',
        itemId: flow.id,
        exampleId: '',
        // A sibling of a root request: a flow belongs to the collection, not to
        // any folder, so it can never sit deeper than one.
        depth: 1,
        label: flow.name,
        windowIndex: offset
      })
      offset += 1
    }
  }

  return { rows, groupOffsets, totalOffset: offset }
}

/** Every drawn row, for callers that do not need the offsets. */
export function sidebarRows(input: SidebarRowsInput): SidebarRow[] {
  return walkSidebar(input).rows
}

/**
 * The flat row index of a group's first request.
 *
 * Replaces the standalone walk in virtualList.ts. An unknown collection or
 * folder — including any group inside a collapsed or missing collection —
 * yields the total row count, which is what the original produced when its
 * loops ran out without matching.
 */
export function sidebarGroupOffset(walk: SidebarWalk, collectionId: string, folder: string): number {
  return walk.groupOffsets.get(groupOffsetKey(collectionId, folder)) ?? walk.totalOffset
}
