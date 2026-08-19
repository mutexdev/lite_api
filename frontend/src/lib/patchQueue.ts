// US-035 — coalesce per-keystroke request patches into at most one call per
// window, and update the UI optimistically so the input never lags.
//
// Before this, every character typed into a URL, header or body field was a
// separate IPC round trip. US-014 made each of those round trips 511x smaller;
// this removes most of them entirely.
//
// THE THREE WAYS THIS CAN LOSE A USER'S KEYSTROKE, all of which the design has
// to answer explicitly, because each one is silent:
//
//   1. A pending patch never flushed. Send, tab switches and shutdown all have
//      to force a flush and WAIT for it. `flush()` returns a promise for that
//      reason — a fire-and-forget flush would race the very request it precedes.
//   2. A patch queued against one request being delivered to another. The
//      target is part of the queue's identity: queuing for a different
//      request flushes the previous one first, rather than merging across them.
//   3. A later patch being overwritten by an earlier one. Merging is
//      last-write-wins per FIELD, not per patch, so a URL edit followed by a
//      header edit keeps both.

export interface PatchTarget {
  collectionId: string
  itemId: string
}

export type PatchFlusher<P> = (target: PatchTarget, patch: P) => Promise<void>

/**
 * Merges `next` over `base`, last-write-wins per field.
 *
 * Deliberately shallow. Request patches are flat field bags whose values are
 * whole replacements — `headers` is the complete new header array, not a delta —
 * so a deep merge would be wrong, not merely slower: it would resurrect
 * fields the user just deleted.
 */
export function mergePatches<P extends object>(base: P, next: P): P {
  return { ...base, ...next }
}

export function sameTarget(a: PatchTarget | null, b: PatchTarget): boolean {
  return a !== null && a.collectionId === b.collectionId && a.itemId === b.itemId
}

export interface CoalescerOptions {
  delayMs?: number
  /** Injected so tests can drive time instead of waiting for it. */
  schedule?: (fn: () => void, ms: number) => unknown
  cancel?: (handle: unknown) => void
}

export class PatchCoalescer<P extends object> {
  private target: PatchTarget | null = null
  private patch: P | null = null
  private handle: unknown = null
  private inFlight: Promise<void> = Promise.resolve()

  private readonly delayMs: number
  private readonly schedule: (fn: () => void, ms: number) => unknown
  private readonly cancel: (handle: unknown) => void
  // Declared and assigned explicitly rather than as a TypeScript parameter
  // property: this repo's tests run under `node --test`, whose strip-only type
  // handling rejects parameter properties (ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX).
  private readonly flusher: PatchFlusher<P>

  constructor(flusher: PatchFlusher<P>, options: CoalescerOptions = {}) {
    this.flusher = flusher
    this.delayMs = options.delayMs ?? 120
    this.schedule = options.schedule ?? ((fn, ms) => setTimeout(fn, ms))
    this.cancel = options.cancel ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>))
  }

  get hasPending(): boolean {
    return this.patch !== null
  }

  /** The target of the currently pending patch, or null when nothing is queued. */
  get pendingTarget(): PatchTarget | null {
    return this.patch === null ? null : this.target
  }

  /**
   * The patch queued but not yet sent, or null.
   *
   * This exists for the in-flight overwrite hazard, which is the subtlest way
   * this design loses a keystroke. While a flush is on the wire the user keeps
   * typing, and those characters land here. When the flush returns, its result
   * describes the request as of when the call was MADE — so applying it
   * verbatim rewinds the input, discarding everything typed in the meantime.
   * The caller re-applies this patch on top of the authoritative result to undo
   * that rewind.
   */
  get pendingPatch(): P | null {
    return this.patch
  }

  /**
   * Queues a patch. Returns a promise that resolves once any flush this call
   * FORCED has completed — which is only when the target changed. Ordinary
   * queuing resolves immediately; the caller is not meant to await typing.
   */
  queue(target: PatchTarget, patch: P): Promise<void> {
    let forced: Promise<void> = Promise.resolve()
    if (this.patch !== null && !sameTarget(this.target, target)) {
      // Switching requests mid-window. Deliver what is queued to the request it
      // was typed into, before accepting anything for the new one.
      forced = this.flush()
    }
    this.target = target
    this.patch = this.patch === null ? patch : mergePatches(this.patch, patch)
    if (this.handle === null) {
      this.handle = this.schedule(() => {
        this.handle = null
        void this.flush()
      }, this.delayMs)
    }
    return forced
  }

  /**
   * Sends any pending patch immediately and resolves when it has been applied.
   *
   * Safe to call with nothing pending. Chains onto any flush already running so
   * two overlapping flushes cannot reorder and deliver an older patch last.
   */
  flush(): Promise<void> {
    if (this.handle !== null) {
      this.cancel(this.handle)
      this.handle = null
    }
    const patch = this.patch
    const target = this.target
    if (patch === null || target === null) return this.inFlight
    this.patch = null
    this.inFlight = this.inFlight.then(() => this.flusher(target, patch))
    return this.inFlight
  }
}
