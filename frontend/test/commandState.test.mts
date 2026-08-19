// The request command strip: labels, and two security-posture cues.
//
// The "TLS verify"/"TLS off" cue and the proxy cue are the only place the UI
// tells anyone how their request will actually travel. A wrong label says
// verified when it is not, or direct when it is proxied, and nothing else on
// screen contradicts it.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  formatRuntimeBytes,
  isProxyConfigUnset,
  collectionProxyMode,
  preferencesProxyMode,
  requestIsTransient,
  requestCommandState
} from '../src/lib/workbench/commandState.ts'

const state = (o: Record<string, unknown> = {}) =>
  requestCommandState(
    o.request as never,
    o.collection as never,
    o.environmentName as never,
    (o.action as string) ?? '',
    Boolean(o.webSocketConnected),
    Boolean(o.grpcConnected),
    o.preferences as never,
    Boolean(o.httpInFlight),
    Boolean(o.cancellationPending),
    undefined as never,
    o.scratchCollectionId as never
  )

// Verification is on only when BOTH switches allow it. Either being off means
// traffic is unverified, so either must show "TLS off".
test('the TLS cue is off when either the request or the preference disables it', () => {
  const on = state({ request: { settings: {} }, preferences: { request: {} } })
  assert.ok(on.transportCues.includes('TLS verify'))

  const requestOff = state({ request: { settings: { verifyTls: false } }, preferences: { request: {} } })
  assert.ok(requestOff.transportCues.includes('TLS off'), 'a request opting out must show TLS off')

  const globalOff = state({ request: { settings: {} }, preferences: { request: { sslVerification: false } } })
  assert.ok(globalOff.transportCues.includes('TLS off'), 'the global preference opting out must show TLS off')

  const bothOff = state({ request: { settings: { verifyTls: false } }, preferences: { request: { sslVerification: false } } })
  assert.ok(bothOff.transportCues.includes('TLS off'))
})

// An unset collection proxy inherits; it does not mean "off".
test('an entirely unset proxy config reads as inherit', () => {
  assert.equal(isProxyConfigUnset(undefined), true)
  assert.equal(isProxyConfigUnset({} as never), true)
  assert.equal(collectionProxyMode(undefined), 'inherit')
  assert.equal(isProxyConfigUnset({ hostname: 'proxy.test' } as never), false, 'any set field means it is configured')
  assert.equal(isProxyConfigUnset({ disabled: true } as never), false, 'explicitly disabled is a decision, not absence')
})

test('collection proxy mode distinguishes off, manual and inherit', () => {
  assert.equal(collectionProxyMode({ disabled: true } as never), 'off')
  assert.equal(collectionProxyMode({ inherit: true, hostname: 'x' } as never), 'inherit')
  assert.equal(collectionProxyMode({ inherit: false, hostname: 'x' } as never), 'manual')
})

test('preference proxy mode reads the source', () => {
  assert.equal(preferencesProxyMode(undefined), 'inherit')
  assert.equal(preferencesProxyMode({ proxy: { disabled: true } } as never), 'off')
  assert.equal(preferencesProxyMode({ proxy: { source: 'pac' } } as never), 'pac')
  assert.equal(preferencesProxyMode({ proxy: { source: 'manual' } } as never), 'manual')
  assert.equal(preferencesProxyMode({ proxy: { source: 'system' } } as never), 'inherit')
})

// The collection's setting wins over the preference; only when the collection
// inherits does the preference decide.
test('the proxy cue prefers the collection over the preference', () => {
  const collectionOff = state({
    collection: { proxy: { disabled: true } },
    preferences: { proxy: { source: 'manual' } }
  })
  assert.ok(collectionOff.transportCues.includes('Proxy off'))

  const collectionManual = state({
    collection: { proxy: { inherit: false, hostname: 'p' } },
    preferences: { proxy: { source: 'pac' } }
  })
  assert.ok(collectionManual.transportCues.includes('Proxy: collection'))

  const inherited = state({ collection: {}, preferences: { proxy: { source: 'pac' } } })
  assert.ok(inherited.transportCues.includes('Proxy: PAC'), 'an inheriting collection defers to the preference')

  const bothDefault = state({ collection: {}, preferences: {} })
  assert.ok(bothDefault.transportCues.includes('Proxy: system'))
})

test('the protocol label follows the request type', () => {
  assert.equal(state({ request: { type: 'grpc' } }).protocol, 'gRPC')
  assert.equal(state({ request: { type: 'websocket' } }).protocol, 'WebSocket')
  assert.equal(state({ request: { type: 'graphql' } }).protocol, 'GraphQL')
  assert.equal(state({ request: { type: 'http' } }).protocol, 'HTTP')
  assert.equal(state({}).protocol, 'HTTP', 'no request still labels something')
})

// A transient request is not on disk, so closing its tab loses it — which is
// why it counts as dirty even with no edits.
test('a scratch or transient request saves as temp and counts as dirty', () => {
  const scratchByFlag = state({ collection: { scratch: true }, request: {} })
  assert.equal(scratchByFlag.saveLabel, 'Save temp')
  assert.equal(scratchByFlag.dirty, true)

  const scratchById = state({ collection: { id: 'c1' }, request: {}, scratchCollectionId: 'c1' })
  assert.equal(scratchById.saveLabel, 'Save temp', 'the workspace scratch id also marks a collection scratch')

  const ordinary = state({ collection: { id: 'c2' }, request: {}, scratchCollectionId: 'c1' })
  assert.equal(ordinary.saveLabel, 'Save')
  assert.equal(ordinary.dirty, false)

  const drafted = state({ collection: { id: 'c2' }, request: { draft: {} }, scratchCollectionId: 'c1' })
  assert.equal(drafted.dirty, true, 'an edited request is dirty regardless')
})

test('requestIsTransient covers both routes and neither', () => {
  assert.equal(requestIsTransient(undefined, { transient: true } as never, undefined), true)
  assert.equal(requestIsTransient({ scratch: true } as never, {} as never, undefined), true)
  assert.equal(requestIsTransient({ id: 'c1' } as never, {} as never, 'c1'), true)
  assert.equal(requestIsTransient({ id: 'c1' } as never, {} as never, 'other'), false)
  assert.equal(requestIsTransient(undefined, undefined, undefined), false)
})

// The cancel affordance has to name what it will actually do — "Disconnect" for
// a socket, "Cancel stream" for gRPC, "Cancel request" for HTTP.
test('the cancel label names the thing being cancelled', () => {
  assert.equal(state({ httpInFlight: true }).cancelLabel, 'Cancel request')
  assert.equal(state({ request: { type: 'websocket' }, webSocketConnected: true }).cancelLabel, 'Disconnect')
  assert.equal(state({ request: { type: 'grpc' }, grpcConnected: true }).cancelLabel, 'Cancel stream')
})

test('cancel is offered only while something is running', () => {
  assert.equal(state({}).canCancel, false)
  assert.equal(state({ httpInFlight: true }).canCancel, true)
  assert.equal(state({ webSocketConnected: true }).canCancel, true)
  assert.equal(state({ grpcConnected: true }).canCancel, true)
})

// A cancelled response is not a failure and not a success; showing it as either
// misreports what happened.
test('a cancelled response reads as cancelled, not as an error', () => {
  const cancelled = state({ request: { response: { cancelled: true, status: 500 } } })
  assert.equal(cancelled.response.status, 'Cancelled')
  assert.equal(cancelled.response.tone, 'warning')
  assert.equal(cancelled.response.statusText, 'Request cancelled')
})

test('the response tone follows the status class', () => {
  assert.equal(state({ request: { response: { status: 200 } } }).response.tone, 'success')
  assert.equal(state({ request: { response: { status: 301 } } }).response.tone, 'warning')
  assert.equal(state({ request: { response: { status: 404 } } }).response.tone, 'danger')
  assert.equal(state({ request: { response: { status: 500 } } }).response.tone, 'danger')
  assert.equal(state({}).response.tone, 'idle')
})

test('an errored response says so rather than showing a blank status text', () => {
  assert.equal(state({ request: { response: { error: 'boom' } } }).response.statusText, 'Request failed')
  assert.equal(state({}).response.statusText, 'No response yet')
})

test('byte sizes scale and keep one decimal only where it adds information', () => {
  assert.equal(formatRuntimeBytes(undefined), '0 B')
  assert.equal(formatRuntimeBytes(0), '0 B')
  assert.equal(formatRuntimeBytes(512), '512 B', 'bytes are never fractional')
  assert.equal(formatRuntimeBytes(1024), '1.0 KB')
  assert.equal(formatRuntimeBytes(1536), '1.5 KB')
  assert.equal(formatRuntimeBytes(10 * 1024), '10 KB', 'past ten the decimal is noise')
  assert.equal(formatRuntimeBytes(1024 * 1024), '1.0 MB')
  assert.equal(formatRuntimeBytes(5 * 1024 * 1024 * 1024), '5.0 GB')
  assert.equal(formatRuntimeBytes(1024 ** 5), '1048576 GB', 'the largest unit absorbs the rest rather than overflowing')
})

test('an absent environment is named rather than left blank', () => {
  assert.equal(state({}).environmentName, 'No environment')
  assert.equal(state({ environmentName: 'staging' }).environmentName, 'staging')
})
