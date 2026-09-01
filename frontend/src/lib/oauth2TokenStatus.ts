// What the OAuth2 form says about the token it is configured to fetch.
//
// A5-06. OAuth2 is the largest form in the app — around twenty fields — and it
// showed nothing at all about the thing all twenty exist to obtain. No token,
// no expiry, no refresh, no last error. The only OAuth2 UI beyond the config
// fields was a modal that appears reactively, during Send, because the Go side
// emits `oauth2:authorize`. So the entire lifecycle was invisible until a
// request happened to trigger it, and a user with a misconfigured client
// discovered it as a 401 on an unrelated request.
//
// The "Static token" field made that worse rather than better: it binds to the
// same `auth.token` Bearer mode uses, sat unlabelled among the OAuth2 fields,
// and nothing said whether it was used instead of, or before, an auto-fetched
// one. Two different tokens with one field between them.
//
// ── WHAT THIS CAN AND CANNOT KNOW ───────────────────────────────────────────
//
// The fetched token lives in the Go process (internal/core/app_oauth2_store.go,
// encrypted in oauth2.json) and NOTHING exports it: the only OAuth2 binding on
// `core.App` is CompleteOAuth2Callback. So this module takes the token record
// as an argument that today is always undefined, and is written so that the day
// a binding exists the UI needs no new logic — only the record.
//
// That constraint is the reason for the shape below. It would have been easy to
// write a status line that assumed a token record and rendered "No token" when
// it was missing, which reads as "the fetch failed" rather than "the app cannot
// see it". The `unknown` state exists so the form says the true thing.
//
// Kept free of Svelte and of the generated types so every state can be asserted
// directly, including the clock-dependent ones.

import { authFieldsFor } from './authFields.ts'

/** A token as the Go store holds one. Fields are optional: a store that only knows an access token is still useful. */
export interface OAuth2TokenRecord {
  accessToken?: string
  /** Epoch milliseconds. Absent means the server did not say. */
  expiresAt?: number
  refreshToken?: string
  /** The last fetch or refresh failure, verbatim from the server where possible. */
  error?: string
}

/** The auth fields this module reads. A structural subset of types.OAuth2Auth. */
export interface OAuth2ConfigLike {
  grantType?: string
  accessTokenUrl?: string
  authorizationUrl?: string
  callbackUrl?: string
  clientId?: string
  clientSecret?: string
  username?: string
  password?: string
  autoFetchToken?: boolean
  autoRefreshToken?: boolean
}

export type OAuth2TokenState =
  /** No token record was supplied, because nothing exports one yet. */
  | 'unknown'
  /** A token was fetched and has not expired. */
  | 'active'
  /** Fetched, and inside the window where it is about to stop working. */
  | 'expiring'
  /** Fetched, and past its expiry. */
  | 'expired'
  /** The last fetch or refresh failed. */
  | 'error'

export interface OAuth2TokenStatus {
  state: OAuth2TokenState
  /** The short line beside the label. Never empty. */
  summary: string
  /** One sentence of context under it, or '' when the summary says everything. */
  detail: string
  /** Matches statusTone.ts's vocabulary so this paints like every other graded thing. */
  tone: 'success' | 'warning' | 'danger' | 'idle'
  /** Labels of required fields still empty. A fetch cannot succeed while this is non-empty. */
  missing: string[]
  /** Whether a fetch is worth offering: the config is complete enough to try. */
  canFetch: boolean
}

/**
 * A token is called "expiring" this long before it actually expires.
 *
 * Five minutes rather than one: the point of the warning is to be seen BEFORE
 * the next send fails, and a one-minute window is one nobody is looking at the
 * Auth tab during.
 */
export const EXPIRING_WINDOW_MS = 5 * 60 * 1000

/**
 * The required fields of the current grant that are still empty.
 *
 * Derived from the same schema the form renders, rather than a second hand-kept
 * list — a required marker on a field and this check disagreeing would be the
 * original bug in miniature.
 */
export function oauth2MissingFields(config: OAuth2ConfigLike | undefined): string[] {
  const values = (config ?? {}) as Record<string, unknown>
  return authFieldsFor('oauth2', config?.grantType ?? '')
    .filter((field) => field.required)
    .filter((field) => String(values[field.name] ?? '').trim() === '')
    .map((field) => field.label)
}

/** How long until `expiresAt`, in whole minutes, as a phrase. Past expiry reads as elapsed. */
function relativeMinutes(deltaMs: number): string {
  const minutes = Math.max(1, Math.round(Math.abs(deltaMs) / 60000))
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  return rest === 0 ? `${hours}h` : `${hours}h ${rest}m`
}

/**
 * The status the Auth tab shows for an OAuth2 config.
 *
 * `now` is a parameter, not a call to Date.now(), because every interesting
 * state here is a function of the clock and a test that cannot set the clock
 * can only assert the uninteresting one.
 */
export function oauth2TokenStatus(
  config: OAuth2ConfigLike | undefined,
  record: OAuth2TokenRecord | undefined,
  now: number
): OAuth2TokenStatus {
  const missing = oauth2MissingFields(config)
  const canFetch = missing.length === 0

  if (record?.error) {
    return {
      state: 'error',
      summary: 'Last fetch failed',
      detail: record.error,
      tone: 'danger',
      missing,
      canFetch
    }
  }

  if (!record || !record.accessToken) {
    // The honest answer, and the reason this state is named `unknown` rather
    // than `none`: the app cannot see the token store, so "no token" would be
    // an assertion it is not entitled to make.
    return {
      state: 'unknown',
      summary: 'Not visible here',
      detail: canFetch
        ? 'A token is fetched when this request is sent. Its value is held encrypted by the app and is not shown in this form.'
        : `Fill in ${missing.join(', ')} before a token can be fetched.`,
      tone: 'idle',
      missing,
      canFetch
    }
  }

  if (record.expiresAt === undefined) {
    return {
      state: 'active',
      summary: 'Token active',
      detail: 'The server did not return an expiry for this token.',
      tone: 'success',
      missing,
      canFetch
    }
  }

  const remaining = record.expiresAt - now
  if (remaining <= 0) {
    return {
      state: 'expired',
      summary: `Expired ${relativeMinutes(remaining)} ago`,
      detail: record.refreshToken
        ? 'A refresh token is stored, so the next send will try to refresh it.'
        : 'No refresh token is stored — the next send fetches a new one.',
      tone: 'danger',
      missing,
      canFetch
    }
  }

  if (remaining <= EXPIRING_WINDOW_MS) {
    return {
      state: 'expiring',
      summary: `Expires in ${relativeMinutes(remaining)}`,
      detail: 'About to expire.',
      tone: 'warning',
      missing,
      canFetch
    }
  }

  return {
    state: 'active',
    summary: `Expires in ${relativeMinutes(remaining)}`,
    detail: '',
    tone: 'success',
    missing,
    canFetch
  }
}

/**
 * What the field formerly labelled "Static token" is actually for.
 *
 * Returned as copy rather than hard-coded in the markup so the two things it
 * can mean — "there is no auto-fetch, this IS the token" and "this is a
 * fallback the fetched token replaces" — are decided once, from the config,
 * instead of left to the reader.
 */
export function staticTokenHelp(config: OAuth2ConfigLike | undefined, token: string | undefined): string {
  const set = String(token ?? '').trim() !== ''
  if (!set) return 'Optional. Paste a token here to send it as-is instead of fetching one.'
  return 'Sent as-is. A token fetched by the flow above replaces it for that request.'
}
