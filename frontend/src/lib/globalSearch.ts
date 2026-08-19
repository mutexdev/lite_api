// Global search: matching, ranking and ordering.
//
// The ranks below are the whole feature. Someone typing three characters and
// pressing Enter gets result zero, so the order decides what opens — and a
// ranking that puts a URL match above a name match sends people to the wrong
// request while looking, to them, like search is simply bad.
//
// Ranks, lowest first:
//   0  collection
//   1  folder, and every collection when the query is empty
//   2  request matched on NAME
//   3  request matched on URL
//   4  request matched on path or method
//
// A "/" in the query turns on path matching, which is what lets
// "users/create" find a request by where it lives rather than what it is
// called.

import type { types } from '../../wailsjs/go/models'

export type GlobalSearchResult = {
  id: string
  type: 'collection' | 'folder' | 'request'
  collectionId: string
  itemId?: string
  name: string
  subtitle: string
  meta: string
  rank: number
}

export function normalizeGlobalSearchQuery(value: string) {
  return value.trim().replace(/\/+/g, '/').toLowerCase()
}

export function isValidGlobalSearchQuery(value: string) {
  return Boolean(value && value !== '/' && !(value.length === 1 && !/[a-z0-9]/i.test(value)))
}

export function globalSearchTermsMatch(values: unknown[], terms: string[]) {
  const haystack = values.map((value) => String(value ?? '').toLowerCase()).join(' ')
  return terms.every((term) => haystack.includes(term))
}

export function globalSearchItemPath(collection: types.Collection, item: types.RequestItem) {
  return [collection.name, item.folderPath, item.name].filter(Boolean).join('/')
}

export function sortGlobalSearchResults(a: GlobalSearchResult, b: GlobalSearchResult) {
  return a.rank - b.rank || a.type.localeCompare(b.type) || a.name.localeCompare(b.name)
}

export function buildGlobalSearchResults(workspace: types.Workspace | undefined, query: string): GlobalSearchResult[] {
  const collections = workspace?.collections ?? []
  const normalized = normalizeGlobalSearchQuery(query)
  if (!normalized) {
    const collectionResults = collections
      .map((collection) => ({
        id: `collection:${collection.id}`,
        type: 'collection' as const,
        collectionId: collection.id,
        name: collection.name,
        subtitle: collection.path || `${collection.items?.length ?? 0} requests`,
        meta: collection.format || 'collection',
        rank: 1
      }))
      .sort(sortGlobalSearchResults)
    return collectionResults
  }
  if (!isValidGlobalSearchQuery(normalized)) return []
  const terms = normalized.split(/[\s/]+/).filter(Boolean)
  const enablePathMatch = normalized.includes('/')
  const results: GlobalSearchResult[] = []
  for (const collection of collections) {
    if (globalSearchTermsMatch([collection.name, collection.path, collection.format], terms)) {
      results.push({
        id: `collection:${collection.id}`,
        type: 'collection',
        collectionId: collection.id,
        name: collection.name,
        subtitle: collection.path || `${collection.items?.length ?? 0} requests`,
        meta: collection.format || 'collection',
        rank: 0
      })
    }

    const folders = new Set((collection.items ?? []).map((item) => item.folderPath).filter(Boolean))
    for (const folder of folders) {
      const folderPath = `${collection.name}/${folder}`
      if (globalSearchTermsMatch([folder, enablePathMatch ? folderPath : ''], terms)) {
        results.push({
          id: `folder:${collection.id}:${folder}`,
          type: 'folder',
          collectionId: collection.id,
          name: folder,
          subtitle: collection.name,
          meta: 'folder',
          rank: 1
        })
      }
    }

    for (const item of collection.items ?? []) {
      const itemPath = globalSearchItemPath(collection, item)
      const nameMatch = globalSearchTermsMatch([item.name], terms)
      const urlMatch = globalSearchTermsMatch([item.url], terms)
      const pathMatch = enablePathMatch && globalSearchTermsMatch([itemPath], terms)
      const methodMatch = globalSearchTermsMatch([item.method, item.type], terms)
      if (nameMatch || urlMatch || pathMatch || methodMatch) {
        results.push({
          id: `request:${collection.id}:${item.id}`,
          type: 'request',
          collectionId: collection.id,
          itemId: item.id,
          name: item.name,
          subtitle: item.folderPath ? `${collection.name} / ${item.folderPath}` : collection.name,
          meta: item.method || item.type || 'request',
          rank: nameMatch ? 2 : urlMatch ? 3 : 4
        })
      }
    }
  }

  return results.sort(sortGlobalSearchResults)
}
