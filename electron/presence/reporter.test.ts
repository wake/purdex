import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createReporter, type HostEntry, type SendReason } from './reporter'

const TOKEN_A = 'tok-AAAA-secret-1111'
const TOKEN_B = 'tok-BBBB-secret-2222'
const V2 = ['push.v1', 'push.presence.v2']
const V1 = ['push.v1']

interface Call {
  url: string
  method: string
  headers: Record<string, string>
  body: Record<string, unknown> | null
  signal: AbortSignal
}
interface Res {
  ok: boolean
  status: number
  json: () => Promise<unknown>
  body: null
}
const res = (status: number, json: unknown = {}): Res => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => json,
  body: null,
})
const infoOk = (caps: string[] = V2) => res(200, { capabilities: caps })

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b })
  return { promise, resolve, reject }
}
/** A request that only ends when its signal aborts. */
const hangUntilAbort = (c: Call) =>
  new Promise<Res>((_, rej) => c.signal.addEventListener('abort', () => rej(new Error('aborted'))))

function makeEnv() {
  const logs: string[] = []
  const ownedEvents: string[][] = []
  const state = { idleMs: 1000, locked: false, lockedMs: 0, sampledAt: Date.now(), minutes: 5, installId: 'INSTALL-1' as string | Error }
  const calls = { info: [] as Call[], put: [] as Call[] }
  const impl = {
    info: (async () => infoOk()) as (c: Call) => Promise<Res>,
    put: (async () => res(204)) as (c: Call) => Promise<Res>,
  }
  const fetchFn = (url: string, init: { method?: string; headers: Record<string, string>; body?: string; signal: AbortSignal }) => {
    const c: Call = {
      url,
      method: init.method ?? 'GET',
      headers: init.headers,
      body: init.body ? (JSON.parse(init.body) as Record<string, unknown>) : null,
      signal: init.signal,
    }
    if (url.endsWith('/api/info')) {
      calls.info.push(c)
      return impl.info(c)
    }
    calls.put.push(c)
    return impl.put(c)
  }
  const reporter = createReporter({
    fetch: fetchFn,
    now: () => Date.now(),
    setTimeout: (cb, ms) => globalThis.setTimeout(cb, ms),
    clearTimeout: (h) => globalThis.clearTimeout(h as ReturnType<typeof setTimeout>),
    setInterval: (cb, ms) => globalThis.setInterval(cb, ms),
    clearInterval: (h) => globalThis.clearInterval(h as ReturnType<typeof setInterval>),
    snapshot: (at) => ({
      idleMs: state.idleMs + (at - state.sampledAt),
      locked: state.locked,
      lockedMs: state.locked ? state.lockedMs + (at - state.sampledAt) : 0,
    }),
    getAwayMinutes: () => state.minutes,
    getInstallId: () => {
      if (state.installId instanceof Error) throw state.installId
      return state.installId
    },
    logger: { warn: (m) => logs.push(m) },
    onOwnedChanged: (ids) => ownedEvents.push(ids),
  })
  return { reporter, logs, ownedEvents, state, calls, impl }
}

const host = (id: string, token: string | null = TOKEN_A, port = 7860): HostEntry => ({
  hostId: id,
  baseUrl: `http://100.64.0.2:${port}`,
  token,
})
const flush = () => vi.advanceTimersByTimeAsync(0)
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms)

async function started(hosts: HostEntry[] = [host('h1')]) {
  const env = makeEnv()
  env.reporter.start()
  env.reporter.setHosts({ rev: 1, hosts })
  await flush()
  return env
}

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

describe('reporter: cadence and capability', () => {
  it('keeps sending every 20 s with no window involved, the first send at the moment the capability is seen', async () => {
    const e = await started()
    expect(e.calls.info).toHaveLength(1)
    expect(e.calls.put).toHaveLength(1)
    await advance(20_000)
    expect(e.calls.put).toHaveLength(2)
    await advance(20_000)
    expect(e.calls.put).toHaveLength(3)
  })
  it('sends to every v2 host and only to those', async () => {
    const env = makeEnv()
    env.impl.info = async (c) => (c.url.includes(':7861') ? infoOk(V1) : infoOk())
    env.reporter.start()
    env.reporter.setHosts({ rev: 1, hosts: [host('h1'), host('h2', TOKEN_A, 7861)] })
    await flush()
    await advance(40_000)
    expect(env.calls.put.every((c) => c.url.includes(':7860'))).toBe(true)
    expect(env.calls.put.length).toBeGreaterThanOrEqual(3)
  })
  it('a host that does not list push.presence.v2 gets no presence', async () => {
    const env = makeEnv()
    env.impl.info = async () => infoOk(V1)
    env.reporter.start()
    env.reporter.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    await advance(60_000)
    expect(env.calls.put).toHaveLength(0)
    expect(env.reporter.getOwned()).toEqual([])
  })
  it('re-probes every 10 min; a lost capability stops sends and leaves the owned set', async () => {
    const env = await started()
    expect(env.reporter.getOwned()).toEqual(['h1'])
    env.impl.info = async () => infoOk(V1)
    await advance(10 * 60_000)
    expect(env.calls.info).toHaveLength(2)
    const putsAfter = env.calls.put.length
    await advance(60_000)
    expect(env.calls.put).toHaveLength(putsAfter)
    expect(env.reporter.getOwned()).toEqual([])
  })
  it('re-probes right after a failed send', async () => {
    const e = await started()
    e.impl.put = async () => res(500)
    await advance(20_000)
    expect(e.calls.put.length).toBeGreaterThanOrEqual(2)
    expect(e.calls.info.length).toBeGreaterThanOrEqual(2)
    expect(e.reporter.getOwned()).toEqual([])
  })
  it('stop() ends everything', async () => {
    const e = await started()
    e.reporter.stop()
    const puts = e.calls.put.length
    const infos = e.calls.info.length
    await advance(11 * 60_000)
    expect(e.calls.put).toHaveLength(puts)
    expect(e.calls.info).toHaveLength(infos)
    expect(e.reporter.getOwned()).toEqual([])
  })
})

describe('reporter: host list revisions', () => {
  it('a lower rev is ignored, a higher rev replaces, an equal rev replaces', async () => {
    const env = makeEnv()
    env.reporter.start()
    expect(env.reporter.setHosts({ rev: 5, hosts: [host('A')] })).toBe('applied')
    await flush()
    expect(env.reporter.setHosts({ rev: 3, hosts: [host('B', TOKEN_A, 7861)] })).toBe('ignored')
    await flush()
    expect(env.calls.info.some((c) => c.url.includes(':7861'))).toBe(false)
    expect(env.reporter.setHosts({ rev: 5, hosts: [host('C', TOKEN_A, 7862)] })).toBe('applied')
    await flush()
    expect(env.calls.info.some((c) => c.url.includes(':7862'))).toBe(true)
    const putsToA = env.calls.put.filter((c) => c.url.includes(':7860')).length
    await advance(60_000)
    expect(env.calls.put.filter((c) => c.url.includes(':7860'))).toHaveLength(putsToA)
    expect(env.reporter.setHosts({ rev: 6, hosts: [host('D', TOKEN_A, 7863)] })).toBe('applied')
  })
})

describe('reporter: identity guard', () => {
  it('an in-flight completion for a removed host is ignored and never resent', async () => {
    const env = await started()
    const d = deferred<Res>()
    env.impl.put = () => d.promise
    await advance(20_000)
    expect(env.calls.put).toHaveLength(2)
    env.reporter.force('lock')
    env.reporter.setHosts({ rev: 2, hosts: [] })
    d.resolve(res(204))
    await flush()
    expect(env.calls.put).toHaveLength(2)
    expect(env.reporter.getOwned()).toEqual([])
    await advance(60_000)
    expect(env.calls.put).toHaveLength(2)
  })
  it('an in-flight send for the old token cannot make the changed host owned', async () => {
    const env = await started()
    const d = deferred<Res>()
    env.impl.put = () => d.promise
    await advance(20_000)
    env.reporter.setHosts({ rev: 2, hosts: [host('h1', TOKEN_B)] })
    env.impl.put = async () => res(500)
    d.resolve(res(204))
    await flush()
    expect(env.reporter.getOwned()).toEqual([])
  })
})

describe('reporter: forced sends', () => {
  it.each<SendReason>(['back', 'lock', 'unlock', 'resume', 'n'])('force(%s) sends at once', async (reason) => {
    const e = await started()
    const n = e.calls.put.length
    e.reporter.force(reason)
    await flush()
    expect(e.calls.put).toHaveLength(n + 1)
  })
  it('a host-list change forces a send to the hosts that stay', async () => {
    const e = await started()
    const n = e.calls.put.length
    e.reporter.setHosts({ rev: 2, hosts: [host('h1'), host('h2', TOKEN_A, 7861)] })
    await flush()
    const toH1 = e.calls.put.filter((c) => c.url.includes(':7860')).length
    expect(toH1).toBe(n + 1)
  })
  it('capability first seen forces that host', async () => {
    const env = makeEnv()
    env.reporter.start()
    env.reporter.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    expect(env.calls.put).toHaveLength(1)
  })
  it('the first success after a failure forces a send (recovery through the probe), once per failure streak', async () => {
    const e = await started()
    let failures = 1
    e.impl.put = async () => (failures-- > 0 ? res(500) : res(204))
    await advance(20_000)
    // tick send failed -> re-probe answered v2 while failed -> one forced send, which succeeded
    expect(e.calls.put).toHaveLength(3)
    expect(e.reporter.getOwned()).toEqual(['h1'])
  })
  it('a host that keeps failing is not hammered: one recovery send per streak, then the 20 s tick only', async () => {
    const e = await started()
    e.impl.put = async () => res(500)
    await advance(20_000)
    expect(e.calls.put).toHaveLength(3)
    await advance(20_000)
    expect(e.calls.put).toHaveLength(4)
  })
  it('coalesces reasons arriving while a send is in flight into one send of the latest state', async () => {
    const e = await started()
    const d = deferred<Res>()
    e.impl.put = () => d.promise
    e.reporter.force('lock')
    await flush()
    const n = e.calls.put.length
    e.reporter.force('unlock')
    e.reporter.force('back')
    e.reporter.force('resume')
    await flush()
    expect(e.calls.put).toHaveLength(n)
    e.state.idleMs = 777_000
    e.state.sampledAt = Date.now()
    e.impl.put = async () => res(204)
    d.resolve(res(204))
    await flush()
    expect(e.calls.put).toHaveLength(n + 1)
    expect(e.calls.put[n].body?.idle_ms).toBe(777_000)
  })
  it('a request that never settles is aborted at 10 s; with nothing forced pending there is no timeout resend, only the streak one recovery send', async () => {
    const e = await started()
    e.impl.put = () => new Promise<Res>(() => {}) // ignores abort entirely
    await advance(20_000)
    const n = e.calls.put.length
    expect(e.calls.put[n - 1].signal.aborted).toBe(false)
    await advance(9_999)
    expect(e.calls.put).toHaveLength(n)
    await advance(1)
    expect(e.calls.put[n - 1].signal.aborted).toBe(true)
    expect(e.reporter.getOwned()).toEqual([])
    // Only the one recovery send of the failure streak (through the re-probe), no timeout resend on top.
    expect(e.calls.put).toHaveLength(n + 1)
    await advance(9_000)
    expect(e.calls.put).toHaveLength(n + 1)
  })
  it('a forced reason that arrived during a timing-out request is sent once with the latest state, and that resend timing out chains nothing', async () => {
    const e = await started()
    e.impl.put = () => new Promise<Res>(() => {})
    e.reporter.force('lock')
    await flush()
    const n = e.calls.put.length
    e.reporter.force('unlock')
    e.state.idleMs = 555_000
    e.state.sampledAt = Date.now()
    await advance(10_000)
    expect(e.calls.put).toHaveLength(n + 1)
    expect(e.calls.put[n].body?.idle_ms).toBe(565_000)
    await advance(10_000) // the resend times out as well
    expect(e.calls.put.length).toBeLessThanOrEqual(n + 2) // at most the 20 s tick that fell in this window
  })
  it('a host that keeps hanging is hit at the 20 s cadence, not every 10 s', async () => {
    const e = await started()
    e.impl.put = () => new Promise<Res>(() => {})
    const before = e.calls.put.length
    await advance(120_000)
    expect(e.calls.put.length - before).toBeLessThanOrEqual(7) // 6 ticks + the streak's one recovery send
  })
})

describe('reporter: results', () => {
  it('a 4xx is logged at most once a minute per host, not retried before the next tick, and not owned', async () => {
    const e = await started()
    e.impl.put = async () => res(403)
    await advance(20_000)
    expect(e.reporter.getOwned()).toEqual([])
    const rejected = () => e.logs.filter((l) => l.includes('rejected')).length
    expect(rejected()).toBe(1)
    const n = e.calls.put.length
    await advance(20_000)
    await advance(20_000)
    expect(e.calls.put).toHaveLength(n + 2)
    expect(rejected()).toBe(1)
    await advance(20_000)
    expect(rejected()).toBe(2)
  })
  it('a force that coalesced behind a request that ends in 4xx is sent once, with the latest state', async () => {
    const e = await started()
    const d = deferred<Res>()
    e.impl.put = () => d.promise
    e.reporter.force('lock')
    await flush()
    const n = e.calls.put.length
    e.reporter.force('unlock')
    e.state.idleMs = 424_000
    e.state.sampledAt = Date.now()
    e.impl.put = async () => res(400)
    d.resolve(res(400))
    await flush()
    expect(e.calls.put).toHaveLength(n + 1)
    expect(e.calls.put[n].body?.idle_ms).toBe(424_000)
    await advance(5_000) // the follow-up's own 4xx does not retry
    expect(e.calls.put).toHaveLength(n + 1)
  })
  it('a 4xx with nothing forced pending is not retried before the next tick', async () => {
    const e = await started()
    const d = deferred<Res>()
    e.impl.put = () => d.promise
    e.reporter.force('lock')
    await flush()
    const n = e.calls.put.length
    d.resolve(res(400))
    await flush()
    expect(e.calls.put).toHaveLength(n)
  })
  it('owned enters on success, leaves on a failed send, comes back on recovery, and every change is broadcast', async () => {
    const e = await started()
    expect(e.ownedEvents).toEqual([['h1']])
    e.impl.put = async () => res(500)
    await advance(20_000)
    expect(e.ownedEvents).toEqual([['h1'], []])
    e.impl.put = async () => res(204)
    await advance(20_000)
    expect(e.ownedEvents).toEqual([['h1'], [], ['h1']])
  })
  it('owned is not broadcast again when nothing changed, and stays through 100 s of good sends (expiry is re-armed)', async () => {
    const e = await started()
    await advance(100_000)
    expect(e.ownedEvents).toEqual([['h1']])
    expect(e.reporter.getOwned()).toEqual(['h1'])
  })
  it('removal and an identity change leave the owned set', async () => {
    const e = await started([host('h1'), host('h2', TOKEN_A, 7861)])
    expect([...e.reporter.getOwned()].sort()).toEqual(['h1', 'h2'])
    e.reporter.setHosts({ rev: 2, hosts: [host('h1', TOKEN_B)] })
    expect(e.reporter.getOwned()).toEqual([])
    await flush()
    expect(e.reporter.getOwned()).toEqual(['h1'])
  })
  it('a 3xx is a failure, not a success', async () => {
    const e = await started()
    e.impl.put = async () => res(302)
    await advance(20_000)
    expect(e.reporter.getOwned()).toEqual([])
  })
})

describe('reporter: the body', () => {
  it('has exactly the v2 fields, client_id mac:<install id>, ttl 45 s, Bearer, JSON', async () => {
    const e = await started()
    const c = e.calls.put[0]
    expect(c.method).toBe('PUT')
    expect(c.url).toBe('http://100.64.0.2:7860/api/push/presence')
    expect(c.headers.Authorization).toBe(`Bearer ${TOKEN_A}`)
    expect(c.headers['Content-Type']).toBe('application/json')
    expect(c.body).toEqual({
      client_id: 'mac:INSTALL-1',
      active: true,
      sessions: [],
      ttl_ms: 45_000,
      v: 2,
      idle_ms: 1000,
      locked: false,
      locked_ms: 0,
      away_after_ms: 300_000,
    })
  })
  it('ages are current at send time', async () => {
    const e = await started()
    await advance(20_000)
    expect(e.calls.put[1].body?.idle_ms).toBe(21_000)
  })
  it('active follows this Mac own rule: idle below N, or locked under 60 s', async () => {
    const e = await started()
    e.state.minutes = 2
    e.state.idleMs = 119_000
    e.state.sampledAt = Date.now()
    e.reporter.force('n')
    await flush()
    expect(e.calls.put.at(-1)?.body).toMatchObject({ active: true, away_after_ms: 120_000 })
    e.state.idleMs = 120_000
    e.state.sampledAt = Date.now()
    e.reporter.force('n')
    await flush()
    expect(e.calls.put.at(-1)?.body).toMatchObject({ active: false })
    e.state.locked = true
    e.state.lockedMs = 59_000
    e.state.idleMs = 0
    e.state.sampledAt = Date.now()
    e.reporter.force('lock')
    await flush()
    expect(e.calls.put.at(-1)?.body).toMatchObject({ active: true, locked: true, locked_ms: 59_000 })
    e.state.lockedMs = 60_000
    e.state.sampledAt = Date.now()
    e.reporter.force('lock')
    await flush()
    expect(e.calls.put.at(-1)?.body).toMatchObject({ active: false, locked_ms: 60_000 })
  })
  it('stays inside the daemon ranges', async () => {
    const e = await started()
    e.state.idleMs = 9_999_999_999_999
    e.state.sampledAt = Date.now()
    e.reporter.force('back')
    await flush()
    expect(e.calls.put.at(-1)?.body?.idle_ms).toBe(2_592_000_000)
  })
  it('sends nothing, and logs once, when the install id cannot be produced', async () => {
    const env = makeEnv()
    env.state.installId = new Error(`disk full ${TOKEN_A}`)
    env.reporter.start()
    env.reporter.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    await advance(60_000)
    expect(env.calls.put).toHaveLength(0)
    expect(env.logs.length).toBe(1)
    expect(env.logs.join('\n')).not.toContain(TOKEN_A)
  })
})

describe('reporter: tokens never reach the log', () => {
  const secretError = () => new Error(`connect failed with ${TOKEN_A} and ${TOKEN_B}`)
  const paths: Array<[string, (e: ReturnType<typeof makeEnv>) => void]> = [
    ['send network error', (e) => { e.impl.put = async () => { throw secretError() } }],
    ['send 500', (e) => { e.impl.put = async () => res(500, { error: TOKEN_A }) }],
    ['send 403', (e) => { e.impl.put = async () => res(403, { error: TOKEN_A }) }],
    ['send timeout', (e) => { e.impl.put = (c) => hangUntilAbort(c) }],
  ]
  it.each(paths)('%s', async (_name, arrange) => {
    const env = makeEnv()
    arrange(env)
    env.reporter.start()
    env.reporter.setHosts({ rev: 1, hosts: [host('h1', TOKEN_A)] })
    await flush()
    await advance(120_000)
    const all = env.logs.join('\n')
    expect(env.logs.length).toBeGreaterThan(0)
    expect(all).not.toContain(TOKEN_A)
    expect(all).not.toContain(TOKEN_B)
    expect(all).not.toContain('Bearer')
  })
  it('never logs a renderer-supplied hostId', async () => {
    const env = makeEnv()
    env.impl.put = async () => res(500)
    env.reporter.start()
    env.reporter.setHosts({ rev: 1, hosts: [host(TOKEN_A, TOKEN_A), host('evil\nforged', TOKEN_B, 7861)] })
    await flush()
    await advance(30_000)
    const all = env.logs.join('\n')
    expect(env.logs.length).toBeGreaterThan(0)
    expect(all).not.toContain(TOKEN_A)
    expect(all).not.toContain('forged')
    expect(all).toMatch(/host#[01]/)
  })
})
