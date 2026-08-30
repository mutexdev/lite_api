// The three rules the AI access settings section depends on.
//
// Each of these is a place where being wrong is invisible: a port that the
// backend silently replaces, a status line that claims a dead listener is
// running, and a token mask that either leaks the credential or masks the
// string that gets copied.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  DEFAULT_MCP_PORT,
  maskToken,
  mcpStatusSummary,
  normalizeMcpPort,
} from '../src/lib/mcpSettings.ts'

const TOKEN = 'a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90'
const COMMAND =
  `claude mcp add --transport http liteapi http://127.0.0.1:43117/mcp --header "Authorization: Bearer ${TOKEN}"`

test('normalizeMcpPort keeps a bindable port', () => {
  assert.equal(normalizeMcpPort(8080), 8080)
  assert.equal(normalizeMcpPort(1), 1)
  assert.equal(normalizeMcpPort(65535), 65535)
})

// The input hands back a string, always. A rule that only accepted numbers
// would send the default on every keystroke of a perfectly valid port.
test('normalizeMcpPort accepts the string the input produces', () => {
  assert.equal(normalizeMcpPort('8080'), 8080)
})

// ZERO IS THE IMPORTANT ONE. net.Listen accepts 0 and binds an ephemeral port,
// so a 0 that survived would produce a server on a port that changes every
// launch while the pairing command still names the old one.
test('normalizeMcpPort rejects every port that cannot be bound', () => {
  assert.equal(normalizeMcpPort(0), DEFAULT_MCP_PORT)
  assert.equal(normalizeMcpPort(-1), DEFAULT_MCP_PORT)
  assert.equal(normalizeMcpPort(70000), DEFAULT_MCP_PORT)
  assert.equal(normalizeMcpPort(65536), DEFAULT_MCP_PORT)
  assert.equal(normalizeMcpPort(''), DEFAULT_MCP_PORT)
  assert.equal(normalizeMcpPort('abc'), DEFAULT_MCP_PORT)
})

// Mirrors prefs.NormalizeMCP. If these two ever disagree the settings field
// shows one port and the listener binds another.
test('normalizeMcpPort agrees with the backend default', () => {
  assert.equal(DEFAULT_MCP_PORT, 43117)
})

test('mcpStatusSummary names the port when the listener is up', () => {
  const summary = mcpStatusSummary({ enabled: true, running: true, port: 43117 })

  assert.equal(summary.stateLabel, 'Running on port 43117')
  assert.equal(summary.tone, 'running')
  assert.equal(summary.lastError, '')
})

// The state that exists only because the toggle and the socket can disagree:
// a checked box with nothing listening reads as working unless it is named.
test('mcpStatusSummary distinguishes enabled from running', () => {
  const summary = mcpStatusSummary({ enabled: true, running: false, port: 43117 })

  assert.equal(summary.stateLabel, 'Enabled, not running')
  assert.equal(summary.tone, 'warning')
})

test('mcpStatusSummary reports off when the feature is off', () => {
  const summary = mcpStatusSummary({ enabled: false, running: false, port: 43117 })

  assert.equal(summary.stateLabel, 'Off')
  assert.equal(summary.tone, 'off')
})

// An absent status is the pre-mount and failed-binding case. Claiming "Running"
// on no evidence is the one answer that would mislead.
test('mcpStatusSummary reports off when there is no status yet', () => {
  assert.equal(mcpStatusSummary(undefined).stateLabel, 'Off')
  assert.equal(mcpStatusSummary(undefined).tone, 'off')
})

// The bind failure is the reason the section can say anything useful about a
// port collision, so it has to survive as its own field rather than being
// folded into the label.
test('mcpStatusSummary surfaces the last error', () => {
  const summary = mcpStatusSummary({
    enabled: true,
    running: false,
    port: 43117,
    lastError: 'listen tcp 127.0.0.1:43117: bind: address already in use',
  })

  assert.equal(summary.stateLabel, 'Enabled, not running')
  assert.match(summary.lastError, /address already in use/)
})

test('maskToken elides the middle of the token and keeps the rest of the command', () => {
  const masked = maskToken(COMMAND)

  assert.ok(!masked.includes(TOKEN), 'the full token is still in the display string')
  assert.ok(masked.includes('a1b2c3'), 'the token prefix was dropped')
  assert.ok(masked.includes('8f90"'), 'the token suffix was dropped')
  assert.ok(
    masked.startsWith('claude mcp add --transport http liteapi http://127.0.0.1:43117/mcp'),
    'the rest of the command did not survive masking',
  )
})

// Six and four: enough to tell two installs apart in a bug report, nowhere near
// enough to reconstruct 32 bytes of entropy.
test('maskToken keeps six characters of prefix and four of suffix', () => {
  const masked = maskToken(TOKEN)

  assert.equal(masked, 'a1b2c3…8f90')
})

// An error string, an empty command, or a command whose token is not 64 hex
// characters must come back untouched rather than mangled.
test('maskToken leaves a command with no 64-hex token alone', () => {
  const plain = 'claude mcp add --transport http liteapi http://127.0.0.1:43117/mcp'

  assert.equal(maskToken(plain), plain)
  assert.equal(maskToken('Bearer deadbeef'), 'Bearer deadbeef')
  assert.equal(maskToken(''), '')
})
