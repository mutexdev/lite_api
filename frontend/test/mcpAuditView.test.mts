// How the recent-activity list reads.
//
// The one that matters is the outcome classification. This list is opened to
// answer "did the boundary hold, or did something break?", and a badge that
// folds "denied" into "error" answers neither: every refusal disappears into a
// column of failures. The rest is the ordinary formatting that stops a row from
// rendering "Invalid Date", "NaN ms", or a megabyte of monospace.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  MCP_AUDIT_LIMIT,
  formatMcpAuditDuration,
  formatMcpAuditTime,
  mcpAuditOutcome,
  mcpAuditOutcomeLabel,
  mcpAuditRows,
  truncateArgsSummary,
} from '../src/lib/mcpSettings.ts'

test('the three backend outcomes stay three', () => {
  assert.equal(mcpAuditOutcome('ok'), 'ok')
  assert.equal(mcpAuditOutcome('denied'), 'denied')
  assert.equal(mcpAuditOutcome('error'), 'error')
})

// A DENIAL IS NOT A FAILURE. mcpserver keeps outcomeDenied apart from
// outcomeError deliberately (protocol.go:298); collapsing them here would undo
// that on the only screen where the user sees either.
test('denied never collapses into error', () => {
  assert.notEqual(mcpAuditOutcome('denied'), mcpAuditOutcome('error'))
})

test('outcomes are matched case- and whitespace-insensitively', () => {
  assert.equal(mcpAuditOutcome(' OK '), 'ok')
  assert.equal(mcpAuditOutcome('Denied'), 'denied')
})

// An outcome this file does not know is styled as an error, not as success.
// A vocabulary drift is a bug either way, but reporting a real failure as "ok"
// is the answer that would mislead someone checking whether something got out.
test('an unknown outcome is not reported as success', () => {
  assert.equal(mcpAuditOutcome('partially-fine'), 'error')
  assert.equal(mcpAuditOutcome(undefined), 'error')
  assert.equal(mcpAuditOutcome(''), 'error')
})

// ...but the badge still says what the backend actually wrote, so a drift is
// visible on screen rather than disguised as a plain error.
test('an unknown outcome keeps its own wording on the badge', () => {
  assert.equal(mcpAuditOutcomeLabel('partially-fine'), 'partially-fine')
  assert.equal(mcpAuditOutcomeLabel('error'), 'error')
  assert.equal(mcpAuditOutcomeLabel('denied'), 'denied')
  assert.equal(mcpAuditOutcomeLabel(undefined), 'error')
})

test('a timestamp from today shows the time alone', () => {
  const now = new Date('2026-08-30T14:03:22Z')
  const at = new Date(now.getTime() - 5_000)

  assert.equal(formatMcpAuditTime(at.toISOString(), now), at.toLocaleTimeString())
})

// The date is only worth its width when it is not today's.
test('an older timestamp carries its date', () => {
  const now = new Date('2026-08-30T14:03:22Z')
  const at = new Date(now.getTime() - 3 * 24 * 60 * 60 * 1000)
  const formatted = formatMcpAuditTime(at.toISOString(), now)

  assert.ok(formatted.startsWith(at.toLocaleDateString()), `expected a date in ${formatted}`)
  assert.ok(formatted.endsWith(at.toLocaleTimeString()), `expected a time in ${formatted}`)
})

// The row's tool and outcome are still worth reading, so a bad timestamp costs
// the timestamp and nothing else. "Invalid Date" would be worse than blank.
test('an unreadable timestamp renders as nothing', () => {
  assert.equal(formatMcpAuditTime('not a date'), '')
  assert.equal(formatMcpAuditTime(undefined), '')
  assert.equal(formatMcpAuditTime(null), '')
  assert.equal(formatMcpAuditTime({}), '')
})

// `N ms`, as history, the runner and the response inspector all render it.
test('durations follow the house style', () => {
  assert.equal(formatMcpAuditDuration(0), '0 ms')
  assert.equal(formatMcpAuditDuration(1234), '1234 ms')
  assert.equal(formatMcpAuditDuration(12.6), '13 ms')
})

test('a missing or impossible duration reads as zero, never NaN', () => {
  assert.equal(formatMcpAuditDuration(undefined), '0 ms')
  assert.equal(formatMcpAuditDuration(Number.NaN), '0 ms')
  assert.equal(formatMcpAuditDuration(-5), '0 ms')
})

// The backend does not cap ArgsSummary — mcp_audit.go raises bufio's line limit
// to 1 MiB precisely because it can be long — so an uncapped row would push
// every entry under it off the panel.
test('a long args summary is cut, and says so', () => {
  const summary = truncateArgsSummary('x'.repeat(500))

  assert.equal(summary.length, 181)
  assert.ok(summary.endsWith('…'), 'a truncated summary must not pass for a whole one')
})

test('a short args summary survives untouched', () => {
  assert.equal(truncateArgsSummary('  {"collection":"api"}  '), '{"collection":"api"}')
  assert.equal(truncateArgsSummary(undefined), '')
})

test('the panel asks for the backend default window', () => {
  assert.equal(MCP_AUDIT_LIMIT, 50)
})

test('rows are display-ready and keep the backend order', () => {
  const now = new Date('2026-08-30T14:03:22Z')
  const rows = mcpAuditRows(
    [
      { at: now.toISOString(), tool: 'run_request', outcome: 'denied', durationMs: 12, argsSummary: 'host=api.example' },
      { at: now.toISOString(), tool: 'list_collections', outcome: 'ok', durationMs: 3 },
    ],
    now,
  )

  assert.deepEqual(rows.map((row) => row.tool), ['run_request', 'list_collections'])
  assert.deepEqual(rows.map((row) => row.outcome), ['denied', 'ok'])
  assert.equal(rows[0].duration, '12 ms')
  assert.equal(rows[0].argsSummary, 'host=api.example')
  assert.equal(rows[1].argsSummary, '')
  assert.notEqual(rows[0].key, rows[1].key, 'two entries in the same millisecond must not share a key')
})

test('an entry missing its tool name still renders a row', () => {
  const rows = mcpAuditRows([{ at: undefined, outcome: 'ok', durationMs: 1 }])

  assert.equal(rows.length, 1)
  assert.equal(rows[0].tool, 'unknown')
  assert.equal(rows[0].time, '')
})

test('no entries produce no rows', () => {
  assert.deepEqual(mcpAuditRows(undefined), [])
  assert.deepEqual(mcpAuditRows([]), [])
})

// --- wiring ----------------------------------------------------------------
//
// Source-text assertions, like mcpSection.test.mts. The poll's teardown is the
// one that is genuinely silent: an interval left running after the Preferences
// panel closes keeps re-reading the log file for the life of the process, and
// nothing on screen would ever say so.

const section = readFileSync(
  fileURLToPath(new URL('../src/lib/views/preferences/McpSection.svelte', import.meta.url)),
  'utf8',
)

test('the section loads the log and lets it be refreshed by hand', () => {
  assert.ok(/GetMCPAuditLog\(MCP_AUDIT_LIMIT\)/.test(section), 'the section never reads the audit log')
  assert.ok(
    /data-testid="mcp-audit-refresh-btn"/.test(section),
    'there is no manual refresh control',
  )
  assert.ok(/void refreshAudit\(\)/.test(section), 'the log is never loaded when the section opens')
})

test('the poll stops when the section does', () => {
  assert.ok(/setInterval\(/.test(section), 'the section does not poll')
  assert.ok(
    /clearInterval\(auditPollTimer\)/.test(section),
    'the audit poll outlives the section; it would re-read the log file forever',
  )
})

// An empty list has two meanings — "nothing has happened" and "this has not
// loaded yet" — and reporting the second as the first tells the user an agent
// did nothing when in truth nobody has looked.
test('the empty state does not claim silence before the first read lands', () => {
  assert.ok(
    /No agent activity recorded yet\./.test(section),
    'the honest empty state is gone',
  )
  assert.ok(
    /auditEntries === undefined/.test(section),
    'a not-yet-loaded log is reported as "no activity"',
  )
})
