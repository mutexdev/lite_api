// What the request command strip shows above the response pane.
//
// Pure presentation, and two of the things it presents are SECURITY POSTURE:
// the "TLS verify" / "TLS off" cue and the proxy cue. A wrong label here tells
// someone their request is verified when it is not, or that it is going direct
// when it is going through a proxy. Nothing else in the UI contradicts it, so
// the label is the only thing the user has.
//
// The TLS cue is deliberately AND-ed rather than OR-ed: verification is on only
// when the request has not disabled it AND the global preference has not. Either
// switch being off means traffic is unverified, so either must show "TLS off".

import type { types } from '../../../wailsjs/go/models'
import type { RequestCommandState } from './types'

export function formatRuntimeBytes(value: number | undefined) {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let amount = value
  let unitIndex = 0
  while (amount >= 1024 && unitIndex < units.length - 1) {
    amount /= 1024
    unitIndex += 1
  }
  const precision = amount >= 10 || unitIndex === 0 ? 0 : 1
  return `${amount.toFixed(precision)} ${units[unitIndex]}`
}

export function isProxyConfigUnset(proxy: types.ProxyConfig | undefined) {
  if (!proxy) return true
  return !proxy.inherit
    && !proxy.disabled
    && !proxy.protocol
    && !proxy.hostname
    && !proxy.port
    && !proxy.bypassProxy
    && !proxy.auth?.username
    && !proxy.auth?.password
    && !proxy.auth?.disabled
}

export function collectionProxyMode(proxy: types.ProxyConfig | undefined) {
  if (isProxyConfigUnset(proxy)) return 'inherit'
  if (proxy?.disabled) return 'off'
  if (proxy?.inherit ?? true) return 'inherit'
  return 'manual'
}

export function preferencesProxyMode(preferences: types.Preferences | undefined) {
  const proxy = preferences?.proxy
  if (proxy?.disabled) return 'off'
  if (proxy?.source === 'pac') return 'pac'
  if (proxy?.source === 'manual') return 'manual'
  return 'inherit'
}

/**
 * A transient request is one that is not on disk yet — either explicitly marked
 * transient, or living in the scratch collection. The save button says "Save
 * temp" for these, and the tab is treated as dirty even without edits, because
 * closing it loses the request entirely.
 */
export function requestIsTransient(
  collection: types.Collection | undefined,
  item: types.RequestItem | undefined,
  scratchCollectionId: string | undefined
) {
  const scratch = Boolean(collection?.scratch || (collection && scratchCollectionId === collection.id))
  return Boolean(item?.transient || scratch)
}

export function requestCommandState(
  request: types.RequestItem | undefined,
  collection: types.Collection | undefined,
  environmentName: string | undefined,
  action: string,
  webSocketConnected: boolean,
  grpcConnected: boolean,
  preferences: types.Preferences | undefined,
  httpInFlight: boolean,
  cancellationPending: boolean,
  backgroundCancellation: RequestCommandState['backgroundCancellation'],
  scratchCollectionId: string | undefined
): RequestCommandState {
  const response = request?.response
  const status = response?.cancelled ? 'Cancelled' : response?.status ? String(response.status) : 'Idle'
  const tone = response?.cancelled ? 'warning' : !response?.status ? 'idle' : response.status < 300 ? 'success' : response.status < 400 ? 'warning' : 'danger'
  const transient = requestIsTransient(collection, request, scratchCollectionId)
  const collectionProxy = collectionProxyMode(collection?.proxy)
  const preferencesProxy = preferencesProxyMode(preferences)
  const proxyCue = collectionProxy === 'off'
    ? 'Proxy off'
    : collectionProxy === 'manual'
      ? 'Proxy: collection'
      : preferencesProxy === 'off'
        ? 'Proxy off'
        : preferencesProxy === 'manual'
          ? 'Proxy: manual'
          : preferencesProxy === 'pac'
            ? 'Proxy: PAC'
            : 'Proxy: system'
  const tlsVerificationEnabled = request?.settings?.verifyTls !== false && preferences?.request?.sslVerification !== false
  return {
    protocol: request?.type === 'grpc' ? 'gRPC' : request?.type === 'websocket' ? 'WebSocket' : request?.type === 'graphql' ? 'GraphQL' : 'HTTP',
    environmentName: environmentName || 'No environment',
    saveLabel: transient ? 'Save temp' : 'Save',
    dirty: transient || Boolean(request?.draft),
    runningLabel: action,
    canCancel: httpInFlight || webSocketConnected || grpcConnected,
    cancelLabel: httpInFlight ? 'Cancel request' : request?.type === 'websocket' ? 'Disconnect' : 'Cancel stream',
    cancelDuringBusy: httpInFlight,
    cancellationPending,
    backgroundCancellation,
    transportCues: [tlsVerificationEnabled ? 'TLS verify' : 'TLS off', proxyCue],
    response: {
      status,
      statusText: response?.cancelled ? 'Request cancelled' : response?.statusText || (response?.error ? 'Request failed' : 'No response yet'),
      duration: `${response?.durationMs ?? 0} ms`,
      size: formatRuntimeBytes(response?.size),
      tone
    }
  }
}
