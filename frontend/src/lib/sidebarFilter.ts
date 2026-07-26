// Filtering and grouping the sidebar tree.
//
// The query is matched case-insensitively as a SUBSTRING, on the fields a
// person would plausibly type: names, the URL, the method, the folder path, and
// a saved example's name and description. Not the body, not the headers —
// matching those would return a request whose visible row shows nothing
// resembling the query, which reads as a bug rather than a feature.
//
// One rule shapes everything here: a query that matches the COLLECTION shows
// every request inside it. Someone typing a collection name is asking to see
// that collection, and filtering its contents down to the few requests that
// happen to repeat the name in their own title would hide most of the answer.
//
// The caller is expected to pass an already-lowercased query. searchHit
// lowercases the candidate, not the needle, so an uppercase query silently
// matches nothing — the single call path in App.svelte lowercases first.

import type { types } from '../../wailsjs/go/models'

export type SidebarGroup = { folder: string; items: types.RequestItem[] }

export function searchHit(value: unknown, query: string) {
  return String(value ?? '').toLowerCase().includes(query)
}

export function collectionMatches(collection: types.Collection, query: string) {
  return [collection.name, collection.format, collection.path].some((value) => searchHit(value, query))
}

export function requestMatches(collection: types.Collection, item: types.RequestItem, query: string) {
  const exampleValues = (item.examples ?? []).flatMap((example) => [example.name, example.description, example.request?.url])
  return [collection.name, item.folderPath, item.name, item.method, item.type, item.url, ...exampleValues].some((value) => searchHit(value, query))
}

export function folderMatches(folder: types.FolderConfig, query: string) {
  return [folder.displayPath, folder.path, folder.name].some((value) => searchHit(value, query))
}

export function filteredItems(collection: types.Collection, query: string) {
  const items = collection.items ?? []
  if (!query || collectionMatches(collection, query)) return items
  return items.filter((item) => requestMatches(collection, item, query))
}

export function filteredFolders(collection: types.Collection, query = '') {
  const folders = collection.folders ?? []
  if (!query.trim()) return folders
  return folders.filter((folder) => folderMatches(folder, query))
}

export function computeGroupedItems(collection: types.Collection, query = '') {
  const groups: { folder: string; items: types.RequestItem[] }[] = []
  const indexByFolder = new Map<string, number>()
  const addGroup = (folder: string) => {
    let index = indexByFolder.get(folder)
    if (index === undefined) {
      index = groups.length
      indexByFolder.set(folder, index)
      groups.push({ folder, items: [] })
    }
    return index
  }
  for (const folder of filteredFolders(collection, query)) {
    addGroup(folder.displayPath || folder.path)
  }
  for (const item of filteredItems(collection, query)) {
    const folder = item.folderPath || ''
    const index = addGroup(folder)
    groups[index].items.push(item)
  }
  return groups
}
