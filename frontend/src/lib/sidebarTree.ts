// US-031 — flattening the collection tree into the linear row list a
// virtualised sidebar needs.
//
// A window can only be computed over a flat list, so the tree has to be
// flattened first — and the flattening is where the correctness lives. Every
// mistake here is silent in a way the windowing is not:
//
//   * a collapsed folder whose children still appear looks like the collapse
//     toggle is broken
//   * a row emitted at the wrong depth renders at the wrong indent, which reads
//     as the request belonging to a different folder
//   * a duplicated or missing key makes Svelte's keyed {#each} reuse the wrong
//     DOM node, so a click lands on a different request than the one under the
//     cursor
//
// Search deliberately OVERRIDES collapse. Someone typing a query wants to see
// matches wherever they are; hiding them inside a folder they collapsed an hour
// ago makes the search look broken.

export type SidebarRowKind = 'collection' | 'folder' | 'request'

export type SidebarRow = {
  /** Stable identity for the keyed {#each}. Unique across the whole list. */
  key: string
  kind: SidebarRowKind
  /** Indent level: collections 0, folders 1, requests 1 or 2. */
  depth: number
  collectionId: string
  /** Folder display path, for folder rows and for requests inside one. */
  folder?: string
  /** Present on request rows. */
  itemId?: string
  collapsed?: boolean
}

export type SidebarCollection = {
  id: string
  name?: string
}

export type SidebarGroup = {
  folder: string
  items: { id: string }[]
}

export type FlattenInput = {
  collections: SidebarCollection[]
  /** Groups for one collection, as the existing groupedItems produces them. */
  groupsFor: (collectionId: string) => SidebarGroup[]
  collapsedCollections: Record<string, boolean>
  collapsedFolders: Record<string, boolean>
  /** A non-empty query overrides every collapse. */
  searchQuery: string
  /** Must match the app's existing key derivation so collapse state lines up. */
  folderKey: (collectionId: string, folder: string) => string
}

export function flattenSidebar(input: FlattenInput): SidebarRow[] {
  const { collections, groupsFor, collapsedCollections, collapsedFolders, searchQuery, folderKey } = input
  const searching = Boolean(searchQuery.trim())
  const rows: SidebarRow[] = []

  for (const collection of collections) {
    const collectionCollapsed = !searching && Boolean(collapsedCollections[collection.id])
    rows.push({
      key: `collection:${collection.id}`,
      kind: 'collection',
      depth: 0,
      collectionId: collection.id,
      collapsed: collectionCollapsed
    })
    if (collectionCollapsed) continue

    for (const group of groupsFor(collection.id)) {
      const hasFolder = Boolean(group.folder)
      const folderCollapsed =
        hasFolder && !searching && Boolean(collapsedFolders[folderKey(collection.id, group.folder)])

      if (hasFolder) {
        rows.push({
          key: `folder:${collection.id}:${group.folder}`,
          kind: 'folder',
          depth: 1,
          collectionId: collection.id,
          folder: group.folder,
          collapsed: folderCollapsed
        })
        if (folderCollapsed) continue
      }

      for (const item of group.items) {
        rows.push({
          // The collection id is part of the key because the same request id
          // can appear under two collections mounted from the same folder on
          // disk, and a duplicate key makes Svelte reuse the wrong DOM node.
          key: `request:${collection.id}:${item.id}`,
          kind: 'request',
          depth: hasFolder ? 2 : 1,
          collectionId: collection.id,
          folder: group.folder || undefined,
          itemId: item.id
        })
      }
    }
  }

  return rows
}

/**
 * indexOfRequest finds a request's position in the flattened list.
 *
 * Keyboard navigation needs it to scroll the active request into view, and it
 * has to search the FLATTENED list rather than the collection's own items:
 * those indices differ as soon as anything is collapsed, and scrolling to the
 * wrong one lands the user somewhere else in the tree.
 */
export function indexOfRequest(rows: SidebarRow[], collectionId: string, itemId: string): number {
  return rows.findIndex((row) => row.kind === 'request' && row.collectionId === collectionId && row.itemId === itemId)
}

/**
 * visibleRequestCount reports how many request rows are actually rendered.
 *
 * Used by the tests and by anything reporting "showing N of M" — counting the
 * collection's items instead would include the ones inside collapsed folders.
 */
export function visibleRequestCount(rows: SidebarRow[]): number {
  return rows.reduce((total, row) => (row.kind === 'request' ? total + 1 : total), 0)
}
