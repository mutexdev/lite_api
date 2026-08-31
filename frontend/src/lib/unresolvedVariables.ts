// Finds {{variables}} in a request that nothing will fill in.
//
// The URL path already refuses to send with an unresolved segment. Headers did
// not: `Authorization: Bearer {{token}}` with no `token` anywhere in scope went
// out over the wire as the literal nine characters `{{token}}`, the server
// answered 401, and nothing in the app connected that 401 to the missing
// variable. The failure is silent in the worst way — the request succeeds at
// the transport level, so no error path is taken at all.
//
// This is the detection half, kept pure and free of the workspace types so the
// rules can be tested directly: the caller supplies a predicate that says
// whether a name resolves, because "is this variable in scope" already has an
// implementation (findTooltipVariable) worth reusing rather than duplicating.
//
// A5-09. It shipped wired to headers ONLY, which left the same silent failure
// live everywhere else a variable is interpolated — the URL, query params, the
// body, and every field of every auth mode. Auth is the worst of those: an
// OAuth2 client secret of literal `{{clientSecret}}` produces a token request
// the server rejects, and the user is looking at a 401 on a request whose auth
// tab reads perfectly. So the scan widened to every interpolated surface, and
// the message widened with it — it now names WHERE as well as WHICH, because
// "unresolved variable {{token}}" is a much smaller clue when it could have
// come from any of six places.

import { authFieldsFor, oauth2TokenPlacementField } from './authFields.ts'

/** One reference that will be sent as literal text. */
export interface UnresolvedVariable {
  /** Where it was found, for the message: "Header", "Header name". */
  location: string
  /** The header (or field) it appeared in, for pointing at a row. */
  field: string
  /** The variable name, without braces or padding. */
  name: string
}

export interface HeaderLike {
  name?: string
  value?: string
  enabled?: boolean
}

/**
 * Matches `{{ name }}` the way the resolver does.
 *
 * Kept identical to resolveTooltipValue's pattern on purpose: a warning that
 * used a looser or stricter pattern than the resolver would either cry wolf
 * over text that resolves fine, or stay quiet about text that does not.
 */
const REFERENCE = /\{\{\s*([^{}]+?)\s*\}\}/g

/**
 * The names referenced in one string, in order, without duplicates.
 *
 * Prompt variables (`{{?name}}`) are excluded: they have no value until the
 * user is asked for one at send time, so reporting them as missing would flag
 * the feature working correctly.
 */
export function referencedVariableNames(value: string | undefined): string[] {
  if (!value) return []
  const names: string[] = []
  for (const match of value.matchAll(REFERENCE)) {
    const name = match[1].trim()
    if (!name || name.startsWith('?')) continue
    if (!names.includes(name)) names.push(name)
  }
  return names
}

/**
 * Every unresolved reference in a request's headers.
 *
 * Disabled headers are skipped — they are not sent, so a stale variable in one
 * is not a problem the user has. Both the name and the value are scanned:
 * a header whose NAME is `{{header}}` is even more broken than one whose value
 * is, and it was equally silent.
 */
export function unresolvedHeaderVariables(
  headers: readonly HeaderLike[] | undefined,
  resolves: (name: string) => boolean
): UnresolvedVariable[] {
  const found: UnresolvedVariable[] = []
  for (const header of headers ?? []) {
    if (header?.enabled === false) continue
    for (const name of referencedVariableNames(header?.name)) {
      if (!resolves(name)) found.push({ location: 'Header name', field: header?.name ?? '', name })
    }
    for (const name of referencedVariableNames(header?.value)) {
      if (!resolves(name)) found.push({ location: 'Header', field: header?.name ?? '', name })
    }
  }
  return found
}

/**
 * The plural noun each location is reported under.
 *
 * Locations are specific ("Header name", "OAuth2 · Client secret") because the
 * warning has to be actionable, but the message must not read as a list of six
 * near-identical phrases. Grouping happens here so the specific location stays
 * available for anything that wants to point at a row.
 */
const LOCATION_GROUPS: { prefix: string; group: string }[] = [
  { prefix: 'Header', group: 'headers' },
  { prefix: 'Query param', group: 'query params' },
  { prefix: 'Path param', group: 'path params' },
  { prefix: 'URL', group: 'the URL' },
  { prefix: 'Body', group: 'the body' },
  { prefix: 'Auth', group: 'auth' }
]

function locationGroup(location: string): string {
  return LOCATION_GROUPS.find((entry) => location.startsWith(entry.prefix))?.group ?? 'the request'
}

/** "headers", "headers and auth", "headers, the body and auth". */
function joinGroups(groups: readonly string[]): string {
  if (groups.length <= 1) return groups[0] ?? 'the request'
  return `${groups.slice(0, -1).join(', ')} and ${groups[groups.length - 1]}`
}

/**
 * One line of text for a set of unresolved references.
 *
 * Names each variable rather than saying "some variables are unresolved",
 * because the whole difficulty of this bug is not knowing WHICH one is missing —
 * the user is looking at a 401 and a header that reads correctly on screen.
 *
 * It names the places too, in the order they were scanned. Once the scan covers
 * six surfaces, "{{token}} is unresolved" leaves the user hunting through all
 * six; "in auth" is the other half of the answer and costs three words.
 */
export function unresolvedVariableMessage(unresolved: readonly UnresolvedVariable[]): string {
  if (unresolved.length === 0) return ''
  const names = [...new Set(unresolved.map((entry) => entry.name))]
  const groups = [...new Set(unresolved.map((entry) => locationGroup(entry.location)))]
  const list = names.map((name) => `{{${name}}}`).join(', ')
  const subject = names.length === 1 ? 'variable' : 'variables'
  return `Unresolved ${subject} in ${joinGroups(groups)}: ${list} — sent as literal text.`
}

/** A query or path param row. Structurally what `types.KeyValue` gives us. */
export interface ParamLike {
  name?: string
  value?: string
  enabled?: boolean
}

/** The body fields that are plain interpolated text. Binary and file bodies have nothing to scan. */
export interface BodyLike {
  mode?: string
  text?: string
  json?: string
  xml?: string
  graphqlQuery?: string
  graphqlVariables?: string
  formUrlEncoded?: readonly ParamLike[]
}

/**
 * The auth config, structurally.
 *
 * Named fields rather than an index signature: the generated `types.AuthConfig`
 * is a CLASS, and a class is not assignable to a type with an index signature
 * however well its properties line up. The sub-objects stay `unknown` because
 * which of their fields matter is decided by the schema, not by this type.
 */
export interface AuthLike {
  mode?: string
  token?: string
  oauth2?: unknown
  oauth1?: unknown
  awsv4?: unknown
}

/**
 * The request shape this scans. Deliberately structural rather than
 * `types.RequestItem`: the scanner must stay testable without constructing a
 * full workspace object, and it reads nine fields out of forty.
 */
export interface ScannableRequest {
  url?: string
  params?: readonly ParamLike[]
  pathParams?: readonly ParamLike[]
  headers?: readonly HeaderLike[]
  body?: BodyLike
  auth?: AuthLike
}

function scanText(
  text: string | undefined,
  location: string,
  field: string,
  resolves: (name: string) => boolean,
  into: UnresolvedVariable[]
) {
  for (const name of referencedVariableNames(text)) {
    if (!resolves(name)) into.push({ location, field, name })
  }
}

/**
 * Unresolved references in a request's query or path params.
 *
 * Disabled rows are skipped for the same reason disabled headers are: they are
 * not sent. Path params are scanned even though the URL bar already refuses to
 * send with an unresolved SEGMENT — a path param's VALUE is a separate field
 * and had no guard at all.
 */
export function unresolvedParamVariables(
  params: readonly ParamLike[] | undefined,
  label: 'Query param' | 'Path param',
  resolves: (name: string) => boolean
): UnresolvedVariable[] {
  const found: UnresolvedVariable[] = []
  for (const param of params ?? []) {
    if (param?.enabled === false) continue
    scanText(param?.name, `${label} name`, param?.name ?? '', resolves, found)
    scanText(param?.value, label, param?.name ?? '', resolves, found)
  }
  return found
}

/**
 * Unresolved references in the body.
 *
 * Only the mode actually being sent is scanned. A JSON body left behind when
 * the user switched to multipart still holds whatever they last typed, and
 * warning about a variable in text that is not going anywhere is precisely the
 * cry-wolf this feature cannot afford.
 */
export function unresolvedBodyVariables(
  body: BodyLike | undefined,
  resolves: (name: string) => boolean
): UnresolvedVariable[] {
  const found: UnresolvedVariable[] = []
  if (!body) return found
  const mode = (body.mode ?? '').toLowerCase()
  if (mode === 'formurlencoded' || mode === 'form-urlencoded') {
    for (const row of body.formUrlEncoded ?? []) {
      if (row?.enabled === false) continue
      scanText(row?.name, 'Body field name', row?.name ?? '', resolves, found)
      scanText(row?.value, 'Body field', row?.name ?? '', resolves, found)
    }
    return found
  }
  if (mode === 'graphql') {
    scanText(body.graphqlQuery, 'Body', 'GraphQL query', resolves, found)
    scanText(body.graphqlVariables, 'Body', 'GraphQL variables', resolves, found)
    return found
  }
  // The remaining text modes all store their content in one of three fields,
  // and which one is decided by the mode name. Anything else — multipart, file,
  // binary, none — has no interpolated text to scan.
  const text = mode === 'json' ? body.json : mode === 'xml' ? body.xml : mode === 'text' || mode === 'sparql' ? body.text : undefined
  scanText(text, 'Body', mode || 'body', resolves, found)
  return found
}

/**
 * Unresolved references in the auth config of whatever mode is selected.
 *
 * The field list comes from `authFields.ts` — the same schema the form renders
 * — rather than a second hand-kept list. That matters more here than anywhere
 * else in this module: a field added to the form and forgotten here would be a
 * new silent-401 surface, which is the exact bug this whole file exists for.
 *
 * Checkbox fields are skipped (a boolean has no text to interpolate) and so is
 * `inherit`, whose fields live on a parent this function cannot see. Inherited
 * auth is scanned when the parent's own form is looked at.
 */
export function unresolvedAuthVariables(
  auth: AuthLike | undefined,
  resolves: (name: string) => boolean
): UnresolvedVariable[] {
  const found: UnresolvedVariable[] = []
  const mode = auth?.mode ?? ''
  if (!auth || !mode || mode === 'none' || mode === 'inherit') return found
  const config = auth as unknown as Record<string, unknown>
  const oauth2 = (config.oauth2 ?? {}) as Record<string, unknown>
  const fields = [...authFieldsFor(mode, String(oauth2.grantType ?? ''))]
  if (mode === 'oauth2') fields.push(oauth2TokenPlacementField(String(oauth2.tokenPlacement ?? '')))
  for (const field of fields) {
    if (field.kind === 'checkbox') continue
    const container = ((field.group === '' ? config : config[field.group]) ?? {}) as Record<string, unknown>
    const value = container[field.name]
    if (typeof value !== 'string') continue
    scanText(value, `Auth · ${field.label}`, field.label, resolves, found)
  }
  // OAuth2's static token is not in the schema — it is a top-level field the
  // OAuth2 form borrows from Bearer mode — and it is as interpolatable as any
  // of them.
  if (mode === 'oauth2') scanText(String(config.token ?? ''), 'Auth · Static token', 'Static token', resolves, found)
  return found
}

/**
 * Every unresolved reference in a request, across every surface that gets
 * interpolated.
 *
 * Order is the order a user reads the request pane — URL, params, headers,
 * body, auth — so the grouped message lists the places in the order they will
 * go looking.
 */
export function unresolvedRequestVariables(
  request: ScannableRequest | undefined,
  resolves: (name: string) => boolean
): UnresolvedVariable[] {
  if (!request) return []
  const found: UnresolvedVariable[] = []
  scanText(request.url, 'URL', 'url', resolves, found)
  found.push(...unresolvedParamVariables(request.params, 'Query param', resolves))
  found.push(...unresolvedParamVariables(request.pathParams, 'Path param', resolves))
  found.push(...unresolvedHeaderVariables(request.headers, resolves))
  found.push(...unresolvedBodyVariables(request.body, resolves))
  found.push(...unresolvedAuthVariables(request.auth, resolves))
  return found
}
