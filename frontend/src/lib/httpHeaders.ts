/**
 * The header names and values a request editor should offer (US-056).
 *
 * Typing a header meant remembering its exact spelling and hyphenation, with
 * nothing in the app to consult: `Content-Type`, `If-None-Match` and
 * `X-Requested-With` are all easy to get subtly wrong, and a misspelled header
 * is not an error anywhere -- it is silently ignored by the server, which is
 * the worst way for a mistake to present itself.
 *
 * The lists are request headers only. Offering `Set-Cookie` or `Age` in a
 * request editor would be worse than offering nothing, because it suggests they
 * mean something there.
 */

import { contentTypeForFilePath } from './contentTypes.ts'

/** Canonically cased request header names, in the spelling to insert. */
export const REQUEST_HEADER_NAMES: readonly string[] = [
  'Accept',
  'Accept-Charset',
  'Accept-Encoding',
  'Accept-Language',
  'Access-Control-Request-Headers',
  'Access-Control-Request-Method',
  'Authorization',
  'Cache-Control',
  'Connection',
  'Content-Disposition',
  'Content-Encoding',
  'Content-Language',
  'Content-Length',
  'Content-Type',
  'Cookie',
  'Date',
  'DNT',
  'Expect',
  'Forwarded',
  'From',
  'Host',
  'idempotency-key',
  'If-Match',
  'If-Modified-Since',
  'If-None-Match',
  'If-Range',
  'If-Unmodified-Since',
  'Keep-Alive',
  'Origin',
  'Pragma',
  'Prefer',
  'Proxy-Authorization',
  'Range',
  'Referer',
  'Sec-Fetch-Dest',
  'Sec-Fetch-Mode',
  'Sec-Fetch-Site',
  'TE',
  'Trailer',
  'Transfer-Encoding',
  'Upgrade',
  'Upgrade-Insecure-Requests',
  'User-Agent',
  'Via',
  'X-API-Key',
  'X-Correlation-ID',
  'X-CSRF-Token',
  'X-Forwarded-For',
  'X-Forwarded-Host',
  'X-Forwarded-Proto',
  'X-HTTP-Method-Override',
  'X-Request-ID',
  'X-Requested-With'
].map((name) => (name === 'idempotency-key' ? 'Idempotency-Key' : name))

/**
 * What an empty header-name cell offers before anything is typed. Ordered by
 * how often a request actually needs them, not alphabetically -- the list is
 * there to save a decision, so the common answer belongs at the top.
 */
export const COMMON_REQUEST_HEADER_NAMES: readonly string[] = [
  'Content-Type',
  'Authorization',
  'Accept',
  'User-Agent',
  'Cache-Control',
  'Cookie'
]

/** How many suggestions a popup shows before it stops being faster to read than to type. */
export const HEADER_SUGGESTION_LIMIT = 8

const MEDIA_TYPES: readonly string[] = [
  'application/json',
  'application/xml',
  'application/x-www-form-urlencoded',
  'multipart/form-data',
  'text/plain',
  'text/html',
  'application/octet-stream',
  'application/graphql-response+json',
  '*/*'
]

/**
 * Values worth offering per header. Only headers with a small, closed, and
 * genuinely memorable-to-get-wrong set of values are listed; suggesting values
 * for Host or Referer would be noise.
 */
export const HEADER_VALUE_SUGGESTIONS: Readonly<Record<string, readonly string[]>> = {
  accept: MEDIA_TYPES,
  'content-type': MEDIA_TYPES,
  authorization: ['Bearer ', 'Basic ', 'Digest '],
  'cache-control': ['no-cache', 'no-store', 'max-age=0', 'must-revalidate'],
  'accept-encoding': ['gzip, deflate, br', 'gzip', 'identity'],
  connection: ['keep-alive', 'close'],
  'transfer-encoding': ['chunked'],
  'x-requested-with': ['XMLHttpRequest'],
  'accept-charset': ['utf-8'],
  expect: ['100-continue'],
  te: ['trailers'],
  prefer: ['return=representation', 'return=minimal', 'respond-async'],
  'x-http-method-override': ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'],
  dnt: ['1', '0'],
  'upgrade-insecure-requests': ['1']
}

interface Ranked {
  value: string
  rank: number
}

/**
 * Ranks a prefix match above a match found later in the name, so typing "type"
 * offers Content-Type while typing "cont" offers Content-Type first and
 * Content-Length after it. Within a rank the curated order is preserved.
 */
function rankMatches(candidates: readonly string[], query: string, limit: number): string[] {
  const needle = query.trim().toLowerCase()
  if (needle === '') return candidates.slice(0, limit)
  const ranked: Ranked[] = []
  for (const value of candidates) {
    const position = value.toLowerCase().indexOf(needle)
    if (position < 0) continue
    ranked.push({ value, rank: position === 0 ? 0 : 1 })
  }
  return ranked
    .map((entry, index) => ({ ...entry, index }))
    .sort((left, right) => left.rank - right.rank || left.index - right.index)
    .slice(0, limit)
    .map((entry) => entry.value)
}

/**
 * Header names to offer for what has been typed so far.
 *
 * `usedNames` is the names on the *other* rows -- the caller leaves out the row
 * being edited, or the list would empty itself the moment a name was completed.
 * Excluding the rest is deliberate: a second Content-Type row is almost always
 * a mistake, and the one case where repeating a header is correct is served by
 * typing it out.
 */
export function suggestHeaderNames(
  query: string,
  usedNames: readonly string[] = [],
  limit: number = HEADER_SUGGESTION_LIMIT
): string[] {
  const used = new Set(usedNames.map((name) => name.trim().toLowerCase()).filter((name) => name !== ''))
  const typed = query.trim().toLowerCase()
  if (typed === '') {
    return COMMON_REQUEST_HEADER_NAMES.filter((name) => !used.has(name.toLowerCase())).slice(0, limit)
  }
  // A name typed out in full is finished. Offering Proxy-Authorization to
  // someone who has just typed Authorization only risks Enter replacing what
  // they meant.
  if (REQUEST_HEADER_NAMES.some((name) => name.toLowerCase() === typed)) return []
  const available = REQUEST_HEADER_NAMES.filter((name) => !used.has(name.toLowerCase()))
  return rankMatches(available, typed, limit)
}

/** Values to offer for a header, given what has been typed into the value cell. */
export function suggestHeaderValues(
  headerName: string,
  query: string,
  limit: number = HEADER_SUGGESTION_LIMIT
): string[] {
  const candidates = HEADER_VALUE_SUGGESTIONS[headerName.trim().toLowerCase()]
  if (!candidates) return []
  const typed = query.trim().toLowerCase()
  if (typed !== '' && candidates.some((value) => value.trim().toLowerCase() === typed)) return []
  return rankMatches(candidates, query, limit)
}

/**
 * The media type a file path implies, for the Content-Type of a file body.
 * Delegates to the map the body editor already uses so the two cannot drift.
 */
export function contentTypeSuggestionForFile(filePath: string): string {
  return contentTypeForFilePath(filePath)
}

/**
 * Moves the active index within a suggestion list, wrapping at both ends.
 * Returns -1 for an empty list so a caller can treat "nothing active" uniformly.
 */
export function moveSuggestionIndex(current: number, delta: number, length: number): number {
  if (length <= 0) return -1
  if (current < 0) return delta > 0 ? 0 : length - 1
  return (current + delta + length) % length
}
