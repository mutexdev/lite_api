// US-056 — bulk edit round-tripping for key/value tables.
//
// The story's criterion is "round-trips to rows without losing disabled state",
// and the reason it singles that out is that a line-based text format has
// nowhere to put anything except a name, a value and a disabled marker.
// Everything else a row carries — `secret`, `description` — has no
// representation, so the obvious implementation quietly resets it. That is
// worse than losing the disabled flag: a secret row silently becoming
// non-secret means a credential starts being written out in the clear.
//
// Two mechanisms handle it:
//
//   * name/value/enabled travel in the text
//   * secret/description are CARRIED OVER from the previous rows by matching
//     the nth occurrence of a name to the nth previous occurrence
//
// Newlines are escaped rather than left to split a row in half. Postman's own
// bulk edit corrupts multi-line values this way; this table supports them
// (`multilineValues`), so silently splitting one into bogus rows would be a
// data-loss bug the user sees only later.

export type BulkRow = {
  name: string
  value: string
  enabled: boolean
  secret?: boolean
  description?: string
}

/**
 * The disabled marker written out. `//` is Postman's, which is the muscle
 * memory this story exists to serve. `~` is also accepted on input because
 * that is what this app emitted before, so existing text keeps parsing.
 */
const DISABLED_PREFIX = '//'

function escapeValue(value: string): string {
  // Backslash first, or the escape introduced for a newline would itself be
  // re-escaped on the next pass and grow on every round trip.
  return value.replace(/\\/g, '\\\\').replace(/\r?\n/g, '\\n')
}

function unescapeValue(value: string): string {
  let out = ''
  for (let i = 0; i < value.length; i++) {
    if (value[i] !== '\\' || i + 1 >= value.length) {
      out += value[i]
      continue
    }
    const next = value[i + 1]
    if (next === 'n') {
      out += '\n'
      i++
    } else if (next === '\\') {
      out += '\\'
      i++
    } else {
      // An unknown escape is left exactly as written rather than swallowed:
      // a Windows path typed as C:\Users must survive unchanged.
      out += value[i]
    }
  }
  return out
}

/** rowsToBulkText renders rows as the editable text form. */
export function rowsToBulkText(rows: BulkRow[]): string {
  return (rows ?? [])
    .map((row) => {
      const prefix = row.enabled === false ? DISABLED_PREFIX + ' ' : ''
      return `${prefix}${row.name ?? ''}: ${escapeValue(row.value ?? '')}`
    })
    .join('\n')
}

/**
 * parseBulkText turns the text form back into rows, carrying over metadata the
 * text cannot express from `previous`.
 *
 * Blank lines are dropped. A line with no separator becomes a name with an
 * empty value, which is what someone half-way through typing has.
 */
export function parseBulkText(text: string, previous: BulkRow[] = []): BulkRow[] {
  // Index the previous rows by name, preserving order, so the nth occurrence
  // of a duplicated name maps to the nth previous one. Matching by name alone
  // would give every duplicate the first row's metadata.
  const carried = new Map<string, BulkRow[]>()
  for (const row of previous ?? []) {
    const key = (row.name ?? '').trim()
    const bucket = carried.get(key)
    if (bucket) bucket.push(row)
    else carried.set(key, [row])
  }
  const taken = new Map<string, number>()

  return text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => {
      let enabled = true
      let rest = line
      if (rest.startsWith(DISABLED_PREFIX)) {
        enabled = false
        rest = rest.slice(DISABLED_PREFIX.length).trimStart()
      } else if (rest.startsWith('~')) {
        enabled = false
        rest = rest.slice(1).trimStart()
      }

      // The FIRST separator only: a value containing a colon (a URL, a time)
      // must not be truncated at its own punctuation.
      let separator = rest.indexOf(':')
      const equals = rest.indexOf('=')
      if (separator < 0 || (equals >= 0 && equals < separator)) separator = equals

      const name = separator >= 0 ? rest.slice(0, separator).trim() : rest.trim()
      const rawValue = separator >= 0 ? rest.slice(separator + 1).trimStart() : ''

      const index = taken.get(name) ?? 0
      taken.set(name, index + 1)
      const source = carried.get(name)?.[index]

      return {
        name,
        value: unescapeValue(rawValue),
        enabled,
        secret: source?.secret ?? false,
        description: source?.description ?? ''
      }
    })
}

/**
 * bulkTextIsLossy reports whether rendering these rows to text would lose
 * something the format cannot carry.
 *
 * Only a name is genuinely unrepresentable: one containing the separator, or
 * one starting with a disabled marker, cannot be read back as itself. Values
 * are escaped and secret/description are carried over, so neither is lossy.
 * The caller uses this to warn rather than to block — silently refusing bulk
 * edit on a table would be more confusing than a note.
 */
export function bulkTextIsLossy(rows: BulkRow[]): boolean {
  return (rows ?? []).some((row) => {
    const name = (row.name ?? '').trim()
    if (!name) return false
    return name.includes(':') || name.includes('=') || name.startsWith(DISABLED_PREFIX) || name.startsWith('~')
  })
}
