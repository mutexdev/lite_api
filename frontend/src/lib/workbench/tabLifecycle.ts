export type LifecycleOpenTab = {
  id: string
  collectionId: string
  itemId: string
  kind: string
  transient?: boolean
}

export type LifecycleRequest = {
  collectionId: string
  id: string
  name: string
  draft?: boolean
  transient?: boolean
}

export type UnsavedRequestTab = {
  tabId: string
  collectionId: string
  requestId: string
  requestName: string
  draft: boolean
  transient: boolean
}

export type UnsavedClosePlan = {
  affected: UnsavedRequestTab[]
  requiresConfirmation: boolean
}

export type RequestDeletionTarget = {
  transient?: boolean
}

export type RequestDeletionAction = 'discard-draft' | 'recoverable-delete'

/** Transient requests have no durable file to recover; discard them in place. */
export function requestDeletionAction(request: RequestDeletionTarget): RequestDeletionAction {
  return request.transient ? 'discard-draft' : 'recoverable-delete'
}

function requestKey(collectionId: string, requestId: string) {
  return `${collectionId}\u0000${requestId}`
}

/**
 * Plans only the frontend decision. It deliberately ignores response-example
 * tabs, because closing one does not discard the request it references.
 */
export function planUnsavedClose(tabs: LifecycleOpenTab[], requests: LifecycleRequest[]): UnsavedClosePlan {
  const requestsByKey = new Map(requests.map((request) => [requestKey(request.collectionId, request.id), request]))
  const seenRequests = new Set<string>()
  const affected: UnsavedRequestTab[] = []

  for (const tab of tabs) {
    if (tab.kind === 'response-example' || !tab.collectionId || !tab.itemId) continue
    const request = requestsByKey.get(requestKey(tab.collectionId, tab.itemId))
    if (!request) continue
    const draft = Boolean(request.draft)
    const transient = Boolean(request.transient || tab.transient)
    if (!draft && !transient) continue

    const key = requestKey(tab.collectionId, tab.itemId)
    if (seenRequests.has(key)) continue
    seenRequests.add(key)
    affected.push({
      tabId: tab.id,
      collectionId: tab.collectionId,
      requestId: tab.itemId,
      requestName: request.name || 'Untitled request',
      draft,
      transient
    })
  }

  return { affected, requiresConfirmation: affected.length > 0 }
}
