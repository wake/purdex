import { describe, expect, it, vi } from 'vitest'
import { createSampler, type ForceReason, type PowerMonitorLike } from './sampler'

type IdleState = 'active' | 'idle' | 'locked' | 'unknown'

function setup(opts: { idleSeconds?: number; state?: IdleState } = {}) {
  const t = { mono: 1000, wall: 1_700_000_000_000 }
  const pm = {
    idle: opts.idleSeconds ?? 0,
    state: (opts.state ?? 'active') as IdleState,
    listeners: new Map<string, Set<() => void>>(),
    stateCalls: [] as number[],
    order: [] as string[],
    onStateRead: null as null | (() => void),
    getSystemIdleTime(): number { return this.idle },
    getSystemIdleState(th: number): IdleState {
      this.stateCalls.push(th)
      this.order.push('read')
      const v = this.state
      this.onStateRead?.()
      return v
    },
    on(ev: string, cb: () => void) {
      this.order.push('on:' + ev)
      if (!this.listeners.has(ev)) this.listeners.set(ev, new Set())
      this.listeners.get(ev)!.add(cb)
    },
    removeListener(ev: string, cb: () => void) { this.listeners.get(ev)?.delete(cb) },
    emit(ev: string) { for (const cb of [...(this.listeners.get(ev) ?? [])]) cb() },
    count(ev: string) { return this.listeners.get(ev)?.size ?? 0 },
  }
  const forced: ForceReason[] = []
  const timers: Array<{ cb: () => void; ms: number; id: number }> = []
  let nextId = 1
  const sampler = createSampler({
    powerMonitor: pm as unknown as PowerMonitorLike,
    now: () => t.mono,
    setInterval: (cb, ms) => { const id = nextId++; timers.push({ cb, ms, id }); return id },
    clearInterval: (id) => { const i = timers.findIndex((x) => x.id === id); if (i >= 0) timers.splice(i, 1) },
    onForce: (r) => forced.push(r),
  })
  const tick = (ms: number) => { t.mono += ms; t.wall += ms; for (const x of [...timers]) x.cb() }
  return { t, pm, sampler, forced, timers, tick }
}

describe('sampler: sampling', () => {
  it('subscribes to the four events before reading the current state', () => {
    const h = setup()
    h.sampler.start()
    expect(h.pm.order.slice(0, 4).sort()).toEqual(['on:lock-screen', 'on:resume', 'on:suspend', 'on:unlock-screen'])
    expect(h.pm.order[4]).toBe('read')
  })
  it('samples idle time every 5 s', () => {
    const h = setup({ idleSeconds: 3 })
    h.sampler.start()
    expect(h.timers.map((x) => x.ms)).toEqual([5000])
    expect(h.sampler.snapshot().idleMs).toBe(3000)
    h.pm.idle = 8
    h.tick(5000)
    expect(h.sampler.snapshot().idleMs).toBe(8000)
  })
  it('snapshot ages the idle sample to the requested time', () => {
    const h = setup({ idleSeconds: 10 })
    h.sampler.start()
    h.t.mono += 2500
    expect(h.sampler.snapshot().idleMs).toBe(12500)
    expect(h.sampler.snapshot(h.t.mono + 1000).idleMs).toBe(13500)
  })
  it('stop removes the timer and the listeners; start twice does not double up', () => {
    const h = setup()
    h.sampler.start()
    h.sampler.start()
    expect(h.timers).toHaveLength(1)
    expect(h.pm.count('lock-screen')).toBe(1)
    h.sampler.stop()
    expect(h.timers).toHaveLength(0)
    for (const ev of ['lock-screen', 'unlock-screen', 'suspend', 'resume']) expect(h.pm.count(ev)).toBe(0)
  })
  it('a throwing powerMonitor keeps the last sample and does not throw out of the timer', () => {
    const h = setup({ idleSeconds: 4 })
    h.sampler.start()
    h.pm.getSystemIdleTime = () => { throw new Error('boom') }
    expect(() => h.tick(5000)).not.toThrow()
    expect(h.sampler.snapshot().idleMs).toBe(4000 + 5000)
  })
})

describe('sampler: back', () => {
  it('emits back when the sampled idle drops by 5 s or more', () => {
    const h = setup({ idleSeconds: 120 })
    h.sampler.start()
    h.pm.idle = 1
    h.tick(5000)
    expect(h.forced).toEqual(['back'])
  })
  it('does not emit for a drop under 5 s, for growth, or for the same value', () => {
    const h = setup({ idleSeconds: 20 })
    h.sampler.start()
    h.pm.idle = 16
    h.tick(5000)
    h.pm.idle = 30
    h.tick(5000)
    h.pm.idle = 30
    h.tick(5000)
    expect(h.forced).toEqual([])
  })
  it('emits exactly at a 5 s drop', () => {
    const h = setup({ idleSeconds: 10 })
    h.sampler.start()
    h.pm.idle = 5
    h.tick(5000)
    expect(h.forced).toEqual(['back'])
  })
})

describe('sampler: lock', () => {
  it('lock starts the monotonic lock age and forces; unlock clears it and forces', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('lock-screen')
    expect(h.forced).toEqual(['lock'])
    h.t.mono += 30_000
    expect(h.sampler.snapshot()).toMatchObject({ locked: true, lockedMs: 30_000 })
    h.pm.emit('unlock-screen')
    expect(h.forced).toEqual(['lock', 'unlock'])
    expect(h.sampler.snapshot()).toMatchObject({ locked: false, lockedMs: 0 })
  })
  it('lock age follows the monotonic clock, not wall-clock jumps', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('lock-screen')
    h.t.mono += 10_000
    h.t.wall += 3_600_000
    expect(h.sampler.snapshot().lockedMs).toBe(10_000)
    h.t.wall -= 7_200_000
    expect(h.sampler.snapshot().lockedMs).toBe(10_000)
  })
  it('snapshot ages the lock to the requested time', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('lock-screen')
    expect(h.sampler.snapshot(h.t.mono + 4000).lockedMs).toBe(4000)
  })
  it('starting already locked reports locked_ms 60000 (age unknown)', () => {
    const h = setup({ state: 'locked' })
    h.sampler.start()
    expect(h.pm.stateCalls).toEqual([60])
    expect(h.sampler.snapshot()).toMatchObject({ locked: true, lockedMs: 60_000 })
    expect(h.forced).toEqual([])
  })
  it('a duplicate lock event does not restart the age', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('lock-screen')
    h.t.mono += 5000
    h.pm.emit('lock-screen')
    expect(h.sampler.snapshot().lockedMs).toBe(5000)
  })
  it('"unknown", "idle" and "active" reads are not locked', () => {
    for (const state of ['unknown', 'idle', 'active'] as const) {
      const h = setup({ state })
      h.sampler.start()
      expect(h.sampler.snapshot().locked).toBe(false)
    }
  })
})

describe('sampler: suspend / resume', () => {
  it('resume forces and reconciles: still locked after resume reports 60000 (the monotonic clock may not count sleep)', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('lock-screen')
    h.t.mono += 10_000
    h.pm.emit('suspend')
    h.pm.state = 'locked'
    h.pm.emit('resume')
    expect(h.forced).toEqual(['lock', 'resume'])
    expect(h.pm.stateCalls.at(-1)).toBe(60)
    expect(h.sampler.snapshot()).toMatchObject({ locked: true, lockedMs: 60_000 })
  })
  it('resume to an unlocked screen clears a lock we believed in', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('lock-screen')
    h.pm.state = 'active'
    h.pm.emit('resume')
    expect(h.sampler.snapshot()).toMatchObject({ locked: false, lockedMs: 0 })
  })
  it('resume to a locked screen we did not know about becomes locked 60000', () => {
    const h = setup()
    h.sampler.start()
    h.pm.state = 'locked'
    h.pm.emit('resume')
    expect(h.sampler.snapshot()).toMatchObject({ locked: true, lockedMs: 60_000 })
  })
  it('resume takes a fresh idle sample and does not also emit back', () => {
    const h = setup({ idleSeconds: 300 })
    h.sampler.start()
    h.pm.idle = 0
    h.pm.emit('resume')
    expect(h.forced).toEqual(['resume'])
    expect(h.sampler.snapshot().idleMs).toBe(0)
  })
  it('suspend alone does not force', () => {
    const h = setup()
    h.sampler.start()
    h.pm.emit('suspend')
    expect(h.forced).toEqual([])
  })
})

describe('sampler: revisions', () => {
  it('an event that arrives while the resume read is in flight wins over the older read', () => {
    const h = setup()
    h.sampler.start()
    h.pm.state = 'locked'
    h.pm.onStateRead = () => { h.pm.emit('unlock-screen') }
    h.pm.emit('resume')
    expect(h.sampler.snapshot().locked).toBe(false)
  })
  it('a lock event during the initial read is kept over a stale "active" read', () => {
    const h = setup()
    h.pm.onStateRead = () => { h.pm.emit('lock-screen') }
    h.sampler.start()
    expect(h.sampler.snapshot().locked).toBe(true)
    expect(h.sampler.snapshot().lockedMs).toBe(0)
  })
})

describe('sampler: callbacks', () => {
  it('a throwing onForce does not break the sampler', () => {
    const fn = vi.fn(() => { throw new Error('x') })
    const h = setup()
    const s = createSampler({
      powerMonitor: h.pm as unknown as PowerMonitorLike,
      now: () => h.t.mono,
      setInterval: () => 1,
      clearInterval: () => {},
      onForce: fn,
    })
    s.start()
    expect(() => h.pm.emit('lock-screen')).not.toThrow()
    expect(fn).toHaveBeenCalled()
    expect(s.snapshot().locked).toBe(true)
  })
})
