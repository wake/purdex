import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEBOUNCE_MS, HEARTBEAT_MS, defaultReporterDeps, startPushPresence, type PresenceBody, type ReporterDeps } from './reporter'
import type { ActivityTracker } from './activity'

// PA-2b: v1 yields to the hosts the Mac App's main process reports for (plan D10).

const tracker: ActivityTracker = { isActive: () => true, lastInputAt: () => 0, subscribe: () => () => {}, dispose: () => {} }

function makeEnv(initialOwned?: string[]) {
  const puts: Array<{ hostId: string; body: PresenceBody }> = []
  let listener: ((ids: string[]) => void) | undefined
  let unsubscribed = 0
  const getOwned = vi.fn(async () => initialOwned ?? [])
  const deps: ReporterDeps = {
    tracker, clientId: () => 'c_aaaaaaaaaaaa:win1',
    visible: () => ({ h1: [{ code: 'c1', name: 'dev' }], h2: [{ code: 'c9', name: 'nine' }] }),
    put: async (hostId, body) => { puts.push({ hostId, body }); return 'ok' },
    supportsPush: async () => true,
    connectedHosts: () => [{ id: 'h1', identity: 'e1:t1' }, { id: 'h2', identity: 'e2:t2' }],
    subscribeChanges: () => () => {},
    owned: initialOwned === undefined ? undefined : {
      get: getOwned,
      subscribe: (fn) => { listener = fn; return () => { unsubscribed++; listener = undefined } },
    },
  }
  return { deps, puts, emit: (ids: string[]) => listener?.(ids), hasListener: () => listener !== undefined, unsubscribed: () => unsubscribed }
}

const settle = () => vi.advanceTimersByTimeAsync(0)
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms)
const forHost = (puts: Array<{ hostId: string; body: PresenceBody }>, id: string) => puts.filter((p) => p.hostId === id).map((p) => p.body)

let stop: (() => void) | undefined
beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { stop?.(); stop = undefined; vi.useRealTimers(); vi.restoreAllMocks(); delete (window as unknown as { electronAPI?: unknown }).electronAPI })

describe('v1 reporter and the owned set', () => {
  it('without the bridge reports to every push host as before', async () => {
    const e = makeEnv()
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    expect(e.puts.map((p) => p.hostId).sort()).toEqual(['h1', 'h2'])
    await tick(HEARTBEAT_MS)
    expect(forHost(e.puts, 'h1').every((b) => b.active)).toBe(true)
  })

  it('default deps: no owned wiring without the bridge or without those methods', () => {
    expect(defaultReporterDeps().owned).toBeUndefined()
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = { presence: { setHosts: async () => true } }
    expect(defaultReporterDeps().owned).toBeUndefined()
  })

  it('default deps: wires getOwned / onOwnedChanged from the bridge', async () => {
    const off = vi.fn()
    const onOwnedChanged = vi.fn(() => off)
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = { presence: { getOwned: async () => ['h1'], onOwnedChanged } }
    const o = defaultReporterDeps().owned!
    expect(await o.get()).toEqual(['h1'])
    const cb = vi.fn()
    expect(o.subscribe(cb)).toBe(off)
    expect(onOwnedChanged).toHaveBeenCalledWith(cb)
  })

  it('bridge but nothing owned: v1 continues for every host', async () => {
    const e = makeEnv([])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    expect(e.puts.map((p) => p.hostId).sort()).toEqual(['h1', 'h2'])
    expect(e.puts.every((p) => p.body.active)).toBe(true)
  })

  it('owned host: one final active:false, then silence; the other host keeps reporting', async () => {
    const e = makeEnv([])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    e.puts.length = 0
    e.emit(['h1'])
    await tick(DEBOUNCE_MS)
    expect(forHost(e.puts, 'h1')).toEqual([{ client_id: 'c_aaaaaaaaaaaa:win1', active: false, sessions: [], ttl_ms: 45000 }])
    e.puts.length = 0
    await tick(HEARTBEAT_MS * 3)
    expect(forHost(e.puts, 'h1')).toEqual([])
    expect(forHost(e.puts, 'h2').length).toBeGreaterThan(0)
  })

  it('a host owned from the start (initial read) is retracted once and never reported active', async () => {
    const e = makeEnv(['h1'])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    await tick(HEARTBEAT_MS * 2)
    expect(forHost(e.puts, 'h1').filter((b) => b.active)).toEqual([])
    expect(forHost(e.puts, 'h2').length).toBeGreaterThan(0)
  })

  it('owned then not owned: v1 resumes at once, not at the next heartbeat', async () => {
    const e = makeEnv([])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    e.emit(['h1'])
    await tick(DEBOUNCE_MS)
    e.puts.length = 0
    e.emit([])
    await settle()
    expect(forHost(e.puts, 'h1')).toHaveLength(1)
    expect(forHost(e.puts, 'h1')[0]).toMatchObject({ active: true, sessions: [{ code: 'c1', name: 'dev' }] })
  })

  it('ids of unknown hosts are ignored', async () => {
    const e = makeEnv([])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    e.puts.length = 0
    e.emit(['ghost', 'h-not-here'])
    await tick(DEBOUNCE_MS * 2)
    expect(e.puts).toEqual([])
    await tick(HEARTBEAT_MS)
    expect(forHost(e.puts, 'h1')[0]?.active).toBe(true)
    expect(forHost(e.puts, 'h2')[0]?.active).toBe(true)
  })

  it('a late initial read does not overwrite a newer event', async () => {
    const e = makeEnv([])
    let resolveGet!: (ids: string[]) => void
    e.deps.owned!.get = () => new Promise((r) => { resolveGet = r })
    stop = startPushPresence(e.deps)
    e.emit(['h1'])
    resolveGet([])
    await tick(DEBOUNCE_MS * 2)
    expect(forHost(e.puts, 'h1').filter((b) => b.active)).toEqual([])
  })

  it('a failing initial read behaves as without the bridge', async () => {
    const e = makeEnv([])
    e.deps.owned!.get = async () => { throw new Error('gone') }
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    expect(e.puts.map((p) => p.hostId).sort()).toEqual(['h1', 'h2'])
  })

  it('unsubscribes when the reporter stops and ignores later events', async () => {
    const e = makeEnv([])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS)
    stop()
    stop = undefined
    expect(e.unsubscribed()).toBe(1)
    expect(e.hasListener()).toBe(false)
  })
})

describe('v1 retracts once per ownership episode (PA-2b R1 / attack)', () => {
  const RETRACT = { client_id: 'c_aaaaaaaaaaaa:win1', active: false, sessions: [], ttl_ms: 45000 }

  it('owned from the start (reload): exactly one active:false once support is known, nothing after', async () => {
    const e = makeEnv(['h1'])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    await tick(HEARTBEAT_MS * 3)
    expect(forHost(e.puts, 'h1')).toEqual([RETRACT])
  })

  it('owned before the capability probe answers: retracted when the probe says push.v1', async () => {
    const e = makeEnv(['h1'])
    let answer!: (ok: boolean) => void
    e.deps.supportsPush = (id) => (id === 'h1' ? new Promise<boolean>((r) => { answer = r }) : Promise.resolve(true))
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    expect(forHost(e.puts, 'h1')).toEqual([])
    answer(true)
    await tick(DEBOUNCE_MS * 2)
    await tick(HEARTBEAT_MS * 2)
    expect(forHost(e.puts, 'h1')).toEqual([RETRACT])
  })

  it('a failed first retraction is retried by the tick path, then not repeated', async () => {
    const e = makeEnv(['h1'])
    let fails = 1
    const real = e.deps.put
    e.deps.put = async (hostId, body) => {
      if (hostId === 'h1' && fails-- > 0) throw new Error('net')
      return real(hostId, body)
    }
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    expect(forHost(e.puts, 'h1')).toEqual([])
    await tick(HEARTBEAT_MS)
    expect(forHost(e.puts, 'h1')).toEqual([RETRACT])
    await tick(HEARTBEAT_MS * 3)
    expect(forHost(e.puts, 'h1')).toEqual([RETRACT])
  })

  it('left and re-entered ownership retracts again', async () => {
    const e = makeEnv(['h1'])
    stop = startPushPresence(e.deps)
    await tick(DEBOUNCE_MS * 2)
    e.emit([])
    await tick(DEBOUNCE_MS * 2)
    e.emit(['h1'])
    await tick(DEBOUNCE_MS * 2)
    expect(forHost(e.puts, 'h1').map((b) => b.active)).toEqual([false, true, false])
  })
})
