// What an open tab shows in the tab strip, and how the sidebar keys its
// collapsed folders.
//
// The identifiers here are the interesting part. A tab stores references — a
// collection id, an item id, an example id — and resolves them against live
// state on every render. Every fallback below exists because one of those
// references can be missing or stale, and the wrong answer is not an error: it
// is a tab labelled after a different request than the one it opens.

import type { types } from '../../../wailsjs/go/models'

/**
 * The stable identity of a response example.
 *
 * Falls back to the name because examples written by earlier versions have no
 * id at all. Dropping the fallback would make every one of those examples
 * unaddressable — they render, and nothing can be opened or renamed.
 */
export function responseExampleIdentifier(example: types.ResponseExample): string {
  return example.id || example.name
}

function itemForTab(
  tab: Pick<types.OpenTab, 'collectionId' | 'itemId'>,
  collections: types.Collection[] | undefined
): types.RequestItem | undefined {
  const collection = collections?.find((candidate) => candidate.id === tab.collectionId)
  return collection?.items?.find((candidate) => candidate.id === tab.itemId)
}

/**
 * Resolves the example a response-example tab points at.
 *
 * Matches on the identifier OR the bare name, so a tab opened before the
 * example gained an id still resolves after an upgrade rewrites the file. One
 * comparison would leave those tabs pointing at nothing, which renders as a
 * blank example pane rather than as an error.
 */
export function findResponseExampleForTab(
  tab: types.OpenTab | undefined,
  collections: types.Collection[] | undefined
): types.ResponseExample | undefined {
  if (!tab || tab.kind !== 'response-example') return undefined
  const target = tab.exampleId || tab.exampleName || ''
  return itemForTab(tab, collections)?.examples?.find(
    (example) => responseExampleIdentifier(example) === target || example.name === target
  )
}

/**
 * The tab's visible label.
 *
 * The last fallback distinguishes a transient request from a saved one:
 * "Scratch request" says the tab holds something that was never written to
 * disk, which is the one thing a user needs to know before closing it.
 */
export function tabLabel(tab: types.OpenTab, collections: types.Collection[] | undefined): string {
  if (tab.kind === 'response-example') {
    return findResponseExampleForTab(tab, collections)?.name || tab.exampleName || 'Example'
  }
  return itemForTab(tab, collections)?.name ?? (tab.transient ? 'Scratch request' : 'Request')
}

/**
 * The method badge, abbreviated where it would not fit.
 *
 * Only DELETE and OPTIONS are shortened, because only those two are long enough
 * to widen the badge past the tab strip's fixed column. Abbreviating the rest
 * would trade legibility for nothing.
 */
export function methodLabel(method: string): string {
  const upper = (method || '').toUpperCase()
  if (upper === 'DELETE') return 'DEL'
  if (upper === 'OPTIONS') return 'OPT'
  return upper
}

/** The method a tab shows, or "" for a tab that is not a request. */
export function tabMethod(tab: types.OpenTab, collections: types.Collection[] | undefined): string {
  if (tab.kind === 'response-example') return ''
  return itemForTab(tab, collections)?.method ?? ''
}

/**
 * The key a collapsed sidebar folder is remembered under.
 *
 * The separator is NUL, and that is deliberate: it is the one byte that cannot
 * appear in a collection id or a folder path. A printable separator such as "/"
 * or ":" would let two different (collection, folder) pairs produce the same
 * key — collection "a" folder "b/c" and collection "a/b" folder "c" — and the
 * two folders would then collapse and expand together.
 */
export function sidebarFolderKey(collectionId: string, folder: string): string {
  return `${collectionId}\u0000${folder}`
}

/**
 * Whether a collection is the workspace's scratch collection.
 *
 * Two sources, and both are needed: the collection carries its own flag, and
 * the workspace names the id. A collection loaded before the flag existed has
 * only the second, and one belonging to another workspace has only the first.
 */
export function collectionIsScratch(
  collection: types.Collection | undefined,
  scratchCollectionId: string | undefined
): boolean {
  if (!collection) return false
  return Boolean(collection.scratch || (scratchCollectionId && scratchCollectionId === collection.id))
}
