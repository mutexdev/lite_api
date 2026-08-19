export const automaticPreviewLimit = 128 * 1024
export const fullRenderLimit = 1024 * 1024
export const embeddedPreviewLimit = 512 * 1024
export const responseViews = ['pretty', 'raw', 'base64', 'hex'] as const
export type ResponseView = typeof responseViews[number]

const utf8Encoder = new TextEncoder()
const utf8Decoder = new TextDecoder()

/** Measures and bounds previews in bytes, matching HTTP response sizes rather than JS UTF-16 code units. */
export function utf8ByteLength(value: string) {
  return utf8Encoder.encode(value).length
}

/** Measures a Base64 payload in decoded bytes without allocating a binary copy. */
export function base64ByteLength(value: string) {
  const compact = value.replace(/\s/g, '')
  if (!compact) return 0
  const padding = compact.endsWith('==') ? 2 : compact.endsWith('=') ? 1 : 0
  // US-038 fix. Unpadded Base64 is legal and common (JWTs, many APIs), and
  // flooring to whole quartets discarded its final partial group — so a 3-byte
  // payload encoded as "AAAA" plus a 2-character tail reported 3 bytes instead
  // of 4. The remainder is 2 characters for one more byte and 3 for two.
  const whole = Math.floor(compact.length / 4) * 3
  const remainder = compact.length % 4
  const partial = remainder === 2 ? 1 : remainder === 3 ? 2 : 0
  return Math.max(0, whole + partial - padding)
}

/** Returns a valid Base64 prefix whose decoded payload fits the supplied byte budget. */
export function sliceBase64Bytes(value: string, byteLimit: number) {
  const compact = value.replace(/\s/g, '')
  if (byteLimit <= 0 || !compact || base64ByteLength(compact) <= byteLimit) return byteLimit <= 0 ? '' : compact
  // A full Base64 quartet represents three bytes. Keep quartets intact so atob
  // always receives a valid prefix rather than an incomplete character group.
  return compact.slice(0, Math.floor(byteLimit / 3) * 4)
}

/** Returns the largest valid UTF-8 string prefix within a byte budget. */
export function sliceUtf8(value: string, byteLimit: number) {
  if (byteLimit <= 0 || !value) return ''
  const encoded = utf8Encoder.encode(value)
  if (encoded.length <= byteLimit) return value

  let end = Math.min(byteLimit, encoded.length)
  // Locate the lead byte of the final included code point (at most three steps)
  // and discard it only when its full UTF-8 sequence crosses the byte budget.
  let lead = end - 1
  while (lead >= 0 && (encoded[lead] & 0xC0) === 0x80) lead -= 1
  const leadByte = encoded[lead] ?? 0
  const width = leadByte < 0x80 ? 1 : leadByte < 0xE0 ? 2 : leadByte < 0xF0 ? 3 : leadByte < 0xF8 ? 4 : 1
  if (lead + width > end) end = Math.max(0, lead)
  return utf8Decoder.decode(encoded.subarray(0, end))
}

// Keep the view transition deterministic at the component boundary. This also
// gives non-DOM checks a small, pure regression seam for Hex -> Pretty.
export function normalizeResponseView(value: string): ResponseView {
  return (responseViews as readonly string[]).includes(value) ? value as ResponseView : 'pretty'
}

export function formatResponseBody(body: string, headers: Record<string, string> = {}) {
  const contentType = Object.entries(headers).find(([name]) => name.toLowerCase() === 'content-type')?.[1]?.toLowerCase() ?? ''
  if (contentType.includes('json') || /^[\s\n]*[{[]/.test(body)) {
    try { return JSON.stringify(JSON.parse(body), null, 2) } catch { return body }
  }
  if (contentType.includes('xml')) {
    try {
      if (new DOMParser().parseFromString(body, 'application/xml').querySelector('parsererror')) return body
      const tokens = body.replace(/>\s+</g, '><').trim().match(/<[^>]+>|[^<]+/g) ?? []
      let depth = 0
      const lines: string[] = []
      for (const token of tokens) {
        const item = token.trim()
        if (!item) continue
        if (item.startsWith('</')) depth = Math.max(0, depth - 1)
        lines.push(`${'  '.repeat(depth)}${item}`)
        if (item.startsWith('<') && !item.startsWith('</') && !item.startsWith('<?') && !item.startsWith('<!') && !item.endsWith('/>')) depth += 1
      }
      return lines.join('\n')
    } catch { return body }
  }
  return body
}

export function responseTextForView(body: string, bodyBase64: string, view: string, pretty: string) {
  if (view === 'raw') return body
  if (view === 'base64') return bodyBase64
  if (view === 'hex') return hexPreview(bodyBase64)
  return pretty
}

function hexPreview(bodyBase64: string) {
  // Callers pass either the automatic preview or an explicitly user-requested
  // full payload. Retain complete quartets so decoded byte coverage is honest.
  // US-038 fix. Whitespace has to be stripped BEFORE aligning quartets:
  // line-wrapped Base64 (which many servers and every MIME encoder emit) made
  // the quartet arithmetic count newlines as data, so the slice landed
  // mid-group and atob decoded a corrupt tail or threw and showed nothing.
  const compact = bodyBase64.replace(/\s/g, '')
  const encoded = compact.slice(0, Math.floor(compact.length / 4) * 4)
  if (!encoded) return ''
  try {
    const bytes = atob(encoded)
    const rows: string[] = []
    for (let offset = 0; offset < bytes.length; offset += 16) {
      const chunk = bytes.slice(offset, offset + 16)
      const hex = Array.from(chunk, (char) => char.charCodeAt(0).toString(16).padStart(2, '0')).join(' ').padEnd(47, ' ')
      const ascii = Array.from(chunk, (char) => { const code = char.charCodeAt(0); return code >= 32 && code <= 126 ? char : '.' }).join('')
      rows.push(`${offset.toString(16).padStart(8, '0')}  ${hex}  ${ascii}`)
    }
    return rows.join('\n')
  } catch { return '' }
}

export function contentType(headers: Record<string, string> = {}) {
  return Object.entries(headers).find(([name]) => name.toLowerCase() === 'content-type')?.[1]?.toLowerCase() ?? ''
}

export function previewKind(headers: Record<string, string> = {}) {
  const type = contentType(headers)
  if (type.startsWith('image/')) return 'image'
  if (type.includes('pdf')) return 'pdf'
  if (type.includes('html')) return 'html'
  if (type.includes('xml')) return 'xml'
  if (type.includes('json')) return 'json'
  if (type && !type.startsWith('text/') && !/(javascript|graphql|yaml|x-www-form-urlencoded)/.test(type)) return 'binary'
  return 'text'
}

export function contentDispositionFilename(headers: Record<string, string> = {}) {
  const disposition = Object.entries(headers).find(([name]) => name.toLowerCase() === 'content-disposition')?.[1] ?? ''
  const encoded = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1]
  const quoted = disposition.match(/filename\s*=\s*"([^"]+)"/i)?.[1] ?? disposition.match(/filename\s*=\s*([^;\s]+)/i)?.[1]
  // US-038 fix. A malformed filename* used to discard the plain filename
  // fallback that sat right beside it, so a download that had a perfectly good
  // name got none. Falling back is what the RFC intends: filename* is
  // preferred, not exclusive.
  const decoded = encoded
    ? (() => {
        try {
          return decodeURIComponent(encoded)
        } catch {
          return ''
        }
      })()
    : ''
  let candidate = decoded || quoted || ''
  candidate = candidate.replace(/[\\/:*?"<>|\x00-\x1f]/g, '_').replace(/^\.+/, '').trim()
  // US-038 fix. Sanitising an all-illegal name produced a filename made only of
  // underscores — `filename=""` became "__", which looks like a real name and
  // saves a file nobody can identify. Nothing usable means no name.
  if (!candidate.replace(/_/g, '').trim()) return ''
  return candidate.slice(0, 180)
}

export function boundedLines(value: string, limit = 2400, characterLimit = fullRenderLimit) {
  const lines: string[] = []
  const end = Math.min(value.length, characterLimit)
  let start = 0
  for (let index = 0; index < end && lines.length < limit; index += 1) {
    if (value[index] === '\n') { lines.push(value.slice(start, index)); start = index + 1 }
  }
  const consumed = lines.length < limit && start < end
  if (consumed) lines.push(value.slice(start, end))
  // US-038 fix. `lines.length >= limit` reported truncation whenever the count
  // merely REACHED the limit, so a body of exactly 2400 lines was labelled
  // truncated while showing every one of them. Truncation means content was
  // left behind: either characters past the budget, or a line the loop stopped
  // before consuming.
  const stoppedEarly = start < end && !consumed
  return { lines, truncated: end < value.length || stoppedEarly }
}

export function lineDiff(left: string, right: string, limit = 2400) {
  const leftLines = boundedLines(left, limit)
  const rightLines = boundedLines(right, limit)
  const a = leftLines.lines
  const b = rightLines.lines
  const rows: Array<{ left: string; right: string; changed: boolean; change: 'added' | 'removed' | 'changed' | 'unchanged' }> = []
  const count = Math.max(a.length, b.length)
  for (let index = 0; index < count; index += 1) {
    const leftLine = a[index]
    const rightLine = b[index]
    // US-038 fix. A row where one side simply HAS NO LINE was reported as
    // "changed", which reads as "this line was edited" when the truth is that
    // one document is longer. `change` distinguishes the three cases; `changed`
    // is kept so existing callers are unaffected.
    const change =
      leftLine === undefined ? 'added' : rightLine === undefined ? 'removed' : leftLine === rightLine ? 'unchanged' : 'changed'
    rows.push({ left: leftLine ?? '', right: rightLine ?? '', changed: change !== 'unchanged', change })
  }
  return { rows, truncated: leftLines.truncated || rightLines.truncated }
}

export function findMatches(value: string, query: string) {
  const needle = query.trim().toLowerCase()
  if (!needle) return []
  const matches: number[] = []
  let offset = 0
  const haystack = value.toLowerCase()
  while (matches.length < 500) {
    const index = haystack.indexOf(needle, offset)
    if (index < 0) break
    matches.push(index)
    offset = index + Math.max(needle.length, 1)
  }
  return matches
}

export type HeaderComparisonRow = { key: string; name: string; current: string; selected: string; change: 'added' | 'removed' | 'changed' | 'unchanged' }

function normalizedHeaders(headers: Record<string, string>) {
  const values = new Map<string, { name: string; value: string }>()
  for (const [name, value] of Object.entries(headers)) values.set(name.toLowerCase(), { name, value: String(value) })
  return values
}

export function compareHeaders(current: Record<string, string>, selected: Record<string, string>) {
  const left = normalizedHeaders(current)
  const right = normalizedHeaders(selected)
  const keys = Array.from(new Set([...left.keys(), ...right.keys()])).sort()
  return keys.map((key): HeaderComparisonRow => {
    const a = left.get(key); const b = right.get(key)
    const change = !a ? 'added' : !b ? 'removed' : a.value === b.value ? 'unchanged' : 'changed'
    return { key, name: a?.name ?? b?.name ?? key, current: a?.value ?? '', selected: b?.value ?? '', change }
  })
}

type JsonShape = { available: boolean; reason?: string; root?: string; added: string[]; removed: string[]; changed: string[] }
function jsonType(value: unknown) { return value === null ? 'null' : Array.isArray(value) ? 'array' : typeof value }
function parseBoundedJson(value: string) {
  if (value.length > fullRenderLimit) return { reason: 'too large' } as const
  try { return { value: JSON.parse(value) as unknown } as const } catch { return { reason: 'invalid JSON' } as const }
}

export function compareJsonStructure(current: string, selected: string): JsonShape {
  const left = parseBoundedJson(current); const right = parseBoundedJson(selected)
  if ('reason' in left || 'reason' in right) return { available: false, reason: 'reason' in left ? left.reason : right.reason, added: [], removed: [], changed: [] }
  const root = `${jsonType(left.value)} → ${jsonType(right.value)}`
  if (!left.value || !right.value || Array.isArray(left.value) || Array.isArray(right.value) || typeof left.value !== 'object' || typeof right.value !== 'object') return { available: true, root, added: [], removed: [], changed: [] }
  const a = left.value as Record<string, unknown>; const b = right.value as Record<string, unknown>
  const added = Object.keys(b).filter((key) => !(key in a)).sort()
  const removed = Object.keys(a).filter((key) => !(key in b)).sort()
  const changed = Object.keys(a).filter((key) => key in b && jsonType(a[key]) !== jsonType(b[key])).sort()
  return { available: true, root, added, removed, changed }
}
