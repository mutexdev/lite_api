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
  proxyConfigWithDefaults
} from '../src/lib/authDefaults.ts'

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
