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
 * One line of text for a set of unresolved references.
 *
 * Names each variable rather than saying "some variables are unresolved",
 * because the whole difficulty of this bug is not knowing WHICH one is missing —
 * the user is looking at a 401 and a header that reads correctly on screen.
 */
export function unresolvedVariableMessage(unresolved: readonly UnresolvedVariable[]): string {
  if (unresolved.length === 0) return ''
  const names = [...new Set(unresolved.map((entry) => entry.name))]
  const list = names.map((name) => `{{${name}}}`).join(', ')
  const subject = names.length === 1 ? 'variable' : 'variables'
  return `Unresolved ${subject} in headers: ${list} — sent as literal text.`
}
