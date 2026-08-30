// The Flow tab's pure logic: what a step is doing, what a run produced, and how
// an editable assertion becomes the wire shape the backend validates.
//
// EVERYTHING TESTABLE ABOUT THE FLOW TAB LIVES HERE, because the alternative is
// that none of it is tested. This repo has no component-rendering harness, so a
// rule that stays inside FlowTab.svelte is a rule nobody can ask a question
// about — and the three rules below are all silent when they break:
//
//   * the step-state reduction. A "flow:progress" event for a DIFFERENT flow
//     that is allowed to land paints running/passed chips onto the flow the
//     user is looking at. Nothing throws; the tab just lies about a run.
//   * the failing-step identification. Fail-fast means the run report stops at
//     the step that broke, so "which step stopped it" is a fact about the
//     LENGTH of a list, not a flag on a row. Get it wrong and a failed run
//     blames the wrong request.
//   * the assertion draft round trip. `equals` is untyped on the wire on
//     purpose (see types.FlowAssert), so a status assertion has to serialise as
//     a NUMBER and a body assertion as a STRING. Serialise a status as "200"
//     and the backend rejects the save with "status equals that is not a
//     number"; serialise it as 200 after the user typed nothing and the save is
//     rejected differently. Both are only visible by saving.
//
// NOTHING HERE VALIDATES A FLOW. internal/core/flows.go owns validation, has
// exactly one implementation of it on purpose, and writes its errors as
// sentences meant to be read by the person who caused them. A second, weaker
// copy in the UI would either disagree with it or duplicate it; the tab's job
// is to send the flow and show what came back.

import type { types } from '../../wailsjs/go/models'

/**
 * The "flow:progress" event payload. Mirrors types.FlowProgress.
 *
 * Hand-written for the same reason McpApprovalRequest is: Wails generates
 * models for values that cross a BINDING, and an event payload does not cross
 * one — it is emitted through EventsEmit and never appears in models.ts. The
 * ws:event and grpc:event payloads are spelled out the same way.
 */
export interface FlowProgressEvent {
  collectionId: string
  flowId: string
  stepId: string
  stepIndex: number
  stepCount: number
  /** "running", "passed" or "failed". Anything else is ignored. */
  state: string
}

/**
 * What one step's chip says.
 *
 * "pending" is the frontend's word, not the backend's: the run emits nothing
 * for a step it has not reached, and fail-fast means it never will for the
 * steps after a failure. A step with no event is therefore not "unknown" — it
 * is a step that has not run, which is exactly what the user needs to see under
 * the one that failed.
 */
export type FlowStepState = 'pending' | 'running' | 'passed' | 'failed'

/** Live per-step state for the flow currently on screen. */
export type FlowRunProgress = {
  collectionId: string
  flowId: string
  /** Keyed by step id. Absent means pending. */
  states: Readonly<Record<string, FlowStepState>>
}

/** A progress record with every step pending — what Run starts from. */
export function emptyFlowProgress(collectionId: string, flowId: string): FlowRunProgress {
  return { collectionId, flowId, states: {} }
}

function progressStateOf(raw: string): FlowStepState | undefined {
  const state = (raw ?? '').trim().toLowerCase()
  if (state === 'running' || state === 'passed' || state === 'failed') return state
  return undefined
}

/**
 * Folds one arriving progress event into the tab's per-step state.
 *
 * THE COLLECTION AND FLOW ARE CHECKED, NOT ASSUMED. "flow:progress" is a global
 * Wails event: an agent running a flow through run_flow emits onto the same
 * channel as the user's own Run button, and two collections can hold flows with
 * ids that mean nothing to each other. An event for anything but the flow this
 * record is tracking is dropped — the alternative is chips lighting up on a
 * flow nobody started.
 *
 * An unrecognised `state` is dropped rather than stored. The vocabulary is
 * three words and the backend owns it; storing a fourth would render a chip
 * with no styling and no meaning.
 *
 * Returns the SAME object when nothing changed, so a caller can skip a reassign
 * — the pattern applyLiveSessionPush uses for the same reason.
 */
export function applyFlowProgress(current: FlowRunProgress, event: FlowProgressEvent): FlowRunProgress {
  if (!event) return current
  if ((event.collectionId ?? '') !== current.collectionId) return current
  if ((event.flowId ?? '') !== current.flowId) return current
  const stepId = (event.stepId ?? '').trim()
  if (!stepId) return current
  const state = progressStateOf(event.state)
  if (!state) return current
  if (current.states[stepId] === state) return current
  return { ...current, states: { ...current.states, [stepId]: state } }
}

/** One step's chip state, defaulting to pending. */
export function flowStepState(progress: FlowRunProgress | undefined, stepId: string): FlowStepState {
  return progress?.states[stepId] ?? 'pending'
}

/** The chip's visible word. */
export function flowStepStateLabel(state: FlowStepState): string {
  return { pending: 'Pending', running: 'Running', passed: 'Passed', failed: 'Failed' }[state]
}

// ---------------------------------------------------------------------------
// The run report
// ---------------------------------------------------------------------------

/** One extracted value, as a row. Sorted so two runs read the same way. */
export type FlowExtractedRow = { name: string; value: string }

/** One rendered step of a finished (or in-flight) run. */
export type FlowRunRow = {
  stepId: string
  /** 1-based, so the row reads "Step 2" the way the backend's errors count. */
  position: number
  requestId: string
  /** The request's name, or the raw id when the request has since been deleted. */
  requestLabel: string
  method: string
  state: FlowStepState
  /** '' for a step that has not produced a result yet. */
  statusLabel: string
  durationLabel: string
  extracted: FlowExtractedRow[]
  assertions: readonly types.FlowAssertResult[]
  error: string
  /** The one step that stopped the run. Exactly one row can carry this. */
  stoppedRun: boolean
}

/** Sorted name/value rows for a step's extractions. */
export function flowExtractedRows(extracted: Record<string, string> | undefined): FlowExtractedRow[] {
  return Object.entries(extracted ?? {})
    .map(([name, value]) => ({ name, value }))
    .sort((left, right) => left.name.localeCompare(right.name))
}

/** "412 ms". Sub-millisecond steps still say a number rather than nothing. */
export function flowDurationLabel(durationMs: number | undefined): string {
  const value = Number(durationMs ?? 0)
  if (!Number.isFinite(value) || value < 0) return ''
  return `${Math.round(value)} ms`
}

/**
 * Which step stopped the run, as an index into the flow's steps.
 *
 * FAIL-FAST IS ENCODED IN THE LENGTH OF `steps`, not in a flag. A flow that
 * failed at step 2 returns two step results and not three — the third is absent
 * BECAUSE it never ran. So the failing step is the last result, and only when
 * the run reports not-ok. Reading it any other way (scanning for a non-empty
 * `error`, say) gives the same answer today and a wrong one the moment a step
 * ever carries a non-fatal note.
 *
 * -1 when the run succeeded, or when it failed before any step ran at all —
 * which is a real case: an unknown flow id or a missing required input is
 * rejected by flowRunPlan before the first request leaves.
 */
export function flowFailingStepIndex(result: types.FlowRunResult | undefined): number {
  if (!result || result.ok) return -1
  const steps = result.steps ?? []
  if (steps.length === 0) return -1
  return steps.length - 1
}

type FlowRequestLookup = Pick<types.RequestItem, 'id' | 'name' | 'method'>

/**
 * The rows the run panel draws: one per declared step, whether or not it ran.
 *
 * DRIVEN BY THE FLOW, NOT BY THE RESULT. A run that stopped at step 2 of four
 * still has to show steps 3 and 4 as pending, because "these never ran" is half
 * of what a failed run is telling the user. Iterating the result would silently
 * shorten the list to the point of failure and leave the reader to notice that
 * two steps they wrote are missing from the report.
 *
 * `progress` fills the state in while the call is still in flight, and the
 * result overrides it once it lands — a step with a result is settled, and a
 * "running" chip left over from an event that arrived after the report would
 * leave a spinner on a finished run.
 */
export function flowRunRows(
  flow: Pick<types.Flow, 'steps'> | undefined,
  requests: readonly FlowRequestLookup[],
  result: types.FlowRunResult | undefined,
  progress: FlowRunProgress | undefined,
): FlowRunRow[] {
  const steps = flow?.steps ?? []
  const resultsByStep = new Map((result?.steps ?? []).map((step) => [step.stepId, step]))
  const failingIndex = flowFailingStepIndex(result)
  const requestsById = new Map(requests.map((request) => [request.id, request]))

  return steps.map((step, index) => {
    const stepResult = resultsByStep.get(step.id)
    const request = requestsById.get(step.requestId)
    const state: FlowStepState = stepResult
      ? stepResult.error
        ? 'failed'
        : 'passed'
      : flowStepState(progress, step.id)
    return {
      stepId: step.id,
      position: index + 1,
      requestId: step.requestId,
      // The id is the fallback rather than "Unknown": a step whose request was
      // deleted is a flow that will not run, and the id is what the backend's
      // own refusal will name.
      requestLabel: request?.name || step.requestId,
      method: (request?.method ?? '').toUpperCase(),
      state,
      statusLabel: stepResult && stepResult.status > 0 ? String(stepResult.status) : '',
      durationLabel: stepResult ? flowDurationLabel(stepResult.durationMs) : '',
      extracted: flowExtractedRows(stepResult?.extracted),
      assertions: stepResult?.assertions ?? [],
      error: stepResult?.error ?? '',
      stoppedRun: failingIndex >= 0 && index === failingIndex,
    }
  })
}

export type FlowRunSummary = {
  tone: 'ok' | 'bad'
  /** "Passed" / "Failed" — the one word the eye lands on first. */
  headline: string
  /** The backend's own sentence, verbatim. '' when the run passed. */
  detail: string
  /** The step id that stopped it, or '' when nothing did. */
  stoppedAtStepId: string
}

/**
 * The banner over the run report.
 *
 * The detail is the backend's error sentence UNCHANGED. It already names the
 * step and the assertion — `step "createTerminal" failed: assertion failed: …`
 * — and rewriting it here would mean maintaining a second vocabulary for the
 * same failure, in a place that cannot see what actually happened.
 */
export function flowRunSummary(
  result: types.FlowRunResult | undefined,
): FlowRunSummary | undefined {
  if (!result) return undefined
  if (result.ok) return { tone: 'ok', headline: 'Passed', detail: '', stoppedAtStepId: '' }
  const failingIndex = flowFailingStepIndex(result)
  return {
    tone: 'bad',
    headline: 'Failed',
    detail: result.error ?? '',
    stoppedAtStepId: failingIndex >= 0 ? (result.steps ?? [])[failingIndex].stepId : '',
  }
}

/** The flow's declared outputs, as rows, in the flow's own order. */
export function flowOutputRows(
  flow: Pick<types.Flow, 'outputs'> | undefined,
  result: types.FlowRunResult | undefined,
): FlowExtractedRow[] {
  const outputs = result?.outputs
  if (!outputs) return []
  const declared = (flow?.outputs ?? []).map((output) => output.name)
  const seen = new Set<string>()
  const rows: FlowExtractedRow[] = []
  for (const name of declared) {
    if (seen.has(name) || !(name in outputs)) continue
    seen.add(name)
    rows.push({ name, value: outputs[name] })
  }
  // Anything the run produced that the flow no longer declares still shows: the
  // report is a record of what happened, and quietly dropping a value because
  // the definition has since been edited would hide it.
  for (const [name, value] of Object.entries(outputs)) {
    if (seen.has(name)) continue
    rows.push({ name, value })
  }
  return rows
}

// ---------------------------------------------------------------------------
// The request picker
// ---------------------------------------------------------------------------

export type FlowRequestOption = {
  id: string
  name: string
  method: string
  /** "POST · Create terminal" — what the option element shows. */
  label: string
}

/**
 * The collection's requests, as picker options.
 *
 * THE METHOD LEADS. A flow is read as a sequence of calls, and a list of bare
 * names ("Create terminal", "Activate terminal") does not say which of them
 * writes. The method is also how a user distinguishes the two requests that a
 * REST collection almost always has under one noun.
 *
 * An empty method renders as GET, which is what the request itself does — the
 * runner treats a blank method as GET, and showing nothing would make the two
 * look like different kinds of request.
 */
export function flowRequestOptions(
  requests: readonly FlowRequestLookup[] | undefined,
): FlowRequestOption[] {
  return (requests ?? []).map((request) => {
    const method = (request.method || 'GET').toUpperCase()
    const name = request.name || request.id
    return { id: request.id, name, method, label: `${method} · ${name}` }
  })
}

/**
 * The label for a step's currently-chosen request.
 *
 * A step can name a request that no longer exists — collections are files the
 * user edits, and deleting a request does not rewrite the flows that used it.
 * That step must still render, and it must render as a PROBLEM rather than as a
 * blank select, because the flow will not save or run until it is repointed.
 */
export function flowSelectedRequestLabel(
  requestId: string,
  requests: readonly FlowRequestLookup[] | undefined,
): string {
  const id = (requestId ?? '').trim()
  if (!id) return ''
  const match = (requests ?? []).find((request) => request.id === id)
  if (!match) return `${id} (missing)`
  return flowRequestOptions([match])[0].label
}

// ---------------------------------------------------------------------------
// Inputs
// ---------------------------------------------------------------------------

export type FlowInputField = {
  name: string
  required: boolean
  description: string
  value: string
}

/**
 * The run form's fields, one per declared input, carrying whatever the user has
 * already typed.
 *
 * Values are kept by NAME across re-derivations so that renaming an input in
 * the editor does not silently move a typed value onto a different field.
 */
export function flowInputFields(
  flow: Pick<types.Flow, 'inputs'> | undefined,
  values: Readonly<Record<string, string>>,
): FlowInputField[] {
  return (flow?.inputs ?? []).map((input) => ({
    name: input.name,
    required: Boolean(input.required),
    description: input.description ?? '',
    value: values[input.name] ?? '',
  }))
}

/**
 * Required inputs the user has not filled in.
 *
 * This does NOT gate the Run button on its own — see FlowRunPanel. The backend
 * refuses the run with its own sentence naming the input, and that sentence is
 * the authority; this exists so the form can mark the field before the round
 * trip, not so the UI can invent a second rule about what "supplied" means.
 */
export function missingRequiredFlowInputs(fields: readonly FlowInputField[]): string[] {
  return fields.filter((field) => field.required && field.value.trim() === '').map((field) => field.name)
}

/** The inputs map RunFlow is called with: every declared input, trimmed of nothing. */
export function flowInputPayload(fields: readonly FlowInputField[]): Record<string, string> {
  const payload: Record<string, string> = {}
  for (const field of fields) payload[field.name] = field.value
  return payload
}

// ---------------------------------------------------------------------------
// Editing: step order
// ---------------------------------------------------------------------------

/**
 * Moves one step up or down.
 *
 * ORDER IS THE FLOW'S ENTIRE SEMANTICS — step N+1 runs against what step N
 * extracted — so this returns a new array rather than mutating, and returns the
 * SAME array when the move would fall off either end. A silent no-op that
 * returned a fresh copy would still redraw the list and read, to the user, as a
 * move that did nothing.
 */
export function moveFlowStep<T>(steps: readonly T[], index: number, delta: number): readonly T[] {
  const target = index + delta
  if (index < 0 || index >= steps.length) return steps
  if (target < 0 || target >= steps.length) return steps
  const next = [...steps]
  const [moved] = next.splice(index, 1)
  next.splice(target, 0, moved)
  return next
}

// ---------------------------------------------------------------------------
// Editing: assertions
// ---------------------------------------------------------------------------

export type FlowAssertKind = 'status' | 'body'
export type FlowStatusCheck = 'equals' | 'in'
export type FlowBodyCheck = 'equals' | 'contains' | 'exists'

/**
 * One assertion as the FORM holds it: every field a string, plus which check is
 * selected.
 *
 * The wire type cannot be edited directly. types.FlowAssert carries `equals` as
 * `any` and expresses "which check is this" only by which fields happen to be
 * set — a shape that is right for a schema three formats share, and impossible
 * to bind a radio group to. The draft is the editable projection; the two
 * converters below are the only place the two shapes meet.
 */
export type FlowAssertDraft = {
  kind: FlowAssertKind
  statusCheck: FlowStatusCheck
  /** Digits, as typed. Empty until the user types. */
  statusEquals: string
  /** Comma- or space-separated status codes. */
  statusIn: string
  path: string
  bodyCheck: FlowBodyCheck
  /** Serves both `equals` and `contains`; only one is live at a time. */
  bodyValue: string
}

export function blankFlowAssertDraft(): FlowAssertDraft {
  return {
    kind: 'status',
    statusCheck: 'equals',
    statusEquals: '200',
    statusIn: '',
    path: '',
    bodyCheck: 'exists',
    bodyValue: '',
  }
}

function parseStatusList(raw: string): number[] {
  return (raw ?? '')
    .split(/[,\s]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
    .map((part) => Number(part))
    .filter((value) => Number.isFinite(value))
}

/**
 * The draft for an assertion loaded off disk.
 *
 * The check is inferred from which field is SET, in the same order the backend
 * validates them, because that is the only signal the wire shape carries. A
 * status assertion with neither `equals` nor `in` cannot be saved — the backend
 * refuses it — but it can certainly be READ, out of a hand-edited YAML file, so
 * it has to land somewhere: it lands on "equals" with an empty box, which is
 * the state the user has to resolve anyway.
 */
export function flowAssertDraftFrom(assertion: types.FlowAssert): FlowAssertDraft {
  const draft = blankFlowAssertDraft()
  const kind = (assertion?.type ?? '').trim().toLowerCase()
  if (kind === 'body') {
    draft.kind = 'body'
    draft.path = assertion.path ?? ''
    if (assertion.equals !== undefined && assertion.equals !== null) {
      draft.bodyCheck = 'equals'
      draft.bodyValue = String(assertion.equals)
    } else if ((assertion.contains ?? '') !== '') {
      draft.bodyCheck = 'contains'
      draft.bodyValue = assertion.contains ?? ''
    } else {
      draft.bodyCheck = 'exists'
      draft.bodyValue = ''
    }
    return draft
  }
  draft.kind = 'status'
  if ((assertion?.in ?? []).length > 0) {
    draft.statusCheck = 'in'
    draft.statusIn = (assertion.in ?? []).join(', ')
    draft.statusEquals = ''
    return draft
  }
  draft.statusCheck = 'equals'
  draft.statusEquals =
    assertion?.equals === undefined || assertion?.equals === null ? '' : String(assertion.equals)
  return draft
}

/**
 * The wire shape for one drafted assertion.
 *
 * `equals` IS TYPED BY THE ASSERTION'S KIND, and that is the whole point of
 * this function. A status equals goes out as a NUMBER because validateFlowAssert
 * refuses "a status equals that is not a number"; a body equals goes out as the
 * STRING the user typed, because a body can equal "created". The untyped field
 * in types.FlowAssert is what makes both legal in one schema, and it is also
 * what makes this the one place that can get it wrong quietly.
 *
 * A status equals box left empty produces an assertion with NO equals rather
 * than `equals: 0` or `equals: NaN`. That flow does not save — and the message
 * it does not save with is the backend's "checks the status but says nothing
 * about it; set equals or in", which is the true and useful one. Sending 0
 * would save a check that can never pass.
 */
export function flowAssertFromDraft(draft: FlowAssertDraft): types.FlowAssert {
  if (draft.kind === 'body') {
    const assertion: types.FlowAssert = { type: 'body', path: draft.path }
    if (draft.bodyCheck === 'equals') assertion.equals = draft.bodyValue
    else if (draft.bodyCheck === 'contains') assertion.contains = draft.bodyValue
    else assertion.exists = true
    return assertion
  }
  const assertion: types.FlowAssert = { type: 'status' }
  if (draft.statusCheck === 'in') {
    assertion.in = parseStatusList(draft.statusIn)
    return assertion
  }
  const trimmed = (draft.statusEquals ?? '').trim()
  if (trimmed !== '' && Number.isFinite(Number(trimmed))) assertion.equals = Number(trimmed)
  return assertion
}

/**
 * How one assertion result reads in the report.
 *
 * The backend's `detail` is written to read the same whether the assertion
 * passed or failed, precisely so a run log can show every check rather than
 * only the broken one — so it is shown verbatim, and the tick is the only thing
 * this adds.
 */
export function flowAssertionLabel(assertion: types.FlowAssertResult): string {
  return assertion.detail || (assertion.ok ? 'passed' : 'failed')
}

// ---------------------------------------------------------------------------
// Editing: the whole flow
// ---------------------------------------------------------------------------

/**
 * One step as the form holds it.
 *
 * Vars are an ARRAY of pairs here and a map on the wire. A map cannot express
 * the state a key/value editor spends most of its life in — a half-typed row
 * whose name is still empty, two rows briefly sharing a name while one is being
 * renamed — and binding rows to map keys makes the cursor jump out of the field
 * on every keystroke that changes a key.
 */
export type FlowStepDraft = {
  id: string
  requestId: string
  vars: { name: string; value: string }[]
  extract: { name: string; from: string; path: string }[]
  assert: FlowAssertDraft[]
}

export type FlowDraft = {
  id: string
  name: string
  description: string
  inputs: { name: string; required: boolean; description: string }[]
  steps: FlowStepDraft[]
  outputs: { name: string; value: string }[]
}

/** A step id proposal: "step1", "step2", … skipping the ones already taken. */
export function nextFlowStepId(existing: readonly string[]): string {
  const taken = new Set(existing.map((id) => id.trim()))
  let index = existing.length + 1
  while (taken.has(`step${index}`)) index += 1
  return `step${index}`
}

export function blankFlowStepDraft(existingIds: readonly string[], requestId = ''): FlowStepDraft {
  return { id: nextFlowStepId(existingIds), requestId, vars: [], extract: [], assert: [] }
}

/** An empty flow, ready to be named. Steps start empty; the backend refuses a
 *  flow with none, and inventing a step pointing at an arbitrary request would
 *  be a worse first impression than an empty list with an Add button. */
export function blankFlowDraft(name = 'New flow'): FlowDraft {
  return { id: '', name, description: '', inputs: [], steps: [], outputs: [] }
}

/** The editable projection of a saved flow. */
export function flowDraftFrom(flow: types.Flow): FlowDraft {
  return {
    id: flow.id ?? '',
    name: flow.name ?? '',
    description: flow.description ?? '',
    inputs: (flow.inputs ?? []).map((input) => ({
      name: input.name ?? '',
      required: Boolean(input.required),
      description: input.description ?? '',
    })),
    steps: (flow.steps ?? []).map((step) => ({
      id: step.id ?? '',
      requestId: step.requestId ?? '',
      vars: Object.entries(step.vars ?? {}).map(([name, value]) => ({ name, value })),
      extract: (step.extract ?? []).map((extract) => ({
        name: extract.name ?? '',
        from: (extract.from ?? 'body').toLowerCase(),
        path: extract.path ?? '',
      })),
      assert: (step.assert ?? []).map(flowAssertDraftFrom),
    })),
    outputs: (flow.outputs ?? []).map((output) => ({ name: output.name ?? '', value: output.value ?? '' })),
  }
}

/**
 * The wire shape for the whole draft.
 *
 * NOTHING IS DROPPED FOR BEING EMPTY except a var row with a blank name, and
 * that one exception is not validation — it is the key/value editor's own
 * housekeeping, because a blank key cannot be a map entry at all. Everything
 * else goes to the backend exactly as typed, including the empty name that will
 * be refused: the refusal is the message the user needs, and a UI that quietly
 * discarded the row would leave them saving successfully and wondering where
 * their extraction went.
 */
export function flowFromDraft(draft: FlowDraft): types.Flow {
  return {
    id: draft.id,
    name: draft.name,
    description: draft.description,
    inputs: draft.inputs.map((input) => ({
      name: input.name,
      required: input.required,
      description: input.description,
    })),
    steps: draft.steps.map((step) => ({
      id: step.id,
      requestId: step.requestId,
      vars: Object.fromEntries(step.vars.filter((entry) => entry.name.trim() !== '').map((entry) => [entry.name, entry.value])),
      extract: step.extract.map((extract) => ({ name: extract.name, from: extract.from, path: extract.path })),
      assert: step.assert.map(flowAssertFromDraft),
    })),
    outputs: draft.outputs.map((output) => ({ name: output.name, value: output.value })),
  } as types.Flow
}

/**
 * The message the editor shows after a rejected save.
 *
 * VERBATIM, and that is the whole implementation. internal/core/flows.go writes
 * its errors as instructions — "flow "X" step "lookup" extract "storeId" reads
 * from a header but names no header; set path to the header name" — and every
 * one of them names the field to fix. Prefixing them with "Could not save:" or
 * mapping them to a house string would throw away the only part of the sentence
 * that tells the user what to do.
 */
export function flowSaveErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  const text = String(error ?? '').trim()
  return text
}

// ---------------------------------------------------------------------------
// The sidebar and the tab strip
// ---------------------------------------------------------------------------

/**
 * Whether a collection draws a "Flows" group at all.
 *
 * A collection with no flows shows NOTHING — not an empty group, not a
 * placeholder row. Flows are opt-in and most collections will never have one;
 * a permanent empty heading under every collection in the tree would cost every
 * user a row to pay for a feature most of them are not using. Creating the
 * first one is reached from the collection's menu, which is where the tree's
 * other creating actions already live.
 */
export function collectionHasFlows(collection: Pick<types.Collection, 'flows'> | undefined): boolean {
  return (collection?.flows ?? []).length > 0
}

/** The flows a collection draws, in the order the file lists them. */
export function collectionFlows(collection: Pick<types.Collection, 'flows'> | undefined): types.Flow[] {
  return [...(collection?.flows ?? [])]
}

/** The sidebar row key for one flow. Same shape as the request and folder keys. */
export function flowRowKey(collectionId: string, flowID: string): string {
  return `fl:${collectionId}:${flowID}`
}

/**
 * A Flow tab's identity in the tab strip.
 *
 * PREFIXED, because the strip holds these alongside the backend's own tabs and
 * the two id spaces are unrelated: a flow id and a tab id could collide, and a
 * collision would make Svelte's keyed `each` reuse one row for the other.
 */
export function flowTabID(collectionId: string, flowID: string): string {
  return `flow:${collectionId}:${flowID}`
}

/** What a Flow tab is labelled, falling back to the id for an unnamed flow. */
export function flowTabLabel(flow: Pick<types.Flow, 'id' | 'name'> | undefined): string {
  return flow?.name?.trim() || flow?.id || 'Flow'
}
