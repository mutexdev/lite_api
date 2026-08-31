// The Flow tab's logic, held to the behaviour a component harness would check
// if this repo had one.
//
// Every case below is silent when it breaks. A progress event accepted from the
// wrong flow paints a run that nobody started; a failing step identified from
// the wrong end of the list blames the wrong request; a status `equals`
// serialised as a string is refused by a backend validator the user never sees
// the input for. None of them throw, and all of them are only visible by
// running a real flow against a real API.

import test from 'node:test'
import assert from 'node:assert/strict'
import {
  applyFlowProgress,
  blankFlowAssertDraft,
  blankFlowStepDraft,
  collectionFlows,
  collectionHasFlows,
  emptyFlowProgress,
  flowAssertDraftFrom,
  flowAssertFromDraft,
  flowDraftFrom,
  flowDurationLabel,
  flowExtractedRows,
  flowFailingStepIndex,
  flowFromDraft,
  flowInputFields,
  flowInputPayload,
  flowOutputRows,
  flowRequestOptions,
  flowRowKey,
  flowRunRows,
  flowRunSummary,
  flowSaveErrorMessage,
  flowSelectedRequestLabel,
  flowStepState,
  flowStepStateLabel,
  flowTabID,
  flowTabLabel,
  missingRequiredFlowInputs,
  moveFlowStep,
  nextFlowStepId,
  type FlowProgressEvent,
} from '../src/lib/flowView.ts'
import type { types } from '../wailsjs/go/models'

// The document's canonical example, trimmed to the parts the UI reads. Keeping
// the real ids means a rename in docs/mcp-agent-interface.md shows up here.
const flow = {
  id: 'flow_8f3k',
  name: 'Provision POS terminal',
  description: 'GraphQL lookup -> create terminal on API B -> activate on API C',
  inputs: [{ name: 'storeCode', required: true, description: 'Store short code, e.g. DHK-04' }],
  steps: [
    { id: 'lookup', requestId: 'req_graphql_store' },
    { id: 'createTerminal', requestId: 'req_apib_create_terminal' },
    { id: 'activate', requestId: 'req_apic_activate' },
  ],
  outputs: [{ name: 'terminalId', value: '{{terminalId}}' }],
} as unknown as types.Flow

const requests = [
  { id: 'req_graphql_store', name: 'Store lookup', method: 'POST' },
  { id: 'req_apib_create_terminal', name: 'Create terminal', method: 'POST' },
  { id: 'req_apic_activate', name: 'Activate terminal', method: 'PUT' },
] as types.RequestItem[]

const progressEvent = (overrides: Partial<FlowProgressEvent> = {}): FlowProgressEvent => ({
  collectionId: 'col_1',
  flowId: 'flow_8f3k',
  stepId: 'lookup',
  stepIndex: 0,
  stepCount: 3,
  state: 'running',
  ...overrides,
})

// ── The step-state reduction ────────────────────────────────────────────────

test('a step starts pending and moves through running to passed', () => {
  let progress = emptyFlowProgress('col_1', 'flow_8f3k')
  assert.equal(flowStepState(progress, 'lookup'), 'pending')

  progress = applyFlowProgress(progress, progressEvent({ state: 'running' }))
  assert.equal(flowStepState(progress, 'lookup'), 'running')

  progress = applyFlowProgress(progress, progressEvent({ state: 'passed' }))
  assert.equal(flowStepState(progress, 'lookup'), 'passed')
  // The steps that have not been reached are still pending, which is the half
  // of a run report that says "these have not happened".
  assert.equal(flowStepState(progress, 'activate'), 'pending')
})

// "flow:progress" is a GLOBAL channel. An agent calling run_flow emits onto the
// same one as the user's own Run button, and two collections can hold flows
// whose ids mean nothing to each other. Accepting a foreign event lights chips
// on a flow nobody started, and nothing anywhere throws.
test('an event for another flow or another collection is ignored', () => {
  const progress = emptyFlowProgress('col_1', 'flow_8f3k')

  assert.equal(applyFlowProgress(progress, progressEvent({ flowId: 'flow_other' })), progress)
  assert.equal(applyFlowProgress(progress, progressEvent({ collectionId: 'col_2' })), progress)
  assert.deepEqual(applyFlowProgress(progress, progressEvent({ flowId: 'flow_other' })).states, {})
})

// The vocabulary is three words and the backend owns it. A fourth would render
// a chip with no styling and no meaning.
test('an unrecognised state, or a step with no id, is dropped', () => {
  const progress = emptyFlowProgress('col_1', 'flow_8f3k')

  assert.equal(applyFlowProgress(progress, progressEvent({ state: 'skipped' })), progress)
  assert.equal(applyFlowProgress(progress, progressEvent({ state: '' })), progress)
  assert.equal(applyFlowProgress(progress, progressEvent({ stepId: '  ' })), progress)
})

// Returning the same object lets the caller skip a reassign, which is the
// pattern applyLiveSessionPush uses for the same reason.
test('a repeat of the state already held returns the same record', () => {
  const first = applyFlowProgress(emptyFlowProgress('col_1', 'flow_8f3k'), progressEvent({ state: 'running' }))
  assert.equal(applyFlowProgress(first, progressEvent({ state: 'running' })), first)
})

test('every state has a chip label', () => {
  assert.deepEqual(
    (['pending', 'running', 'passed', 'failed'] as const).map(flowStepStateLabel),
    ['Pending', 'Running', 'Passed', 'Failed'],
  )
})

// ── The run report ──────────────────────────────────────────────────────────

const failedRun = {
  flowId: 'flow_8f3k',
  ok: false,
  error: 'step "createTerminal" failed: assertion failed: status 500 is not 201',
  steps: [
    {
      stepId: 'lookup',
      requestId: 'req_graphql_store',
      status: 200,
      durationMs: 412,
      extracted: { region: 'apac', storeId: 'st_9' },
      assertions: [{ ok: true, detail: 'status 200 is 200' }],
    },
    {
      stepId: 'createTerminal',
      requestId: 'req_apib_create_terminal',
      status: 500,
      durationMs: 88,
      assertions: [{ ok: false, detail: 'status 500 is not 201' }],
      error: 'assertion failed: status 500 is not 201',
    },
  ],
} as unknown as types.FlowRunResult

// FAIL-FAST IS ENCODED IN THE LENGTH OF `steps`: a flow that failed at step 2
// carries two results and not three, and the third is absent BECAUSE it never
// ran. Reading the failure any other way gives the same answer today and a
// wrong one the moment a step carries a non-fatal note.
test('the failing step is the last result, and only for a run that failed', () => {
  assert.equal(flowFailingStepIndex(failedRun), 1)
  assert.equal(flowFailingStepIndex({ ...failedRun, ok: true } as types.FlowRunResult), -1)
  assert.equal(flowFailingStepIndex(undefined), -1)
})

// A refusal before the first request left — an unknown flow id, a missing
// required input — has no per-step report at all. Nothing may be blamed.
test('a run that failed before any step ran blames no step', () => {
  const rejected = { flowId: 'flow_8f3k', ok: false, error: 'input "storeCode" is required', steps: [] }
  assert.equal(flowFailingStepIndex(rejected as unknown as types.FlowRunResult), -1)
  assert.equal(flowRunSummary(rejected as unknown as types.FlowRunResult)?.stoppedAtStepId, '')
})

// DRIVEN BY THE FLOW, NOT BY THE RESULT. A report that stopped at step 2 of
// three still has to draw step 3 as pending: "this never ran" is half of what a
// failed run is saying, and iterating the result would quietly shorten the list
// to the point of failure.
test('a failed run still renders the steps that never ran, as pending', () => {
  const rows = flowRunRows(flow, requests, failedRun, undefined)

  assert.deepEqual(rows.map((row) => row.stepId), ['lookup', 'createTerminal', 'activate'])
  assert.deepEqual(rows.map((row) => row.state), ['passed', 'failed', 'pending'])
  assert.deepEqual(rows.map((row) => row.position), [1, 2, 3])
})

test('exactly one row is marked as the step that stopped the run', () => {
  const rows = flowRunRows(flow, requests, failedRun, undefined)
  assert.deepEqual(rows.filter((row) => row.stoppedRun).map((row) => row.stepId), ['createTerminal'])
})

test('a passing run marks no stopper', () => {
  const passed = { ...failedRun, ok: true, error: '' } as types.FlowRunResult
  assert.deepEqual(flowRunRows(flow, requests, passed, undefined).filter((row) => row.stoppedRun), [])
})

test('a row carries the status, duration, assertions and extracted values of its step', () => {
  const [lookup, createTerminal] = flowRunRows(flow, requests, failedRun, undefined)

  assert.equal(lookup.statusLabel, '200')
  assert.equal(lookup.durationLabel, '412 ms')
  assert.deepEqual(lookup.extracted, [
    { name: 'region', value: 'apac' },
    { name: 'storeId', value: 'st_9' },
  ])
  assert.deepEqual(lookup.assertions.map((assertion) => assertion.ok), [true])

  assert.equal(createTerminal.error, 'assertion failed: status 500 is not 201')
  assert.deepEqual(createTerminal.assertions.map((assertion) => assertion.detail), ['status 500 is not 201'])
})

// A settled step must not keep a "running" chip from an event that arrived
// after the report — that leaves a spinner on a finished run.
test('a result overrides whatever the live progress last said', () => {
  const stale = applyFlowProgress(
    emptyFlowProgress('col_1', 'flow_8f3k'),
    progressEvent({ stepId: 'createTerminal', state: 'running' }),
  )
  const rows = flowRunRows(flow, requests, failedRun, stale)
  assert.equal(rows.find((row) => row.stepId === 'createTerminal')?.state, 'failed')
})

test('with no result yet, the rows read their state from the live progress', () => {
  const progress = applyFlowProgress(emptyFlowProgress('col_1', 'flow_8f3k'), progressEvent({ state: 'running' }))
  assert.deepEqual(flowRunRows(flow, requests, undefined, progress).map((row) => row.state), [
    'running',
    'pending',
    'pending',
  ])
})

test('the summary carries the backend sentence unaltered and names the stopping step', () => {
  const summary = flowRunSummary(failedRun)

  assert.equal(summary?.tone, 'bad')
  assert.equal(summary?.headline, 'Failed')
  assert.equal(summary?.detail, 'step "createTerminal" failed: assertion failed: status 500 is not 201')
  assert.equal(summary?.stoppedAtStepId, 'createTerminal')
})

test('a passing run summarises as passed with no detail', () => {
  const summary = flowRunSummary({ flowId: 'f', ok: true, steps: [] } as unknown as types.FlowRunResult)
  assert.equal(summary?.tone, 'ok')
  assert.equal(summary?.detail, '')
})

test('outputs are listed in the flow\'s declared order', () => {
  const result = {
    flowId: 'flow_8f3k',
    ok: true,
    steps: [],
    outputs: { unexpected: 'x', terminalId: 'term_77' },
  } as unknown as types.FlowRunResult

  // Declared first, then anything the run produced that the definition no
  // longer names — dropping the latter would hide a value that really happened.
  assert.deepEqual(flowOutputRows(flow, result), [
    { name: 'terminalId', value: 'term_77' },
    { name: 'unexpected', value: 'x' },
  ])
})

test('extracted values are sorted so two runs read the same way', () => {
  assert.deepEqual(flowExtractedRows({ zeta: '1', alpha: '2' }), [
    { name: 'alpha', value: '2' },
    { name: 'zeta', value: '1' },
  ])
  assert.deepEqual(flowExtractedRows(undefined), [])
})

test('a duration always says a number, and a nonsensical one says nothing', () => {
  assert.equal(flowDurationLabel(0), '0 ms')
  assert.equal(flowDurationLabel(412.6), '413 ms')
  assert.equal(flowDurationLabel(-1), '')
})

// ── The request picker ──────────────────────────────────────────────────────

// THE METHOD LEADS. A list of bare names does not say which call writes, and a
// REST collection almost always has two requests under one noun.
test('a picker option is labelled with its method first', () => {
  assert.deepEqual(flowRequestOptions(requests).map((option) => option.label), [
    'POST · Store lookup',
    'POST · Create terminal',
    'PUT · Activate terminal',
  ])
})

// A blank method IS GET — that is what the runner does with it — so showing
// nothing would make two identical requests look like different kinds.
test('a request with no method is labelled GET, and one with no name falls back to its id', () => {
  const [option] = flowRequestOptions([{ id: 'req_x', name: '', method: '' } as types.RequestItem])
  assert.equal(option.label, 'GET · req_x')
  assert.equal(option.method, 'GET')
})

// A step can name a request that has since been deleted; collections are files
// the user edits. That step must render as a PROBLEM rather than as a blank
// select, because the flow will neither save nor run until it is repointed.
test('a step pointing at a deleted request says so rather than rendering blank', () => {
  assert.equal(flowSelectedRequestLabel('req_gone', requests), 'req_gone (missing)')
  assert.equal(flowSelectedRequestLabel('req_apic_activate', requests), 'PUT · Activate terminal')
  assert.equal(flowSelectedRequestLabel('', requests), '')
})

test('a run row falls back to the request id when the request is gone', () => {
  const [row] = flowRunRows(flow, [], failedRun, undefined)
  assert.equal(row.requestLabel, 'req_graphql_store')
  assert.equal(row.method, '')
})

// ── Inputs ──────────────────────────────────────────────────────────────────

test('input fields carry what the user typed, keyed by name', () => {
  const fields = flowInputFields(flow, { storeCode: 'DHK-04' })

  assert.deepEqual(fields, [
    { name: 'storeCode', required: true, description: 'Store short code, e.g. DHK-04', value: 'DHK-04' },
  ])
  assert.deepEqual(flowInputPayload(fields), { storeCode: 'DHK-04' })
})

test('a required input left blank is reported, and whitespace does not count as filled', () => {
  assert.deepEqual(missingRequiredFlowInputs(flowInputFields(flow, {})), ['storeCode'])
  assert.deepEqual(missingRequiredFlowInputs(flowInputFields(flow, { storeCode: '   ' })), ['storeCode'])
  assert.deepEqual(missingRequiredFlowInputs(flowInputFields(flow, { storeCode: 'x' })), [])
})

test('an optional input left blank is not reported', () => {
  const optional = { inputs: [{ name: 'note', required: false, description: '' }] } as unknown as types.Flow
  assert.deepEqual(missingRequiredFlowInputs(flowInputFields(optional, {})), [])
})

// ── Step order ──────────────────────────────────────────────────────────────

// Order IS the flow's semantics: step N+1 runs against what step N extracted.
test('a step moves, and a move off either end changes nothing at all', () => {
  const steps = ['a', 'b', 'c']

  assert.deepEqual(moveFlowStep(steps, 0, 1), ['b', 'a', 'c'])
  assert.deepEqual(moveFlowStep(steps, 2, -1), ['a', 'c', 'b'])
  // The SAME array, not a fresh copy: a no-op that redrew the list would read
  // to the user as a move that did nothing.
  assert.equal(moveFlowStep(steps, 0, -1), steps)
  assert.equal(moveFlowStep(steps, 2, 1), steps)
  assert.equal(moveFlowStep(steps, 7, 1), steps)
})

test('a proposed step id does not collide with one already used', () => {
  assert.equal(nextFlowStepId([]), 'step1')
  assert.equal(nextFlowStepId(['lookup']), 'step2')
  assert.equal(nextFlowStepId(['a', 'step3']), 'step4')
  assert.equal(blankFlowStepDraft(['a', 'b']).id, 'step3')
})

// ── Assertions: the untyped `equals` ────────────────────────────────────────

// `{"type":"status","equals":200}` and `{"type":"body","equals":"created"}` are
// both legal in one schema, which is what makes this the single place that can
// get the type wrong quietly. A status equals sent as "200" is refused by
// validateFlowAssert with "a status equals that is not a number".
test('a status equals is serialised as a number and a body equals as a string', () => {
  const status = flowAssertFromDraft({ ...blankFlowAssertDraft(), kind: 'status', statusCheck: 'equals', statusEquals: '200' })
  assert.deepEqual(status, { type: 'status', equals: 200 })
  assert.equal(typeof status.equals, 'number')

  const body = flowAssertFromDraft({
    ...blankFlowAssertDraft(),
    kind: 'body',
    path: '$.terminal.state',
    bodyCheck: 'equals',
    bodyValue: 'created',
  })
  assert.deepEqual(body, { type: 'body', path: '$.terminal.state', equals: 'created' })
  assert.equal(typeof body.equals, 'string')
})

// An empty box must NOT become `equals: 0`: that saves a check which can never
// pass. Omitting it lets the backend answer with the true message — "checks the
// status but says nothing about it; set equals or in".
test('an empty status box produces no equals at all', () => {
  const assertion = flowAssertFromDraft({ ...blankFlowAssertDraft(), statusCheck: 'equals', statusEquals: '  ' })
  assert.deepEqual(assertion, { type: 'status' })
  assert.equal('equals' in assertion, false)
})

test('a status list accepts commas or spaces and drops what is not a number', () => {
  assert.deepEqual(
    flowAssertFromDraft({ ...blankFlowAssertDraft(), statusCheck: 'in', statusIn: '200, 201  204' }),
    { type: 'status', in: [200, 201, 204] },
  )
})

test('a body exists check carries no value', () => {
  assert.deepEqual(
    flowAssertFromDraft({ ...blankFlowAssertDraft(), kind: 'body', path: '$.a', bodyCheck: 'exists', bodyValue: 'ignored' }),
    { type: 'body', path: '$.a', exists: true },
  )
})

// The wire shape says which check it is only by which field happens to be set,
// so reading it back has to infer in the order the backend validates.
test('every assertion in the canonical example survives a draft round trip', () => {
  const wire: types.FlowAssert[] = [
    { type: 'status', equals: 200 },
    { type: 'status', in: [200, 201] },
    { type: 'body', path: '$.terminal.state', equals: 'created' },
    { type: 'body', path: '$.terminal.id', exists: true },
    { type: 'body', path: '$.message', contains: 'ok' },
  ]

  for (const assertion of wire) {
    assert.deepEqual(flowAssertFromDraft(flowAssertDraftFrom(assertion)), assertion)
  }
})

// A status of 200 may arrive as an int from YAML or a float64 from JSON; the
// draft has to read either into the same box.
test('a status equals decoded as a float reads back as its digits', () => {
  assert.equal(flowAssertDraftFrom({ type: 'status', equals: 200 } as types.FlowAssert).statusEquals, '200')
})

// Refused by the backend, but perfectly readable out of a hand-edited file. It
// has to land somewhere the user can resolve it.
test('a status assertion with no check at all opens on an empty equals box', () => {
  const draft = flowAssertDraftFrom({ type: 'status' } as types.FlowAssert)
  assert.equal(draft.statusCheck, 'equals')
  assert.equal(draft.statusEquals, '')
})

// ── The whole flow ──────────────────────────────────────────────────────────

test('the canonical example survives a draft round trip', () => {
  const full = {
    ...flow,
    steps: [
      {
        id: 'lookup',
        requestId: 'req_graphql_store',
        vars: { code: '{{storeCode}}' },
        extract: [
          { name: 'storeId', from: 'body', path: '$.data.store.id' },
          { name: 'region', from: 'body', path: '$.data.store.region' },
        ],
        assert: [{ type: 'status', equals: 200 }],
      },
      {
        id: 'createTerminal',
        requestId: 'req_apib_create_terminal',
        vars: { storeId: '{{storeId}}', region: '{{region}}' },
        extract: [{ name: 'terminalId', from: 'body', path: '$.terminal.id' }],
        assert: [
          { type: 'status', in: [200, 201] },
          { type: 'body', path: '$.terminal.state', equals: 'created' },
        ],
      },
      { id: 'activate', requestId: 'req_apic_activate', vars: { terminalId: '{{terminalId}}' }, assert: [{ type: 'status', equals: 200 }] },
    ],
  } as unknown as types.Flow

  const round = flowFromDraft(flowDraftFrom(full))

  assert.equal(round.id, 'flow_8f3k')
  assert.equal(round.name, 'Provision POS terminal')
  assert.deepEqual(round.inputs, [{ name: 'storeCode', required: true, description: 'Store short code, e.g. DHK-04' }])
  assert.deepEqual(round.steps[0].vars, { code: '{{storeCode}}' })
  assert.deepEqual(round.steps[0].extract, [
    { name: 'storeId', from: 'body', path: '$.data.store.id' },
    { name: 'region', from: 'body', path: '$.data.store.region' },
  ])
  assert.deepEqual(round.steps[1].assert, [
    { type: 'status', in: [200, 201] },
    { type: 'body', path: '$.terminal.state', equals: 'created' },
  ])
  assert.deepEqual(round.outputs, [{ name: 'terminalId', value: '{{terminalId}}' }])
})

// The one exception to "send it as typed": a blank key cannot be a map entry at
// all. Everything else — an extraction with no name, a step with no request —
// goes to the backend so the backend's own sentence is what the user reads.
test('only a var row with a blank name is dropped; nothing else is', () => {
  const draft = flowDraftFrom(flow)
  draft.steps[0].vars = [{ name: '', value: 'orphan' }, { name: 'code', value: 'x' }]
  draft.steps[0].extract = [{ name: '', from: 'body', path: '' }]

  const wire = flowFromDraft(draft)
  assert.deepEqual(wire.steps[0].vars, { code: 'x' })
  assert.equal(wire.steps[0].extract?.length, 1, 'a nameless extraction must reach the backend to be refused')
})

// ── Validation messages ─────────────────────────────────────────────────────

// VERBATIM. internal/core/flows.go writes its errors as instructions naming the
// field to fix; prefixing or remapping them throws away the only part of the
// sentence that says what to do.
test('a backend refusal is passed through word for word', () => {
  const sentence =
    'flow "Provision POS terminal" step "lookup" extract "storeId" reads from a header but names no header; set path to the header name'

  assert.equal(flowSaveErrorMessage(new Error(sentence)), sentence)
  assert.equal(flowSaveErrorMessage(sentence), sentence)
  assert.equal(flowSaveErrorMessage(undefined), '')
})

// ── The sidebar and the tab strip ───────────────────────────────────────────

// A collection with no flows draws NOTHING. Flows are opt-in and most
// collections will never have one; a permanent empty heading under every
// collection charges every user a row for a feature they are not using.
test('a collection with no flows draws no group', () => {
  assert.equal(collectionHasFlows({ flows: [] } as unknown as types.Collection), false)
  assert.equal(collectionHasFlows({} as types.Collection), false)
  assert.equal(collectionHasFlows({ flows: [flow] } as unknown as types.Collection), true)
  assert.deepEqual(collectionFlows({} as types.Collection), [])
})

// PREFIXED, because the strip holds these alongside the backend's own tabs and
// the two id spaces are unrelated — a collision would make a keyed each reuse
// one row for the other.
test('a flow tab id cannot collide with a backend tab id', () => {
  assert.equal(flowTabID('col_1', 'flow_8f3k'), 'flow:col_1:flow_8f3k')
  assert.equal(flowRowKey('col_1', 'flow_8f3k'), 'fl:col_1:flow_8f3k')
})

test('a flow tab is labelled by name, falling back to its id', () => {
  assert.equal(flowTabLabel(flow), 'Provision POS terminal')
  assert.equal(flowTabLabel({ id: 'flow_1', name: '   ' } as types.Flow), 'flow_1')
  assert.equal(flowTabLabel(undefined), 'Flow')
})
