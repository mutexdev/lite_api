import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  DEFAULT_OPENAPI_SYNC_INTERVAL,
  allEndpointDecisions,
  defaultOpenAPISyncDecision,
  formatOpenAPISyncCheckedAt,
  formattedOpenAPISpecContent,
  normalizedOpenAPISyncSettingsInterval,
  openAPILocalDriftIDs,
  openAPILocalDriftLabel,
  openAPISyncAutoCheckEnabled,
  openAPISyncAutoCheckStatusLine,
  openAPISyncConfigFor,
  openAPISyncIntervalMinutes,
  openAPISyncSpecDiffSummary,
  reconcileEndpointDecisions,
} from '../src/lib/openApiSync.ts'
import type { types } from '../wailsjs/go/models'

function config(over: Partial<types.OpenAPISyncConfig> = {}): types.OpenAPISyncConfig {
  return { sourceUrl: 'https://example.test/spec.json', ...over } as types.OpenAPISyncConfig
}

function change(over: Partial<types.OpenAPISyncEndpointChange> = {}): types.OpenAPISyncEndpointChange {
  return { id: 'e1', ...over } as types.OpenAPISyncEndpointChange
}

test('the sync config is the first entry on the collection', () => {
  const cfg = config()
  assert.equal(openAPISyncConfigFor({ openapi: [cfg] } as types.Collection), cfg)
  assert.equal(openAPISyncConfigFor(undefined), undefined)
  assert.equal(openAPISyncConfigFor({} as types.Collection), undefined)
})

// A zero or negative interval becomes a setInterval that fires continuously,
// which turns the auto-check into a request flood against the spec's host.
test('a non-positive or unparseable interval falls back to the default', () => {
  for (const value of [0, -5, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(
      openAPISyncIntervalMinutes(config({ autoCheckInterval: value })),
      DEFAULT_OPENAPI_SYNC_INTERVAL,
      String(value),
    )
  }
  assert.equal(openAPISyncIntervalMinutes(config({ autoCheckInterval: 30 })), 30)
  assert.equal(openAPISyncIntervalMinutes(undefined), DEFAULT_OPENAPI_SYNC_INTERVAL)
})

// A config saved before the flag existed has autoCheck undefined. Reading it as
// truthy would silently stop polling for every collection connected by an older
// build.
test('auto-check is on unless it is explicitly false', () => {
  assert.equal(openAPISyncAutoCheckEnabled(config()), true)
  assert.equal(openAPISyncAutoCheckEnabled(config({ autoCheck: undefined })), true)
  assert.equal(openAPISyncAutoCheckEnabled(config({ autoCheck: false })), false)
})

test('auto-check is off without a source url, whatever the flag says', () => {
  assert.equal(openAPISyncAutoCheckEnabled(config({ sourceUrl: '', autoCheck: true })), false)
})

// An interval outside the offered list leaves the <select> with no matching
// option, which renders blank and then saves whatever the next interaction
// happens to land on.
test('a stored interval is snapped onto one the dialog can display', () => {
  assert.equal(normalizedOpenAPISyncSettingsInterval(15), 15)
  assert.equal(normalizedOpenAPISyncSettingsInterval(7), DEFAULT_OPENAPI_SYNC_INTERVAL)
  assert.equal(normalizedOpenAPISyncSettingsInterval(undefined), DEFAULT_OPENAPI_SYNC_INTERVAL)
})

test('an unparseable timestamp shows the raw value rather than "Invalid Date"', () => {
  assert.equal(formatOpenAPISyncCheckedAt('not a time'), 'not a time')
  assert.equal(formatOpenAPISyncCheckedAt(''), '')
  assert.equal(formatOpenAPISyncCheckedAt(undefined), '')
  assert.notEqual(formatOpenAPISyncCheckedAt('2026-01-02T03:04:05Z'), '2026-01-02T03:04:05Z')
})

test('the status line reports the cadence when auto-check is on', () => {
  const line = openAPISyncAutoCheckStatusLine({
    config: config({ autoCheckInterval: 30 }),
    hasCollection: true,
  })
  assert.equal(line, 'Auto Check for Updates: Every 30 min')
})

test('the status line says disabled when auto-check is off', () => {
  const line = openAPISyncAutoCheckStatusLine({
    config: config({ autoCheck: false }),
    hasCollection: true,
    status: { checkedAt: '2026-01-02T03:04:05Z', hasUpdates: true },
  })
  assert.equal(line, 'Auto Check for Updates: Disabled')
})

test('there is no status line at all without a source url', () => {
  assert.equal(openAPISyncAutoCheckStatusLine({ config: config({ sourceUrl: '' }), hasCollection: true }), '')
  assert.equal(openAPISyncAutoCheckStatusLine({ config: undefined, hasCollection: true }), '')
})

// A failed check must not also show the last successful result: side by side,
// "Updates found" reads as though the spec were still being tracked, which is
// the opposite of what the failure means.
test('a failed check reports the failure and nothing else', () => {
  const line = openAPISyncAutoCheckStatusLine({
    config: config(),
    hasCollection: true,
    errorMessage: 'dial tcp: connection refused',
    status: { checkedAt: '2026-01-02T03:04:05Z', hasUpdates: true },
  })
  assert.match(line, /Last check failed$/)
  assert.doesNotMatch(line, /Updates found/)
})

test('a successful check reports whether updates were found, with the time', () => {
  const found = openAPISyncAutoCheckStatusLine({
    config: config(),
    hasCollection: true,
    status: { checkedAt: '2026-01-02T03:04:05Z', hasUpdates: true },
  })
  const clean = openAPISyncAutoCheckStatusLine({
    config: config(),
    hasCollection: true,
    status: { checkedAt: '2026-01-02T03:04:05Z', hasUpdates: false },
  })
  assert.match(found, /· Updates found /)
  assert.match(clean, /· No updates /)
})

test('a JSON spec is pretty-printed and everything else is left alone', () => {
  assert.equal(formattedOpenAPISpecContent('{"a":1}'), '{\n  "a": 1\n}')
  assert.equal(formattedOpenAPISpecContent('openapi: 3.0.0\ninfo: {}'), 'openapi: 3.0.0\ninfo: {}')
  assert.equal(formattedOpenAPISpecContent('{ broken'), '{ broken')
  assert.equal(formattedOpenAPISpecContent(undefined), '')
})

// A spec served with a leading newline or indentation is ordinary. Without the
// trimStart it fails the `{` test and renders as the unformatted single line it
// arrived as — which is what the viewer exists to avoid.
//
// This replaces a test that asserted a YAML document came back unchanged. That
// one could not fail: a YAML mapping is not valid JSON, so removing the `{`
// test entirely still routed it through the catch and returned it untouched.
test('leading whitespace does not stop a JSON spec being formatted', () => {
  assert.equal(formattedOpenAPISpecContent('\n  {"a":1}'), '{\n  "a": 1\n}')
})

// The `{` test's only observable effect: top-level JSON that is not an object.
// A YAML mapping reaches the same catch with or without it, so this is the one
// input that distinguishes the two.
test('top-level JSON that is not an object is left as it arrived', () => {
  assert.equal(formattedOpenAPISpecContent('[1,2,3]'), '[1,2,3]')
  assert.equal(formattedOpenAPISpecContent('42'), '42')
})

test('the spec diff summary counts an absent field as zero', () => {
  assert.equal(openAPISyncSpecDiffSummary(undefined), '')
  assert.equal(
    openAPISyncSpecDiffSummary({ added: 2 } as types.OpenAPISyncSpecDiffResult),
    '2 added · 0 updated · 0 removed · 0 unchanged',
  )
})

test('a change without a backend default is accepted by default', () => {
  assert.equal(defaultOpenAPISyncDecision(change()), 'accept-incoming')
  assert.equal(defaultOpenAPISyncDecision(change({ defaultDecision: 'keep-mine' })), 'keep-mine')
})

// Re-running the check must not throw away choices the user already made — the
// dialog is often re-opened between deciding and applying.
test('a decision the user made survives reconciliation', () => {
  const got = reconcileEndpointDecisions([change({ id: 'a' }), change({ id: 'b' })], { a: 'keep-mine' })
  assert.deepEqual(got, { a: 'keep-mine', b: 'accept-incoming' })
})

// The map is rebuilt from whatever it held before. A value left by an older
// build or a renamed decision would otherwise ride along and be handed to the
// backend as an endpoint's disposition — deciding whether local edits survive.
test('an unrecognised decision is replaced by the default, not carried through', () => {
  for (const stale of ['merge', '', 'ACCEPT-INCOMING', 'accept_incoming']) {
    const got = reconcileEndpointDecisions([change({ id: 'a', defaultDecision: 'keep-mine' })], { a: stale })
    assert.deepEqual(got, { a: 'keep-mine' }, stale)
  }
})

// Built fresh rather than merged: a decision for an endpoint the spec no longer
// has would accumulate forever, and re-apply if that id ever came back.
test('a decision for a change that is gone is dropped', () => {
  assert.deepEqual(reconcileEndpointDecisions([change({ id: 'b' })], { a: 'keep-mine' }), {
    b: 'accept-incoming',
  })
  assert.deepEqual(reconcileEndpointDecisions([], { a: 'keep-mine' }), {})
  assert.deepEqual(reconcileEndpointDecisions(undefined, { a: 'keep-mine' }), {})
})

test('the bulk buttons set every change to one decision', () => {
  assert.deepEqual(allEndpointDecisions([change({ id: 'a' }), change({ id: 'b' })], 'keep-mine'), {
    a: 'keep-mine',
    b: 'keep-mine',
  })
  assert.deepEqual(allEndpointDecisions(undefined, 'keep-mine'), {})
})

test('drift ids are filtered to one kind', () => {
  const result = {
    changes: [
      { id: 'a', change: 'missing' },
      { id: 'b', change: 'local-only' },
      { id: 'c', change: 'missing' },
    ],
  } as types.OpenAPILocalDriftResult
  assert.deepEqual(openAPILocalDriftIDs(result, 'missing'), ['a', 'c'])
  assert.deepEqual(openAPILocalDriftIDs(result, 'local-only'), ['b'])
  assert.deepEqual(openAPILocalDriftIDs(undefined, 'missing'), [])
})

// "missing" and "local-only" are written from the SPEC's point of view; the
// panel reads from the collection's, where a request the spec lacks was ADDED
// and one the collection lacks was DELETED. Rendering the raw values inverts
// both, telling the user their new request was deleted.
test('the drift labels are stated from the collection\'s point of view', () => {
  assert.equal(openAPILocalDriftLabel('missing'), 'deleted')
  assert.equal(openAPILocalDriftLabel('local-only'), 'added')
  assert.notEqual(openAPILocalDriftLabel('missing'), openAPILocalDriftLabel('local-only'))
  assert.equal(openAPILocalDriftLabel('changed'), 'changed')
})
