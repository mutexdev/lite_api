// The one answer to "is this result good", for every surface that shows one.
//
// A8-01. The app had FOUR rules and they disagreed about the same number. The
// response pane graded 3xx amber (workbench/commandState.ts: `status < 400`
// -> 'warning'); History graded the identical entry green (`status >= 200`
// -> 'ok', and a 3xx never reached the `>= 400` branch); the Runner coloured
// its Status and Code cells not at all; and a Flow step ignored the code
// entirely, colouring only a chip driven by assertion pass/fail. So one 302,
// looked at in four places, was good, fine, unremarkable and unjudged.
//
// That is not a styling inconsistency. It is the app teaching a user a rule in
// one pane and breaking it in the next, which is exactly how a product starts
// reading as several products stapled together. Colour is the fastest signal on
// a results screen and it has to mean one thing.
//
// ── WHAT A 3xx IS, AND WHY IT IS AMBER ──────────────────────────────────────
//
// A 3xx that is VISIBLE is a redirect the client did not follow.
//
// LiteAPI follows redirects by default — types.NewRequestItem ships
// FollowRedirects: true, MaxRedirects: 5 (internal/types/request_item.go:87),
// and internal/core/app_execute_http.go installs a CheckRedirect that honours
// it. So a 3xx only ever reaches a results row for one of three reasons: the
// user turned redirect-following off, the chain ran past MaxRedirects, or the
// Location header was missing or unusable.
//
// In all three the response on screen is a POINTER to the resource, not the
// resource. Nothing failed — the server answered correctly and the transport
// was clean, so 'danger' would be a lie and would train people to ignore red.
// But the request did not deliver what was asked for, and grading it 'success'
// hides the single most common cause of "my assertion passed but the body is
// empty": a 301 with the payload one hop away.
//
// So: amber. Not an error, not the answer. Look at this one.
//
// This also keeps the canonical surface — the response pane, the place people
// learn the vocabulary — unchanged, and moves the other three onto it, rather
// than inventing a fourth rule that would have to be re-learned.
//
// ── WHY 1xx SITS WITH SUCCESS ───────────────────────────────────────────────
//
// It is unreachable: Go's http client consumes informational responses and
// never surfaces one as a final status, so nothing can render a 1xx today. The
// bucket exists only to keep the function total. Grouping it with success
// rather than minting a fifth tone avoids adding a colour no surface paints.

/**
 * The four tones every results surface in the app can paint.
 *
 * 'idle' is not a grade — it is the absence of one, for a row that has not run
 * yet or a response pane with nothing in it. It deliberately maps to no colour
 * class at all, because a grey badge is still a badge and a flow of thirty
 * pending steps wearing thirty of them drowns the one step that is moving.
 */
export type StatusTone = 'success' | 'warning' | 'danger' | 'idle'

/**
 * The bucketing, and the only copy of it.
 *
 * Matches what the response pane already did, boundary for boundary, so
 * adopting this function anywhere is a no-op on that surface and a correction
 * everywhere else.
 */
export function statusTone(status: number | undefined): StatusTone {
  const code = Number(status ?? 0)
  // NaN is neither < 300 nor >= 400, so it has to be caught before the
  // comparisons or it would fall through to the final `else` and read as a
  // failure. A status we could not parse is a status we do not have.
  if (!Number.isFinite(code) || code <= 0) return 'idle'
  if (code < 300) return 'success'
  if (code < 400) return 'warning'
  return 'danger'
}

/**
 * A whole result rather than a bare code: what History, Runner and Flow rows
 * actually hold.
 *
 * PRECEDENCE IS THE POINT. A transport error outranks whatever code came with
 * it, because a row that carries both ("200" plus "unexpected EOF") is a row
 * where the code is stale and the error is what happened. Cancellation outranks
 * a code too, and is amber rather than red: the user stopped it, and painting a
 * deliberate cancel the same colour as a 500 makes "I hit Cancel" look like a
 * fault report.
 */
export function resultTone(result: {
  status?: number | undefined
  error?: string | undefined
  cancelled?: boolean | undefined
}): StatusTone {
  if (result.cancelled) return 'warning'
  if (result.error) return 'danger'
  return statusTone(result.status)
}

/**
 * The Runner's verdict words, which are not HTTP codes.
 *
 * internal/core/app_runner.go emits exactly four: "passed", "failed",
 * "skipped" and "cancelled". Skipped is amber rather than grey because a
 * skipped request in a 40-request run is a request that did not happen — the
 * thing the user most needs to notice after a failure — and unknown words fall
 * to 'idle' rather than to a colour, so a fifth verdict added on the backend
 * shows up uncoloured rather than mis-coloured.
 */
export function outcomeTone(outcome: string | undefined): StatusTone {
  switch ((outcome ?? '').toLowerCase()) {
    case 'passed':
      return 'success'
    case 'failed':
      return 'danger'
    case 'cancelled':
    case 'skipped':
      return 'warning'
    default:
      return 'idle'
  }
}

/**
 * The utility class in style.css that paints a tone.
 *
 * Kept as a function rather than a lookup written out at each call site so the
 * mapping cannot drift: `.ok`, `.warn` and `.bad` are the three global classes
 * (style.css defines them together with `.response-summary .ok/.warn/.bad`),
 * and 'idle' is the empty string on purpose — see the note on StatusTone.
 *
 * `.warning` is NOT one of them. It exists only as `.runner-summary .warning`,
 * scoped to that one bar; writing it on a row would silently paint nothing.
 */
export function toneClass(tone: StatusTone): '' | 'ok' | 'warn' | 'bad' {
  switch (tone) {
    case 'success':
      return 'ok'
    case 'warning':
      return 'warn'
    case 'danger':
      return 'bad'
    default:
      return ''
  }
}

/**
 * What a screen reader gets instead of the colour.
 *
 * Colour is the whole signal on these rows, so every tone that means something
 * needs a word beside it. 'idle' returns '' — there is nothing to announce
 * about a row that has not run, and "idle" read out thirty times is noise.
 */
export function toneLabel(tone: StatusTone): string {
  switch (tone) {
    case 'success':
      return 'succeeded'
    case 'warning':
      return 'needs attention'
    case 'danger':
      return 'failed'
    default:
      return ''
  }
}
