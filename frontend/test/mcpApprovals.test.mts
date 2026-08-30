// The queue and the clock behind the new-host approval dialog.
//
// Every case here is one the component cannot be asked about — this repo has no
// component-rendering harness — and every one of them is silent when it breaks:
// a dropped prompt looks like an agent that hung, a duplicated prompt looks like
// a dialog that will not close, and an expiry timed from the wrong moment leaves
// three buttons on screen that the backend has already stopped listening for.

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  MCP_APPROVAL_TIMEOUT_MS,
  approvalSecretsLabel,
  approvalTimeRemaining,
  dropApprovalPrompt,
  expireApprovalPrompts,
  queueApprovalPrompt,
  type McpApprovalPrompt,
  type McpApprovalRequest,
} from '../src/lib/mcpSettings.ts'

const request = (overrides: Partial<McpApprovalRequest> = {}): McpApprovalRequest => ({
  id: 'mcp-approval-1',
  requestName: 'Create charge',
  host: 'api.stripe.com',
  secretNames: ['STRIPE_KEY'],
  ...overrides,
})

// Mirrors core.mcpApprovalDefaultTimeout. If the two drift, the dialog either
// expires while the backend is still waiting for an answer, or keeps offering
// buttons for a run that was denied a minute ago.
test('the approval timeout agrees with the backend', () => {
  assert.equal(MCP_APPROVAL_TIMEOUT_MS, 60_000)
})

test('a prompt is queued with the fields the dialog renders', () => {
  const queue = queueApprovalPrompt([], request(), 1_000)

  assert.equal(queue.length, 1)
  assert.equal(queue[0].id, 'mcp-approval-1')
  assert.equal(queue[0].requestName, 'Create charge')
  assert.equal(queue[0].host, 'api.stripe.com')
  assert.deepEqual(queue[0].secretNames, ['STRIPE_KEY'])
  assert.equal(queue[0].receivedAt, 1_000)
})

// FIFO, and appended rather than swapped in. A prompt that replaced the one on
// screen would move the user's part-answered question out from under them and
// hand the same three buttons to a different run.
test('a second prompt waits behind the first', () => {
  let queue = queueApprovalPrompt([], request({ id: 'a' }), 1_000)
  queue = queueApprovalPrompt(queue, request({ id: 'b', host: 'api.example.com' }), 2_000)

  assert.deepEqual(queue.map((prompt) => prompt.id), ['a', 'b'])
})

// The backend rejects an empty id, so this prompt could never be answered — it
// would sit there until it expired, with three buttons that all fail.
test('a prompt with no id is refused', () => {
  assert.deepEqual(queueApprovalPrompt([], request({ id: '  ' }), 1_000), [])
})

// A re-emitted event must not leave a second copy behind after the first is
// answered: dropApprovalPrompt removes by id, so the twin would never close.
test('a repeated id does not queue twice', () => {
  const first = queueApprovalPrompt([], request(), 1_000)
  const again = queueApprovalPrompt(first, request({ host: 'elsewhere.example' }), 2_000)

  assert.equal(again.length, 1)
  assert.equal(again[0].host, 'api.stripe.com', 'the first prompt was overwritten')
})

test('secret names are trimmed, emptied and de-duplicated', () => {
  const queue = queueApprovalPrompt(
    [],
    request({ secretNames: [' TOKEN ', '', 'TOKEN', 'OTHER'] }),
    1_000,
  )

  assert.deepEqual(queue[0].secretNames, ['TOKEN', 'OTHER'])
})

test('answering removes only that prompt', () => {
  let queue = queueApprovalPrompt([], request({ id: 'a' }), 1_000)
  queue = queueApprovalPrompt(queue, request({ id: 'b' }), 1_000)

  assert.deepEqual(dropApprovalPrompt(queue, 'a').map((prompt) => prompt.id), ['b'])
  assert.deepEqual(dropApprovalPrompt(queue, 'missing').map((prompt) => prompt.id), ['a', 'b'])
})

// THE CLOCK STARTS WHEN THE BACKEND EMITTED, NOT WHEN THE DIALOG OPENED. A
// prompt that spent 45 seconds behind another one has 15 seconds of real life
// left; timing it from display would keep it on screen for a full minute after
// the run it guards had already been denied.
test('time remaining is measured from arrival, not from display', () => {
  const prompt: McpApprovalPrompt = { ...request(), secretNames: ['STRIPE_KEY'], receivedAt: 1_000 }

  assert.equal(approvalTimeRemaining(prompt, 1_000), 60_000)
  assert.equal(approvalTimeRemaining(prompt, 46_000), 15_000)
  assert.equal(approvalTimeRemaining(prompt, 61_000), 0)
  assert.equal(approvalTimeRemaining(prompt, 500_000), 0, 'a stale prompt must not report negative time')
})

test('the sweep retires exactly the prompts the backend has given up on', () => {
  let queue = queueApprovalPrompt([], request({ id: 'old' }), 0)
  queue = queueApprovalPrompt(queue, request({ id: 'new' }), 30_000)

  const swept = expireApprovalPrompts(queue, 61_000)

  assert.deepEqual(swept.queue.map((prompt) => prompt.id), ['new'])
  assert.deepEqual(swept.expired.map((prompt) => prompt.id), ['old'])
})

// Returned rather than dropped so the app can say what happened. A dialog that
// simply disappeared would read as one that had been approved.
test('the sweep hands back what it expired', () => {
  const queue = queueApprovalPrompt([], request(), 0)
  const swept = expireApprovalPrompts(queue, MCP_APPROVAL_TIMEOUT_MS)

  assert.equal(swept.queue.length, 0)
  assert.equal(swept.expired.length, 1)
  assert.equal(swept.expired[0].requestName, 'Create charge')
})

test('the sweep leaves a live queue alone', () => {
  const queue = queueApprovalPrompt([], request(), 1_000)

  assert.deepEqual(expireApprovalPrompts(queue, 2_000).expired, [])
})

test('secret names read as a sentence', () => {
  assert.equal(approvalSecretsLabel(['TOKEN']), 'TOKEN')
  assert.equal(approvalSecretsLabel(['A', 'B']), 'A and B')
  assert.equal(approvalSecretsLabel(['A', 'B', 'C']), 'A, B and C')
  assert.equal(approvalSecretsLabel([]), '')
  assert.equal(approvalSecretsLabel(['', '  ']), '')
})

// --- wiring ----------------------------------------------------------------
//
// Source-text assertions, in the style of mcpSection.test.mts: the repo has no
// component-rendering harness, and these particular facts are invisible when
// they break. An unsubscribed EventsOn leaks a handler per window; a dialog
// wired to the wrong `remember` argument writes a permanent allowance the user
// asked for once.

const read = (relative: string) =>
  readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8')

const app = read('../src/App.svelte')
const dialog = read('../src/lib/views/mcp/McpApprovalModal.svelte')

test('the approval event is subscribed and unsubscribed together', () => {
  assert.ok(
    /stopMcpApproval = EventsOn\('mcp:approval'/.test(app),
    "App.svelte does not listen for 'mcp:approval'",
  )
  assert.ok(
    /stopMcpApproval\?\.\(\)/.test(app),
    'the mcp:approval subscription is never released; onDestroy leaks a handler',
  )
  assert.ok(
    /clearInterval\(mcpApprovalSweep\)/.test(app),
    'the expiry sweep keeps running after the component is destroyed',
  )
})

// THE THREE ANSWERS ARE THREE DIFFERENT CALLS, and the pair of booleans is the
// only thing that separates them. "Allow once" sent with remember=true persists
// a (secret, host) pair the user agreed to for one run.
test('the dialog sends the right pair of booleans for each answer', () => {
  for (const [testid, args] of [
    ['mcp-approval-deny', 'prompt.id, false, false'],
    ['mcp-approval-allow-once', 'prompt.id, true, false'],
    ['mcp-approval-allow-remember', 'prompt.id, true, true'],
  ] as const) {
    const button = dialog.slice(dialog.indexOf(`data-testid="${testid}"`))
    const call = /onResolve\(([^)]*)\)/.exec(button)
    assert.ok(call, `the ${testid} button does not call onResolve`)
    assert.equal(call[1], args, `${testid} answers with the wrong arguments`)
  }
})

// Escape and the backdrop both route through Modal's onClose, and the only safe
// meaning for "the user made this go away without choosing" is deny — which is
// also what the backend does when nobody answers.
test('dismissing the dialog denies', () => {
  const close = /onClose=\{\(\) => onResolve\(prompt\.id, (\w+), (\w+)\)\}/.exec(dialog)
  assert.ok(close, 'the dialog does not resolve on close')
  assert.equal(close[1], 'false', 'closing the approval dialog approves the run')
})

// The value is what the guard exists to keep put; the name is what makes the
// question answerable. A dialog that grew a value would defeat the boundary it
// is asking the user to police.
test('the dialog says so, and shows names rather than values', () => {
  assert.ok(/data-testid="mcp-approval-secrets"/.test(dialog), 'the dialog never names the secrets')
  assert.ok(/data-testid="mcp-approval-host"/.test(dialog), 'the dialog never names the host')
  assert.ok(/data-testid="mcp-approval-request"/.test(dialog), 'the dialog never names the request')
  assert.ok(
    /never gives an AI tool the value of a secret/.test(dialog),
    'the dialog no longer explains that only the name is shown',
  )
})

// A custom property declared in one component resolves in none of the 12 theme
// blocks in style.css, and the rule silently falls back.
test('the dialog defines no new CSS custom property', () => {
  const styleStart = dialog.indexOf('<style>')
  assert.ok(styleStart > 0, 'the dialog has no styles at all')
  const css = dialog.slice(styleStart, dialog.indexOf('</style>', styleStart))

  assert.ok(
    !/^\s*--[\w-]+\s*:/m.test(css),
    'the approval dialog defines a CSS custom property; it would resolve in no theme',
  )
})
