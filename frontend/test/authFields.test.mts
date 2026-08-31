// The auth field set is one list, and every level renders that list.
//
// The bug this pins is not a crash. Folder-level auth was a hand-copied,
// smaller version of request-level auth: OAuth2 lost fourteen fields, OAuth1
// nine, AWSv4 two, and nothing on screen said so. A user who hoisted a working
// request config up to the folder lost most of it silently.
//
// Two things are asserted, and they have to be asserted together:
//
//   * the schema below is complete — every field the biggest form ever had is
//     still in it, so "extract the form" cannot quietly mean "extract the small
//     one";
//   * every auth surface in App.svelte renders that schema rather than its own
//     markup, so a fifth surface added later cannot start the drift again.
//
// The second is a source-text assertion because the repo has no
// component-rendering harness; see secretMasking.test.mts for the same shape.

import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import {
  apiKeyPlacementOptions,
  authFieldsFor,
  authModeHasFields,
  authModeNotes,
  authModes,
  normalizeApiKeyPlacement,
  oauth2TokenPlacementField
} from '../src/lib/authFields.ts'

const read = (relative: string) => readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const namesFor = (mode: string, grant = '') =>
  authFieldsFor(mode, grant).map((field) => `${field.group}.${field.name}`)

test('every mode except none and inherit has fields, and those two have copy instead', () => {
  for (const mode of authModes) {
    if (mode === 'none' || mode === 'inherit') {
      assert.equal(authModeHasFields(mode), false, `${mode} should have no fields`)
      assert.ok(authModeNotes[mode], `${mode} has no fields and no explanation either`)
    } else {
      assert.ok(authModeHasFields(mode), `${mode} renders an empty form`)
    }
  }
})

test('inherit is explained, not called unimplemented', () => {
  // A5-01's regression guard from the other direction: the message must
  // describe the resolution rule, never the "partial signer" wording.
  assert.match(authModeNotes.inherit, /nearest parent/i)
  assert.doesNotMatch(authModeNotes.inherit, /partial|unimplemented/i)
})

test('API key placement has exactly one stored vocabulary', () => {
  // The folder form wrote `queryparams` for the same choice the request form
  // wrote `query` for. One of them had to go, and it is not enough to fix the
  // markup — the option list itself must not be able to offer the other value.
  assert.deepEqual(
    apiKeyPlacementOptions.map((option) => option.value),
    ['header', 'query']
  )
})

test('a folder that already stored queryparams reads back as query', () => {
  // Existing folders on disk carry the old spelling. Reading it as Header would
  // be a silent, wrong answer about where the key is sent.
  assert.equal(normalizeApiKeyPlacement('queryparams'), 'query')
  assert.equal(normalizeApiKeyPlacement('QueryParams'), 'query')
  assert.equal(normalizeApiKeyPlacement('query'), 'query')
  assert.equal(normalizeApiKeyPlacement('url'), 'query')
  assert.equal(normalizeApiKeyPlacement('header'), 'header')
  assert.equal(normalizeApiKeyPlacement(''), 'header')
  assert.equal(normalizeApiKeyPlacement(undefined), 'header')
})

test('OAuth2 keeps every field the request-level form used to have', () => {
  // The list the folder form was missing. Written out rather than counted so a
  // deletion names itself in the failure.
  const authCode = namesFor('oauth2', 'authorization_code')
  for (const field of [
    'oauth2.grantType',
    'oauth2.callbackUrl',
    'oauth2.authorizationUrl',
    'oauth2.accessTokenUrl',
    'oauth2.clientId',
    'oauth2.clientSecret',
    'oauth2.scope',
    'oauth2.state',
    'oauth2.credentialsPlacement',
    'oauth2.pkce',
    'oauth2.tokenSource',
    'oauth2.tokenPlacement'
  ]) {
    assert.ok(authCode.includes(field), `${field} is gone from the authorization_code form`)
  }
  const password = namesFor('oauth2', 'password')
  assert.ok(password.includes('oauth2.username'), 'the password grant lost its username')
  assert.ok(password.includes('oauth2.password'), 'the password grant lost its password')
})

test('OAuth2 hides the fields a grant type genuinely does not have', () => {
  // Not cosmetic: a value typed into a field that is never sent is a debugging
  // dead end, and the implicit grant has no token endpoint at all.
  const implicit = namesFor('oauth2', 'implicit')
  assert.ok(!implicit.includes('oauth2.accessTokenUrl'))
  const clientCredentials = namesFor('oauth2', 'client_credentials')
  assert.ok(!clientCredentials.includes('oauth2.callbackUrl'))
  assert.ok(!clientCredentials.includes('oauth2.pkce'))
  assert.ok(!clientCredentials.includes('oauth2.username'))
})

test('token placement decides which follow-up field is shown', () => {
  assert.equal(oauth2TokenPlacementField('header').name, 'tokenHeaderPrefix')
  assert.equal(oauth2TokenPlacementField(undefined).name, 'tokenHeaderPrefix')
  assert.equal(oauth2TokenPlacementField('url').name, 'tokenQueryKey')
})

test('OAuth1 keeps every field the request-level form used to have', () => {
  const fields = namesFor('oauth1')
  for (const field of [
    'oauth1.consumerKey',
    'oauth1.consumerSecret',
    'oauth1.accessToken',
    'oauth1.accessTokenSecret',
    'oauth1.signatureMethod',
    'oauth1.placement',
    'oauth1.callbackUrl',
    'oauth1.verifier',
    'oauth1.timestamp',
    'oauth1.nonce',
    'oauth1.version',
    'oauth1.realm',
    'oauth1.privateKey',
    'oauth1.privateKeyType',
    'oauth1.includeBodyHash'
  ]) {
    assert.ok(fields.includes(field), `${field} is gone from the OAuth1 form`)
  }
})

test('AWSv4 keeps the session token and the profile the folder form dropped', () => {
  const fields = namesFor('awsv4')
  assert.ok(fields.includes('awsv4.sessionToken'))
  assert.ok(fields.includes('awsv4.profileName'))
})

test('credential-bearing fields are masked, and non-secrets are not', () => {
  // The rule is "if it is a credential it is a secret field", stated as a list
  // so adding a new secret without masking it fails here.
  const secretsOf = (mode: string, grant = '') =>
    authFieldsFor(mode, grant).filter((field) => field.kind === 'secret').map((field) => field.name)

  assert.deepEqual(secretsOf('basic'), ['password'])
  assert.deepEqual(secretsOf('bearer'), ['token'])
  assert.deepEqual(secretsOf('apikey'), ['apiValue'])
  assert.deepEqual(secretsOf('awsv4'), ['secretAccessKey', 'sessionToken'])
  assert.deepEqual(secretsOf('oauth1'), ['consumerSecret', 'accessTokenSecret'])
  assert.deepEqual(secretsOf('oauth2', 'password'), ['clientSecret', 'password'])
})

test('no mode declares the same storage path twice', () => {
  // A duplicate path renders two controls writing the same value, and whichever
  // is edited second wins for reasons invisible on screen.
  for (const mode of authModes) {
    for (const grant of ['client_credentials', 'password', 'authorization_code', 'implicit']) {
      const paths = namesFor(mode, grant)
      assert.equal(new Set(paths).size, paths.length, `${mode}/${grant} has a duplicate field`)
    }
  }
})

test('every field is labelled, and no placeholder just repeats its label', () => {
  // A placeholder that restates the label teaches nothing; the point of adding
  // them was "what does a valid value look like".
  for (const mode of authModes) {
    for (const field of authFieldsFor(mode, 'authorization_code')) {
      assert.ok(field.label.trim(), `${mode}.${field.name} has no label`)
      if (field.placeholder) {
        assert.notEqual(
          field.placeholder.trim().toLowerCase(),
          field.label.trim().toLowerCase(),
          `${mode}.${field.name} placeholder restates its label`
        )
      }
    }
  }
})

test('every auth surface renders the shared form rather than its own markup', () => {
  // The anti-drift guard. Four hand-copied forms is how this area broke; the
  // count is asserted so a fifth surface has to join them rather than start a
  // fifth copy.
  const app = read('../src/App.svelte')
  const usages = [...app.matchAll(/<AuthForm\b/g)].length
  assert.ok(usages >= 4, `expected at least four <AuthForm> call sites, found ${usages}`)

  // And no surface may go back to spelling the auth fields out by hand. These
  // were the tell-tale strings of the four copies.
  assert.ok(!app.includes('<option value="queryparams">'), 'the folder form still writes queryparams')
  assert.doesNotMatch(
    app,
    /field-label">Consumer secret</,
    'an auth form is spelling OAuth1 fields out again instead of using AuthForm'
  )
  assert.doesNotMatch(
    app,
    /field-label">Client secret</,
    'an auth form is spelling OAuth2 fields out again instead of using AuthForm'
  )
})
