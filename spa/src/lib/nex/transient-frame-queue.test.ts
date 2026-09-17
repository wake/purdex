// spa/src/lib/nex/transient-frame-queue.test.ts
import { describe, it, expect, vi, afterEach } from 'vitest'
import { createTransientFrameQueue, type TransientFrame } from './transient-frame-queue'

/** A manual scheduler: `fire()` runs every callback scheduled so far, in order. */
function manualScheduler() {
  const scheduled: { id: number; cb: () => void }[] = []
  const cancelled: number[] = []
  let next = 1
  return {
    scheduled,
    cancelled,
    schedule: vi.fn((cb: () => void) => { const id = next++; scheduled.push({ id, cb }); return id }),
    cancel: vi.fn((h: unknown) => { cancelled.push(h as number) }),
    fire() { const batch = scheduled.splice(0); for (const s of batch) s.cb() },
  }
}

function make() {
  const sched = manualScheduler()
  const flush = vi.fn<(batch: TransientFrame[]) => void>()
  const q = createTransientFrameQueue({ flush, schedule: sched.schedule, cancel: sched.cancel })
  return { q, flush, sched }
}

describe('createTransientFrameQueue', () => {
  afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

  it('coalesces N enqueues into one scheduled flush carrying all N in order', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 1 })
    q.enqueue('stream_event', { n: 2 })
    q.enqueue('stream_snapshot', { n: 3 })
    expect(sched.schedule).toHaveBeenCalledTimes(1)
    expect(flush).not.toHaveBeenCalled()
    sched.fire()
    expect(flush).toHaveBeenCalledTimes(1)
    expect(flush).toHaveBeenCalledWith([
      { kind: 'stream_event', payload: { n: 1 } },
      { kind: 'stream_event', payload: { n: 2 } },
      { kind: 'stream_snapshot', payload: { n: 3 } },
    ])
  })

  it('a flush with nothing queued is skipped, and a new enqueue after a flush schedules again', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 1 })
    sched.fire()
    expect(flush).toHaveBeenCalledTimes(1)
    q.enqueue('stream_event', { n: 2 })
    expect(sched.schedule).toHaveBeenCalledTimes(2)
    sched.fire()
    expect(flush).toHaveBeenCalledTimes(2)
    expect(flush).toHaveBeenLastCalledWith([{ kind: 'stream_event', payload: { n: 2 } }])
  })

  it('flushNow writes the batch synchronously; the already-scheduled flush then writes nothing', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 1 })
    q.flushNow()
    expect(flush).toHaveBeenCalledTimes(1)
    expect(flush).toHaveBeenCalledWith([{ kind: 'stream_event', payload: { n: 1 } }])
    sched.fire()
    expect(flush).toHaveBeenCalledTimes(1)
  })

  it('flushNow with an empty queue writes nothing', () => {
    const { q, flush } = make()
    q.flushNow()
    expect(flush).not.toHaveBeenCalled()
  })

  it('bumpGeneration drops the queue and cancels the scheduled flush', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 1 })
    q.bumpGeneration()
    expect(sched.cancel).toHaveBeenCalledWith(1)
    sched.fire()
    expect(flush).not.toHaveBeenCalled()
    q.flushNow()
    expect(flush).not.toHaveBeenCalled()
  })

  it('a scheduled flush captured under an older generation writes nothing even if the scheduler still fires it', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 'old' })
    const stale = sched.scheduled[0].cb
    q.bumpGeneration()
    // The new connection queues its own frames under the new generation.
    q.enqueue('stream_snapshot', { n: 'new' })
    stale()
    expect(flush).not.toHaveBeenCalled()
    sched.fire()
    expect(flush).toHaveBeenCalledTimes(1)
    expect(flush).toHaveBeenCalledWith([{ kind: 'stream_snapshot', payload: { n: 'new' } }])
  })

  it('drop clears the queue and cancels the scheduled flush without changing the generation', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 1 })
    q.drop()
    expect(sched.cancel).toHaveBeenCalledWith(1)
    sched.fire()
    expect(flush).not.toHaveBeenCalled()
    q.enqueue('stream_event', { n: 2 })
    sched.fire()
    expect(flush).toHaveBeenCalledWith([{ kind: 'stream_event', payload: { n: 2 } }])
  })

  it('close drops the queue and nothing flushes afterwards: not the scheduled flush, not flushNow, not a later enqueue', () => {
    const { q, flush, sched } = make()
    q.enqueue('stream_event', { n: 1 })
    const scheduledBeforeClose = sched.scheduled[0].cb
    q.close()
    expect(sched.cancel).toHaveBeenCalledWith(1)
    scheduledBeforeClose()
    q.flushNow()
    q.enqueue('stream_event', { n: 2 })
    sched.fire()
    q.flushNow()
    expect(flush).not.toHaveBeenCalled()
    expect(sched.schedule).toHaveBeenCalledTimes(1)
  })

  it('default scheduler uses requestAnimationFrame / cancelAnimationFrame when available', () => {
    let rafCb: FrameRequestCallback | null = null
    const raf = vi.fn((cb: FrameRequestCallback) => { rafCb = cb; return 42 })
    const caf = vi.fn()
    vi.stubGlobal('requestAnimationFrame', raf)
    vi.stubGlobal('cancelAnimationFrame', caf)
    const flush = vi.fn<(batch: TransientFrame[]) => void>()
    const q = createTransientFrameQueue({ flush })
    q.enqueue('stream_event', { n: 1 })
    expect(raf).toHaveBeenCalledTimes(1)
    rafCb!(0)
    expect(flush).toHaveBeenCalledWith([{ kind: 'stream_event', payload: { n: 1 } }])
    q.enqueue('stream_event', { n: 2 })
    q.drop()
    expect(caf).toHaveBeenCalledWith(42)
  })

  it('default scheduler falls back to setTimeout(16) / clearTimeout without requestAnimationFrame', () => {
    vi.stubGlobal('requestAnimationFrame', undefined)
    vi.stubGlobal('cancelAnimationFrame', undefined)
    vi.useFakeTimers()
    const flush = vi.fn<(batch: TransientFrame[]) => void>()
    const q = createTransientFrameQueue({ flush })
    q.enqueue('stream_event', { n: 1 })
    vi.advanceTimersByTime(15)
    expect(flush).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(flush).toHaveBeenCalledWith([{ kind: 'stream_event', payload: { n: 1 } }])
    q.enqueue('stream_event', { n: 2 })
    q.close()
    vi.advanceTimersByTime(100)
    expect(flush).toHaveBeenCalledTimes(1)
  })
})
