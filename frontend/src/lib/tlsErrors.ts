/**
 * Reading a certificate failure back out of a response error (US-059).
 *
 * The backend rewrites a certificate failure into a sentence that names the
 * host, the fault and the remedies. This turns that sentence back into the two
 * things the response pane can offer as buttons, so the user does not have to
 * follow prose to a settings screen they have never opened.
 */

/** Must match tlsFailureMarker in internal/core/tls_error.go. */
export const TLS_FAILURE_MARKER = 'TLS certificate verification failed'

export interface TLSFailure {
  /** The explanation, without the raw Go error appended to it. */
  summary: string
  /** The machine wording, worth keeping for a bug report but not for the eye. */
  detail: string
  /** Whether adding a CA could plausibly fix this; false for expiry and name mismatches. */
  suggestsCustomCA: boolean
}

/**
 * Returns the parts of a certificate failure, or undefined when the error is an
 * ordinary network failure that has nothing to do with verification.
 */
export function parseTLSFailure(message: string | undefined): TLSFailure | undefined {
  const text = (message ?? '').trim()
  if (!text.startsWith(TLS_FAILURE_MARKER)) return undefined
  const newline = text.indexOf('\n')
  const summary = (newline < 0 ? text : text.slice(0, newline)).trim()
  const detail = newline < 0 ? '' : text.slice(newline + 1).trim()
  return {
    summary,
    detail,
    suggestsCustomCA: summary.includes('Custom CA certificate')
  }
}

export function isTLSFailure(message: string | undefined): boolean {
  return parseTLSFailure(message) !== undefined
}
