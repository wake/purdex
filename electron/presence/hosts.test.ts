import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createHosts, type HostEntry, type HostState } from './hosts'

const TOKEN_A = 'tok-AAAA-secret-1111'
const TOKEN_B = 'tok-BBBB-secret-2222'
const V2 = ['push.v1', 'push.presence.v2']
const V1 = ['push.v1']

interface Call {
  url: string
  method: string
  headers: Record<string, string>
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
  const promise = new Promise<T>((a) => { resolve = a })
  return { promise, resolve }
}
/** A request that only ends when its signal aborts. */
const hangUntilAbort = (c: Call) =>
  new Promise<Res>((_, rej) => c.signal.addEventListener('abort', () => rej(new Error('aborted'))))

function makeEnv() {
  const logs: string[] = []
  const events: string[] = []
  const calls: Call[] = []
  const impl = { info: (async () => infoOk()) as (c: Call) => Promise<Res> }
  const hosts = createHosts<{ id: string }>(
    {
      fetch: (url, init) => {
        const c: Call = { url, method: init.method ?? 'GET', headers: init.headers, signal: init.signal }
        calls.push(c)
        return impl.info(c)
      },
      setTimeout: (cb, ms) => globalThis.setTimeout(cb, ms),
      clearTimeout: (h) => globalThis.clearTimeout(h as ReturnType<typeof setTimeout>),
      logger: { warn: (m) => logs.push(m) },
    },
    {
      newSlot: (e) => ({ id: e.hostId }),
      onDispose: (st: HostState<{ id: string }>) => events.push(`dispose:${st.hostId}`),
      onCapable: (st, reason) => events.push(`${reason}:${st.hostId}`),
      onIncapable: (st) => events.push(`incapable:${st.hostId}`),
      onListChanged: () => events.push('list-changed'),
      onListReady: () => events.push('list-ready'),
      onStopped: () => events.push('stopped'),
    },
  )
  return { hosts, logs, events, calls, impl }
}

const host = (id: string, token: string | null = TOKEN_A, port = 7860): HostEntry => ({
  hostId: id,
  baseUrl: `http://100.64.0.2:${port}`,
  token,
})
const flush = () => vi.advanceTimersByTimeAsync(0)
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms)

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { vi.useRealTimers() })

describe('hosts: capability probe', () => {
  it('reports the capability once, only for the hosts that announce push.presence.v2', async () => {
    const env = makeEnv()
    env.impl.info = async (c) => (c.url.includes(':7861') ? infoOk(V1) : infoOk())
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1'), host('h2', TOKEN_A, 7861)] })
    await flush()
    await advance(40_000)
    expect(env.events.filter((e) => e.startsWith('capability:'))).toEqual(['capability:h1'])
    expect(env.events).toContain('incapable:h2')
    expect(env.hosts.list().find((s) => s.hostId === 'h2')?.caps).toBe('no')
  })
  it('a host that does not list push.presence.v2 is never reported capable', async () => {
    const env = makeEnv()
    env.impl.info = async () => infoOk(V1)
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    await advance(60_000)
    expect(env.events.some((e) => e.startsWith('capability:') || e.startsWith('recovered:'))).toBe(false)
    expect(env.events).toContain('incapable:h1')
  })
  it('retries a failed capability probe after 20 s', async () => {
    const env = makeEnv()
    let n = 0
    env.impl.info = async () => { n++; if (n === 1) throw new Error('down'); return infoOk() }
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    expect(env.calls).toHaveLength(1)
    expect(env.events).not.toContain('capability:h1')
    await advance(19_999)
    expect(env.calls).toHaveLength(1)
    await advance(1)
    expect(env.calls).toHaveLength(2)
    expect(env.events).toContain('capability:h1')
  })
  it('probes with the Bearer token, a 5 s deadline, and no Authorization header for a null token', async () => {
    const env = makeEnv()
    env.impl.info = async (c) => hangUntilAbort(c)
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1'), host('h2', null, 7861)] })
    await flush()
    expect(env.calls[0].headers.Authorization).toBe(`Bearer ${TOKEN_A}`)
    expect(env.calls[1].headers.Authorization).toBeUndefined()
    await advance(5000)
    expect(env.calls[0].signal.aborted).toBe(true)
  })
  it('re-probes every 10 min; a lost capability is reported as incapable', async () => {
    const env = makeEnv()
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    expect(env.events).toContain('capability:h1')
    env.impl.info = async () => infoOk(V1)
    await advance(10 * 60_000)
    expect(env.calls).toHaveLength(2)
    expect(env.events).toContain('incapable:h1')
    await advance(10 * 60_000)
    expect(env.calls).toHaveLength(3)
  })
  it('a probe answer after a failure streak asks for one recovery send, once per streak', async () => {
    const env = makeEnv()
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    const st = env.hosts.list()[0]
    env.hosts.markFailed(st)
    await env.hosts.probe(st)
    expect(env.events.filter((e) => e === 'recovered:h1')).toHaveLength(1)
    env.hosts.markFailed(st)
    await env.hosts.probe(st)
    expect(env.events.filter((e) => e === 'recovered:h1')).toHaveLength(1)
    env.hosts.markRecovered(st)
    env.hosts.markFailed(st)
    await env.hosts.probe(st)
    expect(env.events.filter((e) => e === 'recovered:h1')).toHaveLength(2)
  })
  it('stop() ends everything', async () => {
    const env = makeEnv()
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    env.hosts.stop()
    const probes = env.calls.length
    await advance(11 * 60_000)
    expect(env.calls).toHaveLength(probes)
    expect(env.events).toContain('dispose:h1')
    expect(env.events.at(-1)).toBe('stopped')
    expect(env.hosts.list()).toEqual([])
  })
})

describe('hosts: host list revisions', () => {
  it('a lower rev is ignored, a higher rev replaces, an equal rev replaces', async () => {
    const env = makeEnv()
    env.hosts.start()
    expect(env.hosts.setHosts({ rev: 5, hosts: [host('A')] })).toBe('applied')
    await flush()
    expect(env.hosts.setHosts({ rev: 3, hosts: [host('B', TOKEN_A, 7861)] })).toBe('ignored')
    await flush()
    expect(env.calls.some((c) => c.url.includes(':7861'))).toBe(false)
    expect(env.hosts.setHosts({ rev: 5, hosts: [host('C', TOKEN_A, 7862)] })).toBe('applied')
    await flush()
    expect(env.calls.some((c) => c.url.includes(':7862'))).toBe(true)
    expect(env.events).toContain('dispose:A')
    expect(env.hosts.list().map((s) => s.hostId)).toEqual(['C'])
    expect(env.hosts.setHosts({ rev: 6, hosts: [host('D', TOKEN_A, 7863)] })).toBe('applied')
  })
  it('keeps host state when the same list is handed over again (no change, no re-probe)', async () => {
    const env = makeEnv()
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    const probes = env.calls.length
    const changes = env.events.filter((e) => e === 'list-changed').length
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    env.hosts.setHosts({ rev: 2, hosts: [host('h1')] })
    await flush()
    expect(env.calls).toHaveLength(probes)
    expect(env.events.filter((e) => e === 'list-changed')).toHaveLength(changes)
  })
  it('a membership or identity change is a list change; list-ready only fires while running', async () => {
    const env = makeEnv()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    expect(env.events).toEqual(['list-changed'])
    env.hosts.start()
    await flush()
    env.hosts.setHosts({ rev: 2, hosts: [host('h1'), host('h2', TOKEN_A, 7861)] })
    env.hosts.setHosts({ rev: 3, hosts: [host('h1', TOKEN_B), host('h2', TOKEN_A, 7861)] })
    env.hosts.setHosts({ rev: 4, hosts: [host('h2', TOKEN_A, 7861)] })
    expect(env.events.filter((e) => e === 'list-changed')).toHaveLength(4)
    expect(env.events.filter((e) => e === 'list-ready')).toHaveLength(3)
  })
  it('works before start() and probes at start', async () => {
    const env = makeEnv()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1')] })
    await flush()
    expect(env.calls).toHaveLength(0)
    env.hosts.start()
    await flush()
    expect(env.calls).toHaveLength(1)
    expect(env.events).toContain('capability:h1')
  })
})

describe('hosts: identity guard', () => {
  it('a probe answer for an old token is discarded after the token changed', async () => {
    const env = makeEnv()
    const pending: Array<ReturnType<typeof deferred<Res>>> = []
    env.impl.info = () => { const d = deferred<Res>(); pending.push(d); return d.promise }
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1', TOKEN_A)] })
    await flush()
    env.hosts.setHosts({ rev: 2, hosts: [host('h1', TOKEN_B)] })
    await flush()
    expect(pending).toHaveLength(2)
    pending[0].resolve(infoOk())
    await flush()
    expect(env.events.some((e) => e.startsWith('capability:'))).toBe(false)
    pending[1].resolve(infoOk())
    await flush()
    expect(env.events.filter((e) => e.startsWith('capability:'))).toEqual(['capability:h1'])
    expect(env.hosts.list()[0].token).toBe(TOKEN_B)
  })
  it('an endpoint change also discards the old answer', async () => {
    const env = makeEnv()
    const pending: Array<ReturnType<typeof deferred<Res>>> = []
    env.impl.info = () => { const d = deferred<Res>(); pending.push(d); return d.promise }
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1', TOKEN_A, 7860)] })
    await flush()
    env.hosts.setHosts({ rev: 2, hosts: [host('h1', TOKEN_A, 7999)] })
    await flush()
    pending[0].resolve(infoOk())
    await flush()
    expect(env.events.some((e) => e.startsWith('capability:'))).toBe(false)
  })
})

describe('hosts: tokens never reach the log', () => {
  const secretError = () => new Error(`connect failed with ${TOKEN_A} and ${TOKEN_B}`)
  const paths: Array<[string, (e: ReturnType<typeof makeEnv>) => void]> = [
    ['probe network error', (e) => { e.impl.info = async () => { throw secretError() } }],
    ['probe 500', (e) => { e.impl.info = async () => res(500, { error: TOKEN_A }) }],
    ['probe 401', (e) => { e.impl.info = async () => res(401, { error: TOKEN_A }) }],
    ['probe bad json', (e) => { e.impl.info = async () => ({ ...res(200), json: async () => { throw secretError() } }) }],
    ['probe wrong shape', (e) => { e.impl.info = async () => res(200, { capabilities: TOKEN_A }) }],
    ['probe timeout', (e) => { e.impl.info = (c) => hangUntilAbort(c) }],
  ]
  it.each(paths)('%s', async (_name, arrange) => {
    const env = makeEnv()
    arrange(env)
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host('h1', TOKEN_A)] })
    await flush()
    await advance(120_000)
    const all = env.logs.join('\n')
    expect(env.logs.length).toBeGreaterThan(0)
    expect(all).not.toContain(TOKEN_A)
    expect(all).not.toContain(TOKEN_B)
    expect(all).not.toContain('Bearer')
  })
  it('never logs a renderer-supplied hostId (a token-valued id, or one with newlines)', async () => {
    const env = makeEnv()
    env.impl.info = async () => res(500)
    env.hosts.start()
    env.hosts.setHosts({ rev: 1, hosts: [host(TOKEN_A, TOKEN_A), host('evil\nforged line\r\u0000', TOKEN_B)] })
    await flush()
    await advance(30_000)
    const all = env.logs.join('\n')
    expect(env.logs.length).toBe(2)
    expect(all).not.toContain(TOKEN_A)
    expect(all).not.toContain('forged')
    expect(all).not.toContain('\u0000')
    expect(all).toContain('host#0')
    expect(all).toContain('host#1')
  })
})
