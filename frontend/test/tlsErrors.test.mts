import assert from 'node:assert/strict'
import test from 'node:test'

import { TLS_FAILURE_MARKER, isTLSFailure, parseTLSFailure } from '../src/lib/tlsErrors.ts'

const unknownAuthority = `${TLS_FAILURE_MARKER} for api.internal: the certificate is signed by an unknown authority. The certificate is not signed by a CA this machine trusts. Add the issuing CA under Preferences → Request → Custom CA certificate, or switch off Verify TLS for this request on its Settings tab — Postman verifies nothing by default, which is why the same request succeeds there.
Get "https://api.internal": tls: failed to verify certificate: x509: certificate signed by unknown authority`

const expired = `${TLS_FAILURE_MARKER} for api.internal: the certificate has expired or is not yet valid. Switch off Verify TLS for this request on its Settings tab if you trust this server anyway. Postman verifies nothing by default, which is why the same request succeeds there.
x509: certificate has expired`

test('the explanation is separated from the machine wording', () => {
  const failure = parseTLSFailure(unknownAuthority)
  assert.ok(failure)
  assert.match(failure.summary, /api\.internal/)
  assert.ok(!failure.summary.includes('x509'), 'the raw error leaked into the summary')
  assert.match(failure.detail, /x509: certificate signed by unknown authority/)
})

test('a custom CA is offered only when it could actually help', () => {
  assert.equal(parseTLSFailure(unknownAuthority)?.suggestsCustomCA, true)
  assert.equal(parseTLSFailure(expired)?.suggestsCustomCA, false)
})

test('ordinary network failures are not treated as certificate failures', () => {
  for (const message of [
    'dial tcp 10.0.0.1:443: connect: connection refused',
    'context deadline exceeded',
    '',
    undefined
  ]) {
    assert.equal(isTLSFailure(message), false, `${message} was treated as a certificate failure`)
  }
})

test('a message with no appended detail still parses', () => {
  const failure = parseTLSFailure(`${TLS_FAILURE_MARKER} for h: the certificate could not be verified.`)
  assert.ok(failure)
  assert.equal(failure.detail, '')
})
