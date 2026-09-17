// spa/src/lib/nex/transient-frame-queue.ts — the per-connection queue of
// transient SSE frames (spec §4.3): frames are coalesced and flushed at most
// once per animation frame, and the queue is bound to a connection
// generation so a flush scheduled under the old connection can never write
// after the transport has reconnected (it would duplicate text the new
// connection's snapshot already carries, or resurrect a partial that a
// replayed `result` cleared). No React, no store: the hook injects `flush`.

export interface TransientFrame {
  kind: string
  payload: Record<string, unknown>
}

export interface TransientFrameQueueOptions {
  /** Receives every non-empty batch, in enqueue order. */
  flush: (batch: TransientFrame[]) => void
  /** Defaults to requestAnimationFrame when available, else setTimeout(…, 16). */
  schedule?: (cb: () => void) => unknown
  cancel?: (handle: unknown) => void
}

export interface TransientFrameQueue {
  enqueue: (kind: string, payload: Record<string, unknown>) => void
  /** Write whatever is queued right now (a durable frame must land after the deltas before it). */
  flushNow: () => void
  /** Connection boundary: drop the queue, cancel the scheduled flush and invalidate it. */
  bumpGeneration: () => void
  /** Drop the queue and cancel the scheduled flush, keeping the generation. */
  drop: () => void
  /** Terminal: drop everything; nothing is ever flushed or queued again. */
  close: () => void
}

type DefaultHandle = { raf: true; h: number } | { raf: false; h: ReturnType<typeof setTimeout> }

// The choice is made per schedule and carried on the handle, so the matching
// cancel never depends on which globals exist at cancel time.
function defaultSchedule(cb: () => void): DefaultHandle {
  return typeof requestAnimationFrame === 'function'
    ? { raf: true, h: requestAnimationFrame(cb) }
    : { raf: false, h: setTimeout(cb, 16) }
}

function defaultCancel(handle: unknown): void {
  const d = handle as DefaultHandle
  if (d.raf) cancelAnimationFrame(d.h)
  else clearTimeout(d.h)
}

export function createTransientFrameQueue(opts: TransientFrameQueueOptions): TransientFrameQueue {
  const { flush } = opts
  let pending: TransientFrame[] = []
  let generation = 0
  let handle: unknown = null
  const schedule = opts.schedule ?? defaultSchedule
  const cancel = opts.cancel ?? (opts.schedule ? () => {} : defaultCancel)
  let closed = false

  const run = (gen: number) => {
    if (closed || gen !== generation) return
    const batch = pending
    pending = []
    handle = null
    if (batch.length) flush(batch)
  }
  const cancelScheduled = () => {
    if (handle === null) return
    cancel(handle)
    handle = null
  }
  const dropAll = () => {
    pending = []
    cancelScheduled()
  }

  return {
    enqueue: (kind, payload) => {
      if (closed) return
      pending.push({ kind, payload })
      if (handle !== null) return
      const gen = generation
      handle = schedule(() => run(gen))
    },
    flushNow: () => run(generation),
    bumpGeneration: () => { generation += 1; dropAll() },
    drop: dropAll,
    close: () => { closed = true; dropAll() },
  }
}
