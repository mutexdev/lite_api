// Filling in auth and proxy defaults before an edit is saved.
//
// These run on every keystroke in the auth and proxy editors, and each one
// merges three things: what is stored, what the user just changed, and what the
// protocol requires when neither says anything.
//
// Order matters and is easy to get backwards. The stored value is the base, the
// user's update overrides it, and the DEFAULT applies only when the merged
// result is still empty. Defaulting before merging would overwrite what the
// user typed; defaulting a field the user deliberately cleared would make it
// impossible to clear.
//
// The OAuth2 defaults are protocol requirements rather than preferences: a
// token request with no grant type, or a bearer token sent with no header
// prefix, is a request the server rejects. Writing them in at edit time means
// the stored request is always sendable.

import type { types } from '../../wailsjs/go/models'

export function oauth2AuthWithDefaults(auth: types.OAuth2Auth | undefined, updates: Partial<types.OAuth2Auth> = {}) {
  const merged = { ...(auth ?? {}), ...updates } as types.OAuth2Auth
  return {
    ...merged,
    grantType: merged.grantType || 'client_credentials',
    credentialsPlacement: merged.credentialsPlacement || 'basic_auth_header',
    tokenSource: merged.tokenSource || 'access_token',
    tokenPlacement: merged.tokenPlacement || 'header',
    tokenHeaderPrefix: merged.tokenHeaderPrefix || 'Bearer',
    tokenQueryKey: merged.tokenQueryKey || 'access_token'
  } as types.OAuth2Auth
}

export function authWithOAuth2Defaults(auth: types.AuthConfig | undefined, updates: Partial<types.AuthConfig> = {}) {
  const next = { ...(auth ?? {}), ...updates } as types.AuthConfig
  if (next.mode === 'oauth2' || updates.oauth2 !== undefined) {
    next.oauth2 = oauth2AuthWithDefaults(auth?.oauth2, updates.oauth2)
  }
  return next
}

export function proxyConfigWithDefaults(config: types.ProxyConfig | undefined, overrides: Partial<types.ProxyConfig> = {}) {
  const auth = config?.auth ?? ({} as types.ProxyAuthConfig)
  return {
    inherit: false,
    disabled: false,
    protocol: config?.protocol || 'http',
    hostname: config?.hostname || '',
    port: config?.port || '',
    bypassProxy: config?.bypassProxy || '',
    ...overrides,
    auth: {
      username: auth.username || '',
      password: auth.password || '',
      disabled: auth.disabled ?? false,
      ...(overrides.auth ?? {})
    }
  } as types.ProxyConfig
}
