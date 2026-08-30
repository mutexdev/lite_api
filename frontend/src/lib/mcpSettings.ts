// The presentation rules for the "AI access (MCP)" settings section.
//
// Everything here is pure so it can be tested without a component harness, and
// so the section itself stays markup. Three things live in this module, and
// each exists because getting it wrong is silent rather than loud:
//
//   - the port guard, which has to agree with the backend or the number in the
//     input is not the number the server binds;
//   - the status wording, which is the only place the user learns that the
//     toggle is on and the listener is nevertheless dead;
//   - the token mask, which is what keeps a long-lived credential out of a
//     screenshot while leaving the real one copyable.

/**
 * The port the backend falls back to. Mirrors mcpserver.DefaultPort.
 *
 * Deliberately not 0. Zero is a legal argument to net.Listen — it asks the OS
 * for an ephemeral port — and the pairing command has the port written into its
 * URL, so a port that moved on every launch would break an already-paired agent
 * with a connection error that says nothing about the port.
 */
export const DEFAULT_MCP_PORT = 43117

const MIN_MCP_PORT = 1
const MAX_MCP_PORT = 65535

/**
 * Settles the port typed into the settings field.
 *
 * Mirrors prefs.NormalizeMCP: anything outside [1, 65535] becomes the default.
 * The backend normalizes again and is the authority — this exists so the input
 * never displays a value the backend is about to silently replace, which is how
 * a user ends up pasting a pairing command for a port nothing is listening on.
 *
 * Non-numeric input (an empty field, a cleared field, "abc") is out of range by
 * the same rule rather than by a separate branch: there is no port there, so
 * there is nothing to preserve.
 */
export function normalizeMcpPort(raw: string | number): number {
  const numeric = Math.trunc(Number(raw))
  if (!Number.isFinite(numeric) || numeric < MIN_MCP_PORT || numeric > MAX_MCP_PORT) {
    return DEFAULT_MCP_PORT
  }
  return numeric
}

/** The shape of types.MCPStatus this module needs; structural so tests need no binding. */
export interface McpStatusView {
  enabled: boolean
  running: boolean
  port: number
  lastError?: string
}

/** What the status line renders. `tone` names a state, not a colour. */
export interface McpStatusSummary {
  stateLabel: string
  tone: 'running' | 'warning' | 'off'
  lastError: string
}

/**
 * Turns a status into the one line the settings section shows.
 *
 * THE MIDDLE STATE IS THE POINT. "enabled but not running" is a real outcome —
 * the port was already taken, or a secondary workspace window deliberately
 * declined to bind — and without a distinct label for it the user sees a
 * checked toggle and assumes an agent can connect. `lastError` is carried
 * separately rather than folded into the label so the section can render it as
 * detail without the label growing a backend error message inside it.
 *
 * An absent status (the binding has not answered yet, or it failed) reads as
 * "Off": claiming "Running" on no evidence is the one wrong answer here.
 */
export function mcpStatusSummary(status: McpStatusView | undefined): McpStatusSummary {
  const lastError = status?.lastError?.trim() ?? ''

  if (!status?.enabled) {
    return { stateLabel: 'Off', tone: 'off', lastError }
  }
  if (status.running) {
    return { stateLabel: `Running on port ${status.port}`, tone: 'running', lastError }
  }
  return { stateLabel: 'Enabled, not running', tone: 'warning', lastError }
}

/** A generated token is 32 random bytes hex-encoded — always 64 characters. */
const TOKEN_PATTERN = /\b[0-9a-fA-F]{64}\b/g
const TOKEN_PREFIX = 6
const TOKEN_SUFFIX = 4

/**
 * The display variant of the pairing command, with the token's middle elided.
 *
 * Only the DISPLAY variant. The Copy button copies the unmasked command, and
 * that split is the whole design: the command has to be shown (the user needs
 * to see which port it names) and it has to be pasteable (a masked token
 * authenticates nobody), so the string on screen and the string on the
 * clipboard are deliberately not the same string.
 *
 * Enough of the token is kept at each end to tell two installs apart — that is
 * what makes it useful in a bug report — while leaving far too little to guess.
 * A command with no 64-character token in it comes back unchanged rather than
 * mangled, so an error string or an empty command survives this untouched.
 */
export function maskToken(command: string): string {
  if (!command) return ''
  return command.replace(
    TOKEN_PATTERN,
    (token) => `${token.slice(0, TOKEN_PREFIX)}…${token.slice(-TOKEN_SUFFIX)}`,
  )
}
