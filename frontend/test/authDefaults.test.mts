// Filling in auth and proxy defaults.
//
// Three-way merges run on every keystroke: stored value, user update, protocol
// default. Getting the order wrong overwrites what someone just typed, or makes
// a field impossible to clear — both look like the editor is fighting you.

import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  oauth2AuthWithDefaults,
  authWithOAuth2Defaults,
  proxyConfigWithDefaults,
  collectionProxyWithDefaults,
  preferenceProxyModeValue,
  proxyModeLabel,
  proxyModeOverrides,
  proxyPreferencesWithDefaults,
} from '../src/lib/authDefaults.ts'
import type { types } from '../wailsjs/go/models'

// These are protocol requirements, not preferences: a token request with no
// grant type is one the server rejects.
test('oauth2 defaults fill every field the protocol needs', () => {
  const filled = oauth2AuthWithDefaults(undefined)
  assert.equal(filled.grantType, 'client_credentials')
  assert.equal(filled.credentialsPlacement, 'basic_auth_header')
  assert.equal(filled.tokenSource, 'access_token')
  assert.equal(filled.tokenPlacement, 'header')
  assert.equal(filled.tokenHeaderPrefix, 'Bearer')
  assert.equal(filled.tokenQueryKey, 'access_token')
})

test('a stored value survives defaulting', () => {
  const stored = { grantType: 'authorization_code', tokenHeaderPrefix: 'Token' } as never
  const filled = oauth2AuthWithDefaults(stored)
  assert.equal(filled.grantType, 'authorization_code')
  assert.equal(filled.tokenHeaderPrefix, 'Token')
  assert.equal(filled.tokenPlacement, 'header', 'unset fields still get their default')
})

// The update must win over the stored value, or typing would appear to do
// nothing.
test('an update overrides what is stored', () => {
  const filled = oauth2AuthWithDefaults({ grantType: 'password' } as never, { grantType: 'authorization_code' } as never)
  assert.equal(filled.grantType, 'authorization_code')
})

// Defaulting after merging is what makes this true. Default-then-merge would
// leave the cleared field holding the default forever.
test('clearing a field falls back to the default rather than sticking', () => {
  const filled = oauth2AuthWithDefaults({ grantType: 'password' } as never, { grantType: '' } as never)
  assert.equal(filled.grantType, 'client_credentials', 'an emptied field returns to the protocol default')
})

test('fields with no default are carried through untouched', () => {
  const filled = oauth2AuthWithDefaults({ clientId: 'abc', scope: 'read' } as never)
  assert.equal(filled.clientId, 'abc')
  assert.equal(filled.scope, 'read')
})

// OAuth2 defaults are only written when the request actually uses OAuth2, or
// every basic-auth request would carry a pointless oauth2 block.
test('oauth2 defaults apply only when the mode is oauth2 or oauth2 was edited', () => {
  const basic = authWithOAuth2Defaults({ mode: 'basic', username: 'u' } as never)
  assert.equal(basic.oauth2, undefined, 'a basic-auth request must not grow an oauth2 block')

  const oauth = authWithOAuth2Defaults({ mode: 'oauth2' } as never)
  assert.equal(oauth.oauth2?.grantType, 'client_credentials')

  const edited = authWithOAuth2Defaults({ mode: 'basic' } as never, { oauth2: { clientId: 'x' } } as never)
  assert.equal(edited.oauth2?.clientId, 'x', 'editing the oauth2 block fills its defaults whatever the mode')
  assert.equal(edited.oauth2?.grantType, 'client_credentials')
})

test('auth updates override stored values', () => {
  const merged = authWithOAuth2Defaults({ mode: 'basic', username: 'old' } as never, { username: 'new' } as never)
  assert.equal(merged.username, 'new')
  assert.equal(merged.mode, 'basic')
})

test('an absent auth config is not an error', () => {
  const merged = authWithOAuth2Defaults(undefined, { mode: 'oauth2' } as never)
  assert.equal(merged.mode, 'oauth2')
  assert.equal(merged.oauth2?.tokenPlacement, 'header')
})

test('proxy defaults fill the protocol and leave the rest blank', () => {
  const filled = proxyConfigWithDefaults(undefined)
  assert.equal(filled.protocol, 'http')
  assert.equal(filled.hostname, '')
  assert.equal(filled.port, '')
  assert.equal(filled.bypassProxy, '')
  assert.equal(filled.inherit, false)
  assert.equal(filled.disabled, false)
})

test('proxy auth is defaulted as its own object', () => {
  const filled = proxyConfigWithDefaults(undefined)
  assert.equal(filled.auth?.username, '')
  assert.equal(filled.auth?.password, '')
  assert.equal(filled.auth?.disabled, false)
})

test('a stored proxy survives defaulting and an override wins', () => {
  const stored = { protocol: 'socks5', hostname: 'p.test', port: '1080' } as never
  assert.equal(proxyConfigWithDefaults(stored).protocol, 'socks5')
  assert.equal(proxyConfigWithDefaults(stored, { protocol: 'https' } as never).protocol, 'https')
  assert.equal(proxyConfigWithDefaults(stored, { protocol: 'https' } as never).hostname, 'p.test')
})

// A partial auth override must not wipe the fields it did not mention.
test('a partial proxy auth override keeps the other credential fields', () => {
  const stored = { auth: { username: 'u', password: 'p', disabled: false } } as never
  const filled = proxyConfigWithDefaults(stored, { auth: { password: 'new' } } as never)
  assert.equal(filled.auth?.password, 'new')
  assert.equal(filled.auth?.username, 'u', 'the username must survive a password-only edit')
})

test('proxy overrides can set the boolean flags', () => {
  assert.equal(proxyConfigWithDefaults(undefined, { disabled: true } as never).disabled, true)
  assert.equal(proxyConfigWithDefaults(undefined, { inherit: true } as never).inherit, true)
})

// A collection with nothing configured INHERITS, while the preferences-level
// config does not. That single field is why both functions exist: defaulting it
// to false would opt every existing collection out of the user's system proxy
// the first time this ran.
test('an unconfigured collection proxy inherits, unlike the preferences one', () => {
  assert.equal(collectionProxyWithDefaults(undefined, true).inherit, true)
  assert.equal(proxyConfigWithDefaults(undefined).inherit, false)
})

test('a configured collection proxy keeps its own inherit flag', () => {
  const config = { inherit: false, hostname: 'proxy.test' } as types.ProxyConfig
  assert.equal(collectionProxyWithDefaults(config, false).inherit, false)
  assert.equal(collectionProxyWithDefaults({} as types.ProxyConfig, false).inherit, true)
})

// The auth block is merged after the spread precisely so this holds. A single
// spread would drop every field the caller did not resend, silently clearing
// the proxy password on an unrelated edit.
test('changing the proxy username keeps the stored password', () => {
  const config = {
    hostname: 'proxy.test',
    auth: { username: 'old', password: 'secret', disabled: false }
  } as types.ProxyConfig
  const next = collectionProxyWithDefaults(config, false, {
    auth: { username: 'new' } as types.ProxyAuthConfig
  })
  assert.equal(next.auth?.username, 'new')
  assert.equal(next.auth?.password, 'secret')
})

test('an override outside the auth block does not touch the credentials', () => {
  const config = { auth: { username: 'u', password: 'p' } } as types.ProxyConfig
  const next = collectionProxyWithDefaults(config, false, { hostname: 'other.test' })
  assert.equal(next.hostname, 'other.test')
  assert.equal(next.auth?.password, 'p')
})

// Off and inherit leave the manual settings untouched: the user is choosing not
// to use them, not discarding them, and coming back to manual must restore what
// they typed.
test('a proxy mode sets only its two flags', () => {
  assert.deepEqual(proxyModeOverrides('manual'), { inherit: false, disabled: false })
  assert.deepEqual(proxyModeOverrides('off'), { inherit: false, disabled: true })
  assert.deepEqual(proxyModeOverrides('system'), { inherit: true, disabled: false })
  assert.deepEqual(proxyModeOverrides('anything else'), { inherit: true, disabled: false })
})

test('switching a configured proxy off and back keeps the host and port', () => {
  const configured = { hostname: 'proxy.test', port: '8080', inherit: false } as types.ProxyConfig
  const off = collectionProxyWithDefaults(configured, false, proxyModeOverrides('off'))
  const back = collectionProxyWithDefaults(off, false, proxyModeOverrides('manual'))
  assert.equal(back.hostname, 'proxy.test')
  assert.equal(back.port, '8080')
  assert.equal(back.disabled, false)
})

// `source` replaced `proxyMode`. A preferences file written before that change
// has only the old field, and ignoring it lets an upgrade silently reset a
// configured PAC or manual proxy to the system one.
test('the legacy proxyMode field is read when source is absent', () => {
  assert.equal(proxyPreferencesWithDefaults({} as types.ProxyPreferences, 'pac').source, 'pac')
  assert.equal(proxyPreferencesWithDefaults({} as types.ProxyPreferences, 'manual').source, 'manual')
  assert.equal(proxyPreferencesWithDefaults({} as types.ProxyPreferences, undefined).source, 'inherit')
})

test('a stored source wins over the legacy field', () => {
  const current = { source: 'manual' } as types.ProxyPreferences
  assert.equal(proxyPreferencesWithDefaults(current, 'pac').source, 'manual')
})

// Disabled is checked first and wins over any configured source: turning the
// proxy off has to mean off, whatever manual or PAC settings are saved beside
// it.
test('a disabled proxy reads as off however it is configured', () => {
  for (const source of ['manual', 'pac', '']) {
    assert.equal(
      preferenceProxyModeValue({ disabled: true, source } as types.ProxyPreferences),
      'off',
      source
    )
  }
})

test('an enabled proxy reads as its source, defaulting to system', () => {
  assert.equal(preferenceProxyModeValue({ source: 'manual' } as types.ProxyPreferences), 'manual')
  assert.equal(preferenceProxyModeValue({ source: 'pac' } as types.ProxyPreferences), 'pac')
  assert.equal(preferenceProxyModeValue({} as types.ProxyPreferences), 'system')
  assert.equal(preferenceProxyModeValue(undefined), 'system')
})

// The stored value and the visible word are two different vocabularies. "manual"
// reads as "On", because the control it labels is a toggle over the manual
// settings shown beside it.
test('the mode label is not the mode name', () => {
  assert.equal(proxyModeLabel('manual'), 'On')
  assert.notEqual(proxyModeLabel('manual'), 'manual')
  assert.equal(proxyModeLabel('off'), 'Off')
  assert.equal(proxyModeLabel('pac'), 'PAC')
  assert.equal(proxyModeLabel('system'), 'System Proxy')
  assert.equal(proxyModeLabel(''), 'System Proxy')
})

// Every mode the value function can produce must have a label, or the control
// renders blank for a state the app can genuinely be in.
test('every mode value has a label', () => {
  const values = ['off', 'manual', 'pac', 'system']
  for (const value of values) {
    assert.notEqual(proxyModeLabel(value), '', value)
  }
  assert.equal(new Set(values.map(proxyModeLabel)).size, values.length, 'two modes share a label')
})
