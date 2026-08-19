// US-035 — tests for coalescing per-keystroke request patches.
//
// Every test here is about a way a keystroke could be LOST or MISDELIVERED,
// because that is the only interesting failure mode: the happy path is a
// debounce, and a debounce that works is indistinguishable from one that
// silently drops the last character until a user complains.
//
// Time is injected rather than waited on. A test that slept for the real 120 ms
// would be slow and flaky, and worse, it would pass even if the delay were
// wrong — sleeping longer than any plausible debounce hides an incorrect window.

import assert from 'node:assert/strict'
import test from 'node:test'

import { PatchCoalescer, mergePatches, sameTarget } from '../src/lib/patchQueue.ts'

/** A controllable clock: nothing fires until `tick()` is called. */
function fakeClock() {
  let pending: Array<{ fn: () => void; handle: number }> = []
  let next = 1
  return {
    schedule: (fn: () => void) => {
      const handle = next++
      pending.push({ fn, handle })
      return handle
    },
    cancel: (handle: unknown) => {
      pending = pending.filter((p) => p.handle !== handle)
    },
    tick() {
      const due = pending
      pending = []
      for (const p of due) p.fn()
    },
    get scheduled() {
      return pending.length
    },
  }
}

const target = { collectionId: 'c1', itemId: 'i1' }
const other = { collectionId: 'c1', itemId: 'i2' }

function harness(delayMs = 120) {
  const clock = fakeClock()
  const calls: Array<{ target: typeof target; patch: Record<string, unknown> }> = []
  const coalescer = new PatchCoalescer<Record<string, unknown>>(
    async (t, p) => {
      calls.push({ target: t as typeof target, patch: p })
    },
    { delayMs, schedule: clock.schedule, cancel: clock.cancel },
  )
  return { clock, calls, coalescer }
}

test('many keystrokes collapse into one call carrying the latest value', async () => {
  const { clock, calls, coalescer } = harness()
  for (const url of ['h', 'ht', 'htt', 'http']) coalescer.queue(target, { url })
  assert.equal(calls.length, 0, 'nothing should be sent before the window elapses')

  clock.tick()
  await coalescer.flush()
  assert.equal(calls.length, 1, 'four keystrokes must collapse into one call')
  assert.equal(calls[0].patch.url, 'http', 'the call must carry the LAST value typed')
})

test('merging is last-write-wins per field, so unrelated edits both survive', async () => {
  const { clock, calls, coalescer } = harness()
  coalescer.queue(target, { url: 'https://a' })
  coalescer.queue(target, { method: 'POST' })
  coalescer.queue(target, { url: 'https://b' })
  clock.tick()
  await coalescer.flush()

  assert.deepEqual(calls[0].patch, { url: 'https://b', method: 'POST' })
})

test('an explicit flush sends immediately and cancels the timer', async () => {
  // This is the Send path: type, then hit Send before the window elapses.
  const { clock, calls, coalescer } = harness()
  coalescer.queue(target, { url: 'https://typed-then-sent' })
  await coalescer.flush()

  assert.equal(calls.length, 1)
  assert.equal(calls[0].patch.url, 'https://typed-then-sent')
  clock.tick()
  await coalescer.flush()
  assert.equal(calls.length, 1, 'the cancelled timer must not fire a second, empty call')
})

test('flush with nothing pending is a no-op, not an empty patch', async () => {
  const { calls, coalescer } = harness()
  await coalescer.flush()
  assert.equal(calls.length, 0, 'an empty flush must not send a patch that clears fields')
})

test('switching requests mid-window delivers the patch to the request it was typed into', async () => {
  // The misdelivery case. Without this, characters typed into request A arrive
  // as an edit to request B — silent corruption of a request the user did not
  // even have open.
  const { clock, calls, coalescer } = harness()
  coalescer.queue(target, { url: 'belongs-to-i1' })
  await coalescer.queue(other, { url: 'belongs-to-i2' })

  assert.equal(calls.length, 1, 'the first request must be flushed before the second is accepted')
  assert.equal(calls[0].target.itemId, 'i1')
  assert.equal(calls[0].patch.url, 'belongs-to-i1')

  clock.tick()
  await coalescer.flush()
  assert.equal(calls.length, 2)
  assert.equal(calls[1].target.itemId, 'i2')
  assert.equal(calls[1].patch.url, 'belongs-to-i2')
})

test('patches for different requests are never merged together', async () => {
  const { clock, calls, coalescer } = harness()
  coalescer.queue(target, { method: 'POST' })
  await coalescer.queue(other, { url: 'x' })
  clock.tick()
  await coalescer.flush()

  assert.equal(calls[0].patch.url, undefined, "i1's patch must not carry i2's url")
  assert.equal(calls[1].patch.method, undefined, "i2's patch must not carry i1's method")
})

test('overlapping flushes stay ordered, so an older patch cannot land last', async () => {
  const clock = fakeClock()
  const order: string[] = []
  let releaseFirst: () => void = () => {}
  const firstDone = new Promise<void>((resolve) => {
    releaseFirst = resolve
  })
  let call = 0
  const coalescer = new PatchCoalescer<Record<string, unknown>>(
    async (_t, p) => {
      call += 1
      if (call === 1) await firstDone
      order.push(String(p.url))
    },
    { delayMs: 120, schedule: clock.schedule, cancel: clock.cancel },
  )

  coalescer.queue(target, { url: 'first' })
  const a = coalescer.flush()
  coalescer.queue(target, { url: 'second' })
  const b = coalescer.flush()
  releaseFirst()
  await Promise.all([a, b])

  assert.deepEqual(order, ['first', 'second'], 'a slow first flush must not let the second overtake it')
})

test('hasPending and pendingTarget report the queue honestly', async () => {
  const { coalescer } = harness()
  assert.equal(coalescer.hasPending, false)
  assert.equal(coalescer.pendingTarget, null)

  coalescer.queue(target, { url: 'x' })
  assert.equal(coalescer.hasPending, true)
  assert.deepEqual(coalescer.pendingTarget, target)

  await coalescer.flush()
  assert.equal(coalescer.hasPending, false, 'a flushed queue must not still claim to hold a patch')
  assert.equal(coalescer.pendingTarget, null)
})

test('mergePatches is shallow, because patch fields are whole replacements', () => {
  // A deep merge would resurrect a header the user just deleted: `headers` is
  // the complete new array, not a delta.
  const merged = mergePatches({ headers: [{ name: 'A' }, { name: 'B' }] }, { headers: [{ name: 'A' }] })
  assert.deepEqual(merged.headers, [{ name: 'A' }], 'the newer, shorter array must win outright')
})

test('sameTarget distinguishes both halves of the identity', () => {
  assert.equal(sameTarget({ collectionId: 'c1', itemId: 'i1' }, { collectionId: 'c1', itemId: 'i1' }), true)
  assert.equal(sameTarget({ collectionId: 'c1', itemId: 'i1' }, { collectionId: 'c1', itemId: 'i2' }), false)
  assert.equal(sameTarget({ collectionId: 'c1', itemId: 'i1' }, { collectionId: 'c2', itemId: 'i1' }), false)
  assert.equal(sameTarget(null, { collectionId: 'c1', itemId: 'i1' }), false)
})

test('pendingPatch exposes what arrived while a flush was in flight', async () => {
  // REGRESSION. This is the bug browser QA caught and the unit tests above did
  // not: while a flush is on the wire the user keeps typing, and the result
  // that comes back describes the request as of when the call was MADE.
  // Applying it verbatim rewinds the input. Measured before the fix: typing
  // "https://coalesced.example/abcdefghij" stored
  // "https://coalesed.example/abcefghij" — characters silently dropped.
  //
  // The caller repairs this by re-applying pendingPatch on top of the
  // authoritative result, so this asserts pendingPatch reports exactly the
  // characters typed during the flush.
  const clock = fakeClock()
  let release: () => void = () => {}
  const inFlight = new Promise<void>((resolve) => {
    release = resolve
  })
  const seen: Array<Record<string, unknown> | null> = []
  const coalescer = new PatchCoalescer<Record<string, unknown>>(
    async () => {
      await inFlight
      // Sampled at the moment the result would be applied.
      seen.push(coalescer.pendingPatch)
    },
    { delayMs: 120, schedule: clock.schedule, cancel: clock.cancel },
  )

  coalescer.queue(target, { url: 'https://ab' })
  const flushed = coalescer.flush()
  // ... user keeps typing while the call is on the wire ...
  coalescer.queue(target, { url: 'https://abcdef' })
  release()
  await flushed

  assert.deepEqual(seen, [{ url: 'https://abcdef' }],
    'the queue must still hold the newer text so the caller can re-apply it over the stale result')
})

test('pendingPatch is null when nothing was typed during the flush', async () => {
  // The other half: re-applying a stale patch when none is pending would
  // resurrect text the user had already moved past.
  const { coalescer } = harness()
  coalescer.queue(target, { url: 'x' })
  await coalescer.flush()
  assert.equal(coalescer.pendingPatch, null)
})

// The three accessors, which coverage found untested.
//
// pendingPatch is not a convenience — it exists for the in-flight overwrite
// hazard the class comment describes. While a flush is on the wire the user
// keeps typing, and those characters queue here. The flush returns a result
// describing the request as of when the call was MADE, so applying it verbatim
// rewinds the input. The caller re-applies pendingPatch on top to undo that.
//
// If the accessor reported null while characters were queued, the caller would
// have nothing to re-apply and the keystrokes typed during the flush would be
// silently discarded. That is the failure this trio guards.

test('hasPending and pendingTarget are false and null with nothing queued', () => {
  const coalescer = new PatchCoalescer(async () => {}, { schedule: () => 1, cancel: () => {} })
  assert.equal(coalescer.hasPending, false)
  assert.equal(coalescer.pendingTarget, null)
  assert.equal(coalescer.pendingPatch, null)
})

test('queuing exposes the pending target and patch', () => {
  const coalescer = new PatchCoalescer<{ url?: string }>(async () => {}, { schedule: () => 1, cancel: () => {} })
  coalescer.queue({ collectionId: 'c1', itemId: 'i1' }, { url: 'https://a.test' })

  assert.equal(coalescer.hasPending, true)
  assert.deepEqual(coalescer.pendingTarget, { collectionId: 'c1', itemId: 'i1' })
  assert.deepEqual(coalescer.pendingPatch, { url: 'https://a.test' })
})

test('pendingPatch reflects the merged patch, not just the last one', () => {
  const coalescer = new PatchCoalescer<{ url?: string; method?: string }>(
    async () => {}, { schedule: () => 1, cancel: () => {} }
  )
  const target = { collectionId: 'c1', itemId: 'i1' }
  coalescer.queue(target, { url: 'https://a.test' })
  coalescer.queue(target, { method: 'POST' })

  // Both edits must be visible: re-applying only the last would drop the URL
  // the user typed first.
  assert.deepEqual(coalescer.pendingPatch, { url: 'https://a.test', method: 'POST' })
})

test('flushing clears the pending state', async () => {
  const coalescer = new PatchCoalescer<{ url?: string }>(async () => {}, { schedule: () => 1, cancel: () => {} })
  coalescer.queue({ collectionId: 'c1', itemId: 'i1' }, { url: 'https://a.test' })
  await coalescer.flush()

  assert.equal(coalescer.hasPending, false)
  assert.equal(coalescer.pendingPatch, null)
  assert.equal(coalescer.pendingTarget, null, 'a flushed queue must not still name a target')
})

// The hazard itself, end to end: characters typed WHILE a flush is in flight
// must still be readable afterwards, or they are lost.
test('a patch queued during an in-flight flush is still pending after it resolves', async () => {
  let release: () => void = () => {}
  const gate = new Promise<void>((resolve) => { release = resolve })

  const coalescer = new PatchCoalescer<{ url?: string }>(
    async () => { await gate },
    { schedule: () => 1, cancel: () => {} }
  )
  const target = { collectionId: 'c1', itemId: 'i1' }
  coalescer.queue(target, { url: 'first' })
  const inFlight = coalescer.flush()

  // The user keeps typing while the request is on the wire.
  coalescer.queue(target, { url: 'second' })
  release()
  await inFlight

  assert.equal(coalescer.hasPending, true, 'characters typed during the flush must survive it')
  assert.deepEqual(coalescer.pendingPatch, { url: 'second' })
})

// The DEFAULT scheduler, which every other test in this file replaces.
//
// Coverage showed patchQueue's function percentage stuck even after the
// accessors were covered: the default schedule/cancel closures were never
// called, because every test injects fakes to control time. Those defaults are
// what production actually runs, so the real setTimeout path was the one piece
// never exercised.
test('the default scheduler flushes on its own after the delay', async () => {
  const sent: Array<{ url?: string }> = []
  const coalescer = new PatchCoalescer<{ url?: string }>(
    async (_target, patch) => { sent.push(patch) },
    { delayMs: 5 } // real setTimeout, real clearTimeout
  )

  coalescer.queue({ collectionId: 'c1', itemId: 'i1' }, { url: 'https://a.test' })
  assert.equal(coalescer.hasPending, true, 'queuing must not send immediately')

  await new Promise((resolve) => setTimeout(resolve, 40))

  assert.deepEqual(sent, [{ url: 'https://a.test' }], 'the timer must fire and deliver the patch')
  assert.equal(coalescer.hasPending, false)
})

// A flushed patch is not delivered twice.
//
// NOT because of the cancel — a control proved that. Replacing the default
// cancel with a no-op fails nothing, because flush() clears this.patch, so the
// stray timer's flush finds nothing pending and returns early. The cancel is
// belt-and-braces; the null check is what carries the guarantee.
//
// Worth stating rather than deleting: this test does exercise the real timer
// path, and the property it asserts is one users depend on. It just is not the
// cancel that provides it, and a comment claiming otherwise would send the next
// person looking in the wrong place.
test('a flushed patch is not delivered again when its timer fires', async () => {
  const sent: Array<{ url?: string }> = []
  const coalescer = new PatchCoalescer<{ url?: string }>(
    async (_target, patch) => { sent.push(patch) },
    { delayMs: 5 }
  )

  coalescer.queue({ collectionId: 'c1', itemId: 'i1' }, { url: 'https://a.test' })
  await coalescer.flush()
  await new Promise((resolve) => setTimeout(resolve, 40))

  assert.equal(sent.length, 1, 'the cancelled timer must not deliver the patch a second time')
})
