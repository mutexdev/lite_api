// The OAuth2 form's answer to "is there a token, and is it any good".
//
// The failure being closed is an absence: twenty fields whose entire purpose is
// to obtain a token, and no statement anywhere about the token. The states
// below are all clock-dependent or store-dependent, which is exactly why they
// live in a pure function taking `now` and the record — the alternative was
// four `$derived` expressions in a 12,000-line component that nothing could
// exercise.

import assert from 'node:assert/strict'
import test from 'node:test'

import {
  EXPIRING_WINDOW_MS,
  oauth2MissingFields,
  oauth2TokenStatus,
  staticTokenHelp
} from '../src/lib/oauth2TokenStatus.ts'

const NOW = 1_700_000_000_000

const complete = {
  grantType: 'client_credentials',
  accessTokenUrl: 'https://auth.example.com/token',
  clientId: 'client',
  clientSecret: 'secret'
}

test('a config missing required fields names them, and cannot fetch', () => {
  const status = oauth2TokenStatus({ grantType: 'client_credentials' }, undefined, NOW)

  assert.equal(status.canFetch, false)
  assert.deepEqual(status.missing, ['Access token URL', 'Client ID', 'Client secret'])
  assert.match(status.detail, /Access token URL/)
})

test('a complete config with no visible token says so honestly', () => {
  // Not "No token": the app cannot see the store, and claiming a token does not
  // exist would be an assertion it is not entitled to make.
  const status = oauth2TokenStatus(complete, undefined, NOW)

  assert.equal(status.state, 'unknown')
  assert.equal(status.canFetch, true)
  assert.equal(status.tone, 'idle')
  assert.doesNotMatch(status.summary, /^No token/)
})

test('a live token reports how long it has left', () => {
  const status = oauth2TokenStatus(complete, { accessToken: 't', expiresAt: NOW + 42 * 60000 }, NOW)

  assert.equal(status.state, 'active')
  assert.equal(status.tone, 'success')
  assert.equal(status.summary, 'Expires in 42m')
})

test('an expiry over an hour away reads in hours, not ninety minutes', () => {
  const status = oauth2TokenStatus(complete, { accessToken: 't', expiresAt: NOW + 90 * 60000 }, NOW)

  assert.equal(status.summary, 'Expires in 1h 30m')
})

test('a token inside the warning window is amber, not green', () => {
  // The point of the window is to be seen BEFORE the next send fails.
  const status = oauth2TokenStatus(
    complete,
    { accessToken: 't', expiresAt: NOW + EXPIRING_WINDOW_MS - 1000 },
    NOW
  )

  assert.equal(status.state, 'expiring')
  assert.equal(status.tone, 'warning')
})

test('an expired token is red and says whether a refresh is possible', () => {
  const withRefresh = oauth2TokenStatus(
    complete,
    { accessToken: 't', refreshToken: 'r', expiresAt: NOW - 60000 },
    NOW
  )
  assert.equal(withRefresh.state, 'expired')
  assert.equal(withRefresh.tone, 'danger')
  assert.match(withRefresh.summary, /Expired 1m ago/)
  assert.match(withRefresh.detail, /refresh/i)

  const without = oauth2TokenStatus(complete, { accessToken: 't', expiresAt: NOW - 60000 }, NOW)
  assert.match(without.detail, /No refresh token/)
})

test('a fetch error outranks every other state and is shown verbatim', () => {
  // A stale "expires in 20m" beside a failed refresh is the worst of both.
  const status = oauth2TokenStatus(
    complete,
    { accessToken: 't', expiresAt: NOW + 20 * 60000, error: 'invalid_client' },
    NOW
  )

  assert.equal(status.state, 'error')
  assert.equal(status.tone, 'danger')
  assert.equal(status.detail, 'invalid_client')
})

test('a token with no expiry is active rather than silently expired', () => {
  const status = oauth2TokenStatus(complete, { accessToken: 't' }, NOW)

  assert.equal(status.state, 'active')
  assert.match(status.detail, /did not return an expiry/)
})

test('missing fields follow the grant type', () => {
  // The password grant needs credentials the client-credentials grant does not,
  // and this must agree with what the form actually renders.
  const password = oauth2MissingFields({ ...complete, grantType: 'password' })
  assert.deepEqual(password, ['Username', 'Password'])

  const implicit = oauth2MissingFields({
    grantType: 'implicit',
    authorizationUrl: 'https://auth.example.com/authorize',
    callbackUrl: 'http://localhost/cb',
    clientId: 'client'
  })
  assert.deepEqual(implicit, [], 'the implicit grant needs neither token URL nor client secret')
})

test('the static token field explains which of two things it is', () => {
  assert.match(staticTokenHelp(complete, ''), /instead of fetching/)
  assert.match(staticTokenHelp(complete, 'abc'), /replaces it/)
})
