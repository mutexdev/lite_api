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

/**
 * Fills in a COLLECTION's proxy config.
 *
 * Identical to `proxyConfigWithDefaults` except for one field, and that field
 * is the whole reason both exist: a collection with nothing configured
 * INHERITS, while the preferences-level config does not. Inherit is what "use
 * whatever the app is set to" means, and it is the only sane default for a
 * collection that has never had a proxy set — defaulting it to false would opt
 * every existing collection out of the user's system proxy the first time this
 * ran.
 */
export function collectionProxyWithDefaults(
  config: types.ProxyConfig | undefined,
  unset: boolean,
  overrides: Partial<types.ProxyConfig> = {}
) {
  const current = config ?? ({} as types.ProxyConfig)
  const auth = current.auth ?? ({} as types.ProxyAuthConfig)
  return {
    inherit: unset ? true : (current.inherit ?? true),
    disabled: current.disabled ?? false,
    protocol: current.protocol || 'http',
    hostname: current.hostname || '',
    port: current.port || '',
    bypassProxy: current.bypassProxy || '',
    ...overrides,
    // Merged separately, and AFTER the spread, so an override carrying only a
    // username keeps the stored password. A single spread would drop every
    // field the caller did not resend — silently clearing proxy credentials on
    // an unrelated edit.
    auth: {
      username: auth.username || '',
      password: auth.password || '',
      disabled: auth.disabled ?? false,
      ...(overrides.auth ?? {})
    }
  } as types.ProxyConfig
}

/**
 * The two flags a proxy mode sets.
 *
 * "off" and "inherit" both leave the manual settings untouched — the user is
 * choosing not to use them, not discarding them, and coming back to "manual"
 * must restore what they typed.
 */
export function proxyModeOverrides(mode: string): Partial<types.ProxyConfig> {
  if (mode === 'manual') return { inherit: false, disabled: false }
  if (mode === 'off') return { inherit: false, disabled: true }
  return { inherit: true, disabled: false }
}

/**
 * Fills in the preferences-level proxy, migrating the legacy `proxyMode` field.
 *
 * `source` replaced `proxyMode`, and a preferences file written before that
 * change has only the old field. Reading it here is what stops an upgrade from
 * silently resetting a configured PAC or manual proxy to the system one.
 */
export function proxyPreferencesWithDefaults(
  current: types.ProxyPreferences | undefined,
  legacyProxyMode: string | undefined,
  overrides: Partial<types.ProxyPreferences> = {}
) {
  const proxy = current ?? ({} as types.ProxyPreferences)
  const pac = { source: proxy.pac?.source || '', ...(overrides.pac ?? {}) }
  const config = proxyConfigWithDefaults(proxy.config, overrides.config ?? {})
  const legacy = legacyProxyMode === 'pac' ? 'pac' : legacyProxyMode === 'manual' ? 'manual' : 'inherit'
  return {
    disabled: proxy.disabled ?? false,
    source: proxy.source || legacy,
    ...overrides,
    pac,
    config
  } as types.ProxyPreferences
}

/**
 * The proxy mode implied by a stored preference.
 *
 * `disabled` is checked FIRST and wins over any configured source: turning the
 * proxy off has to mean off, whatever manual or PAC settings are still saved
 * beside it.
 */
export function preferenceProxyModeValue(proxy: types.ProxyPreferences | undefined): string {
  if (proxy?.disabled) return 'off'
  if (proxy?.source === 'manual') return 'manual'
  if (proxy?.source === 'pac') return 'pac'
  return 'system'
}

/**
 * The label shown for a proxy mode.
 *
 * Deliberately not the mode name: "manual" reads as "On" in the UI, because the
 * control it labels is a toggle over the manual settings shown beside it. The
 * stored value and the visible word are two different vocabularies and must not
 * be collapsed into one.
 */
export function proxyModeLabel(mode: string): string {
  if (mode === 'off') return 'Off'
  if (mode === 'manual') return 'On'
  if (mode === 'pac') return 'PAC'
  return 'System Proxy'
}
