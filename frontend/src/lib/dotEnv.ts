// Reading a .env file into editable rows.
//
// These become the process.env values a request can interpolate, so a line
// parsed wrongly means a request sends the wrong secret, or none. The parse is
// deliberately forgiving about layout and strict about shape: a line that is
// not a definition is skipped rather than guessed at.

export type DotEnvRow = {
  /** Index in the original file, so an edit can be written back in place. */
  lineIndex: number
  name: string
  value: string
}

/**
 * Parses .env content into rows.
 *
 * The rules, each of which a real file relies on:
 *
 *   * blank lines and `#` comments are skipped. A comment is not a variable and
 *     making one into a row named "#" would let it be edited into existence.
 *   * a leading `export ` is stripped, because shell-sourceable .env files
 *     carry it and the variable is `FOO`, not `export FOO`.
 *   * the split is at the FIRST `=` only. A value legitimately contains them —
 *     connection strings, base64, JWTs — and splitting on all would truncate
 *     the secret at its first padding character.
 *   * a line with no `=`, or one starting with `=`, is skipped. Both are
 *     malformed, and an empty variable name is not addressable.
 *   * lineIndex counts EVERY line including the skipped ones, so writing an
 *     edit back lands on the line it came from.
 */
export function parseDotEnvRows(content: string): DotEnvRow[] {
  return content.split('\n').flatMap((line, lineIndex) => {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) return []
    const exported = trimmed.startsWith('export ') ? trimmed.slice(7).trimStart() : trimmed
    const equalIndex = exported.indexOf('=')
    if (equalIndex <= 0) return []
    return [
      {
        lineIndex,
        name: exported.slice(0, equalIndex).trim(),
        value: exported.slice(equalIndex + 1).trim()
      }
    ]
  })
}
