// US-033 — deciding whether an editor document is "large", without allocating
// a copy of it on every keystroke.
//
// The only consumer of the byte length is a threshold test:
// `large = byteLength > largeDocumentBytes`. The previous implementation
// answered it with `new TextEncoder().encode(value).byteLength`, which
// allocates a Uint8Array the size of the whole document — half a megabyte per
// keystroke on a 500 KB body, handed straight to the garbage collector.
//
// Nothing needs the exact count. Three cheaper answers, in order:
//
//   1. UTF-8 length is always >= the UTF-16 length, so a document whose
//      `.length` already exceeds the limit is large with no work at all.
//   2. UTF-8 length is always <= 3x the UTF-16 length. A surrogate pair is two
//      UTF-16 units encoding to four UTF-8 bytes, which is two bytes per unit,
//      so the worst case is a 3-byte BMP character at one unit each. A document
//      whose length x 3 fits the limit is therefore small with no work either.
//   3. Only in the band between those does the size have to be measured — and
//      then with TextEncoder.encodeInto into a shared, reused buffer, which
//      keeps the encoding native while allocating nothing per call.
//
// The memo on top means repeated reactive invalidations with an unchanged
// string cost one string comparison.

/** Matches CodeEditor's threshold; passed in so the two cannot drift. */
export type DocumentSizeMemo = {
  value: string
  limit: number
  large: boolean
}

// One reusable destination buffer, allocated lazily and shared by every editor.
//
// encodeInto writes into a caller-supplied array instead of returning a new
// one, so the per-keystroke allocation disappears while the encoding stays
// native. A scan in JavaScript was measured at 27x the time of the native
// encoder on a 500 KB document — trading half a megabyte of garbage for 1.6 ms
// of blocking main-thread work per keystroke, which is the worse end of the
// deal. One resident buffer costs a single allocation for the process.
let probeBuffer: Uint8Array | null = null
let probeBufferLimit = -1

const probeEncoder = new TextEncoder()

/**
 * utf8ExceedsLimit reports whether value encodes to MORE than limit bytes.
 *
 * Answers from the O(1) bounds where it can, and otherwise encodes into the
 * shared buffer. Never allocates per call once the buffer exists.
 */
export function utf8ExceedsLimit(value: string, limit: number): boolean {
  if (limit < 0) return true

  // Bound 1: one UTF-16 unit is at least one UTF-8 byte.
  if (value.length > limit) return true
  // Bound 2: at most three UTF-8 bytes per UTF-16 unit. A surrogate pair is two
  // units encoding to four bytes, which is two per unit, so the worst case is a
  // 3-byte BMP character at one unit each.
  if (value.length * 3 <= limit) return false

  // The buffer is four bytes longer than the limit so a document sitting
  // exactly on it still fits, and one that does not is detectable by a short
  // read rather than by arithmetic on a truncated count.
  const required = limit + 4
  if (!probeBuffer || probeBufferLimit !== limit) {
    probeBuffer = new Uint8Array(required)
    probeBufferLimit = limit
  }

  const { read, written } = probeEncoder.encodeInto(value, probeBuffer)
  // A short read means the source did not fit in limit+4 bytes at all.
  if (read < value.length) return true
  return written > limit
}

/**
 * isLargeDocument answers the threshold question, reusing the previous answer
 * when the string has not changed.
 *
 * The memo is a plain value rather than a Map: the editor asks about one
 * document at a time, and a cache keyed by content would hold whole documents
 * alive for no benefit.
 */
export function isLargeDocument(value: string, limit: number, memo: DocumentSizeMemo | null): { large: boolean; memo: DocumentSizeMemo } {
  if (memo && memo.limit === limit && memo.value === value) {
    return { large: memo.large, memo }
  }
  const large = utf8ExceedsLimit(value, limit)
  return { large, memo: { value, limit, large } }
}

/**
 * variableSignature identifies a variable list for configuration-change
 * detection.
 *
 * Memoised on ARRAY IDENTITY rather than contents. The array is rebuilt only
 * when the variables actually change, so identity is the correct key — and
 * hashing the contents to detect that would cost exactly what this avoids.
 * A same-identity call is one reference comparison.
 */
export type VariableSignatureInput = {
  name: string
  scope: string
  secret: boolean
  found: boolean
  validName: boolean
}

export type SignatureMemo = {
  items: readonly VariableSignatureInput[]
  signature: string
}

export function variableSignature(
  items: readonly VariableSignatureInput[],
  memo: SignatureMemo | null
): { signature: string; memo: SignatureMemo } {
  if (memo && memo.items === items) {
    return { signature: memo.signature, memo }
  }
  const signature = items
    .map((item) => `${item.name}:${item.scope}:${item.secret}:${item.found}:${item.validName}`)
    .join('|')
  return { signature, memo: { items, signature } }
}
