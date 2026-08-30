// The presentation rules for the "AI access (MCP)" settings section.
//
// Everything here is pure so it can be tested without a component harness, and
// so the section itself stays markup. Each rule here exists because getting it
// wrong is silent rather than loud:
//
//   - the port guard, which has to agree with the backend or the number in the
//     input is not the number the server binds;
//   - the status wording, which is the only place the user learns that the
//     toggle is on and the listener is nevertheless dead;
//   - the token mask, which is what keeps a long-lived credential out of a
//     screenshot while leaving the real one copyable;
//   - the approval queue and its expiry, which decide which blocked run the
//     user is answering and when the question stops being answerable;
//   - the audit row view, which is where a denial has to stay visibly distinct
//     from a failure.

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

// --- the new-host approval prompt -----------------------------------------
//
// The backend blocks a run on this dialog. That single fact drives everything
// below: the queue exists because a second prompt can arrive while the first is
// answered, and the expiry exists because the goroutine on the other side has
// already given up by the time it fires. Both are pure so the awkward cases —
// two prompts at once, a prompt that ages while queued — can be tested without
// a component harness, which this repo does not have.

/**
 * How long the backend waits for an answer. Mirrors core.mcpApprovalDefaultTimeout.
 *
 * The frontend copy is not the authority and must not pretend to be. The
 * backend has ALREADY DENIED the run when this elapses, so the client-side
 * expiry is not a decision — it is the dialog admitting that the question it is
 * asking no longer has an answer. Leaving the prompt on screen past this point
 * offers the user three buttons that all fail.
 */
export const MCP_APPROVAL_TIMEOUT_MS = 60_000

/** The "mcp:approval" event payload. Mirrors types.MCPApprovalRequest. */
export interface McpApprovalRequest {
  id: string
  requestName: string
  host: string
  secretNames: string[]
}

/** One queued prompt: the request plus when this window first saw it. */
export interface McpApprovalPrompt extends McpApprovalRequest {
  /** Epoch ms. The expiry clock runs from here, NOT from when it is shown. */
  receivedAt: number
}

/**
 * Adds one arriving prompt to the queue.
 *
 * FIFO, and appended rather than made active: a prompt that replaced the one on
 * screen would take the user's attention off a question they were part-way
 * through answering and hand the same three buttons to a different run — which
 * is how a "yes" lands on the wrong host.
 *
 * Two arrivals are dropped rather than queued. A prompt with no id cannot be
 * answered at all (ResolveMCPApproval rejects an empty id), so showing it would
 * offer a decision that cannot be delivered; and a duplicate id is a re-emitted
 * event, which would leave a second, permanently unanswerable copy behind after
 * the first is resolved.
 *
 * Secret names are trimmed and de-duplicated. The same name twice reads as two
 * different credentials in a list whose whole purpose is to say how many are
 * about to travel — and it would also collide in the keyed `each` that renders
 * them.
 */
export function queueApprovalPrompt(
  queue: readonly McpApprovalPrompt[],
  request: McpApprovalRequest,
  receivedAt: number,
): McpApprovalPrompt[] {
  const id = (request?.id ?? '').trim()
  if (!id) return [...queue]
  if (queue.some((prompt) => prompt.id === id)) return [...queue]
  return [
    ...queue,
    {
      id,
      requestName: (request.requestName ?? '').trim(),
      host: (request.host ?? '').trim(),
      secretNames: [
        ...new Set((request.secretNames ?? []).map((name) => (name ?? '').trim()).filter((name) => name !== '')),
      ],
      receivedAt,
    },
  ]
}

/** Removes one prompt, by id. Used by every exit: answer, error and expiry. */
export function dropApprovalPrompt(
  queue: readonly McpApprovalPrompt[],
  id: string,
): McpApprovalPrompt[] {
  return queue.filter((prompt) => prompt.id !== id)
}

/**
 * Milliseconds left before the backend's own deadline, never below zero.
 *
 * Measured from `receivedAt` rather than from when the dialog opened. The
 * backend starts its 60s the moment it emits, so a prompt that spent 40s behind
 * another one in the queue has 20s of real life left — timing it from display
 * would keep it on screen for 60 more seconds after the run it guards was
 * already denied.
 */
export function approvalTimeRemaining(prompt: McpApprovalPrompt, now: number): number {
  return Math.max(0, prompt.receivedAt + MCP_APPROVAL_TIMEOUT_MS - now)
}

/**
 * Splits the queue into what is still answerable and what has aged out.
 *
 * Returns the expired prompts rather than discarding them so the caller can say
 * so. A dialog that simply vanished would read as "something was approved".
 */
export function expireApprovalPrompts(
  queue: readonly McpApprovalPrompt[],
  now: number,
): { queue: McpApprovalPrompt[]; expired: McpApprovalPrompt[] } {
  const live: McpApprovalPrompt[] = []
  const expired: McpApprovalPrompt[] = []
  for (const prompt of queue) {
    if (approvalTimeRemaining(prompt, now) > 0) live.push(prompt)
    else expired.push(prompt)
  }
  return { queue: live, expired }
}

/**
 * The secret names as a sentence: "TOKEN", "A and B", "A, B and C".
 *
 * Named, never valued — naming the credential is the whole reason the prompt is
 * worth reading, and the value is the thing the guard exists to keep put.
 */
export function approvalSecretsLabel(names: readonly string[]): string {
  const clean = names.map((name) => (name ?? '').trim()).filter((name) => name !== '')
  if (clean.length === 0) return ''
  if (clean.length === 1) return clean[0]
  return `${clean.slice(0, -1).join(', ')} and ${clean[clean.length - 1]}`
}

// --- the recent activity list ---------------------------------------------

/**
 * How many entries the panel asks for. Mirrors core.mcpAuditDefaultLimit.
 *
 * "Recent activity", not an archive: the backend retains 500 and will serve up
 * to 200, and a settings panel that rendered either is a scroll region nobody
 * reads. The file is re-read per call, so the poll below asks for the smallest
 * useful window.
 */
export const MCP_AUDIT_LIMIT = 50

/** The three outcomes the backend records. Mirrors mcpserver's outcome consts. */
export type McpAuditOutcome = 'ok' | 'denied' | 'error'

/** The shape of types.MCPAuditEntry this module needs; structural, so tests need no binding. */
export interface McpAuditEntryView {
  /** Go `time.Time`, so a string over the wire. Typed loosely for the same reason models.ts is. */
  at?: unknown
  tool?: string
  argsSummary?: string
  outcome?: string
  durationMs?: number
}

/** One rendered row. Every field is display-ready; the markup does no formatting. */
export interface McpAuditRow {
  key: string
  time: string
  tool: string
  outcome: McpAuditOutcome
  outcomeLabel: string
  duration: string
  argsSummary: string
}

/**
 * Classifies an entry's outcome.
 *
 * DENIED IS NOT AN ERROR, and keeping them apart is the point of the badge. A
 * denial is the guard doing its job — the user said no, or a rule refused — and
 * folding it into "error" would hide every refusal in a column of failures,
 * which is precisely the column a user scans to find out whether the boundary
 * held.
 *
 * An outcome this file does not recognise is classed as an error rather than as
 * "ok". A vocabulary drift is a bug, and the failure mode of the wrong guess
 * matters: reporting a real failure as success is the one answer that would
 * mislead someone reading this list to check whether something got through.
 */
export function mcpAuditOutcome(raw: string | undefined): McpAuditOutcome {
  const value = (raw ?? '').trim().toLowerCase()
  if (value === 'ok') return 'ok'
  if (value === 'denied') return 'denied'
  return 'error'
}

/** The badge text. An unrecognised outcome shows what the backend actually said. */
export function mcpAuditOutcomeLabel(raw: string | undefined): string {
  const value = (raw ?? '').trim()
  const outcome = mcpAuditOutcome(value)
  if (outcome === 'error' && value !== '' && value.toLowerCase() !== 'error') return value
  return outcome
}

function toDate(at: unknown): Date | undefined {
  if (at instanceof Date) return Number.isNaN(at.getTime()) ? undefined : at
  if (typeof at !== 'string' && typeof at !== 'number') return undefined
  const date = new Date(at)
  return Number.isNaN(date.getTime()) ? undefined : date
}

/**
 * A compact local timestamp: the time alone for today, date and time otherwise.
 *
 * Local, and via toLocale* like every other timestamp in the app
 * (formatOpenAPISyncCheckedAt, notificationView) — an audit line is read to
 * answer "was that just now, or was that me an hour ago?", and a UTC string
 * makes the reader do the arithmetic. The date is dropped for today because it
 * is the same on every row that matters and only costs width.
 *
 * An unparseable timestamp renders empty rather than "Invalid Date": the row's
 * tool and outcome are still worth reading.
 */
export function formatMcpAuditTime(at: unknown, now: Date = new Date()): string {
  const date = toDate(at)
  if (!date) return ''
  const sameDay =
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
  return sameDay ? date.toLocaleTimeString() : `${date.toLocaleDateString()} ${date.toLocaleTimeString()}`
}

/** House style for durations, as used by history and the runner: `N ms`. */
export function formatMcpAuditDuration(ms: number | undefined): string {
  const value = Number(ms)
  if (!Number.isFinite(value) || value < 0) return '0 ms'
  return `${Math.round(value)} ms`
}

/** How much of an args summary a row shows before it is cut. */
const ARGS_SUMMARY_LIMIT = 180

/**
 * Caps the args summary.
 *
 * The BACKEND DOES NOT CAP IT — mcp_audit.go raises bufio's line limit to 1 MiB
 * specifically because a summary can be long — so an unbounded one would render
 * as a wall of monospace that pushes every following row off the screen. Cut
 * with an ellipsis so a truncated line cannot be mistaken for the whole of a
 * short one.
 */
export function truncateArgsSummary(value: string | undefined, limit = ARGS_SUMMARY_LIMIT): string {
  const text = (value ?? '').trim()
  if (text.length <= limit) return text
  return `${text.slice(0, limit)}…`
}

/**
 * Turns the backend's entries into rows.
 *
 * Order is preserved: mcpAuditStore.List already returns newest first, and
 * re-sorting here would be a second opinion about a question the store has
 * already answered.
 */
export function mcpAuditRows(
  entries: readonly McpAuditEntryView[] | undefined,
  now: Date = new Date(),
): McpAuditRow[] {
  return (entries ?? []).map((entry, index) => ({
    // The entries carry no id, so the key is position plus timestamp: enough to
    // keep a row's identity stable across a poll that prepends new entries.
    key: `${index}:${String(entry?.at ?? '')}`,
    time: formatMcpAuditTime(entry?.at, now),
    tool: (entry?.tool ?? '').trim() || 'unknown',
    outcome: mcpAuditOutcome(entry?.outcome),
    outcomeLabel: mcpAuditOutcomeLabel(entry?.outcome),
    duration: formatMcpAuditDuration(entry?.durationMs),
    argsSummary: truncateArgsSummary(entry?.argsSummary),
  }))
}
