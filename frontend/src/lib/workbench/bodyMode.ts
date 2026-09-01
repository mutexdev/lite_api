// How the eight stored body modes are PRESENTED as six choices.
//
// The stored model is `none | json | text | xml | formUrlEncoded | multipartForm
// | file | graphql` (plus `sparql`, which no picker offers but importers
// produce). That list is fine on disk and wrong on screen: it puts three
// serialisation formats — JSON, XML, text — on the same footing as the decision
// between a raw body and a multipart form, so a user picking "how am I sending
// this" has to answer "in what syntax" at the same time.
//
// Postman, Insomnia and Bruno all split the question in two, and this module is
// that split: a MODE (`none | form | form-encoded | raw | binary | graphql`),
// and, only when the mode is raw, a FORMAT (`json | xml | text`).
//
// Deliberately a pure mapping over the existing stored values rather than a
// migration. Nothing on disk changes, nothing in Go changes, every imported
// collection keeps working, and `sparql` — which the old picker could display
// but never select, stranding anyone who imported one — becomes reachable
// again as a raw format.

export type BodyMode = 'none' | 'form' | 'form-encoded' | 'raw' | 'binary' | 'graphql'
export type BodyFormat = 'json' | 'xml' | 'text' | 'sparql'

/** The stored values this module speaks. */
export type StoredBodyMode =
  | 'none' | 'json' | 'text' | 'xml' | 'formUrlEncoded' | 'multipartForm' | 'file' | 'graphql' | 'sparql'

export const bodyModeOptions: { value: BodyMode; label: string; title: string }[] = [
  { value: 'none', label: 'None', title: 'Send no request body' },
  { value: 'form', label: 'Form data', title: 'multipart/form-data, for file uploads and mixed fields' },
  { value: 'form-encoded', label: 'x-www-form-urlencoded', title: 'application/x-www-form-urlencoded key/value pairs' },
  { value: 'raw', label: 'Raw', title: 'A body you type, in JSON, XML or plain text' },
  { value: 'binary', label: 'Binary', title: 'Send a file from disk as the whole body' },
  { value: 'graphql', label: 'GraphQL', title: 'A GraphQL query and its variables' }
]

export const bodyFormatOptions: { value: BodyFormat; label: string }[] = [
  { value: 'json', label: 'JSON' },
  { value: 'xml', label: 'XML' },
  { value: 'text', label: 'Text' },
  { value: 'sparql', label: 'SPARQL' }
]

/**
 * The flat list, with readable labels, for the few places a single dropdown is
 * still the right control.
 *
 * The saved-example editor is one: it is a metadata form of label/control pairs
 * describing a recorded request, not the live body composer, and splitting one
 * field into two there would be worse. What it does NOT get to keep is showing
 * users the raw stored strings — `formUrlEncoded` and `multipartForm` were
 * rendered verbatim as option text.
 */
export const storedBodyModeOptions: { value: StoredBodyMode; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'json', label: 'JSON' },
  { value: 'xml', label: 'XML' },
  { value: 'text', label: 'Text' },
  { value: 'sparql', label: 'SPARQL' },
  { value: 'formUrlEncoded', label: 'x-www-form-urlencoded' },
  { value: 'multipartForm', label: 'Form data' },
  { value: 'file', label: 'Binary' },
  { value: 'graphql', label: 'GraphQL' }
]

const storedToMode: Record<StoredBodyMode, BodyMode> = {
  none: 'none',
  json: 'raw',
  text: 'raw',
  xml: 'raw',
  sparql: 'raw',
  formUrlEncoded: 'form-encoded',
  multipartForm: 'form',
  file: 'binary',
  graphql: 'graphql'
}

/** The mode segment to show as selected for a stored value. */
export function modeOf(stored: string): BodyMode {
  return storedToMode[stored as StoredBodyMode] ?? 'none'
}

/** The raw format to show as selected. Meaningless unless `modeOf` is `raw`. */
export function formatOf(stored: string): BodyFormat {
  return stored === 'xml' || stored === 'text' || stored === 'sparql' ? stored : 'json'
}

/** True when the format dropdown applies at all. */
export function usesFormat(stored: string): boolean {
  return modeOf(stored) === 'raw'
}

/** The CodeEditor language for a stored mode. */
export function editorLanguage(stored: string): 'json' | 'xml' | 'graphql' | 'text' {
  if (stored === 'json') return 'json'
  if (stored === 'xml') return 'xml'
  if (stored === 'graphql') return 'graphql'
  return 'text'
}

/**
 * The raw format to restore when a request returns to Raw, per request.
 *
 * The first version of this took only the mode being LEFT, which remembers
 * exactly one hop. That is enough for `xml → form → raw` clicked without pause
 * and wrong for everything else, because the caller re-derives the previous
 * mode from whatever the last click wrote: by the second hop the memory is
 * `multipartForm`, which is not a raw format, so Raw resolves to JSON.
 *
 * The consequence is not cosmetic and is exactly what the one-hop version was
 * written to prevent. The XML text is not deleted — it is still in the
 * request's `xml` field — but the editor now shows the empty `json` field
 * instead, and the backend chooses what to serialise from `mode` alone, so the
 * request goes out as `application/json` with a body the user never wrote and
 * cannot see. No error, no edit, no warning.
 *
 * Keyed by request because the format is a property of that request's body,
 * not of the picker. Without the key, opening a second request and switching
 * its mode would rewrite the first request's remembered format.
 */
export type FormatMemory = Map<string, BodyFormat>

/** Records the raw format of a request whose stored mode is a raw one. */
export function rememberFormat(memory: FormatMemory, requestId: string, stored: string): void {
  if (usesFormat(stored)) memory.set(requestId, formatOf(stored))
}

/**
 * The format to show, preferring what is stored and falling back to memory.
 *
 * A stored raw mode is always authoritative — it is what will actually be sent.
 * Memory only answers when the request is not currently in a raw mode, which is
 * precisely the case where there is nothing stored to read.
 */
export function recallFormat(memory: FormatMemory, requestId: string, stored: string): BodyFormat {
  if (usesFormat(stored)) return formatOf(stored)
  return memory.get(requestId) ?? 'json'
}

/**
 * The stored value to write when a mode segment is chosen.
 *
 * `remembered` is the format this request was last seen using — see
 * `recallFormat`. Passing nothing keeps the old one-hop behaviour, which is
 * correct for callers that genuinely have no memory to offer, so the default
 * cannot silently become the bug described above for the caller that does.
 */
export function storedForMode(mode: BodyMode, previous: string, remembered?: BodyFormat): StoredBodyMode {
  if (mode === 'raw') {
    if (usesFormat(previous)) return previous as StoredBodyMode
    return remembered ?? 'json'
  }
  if (mode === 'form') return 'multipartForm'
  if (mode === 'form-encoded') return 'formUrlEncoded'
  if (mode === 'binary') return 'file'
  if (mode === 'graphql') return 'graphql'
  return 'none'
}

/** The stored value to write when a raw format is chosen. */
export function storedForFormat(format: BodyFormat): StoredBodyMode {
  return format
}

/**
 * A short label for what will actually go on the wire.
 *
 * Shown in the toolbar's status slot. The old picker made the Content-Type
 * implicit in a mode name like `formUrlEncoded`; splitting mode from format
 * makes it less obvious still, so it is stated outright rather than left to be
 * discovered in the Headers tab.
 */
export function contentTypeHint(stored: string): string {
  switch (stored) {
    case 'json': return 'application/json'
    case 'xml': return 'application/xml'
    case 'text': return 'text/plain'
    case 'sparql': return 'application/sparql-query'
    case 'formUrlEncoded': return 'application/x-www-form-urlencoded'
    case 'multipartForm': return 'multipart/form-data'
    case 'graphql': return 'application/json'
    case 'file': return 'from the file'
    default: return ''
  }
}
