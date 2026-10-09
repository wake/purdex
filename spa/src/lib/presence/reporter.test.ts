import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEBOUNCE_MS, HEARTBEAT_MS, PRESENCE_TTL_MS, startPushPresence, type ConnectedHost, type PresenceBody, type ReporterDeps } from './reporter'
import type { ActivityTracker } from './activity'
import type { PresenceSession } from './visible-sessions'

// PU-4 Task 3: what the Mac window tells each push-capable daemon.

class FakeTracker implements ActivityTracker {
  active = true
  subs = new Set<(a: boolean) => void>()
  disposed = false
  isActive() { return this.active }
  lastInputAt() { return 0 }
  subscribe(fn: (a: boolean) => void) { this.subs.add(fn); return () => { this.subs.delete(fn) } }
  dispose() { this.disposed = true }
  set(active: boolean) { this.active = active; for (const fn of [...this.subs]) fn(active) }
}

interface Env {
  deps: ReporterDeps
  tracker: FakeTracker
  puts: Array<{ hostId: string; body: PresenceBody }>
  shown: Record<string, PresenceSession[]>
  hosts: ConnectedHost[]
  capable: Set<string>
  change: () => void
  failPuts: { on: boolean }
  probes: string[]
}

function makeEnv(): Env {
  const tracker = new FakeTracker()
  const puts: Env['puts'] = []
  const changeFns = new Set<() => void>()
  const failPuts = { on: false }
  const probes: string[] = []
  const env: Env = {
    tracker, puts, shown: { h1: [{ code: 'c1', name: 'dev' }], h2: [{ code: 'c9', name: 'nine' }] },
    hosts: [{ id: 'h1', identity: 'e1:t1' }, { id: 'h2', identity: 'e2:t2' }], capable: new Set(['h1', 'h2']),
    change: () => changeFns.forEach((f) => f()), failPuts, probes,
    deps: undefined as never,
  }
  env.deps = {
    tracker, clientId: () => 'c_aaaaaaaaaaaa:win1',
    visible: () => env.shown,
    put: async (hostId, body) => { puts.push({ hostId, body }); if (failPuts.on) throw new Error('down') },
    supportsPush: async (hostId) => { probes.push(hostId); return env.capable.has(hostId) },
    connectedHosts: () => env.hosts,
    subscribeChanges: (fn) => { changeFns.add(fn); return () => { changeFns.delete(fn) } },
  }
  return env
}

const settle = () => vi.advanceTimersByTimeAsync(0)
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms)

let stop: () => void
beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { stop?.(); vi.useRealTimers() })

describe('push presence reporter', () => {
  it('reports to every push-capable host after the debounce, each with only its own sessions', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await settle()
    expect(e.puts).toHaveLength(0) // not before the debounce
    await tick(DEBOUNCE_MS)
    expect(e.puts.map((p) => p.hostId).sort()).toEqual(['h1', 'h2'])
    const h1 = e.puts.find((p) => p.hostId === 'h1')!.body
    expect(h1).toEqual({ client_id: 'c_aaaaaaaaaaaa:win1', active: true, sessions: [{ code: 'c1', name: 'dev' }], ttl_ms: PRESENCE_TTL_MS })
    expect(e.puts.find((p) => p.hostId === 'h2')!.body.sessions).toEqual([{ code: 'c9', name: 'nine' }])
  })

  it('asks nothing of, and sends nothing to, a host without push.v1', async () => {
    const e = makeEnv()
    e.capable.delete('h2')
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts.map((p) => p.hostId)).toEqual(['h1'])
    await tick(HEARTBEAT_MS * 3)
    expect(e.puts.every((p) => p.hostId === 'h1')).toBe(true)
  })

  it('a host that is not connected is not asked and gets nothing', async () => {
    const e = makeEnv()
    e.hosts = [{ id: 'h1', identity: 'e1:t1' }]
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    expect(e.probes).toEqual(['h1'])
    expect(e.puts.map((p) => p.hostId)).toEqual(['h1'])
  })

  it('sends again on a change (a tab switch changes what is shown), debounced into one', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.puts.length = 0
    e.shown = { h1: [{ code: 'c2', name: 'ops' }], h2: [{ code: 'c9', name: 'nine' }] }
    e.change(); e.change(); e.change()
    await tick(DEBOUNCE_MS - 1)
    expect(e.puts).toHaveLength(0)
    await tick(1)
    expect(e.puts.map((p) => p.hostId)).toEqual(['h1']) // h2's list did not change: nothing for it
    expect(e.puts[0].body.sessions).toEqual([{ code: 'c2', name: 'ops' }])
  })

  it('does not repeat itself when nothing changed, but heartbeats every 20 s while active', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.puts.length = 0
    e.change()
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts).toHaveLength(0)
    await tick(HEARTBEAT_MS)
    expect(e.puts.map((p) => p.hostId).sort()).toEqual(['h1', 'h2'])
    await tick(HEARTBEAT_MS)
    expect(e.puts).toHaveLength(4)
  })

  it('sends active:false once when the user stops being there, then stays quiet', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.puts.length = 0
    e.tracker.set(false)
    await tick(DEBOUNCE_MS)
    expect(e.puts.map((p) => [p.hostId, p.body.active, p.body.sessions.length]).sort()).toEqual([['h1', false, 0], ['h2', false, 0]])
    e.puts.length = 0
    await tick(HEARTBEAT_MS * 3)
    e.change()
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts).toHaveLength(0)
  })

  it('resumes with active:true and the sessions when the user comes back', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.tracker.set(false)
    await tick(DEBOUNCE_MS)
    e.puts.length = 0
    e.tracker.set(true)
    await tick(DEBOUNCE_MS)
    expect(e.puts.map((p) => p.body.active)).toEqual([true, true])
    expect(e.puts.find((p) => p.hostId === 'h1')!.body.sessions).toEqual([{ code: 'c1', name: 'dev' }])
  })

  it('a window that was never active reports nothing at all (no stray active:false)', async () => {
    const e = makeEnv()
    e.tracker.active = false
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2 + HEARTBEAT_MS * 2)
    expect(e.puts).toHaveLength(0)
  })

  it('a failed PUT is ignored and retried by the next tick', async () => {
    const e = makeEnv()
    e.failPuts.on = true
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts.length).toBeGreaterThan(0)
    e.puts.length = 0
    e.failPuts.on = false
    await tick(HEARTBEAT_MS)
    expect(e.puts.map((p) => p.hostId).sort()).toEqual(['h1', 'h2'])
    await tick(DEBOUNCE_MS)
    e.change()
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts).toHaveLength(2) // now recorded as sent: a change with the same content sends nothing more
  })

  it('a failed active:false is retried', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.puts.length = 0
    e.failPuts.on = true
    e.tracker.set(false)
    await tick(DEBOUNCE_MS)
    expect(e.puts).toHaveLength(2)
    e.failPuts.on = false
    e.puts.length = 0
    await tick(HEARTBEAT_MS)
    expect(e.puts.map((p) => p.body.active)).toEqual([false, false])
  })

  it('a host re-pointed to another daemon is asked again and reported to afresh', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.puts.length = 0
    e.probes.length = 0
    e.hosts = [{ id: 'h1', identity: 'e1b:t1' }, { id: 'h2', identity: 'e2:t2' }]
    e.change()
    await tick(DEBOUNCE_MS * 2)
    expect(e.probes).toEqual(['h1'])
    expect(e.puts.map((p) => p.hostId)).toEqual(['h1']) // the new daemon knows nothing of this window yet
  })

  it('a host that was removed and comes back is asked again', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.hosts = [{ id: 'h2', identity: 'e2:t2' }]
    e.change()
    await tick(DEBOUNCE_MS)
    e.probes.length = 0
    e.hosts = [{ id: 'h1', identity: 'e1:t1' }, { id: 'h2', identity: 'e2:t2' }]
    e.change()
    await tick(DEBOUNCE_MS * 2)
    expect(e.probes).toEqual(['h1'])
  })

  it('a probe that fails leaves the host silent, and the next connect asks again', async () => {
    const e = makeEnv()
    let fail = true
    e.deps.supportsPush = async (hostId) => { e.probes.push(hostId); if (fail) throw new Error('down'); return true }
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts).toHaveLength(0)
    fail = false
    e.hosts = e.hosts.map((h) => ({ ...h, identity: h.identity + 'x' })) // reconnect under a new identity
    e.change()
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts.length).toBeGreaterThan(0)
  })

  it('stop() ends the timers, the subscriptions and the tracker', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    stop()
    e.puts.length = 0
    e.change()
    e.tracker.set(false)
    await tick(HEARTBEAT_MS * 3)
    expect(e.puts).toHaveLength(0)
    expect(e.tracker.disposed).toBe(true)
    expect(vi.getTimerCount()).toBe(0) // no heartbeat interval or debounce left running
  })
})
