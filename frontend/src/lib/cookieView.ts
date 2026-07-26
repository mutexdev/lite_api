// Presenting the cookie jar.
//
// Grouping, searching and the header preview. None of it decides what gets
// SENT — the Go cookie jar does that — but all of it decides what the user
// believes is stored, which is how someone notices a cookie they did not
// expect, or fails to notice one they should have.

import type { types } from '../../wailsjs/go/models'
import { searchHit } from './sidebarFilter.ts'

export type CookieForm = {
  id: string
  name: string
  value: string
  domain: string
  path: string
  expires: string
  session: boolean
  secure: boolean
  httpOnly: boolean
  sameSite: string
  hostOnly: boolean
}

export type CookieGroup = {
  domain: string
  cookies: types.CookieEntry[]
  header: string
}

/**
 * A blank cookie, as the editor opens it.
 *
 * The defaults are the safe reading of an unfinished form: path "/" because a
 * cookie with no path is a cookie for nothing, session true because a cookie
 * with no expiry IS a session cookie, and hostOnly true because the narrower
 * scope is the one to opt out of rather than into.
 */
export function emptyCookieForm(): CookieForm {
  return {
    id: '',
    name: '',
    value: '',
    domain: '',
    path: '/',
    expires: '',
    session: true,
    secure: false,
    httpOnly: false,
    sameSite: '',
    hostOnly: true
  }
}

/**
 * The flag summary shown beside a cookie.
 *
 * "none" rather than an empty string when nothing is set: a blank cell reads as
 * "not loaded yet", while "none" states that the cookie carries no protections
 * — which for a cookie is information, not an absence of it.
 */
export function cookieFlags(cookie: types.CookieEntry): string {
  const flags = []
  if (cookie.secure) flags.push('secure')
  if (cookie.httpOnly) flags.push('httpOnly')
  if (cookie.sameSite) flags.push(`sameSite=${cookie.sameSite}`)
  if (cookie.hostOnly) flags.push('hostOnly')
  return flags.join(', ') || 'none'
}

/**
 * Searching includes the FLAGS, so "httpOnly" or "sameSite=lax" finds every
 * cookie carrying that protection. That is the query someone runs when auditing
 * a jar, and matching only names would answer it with nothing.
 */
export function cookieMatches(cookie: types.CookieEntry, query: string): boolean {
  return [cookie.name, cookie.value, cookie.domain, cookie.path, cookie.sameSite, cookieFlags(cookie)].some((value) =>
    searchHit(value, query)
  )
}

/** The Cookie header these cookies would produce, as a preview. */
export function cookieHeaderPreview(cookies: types.CookieEntry[]): string {
  return cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join('; ')
}

/**
 * Groups cookies by domain, sorted, with each group's header preview.
 *
 * A cookie with no domain is grouped under "(no domain)" rather than dropped:
 * it is still in the jar, and hiding it means nobody can delete it.
 *
 * Within a group the order is path then name, so two cookies of the same name
 * scoped to different paths sit together and are visibly distinct — that pair
 * is exactly the confusion this panel exists to resolve.
 */
export function cookieGroups(cookies: types.CookieEntry[] | undefined, query: string): CookieGroup[] {
  const groups = new Map<string, types.CookieEntry[]>()
  for (const cookie of cookies ?? []) {
    if (query && !cookieMatches(cookie, query)) continue
    const domain = cookie.domain || '(no domain)'
    groups.set(domain, [...(groups.get(domain) ?? []), cookie])
  }
  return Array.from(groups.entries())
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([domain, groupCookies]) => ({
      domain,
      cookies: groupCookies.sort(
        (a, b) => (a.path || '/').localeCompare(b.path || '/') || a.name.localeCompare(b.name)
      ),
      header: cookieHeaderPreview(groupCookies)
    }))
}
