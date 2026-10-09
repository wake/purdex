import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { hostFetch } from './host-api'
import {
  createPairingSession,
  mintAndPackage,
  PAIRING_POLL_MS,
  PAIRING_TTL_MS,
  type PairingInput,
  type PairingState,
} from './pairing'

vi.mock('./host-api', () => ({ hostFetch: vi.fn() }))

const T0 = 1_700_000_000_000
const FAKE_TOKENS = ['pdxd_fake_mac', 'pdxd_fake_air', 'pdxd_fake_relay', 'host-secret-token']

interface Call {
  hostId: string
  method: string
  path: string
  body: Record<string, unknown> | null
}

let calls: Call[]
type Responder = (c: Call) => Response | Promise<Response | undefined> | undefined
let responders: Responder[]

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}
const empty = (status: number) => new Response(null, { status })

/** The default daemon: every route answers its happy path; a test prepends a responder to change one. */
function defaultResponse(c: Call): Response {
  if (c.method === 'POST' && c.path === '/api/devices') {
    const b = c.body!
    return json(
      {
        id: `d_${c.hostId}`,
        token: `pdxd_fake_${c.hostId}`,
        pairing_id: b.pairing_id,
        profile_id: b.profile_id ?? '',
        label: b.label,
        created_at: Date.now(),
        use_by: Date.now() + 1_200_000,
      },
      201,
    )
  }
  if (c.method === 'POST' && c.path === '/api/host-transfer/pairings') return json({ code: 'ABCD2345', expiresAt: Date.now() + 600_000 })
  if (c.method === 'GET' && c.path.startsWith('/api/host-transfer/pairings/')) return json({ claimed: false, expiresAt: Date.now() + 1 })
  return empty(204)
}

let sot: string
let air: string
let relay: string
let input: PairingInput
let consoleSpies: ReturnType<typeof vi.spyOn>[]

function addHost(name: string, ip: string, daemonId: string | undefined, token = 'host-secret-token'): string {
  const id = useHostStore.getState().addHost({ name, ip, port: 7860, token })
  if (daemonId) useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id]: { ...s.hosts[id], daemonId } } }))
  return id
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(T0)
  useHostStore.getState().reset()
  sot = addHost('mac', '100.64.0.2', 'd1_mac')
  air = addHost('air', '100.64.0.4', 'd1_air')
  relay = sot
  calls = []
  responders = []
  vi.mocked(hostFetch).mockReset()
  vi.mocked(hostFetch).mockImplementation(async (hostId: string, path: string, init?: RequestInit) => {
    const c: Call = {
      hostId,
      method: init?.method ?? 'GET',
      path,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
    }
    calls.push(c)
    for (const r of responders) {
      const out = await r(c)
      if (out) return out
    }
    return defaultResponse(c)
  })
  const hosts = useHostStore.getState().hosts
  input = {
    profile: { sotHostId: sot, profileId: 'p_main', profileName: 'Main' },
    relay: hosts[relay],
    hosts: [hosts[sot], hosts[air]],
    label: "Wake's iPhone",
  }
  consoleSpies = (['log', 'warn', 'error', 'info', 'debug'] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}))
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

const minted = () => calls.filter((c) => c.method === 'POST' && c.path === '/api/devices')
const entryCreates = () => calls.filter((c) => c.method === 'POST' && c.path === '/api/host-transfer/pairings')
const entryDeletes = () => calls.filter((c) => c.method === 'DELETE' && c.path.startsWith('/api/host-transfer/pairings/'))
const revokes = () => calls.filter((c) => c.method === 'DELETE' && c.path.startsWith('/api/devices?pairing_id='))
const polls = () => calls.filter((c) => c.method === 'GET')

function expectNoTokenLeak(...values: unknown[]) {
  const text = JSON.stringify([values, consoleSpies.map((s) => s.mock.calls)])
  for (const t of FAKE_TOKENS) expect(text).not.toContain(t)
}

describe('mintAndPackage', () => {
  it('mints on every host and builds exact pair rows', async () => {
    const res = await mintAndPackage(input)
    expect(res.kind).toBe('ok')
    const body = entryCreates()[0].body as { rows: Record<string, unknown>[]; expires_in_s: number }
    expect(body.rows).toHaveLength(2)
    const pairingId = minted()[0].body!.pairing_id as string
    expect(pairingId).toMatch(/^[0-9a-f-]{36}$/)
    for (const row of body.rows) {
      expect(Object.keys(row).sort()).toEqual(
        ['daemonId', 'deviceId', 'ip', 'kind', 'look', 'name', 'pairingId', 'port', 'profile', 'token', 'v'].sort(),
      )
    }
    const macRow = body.rows.find((r) => r.daemonId === 'd1_mac')!
    expect(macRow).toEqual({
      v: 1,
      kind: 'pair',
      name: 'mac',
      ip: '100.64.0.2',
      port: 7860,
      daemonId: 'd1_mac',
      look: {},
      token: `pdxd_fake_${sot}`,
      deviceId: `d_${sot}`,
      pairingId,
      profile: { hostDaemonId: 'd1_mac', profileId: 'p_main', name: 'Main' },
    })
    expect(body.rows.find((r) => r.daemonId === 'd1_air')!.profile).toEqual({ hostDaemonId: 'd1_mac', profileId: 'p_main', name: 'Main' })
    if (res.kind === 'ok') {
      expect(res.code).toBe('ABCD2345')
      expect(res.pairingId).toBe(pairingId)
      expect(res.deadline).toBe(T0 + PAIRING_TTL_MS)
      expect(res.mintedHostIds.sort()).toEqual([sot, air].sort())
      expect(res.leftOut).toEqual([])
      expect(res.qrUrl).toBe('purdex://pair?v=1&relay=100.64.0.2:7860&code=ABCD2345')
    }
    expectNoTokenLeak(res)
  })

  it('carries only look fields that pass the guards, as an object', async () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, [air]: { ...s.hosts[air], icon: 'Laptop', color: '#ff0000' } },
    }))
    input.hosts = [useHostStore.getState().hosts[sot], useHostStore.getState().hosts[air]]
    await mintAndPackage(input)
    const rows = (entryCreates()[0].body as { rows: { daemonId: string; look: Record<string, unknown> }[] }).rows
    expect(rows.find((r) => r.daemonId === 'd1_air')!.look).toEqual({ icon: 'Laptop', color: '#ff0000' })
    expect(rows.find((r) => r.daemonId === 'd1_mac')!.look).toEqual({})
  })

  it('uses the injected look selector for name and look', async () => {
    input.lookOf = (id) => (id === air ? { name: 'Air (shown)', icon: 'Laptop' } : {})
    await mintAndPackage(input)
    const rows = (entryCreates()[0].body as { rows: { daemonId: string; name: string; look: unknown }[] }).rows
    const r = rows.find((x) => x.daemonId === 'd1_air')!
    expect(r.name).toBe('Air (shown)')
    expect(r.look).toEqual({ icon: 'Laptop' })
  })

  it('sends profile_id only on the SOT host and a device client descriptor', async () => {
    await mintAndPackage(input)
    for (const c of minted()) {
      expect(c.body!.label).toBe("Wake's iPhone")
      expect(c.body!.client).toEqual({ kind: 'app', label: 'Purdex.app' })
      if (c.hostId === sot) expect(c.body!.profile_id).toBe('p_main')
      else expect('profile_id' in c.body!).toBe(false)
    }
  })

  it('computes use_within_s as remaining + 300 and the entry expires_in_s as the remaining time', async () => {
    await mintAndPackage(input)
    for (const c of minted()) expect(c.body!.use_within_s).toBe(900)
    expect(entryCreates()[0].body!.expires_in_s).toBe(600)
  })

  it('a slow mint shortens the entry but never the 300 s margin of the tokens', async () => {
    responders.push(async (c) => {
      if (c.method === 'POST' && c.path === '/api/devices' && c.hostId === air) vi.setSystemTime(Date.now() + 40_500)
      return undefined
    })
    await mintAndPackage(input)
    for (const c of minted()) expect(c.body!.use_within_s).toBe(900)
    expect(entryCreates()[0].body!.expires_in_s).toBe(559) // floor(559.5)
  })

  it('less than 60 s left after the mints aborts, revokes everything and creates no entry', async () => {
    responders.push(async (c) => {
      if (c.method === 'POST' && c.path === '/api/devices' && c.hostId === air) vi.setSystemTime(Date.now() + 545_000)
      return undefined
    })
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason: 'too_late' })
    expect(entryCreates()).toHaveLength(0)
    expect(revokes().map((c) => c.hostId).sort()).toEqual([sot, air].sort())
    expect(new Set(revokes().map((c) => c.path)).size).toBe(1)
    expectNoTokenLeak(res)
  })

  it('an SOT failure aborts: no entry, and what the other hosts minted is revoked', async () => {
    responders.push((c) => (c.method === 'POST' && c.path === '/api/devices' && c.hostId === sot ? json({ reason: 'x' }, 500) : undefined))
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason: 'sot_failed' })
    expect(entryCreates()).toHaveLength(0)
    expect(revokes().map((c) => c.hostId)).toEqual([air])
    expect(revokes()[0].path).toBe(`/api/devices?pairing_id=${minted()[0].body!.pairing_id}`)
  })

  it('an SOT network error is an SOT failure too', async () => {
    vi.mocked(hostFetch).mockImplementation(async (hostId, path, init) => {
      const c: Call = { hostId, method: init?.method ?? 'GET', path, body: typeof init?.body === 'string' ? JSON.parse(init.body) : null }
      calls.push(c)
      if (hostId === sot && path === '/api/devices') throw new Error('boom pdxd_fake_leak')
      return defaultResponse(c)
    })
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason: 'sot_failed' })
    expect(JSON.stringify(res)).not.toContain('pdxd_')
  })

  it('another host failing is left out and reported; the pairing goes on without it', async () => {
    responders.push((c) => (c.method === 'POST' && c.path === '/api/devices' && c.hostId === air ? json({ reason: 'x' }, 500) : undefined))
    const res = await mintAndPackage(input)
    expect(res.kind).toBe('ok')
    if (res.kind === 'ok') {
      expect(res.leftOut).toEqual([{ hostId: air, reason: 'mint_failed' }])
      expect(res.mintedHostIds).toEqual([sot])
    }
    expect((entryCreates()[0].body as { rows: unknown[] }).rows).toHaveLength(1)
    expect(revokes()).toHaveLength(0)
  })

  it('a host without a daemon id cannot be a row: left out before minting', async () => {
    const bare = addHost('bare', '100.64.0.9', undefined)
    input.hosts = [...input.hosts, useHostStore.getState().hosts[bare]]
    const res = await mintAndPackage(input)
    expect(res.kind).toBe('ok')
    if (res.kind === 'ok') expect(res.leftOut).toEqual([{ hostId: bare, reason: 'no_daemon_id' }])
    expect(minted().some((c) => c.hostId === bare)).toBe(false)
  })

  it('an SOT host without a daemon id fails the pairing before anything is minted', async () => {
    const bareSot = addHost('baresot', '100.64.0.9', undefined)
    input.profile.sotHostId = bareSot
    input.hosts = [useHostStore.getState().hosts[bareSot], useHostStore.getState().hosts[air]]
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason: 'sot_failed' })
    expect(minted()).toHaveLength(0)
  })

  it('an SOT host that is not a candidate fails the pairing', async () => {
    input.hosts = [useHostStore.getState().hosts[air]]
    expect(await mintAndPackage(input)).toMatchObject({ kind: 'failed', reason: 'sot_failed' })
    expect(minted()).toHaveLength(0)
  })

  it.each([
    [400, { reason: 'bad_payload' }, 'bad_payload'],
    [429, { reason: 'capacity' }, 'capacity'],
    [503, { reason: 'unavailable' }, 'unavailable'],
    [401, { reason: 'unauthorized' }, 'unauthorized'],
    [403, { reason: 'no_token' }, 'no_token'],
    [404, { reason: 'x' }, 'unsupported'],
    [500, { reason: 'x' }, 'malformed'],
  ])('entry create status %i → %s, and everything minted is revoked', async (status, body, reason) => {
    responders.push((c) => (c.method === 'POST' && c.path === '/api/host-transfer/pairings' ? json(body, status) : undefined))
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason })
    expect(revokes().map((c) => c.hostId).sort()).toEqual([sot, air].sort())
  })

  it('an entry create that never answers times out after 10 s and revokes', async () => {
    responders.push((c) => (c.method === 'POST' && c.path === '/api/host-transfer/pairings' ? new Promise<Response>(() => {}) : undefined))
    const p = mintAndPackage(input)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(await p).toMatchObject({ kind: 'failed', reason: 'timeout' })
    expect(revokes()).toHaveLength(2)
  })

  it('an entry create that throws is a network failure', async () => {
    responders.push((c) => {
      if (c.method === 'POST' && c.path === '/api/host-transfer/pairings') throw new Error('down')
      return undefined
    })
    expect(await mintAndPackage(input)).toMatchObject({ kind: 'failed', reason: 'network' })
  })

  it('a failed revoke after a failure is reported in revokeFailed', async () => {
    responders.push((c) => {
      if (c.method === 'POST' && c.path === '/api/host-transfer/pairings') return json({ reason: 'capacity' }, 429)
      if (c.method === 'DELETE' && c.hostId === air) return json({ reason: 'x' }, 500)
      return undefined
    })
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason: 'capacity', revokeFailed: [air] })
  })

  it('a relay that is not in the host store is never sent to', async () => {
    input.relay = { ...input.relay, id: 'nope' }
    const res = await mintAndPackage(input)
    expect(res).toMatchObject({ kind: 'failed', reason: 'unknown_host' })
    expect(calls).toHaveLength(0)
  })

  it('never logs or returns a token', async () => {
    responders.push((c) => (c.method === 'POST' && c.path === '/api/host-transfer/pairings' ? json({ reason: 'bad_payload' }, 400) : undefined))
    const res = await mintAndPackage(input)
    expectNoTokenLeak(res)
  })
})

describe('createPairingSession', () => {
  async function ready() {
    const s = createPairingSession(input)
    await s.start()
    return s
  }

  it('goes minting → ready with the result and polls every 2 s', async () => {
    const s = createPairingSession(input)
    const seen: string[] = []
    s.subscribe((st) => seen.push(st.phase))
    expect(s.getState().phase).toBe('idle')
    const started = s.start()
    expect(s.getState().phase).toBe('minting')
    await started
    expect(s.getState().phase).toBe('ready')
    expect(seen).toEqual(['minting', 'ready'])
    expect(s.getState().result?.code).toBe('ABCD2345')
    expect(polls()).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(PAIRING_POLL_MS - 1)
    expect(polls()).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(1)
    expect(polls()).toHaveLength(1)
    expect(polls()[0]).toMatchObject({ hostId: relay, path: '/api/host-transfer/pairings/ABCD2345' })
    await vi.advanceTimersByTimeAsync(PAIRING_POLL_MS)
    expect(polls()).toHaveLength(2)
    await s.close()
  })

  it('a failed mint ends in failed with the reason, and polls nothing', async () => {
    responders.push((c) => (c.method === 'POST' && c.path === '/api/devices' && c.hostId === sot ? json({}, 500) : undefined))
    const s = createPairingSession(input)
    await s.start()
    expect(s.getState()).toMatchObject({ phase: 'failed', failure: { reason: 'sot_failed' } })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(polls()).toHaveLength(0)
  })

  it('a seen claim → claimed; closing afterwards sends no DELETE and revokes nothing', async () => {
    responders.push((c) => (c.method === 'GET' ? json({ claimed: true, claimedAt: Date.now(), expiresAt: Date.now() + 1000 }) : undefined))
    const s = await ready()
    await vi.advanceTimersByTimeAsync(PAIRING_POLL_MS)
    expect(s.getState()).toMatchObject({ phase: 'claimed', seenClaim: true })
    await s.close()
    expect(entryDeletes()).toHaveLength(0)
    expect(revokes()).toHaveLength(0)
    expect(s.getState().phase).toBe('claimed')
    // and polling stopped
    const n = polls().length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(polls().length).toBe(n)
  })

  it('the deadline after a seen claim does nothing either', async () => {
    responders.push((c) => (c.method === 'GET' ? json({ claimed: true, claimedAt: 1, expiresAt: 2 }) : undefined))
    await ready()
    await vi.advanceTimersByTimeAsync(PAIRING_TTL_MS + 1000)
    expect(entryDeletes()).toHaveLength(0)
    expect(revokes()).toHaveLength(0)
  })

  it('close → 204 → revokes on every host that minted', async () => {
    const s = await ready()
    await s.close()
    expect(entryDeletes()).toHaveLength(1)
    expect(entryDeletes()[0]).toMatchObject({ hostId: relay, path: '/api/host-transfer/pairings/ABCD2345' })
    expect(revokes().map((c) => c.hostId).sort()).toEqual([sot, air].sort())
    expect(s.getState()).toMatchObject({ phase: 'closed', revokeFailed: [], unknownOutcome: false })
    // DELETE of the entry goes before the revokes
    const order = calls.map((c) => c.method + c.path.split('?')[0])
    expect(order.indexOf('DELETE/api/host-transfer/pairings/ABCD2345')).toBeLessThan(order.indexOf('DELETE/api/devices'))
  })

  it('close is idempotent', async () => {
    const s = await ready()
    await Promise.all([s.close(), s.close()])
    await s.close()
    expect(entryDeletes()).toHaveLength(1)
    expect(revokes()).toHaveLength(2)
  })

  it('a host that fails to revoke is reported in revokeFailed', async () => {
    responders.push((c) => (c.method === 'DELETE' && c.path.startsWith('/api/devices') && c.hostId === air ? json({}, 500) : undefined))
    const s = await ready()
    await s.close()
    expect(s.getState()).toMatchObject({ phase: 'closed', revokeFailed: [air] })
  })

  it('a revoke that throws is reported in revokeFailed', async () => {
    responders.push((c) => {
      if (c.method === 'DELETE' && c.path.startsWith('/api/devices') && c.hostId === air) throw new Error('offline')
      return undefined
    })
    const s = await ready()
    await s.close()
    expect(s.getState().revokeFailed).toEqual([air])
  })

  it('close → 409 (claimed between polls) → claimed, nothing revoked', async () => {
    responders.push((c) => (c.method === 'DELETE' && c.path.startsWith('/api/host-transfer') ? json({ reason: 'claimed' }, 409) : undefined))
    const s = await ready()
    await s.close()
    expect(s.getState()).toMatchObject({ phase: 'claimed', seenClaim: true, unknownOutcome: false })
    expect(revokes()).toHaveLength(0)
  })

  it('close → 404 → nothing revoked, outcome unknown', async () => {
    responders.push((c) => (c.method === 'DELETE' && c.path.startsWith('/api/host-transfer') ? json({ reason: 'not_found' }, 404) : undefined))
    const s = await ready()
    await s.close()
    expect(s.getState()).toMatchObject({ phase: 'gone', unknownOutcome: true })
    expect(revokes()).toHaveLength(0)
  })

  it('close → network error → nothing revoked, outcome unknown', async () => {
    responders.push((c) => {
      if (c.method === 'DELETE' && c.path.startsWith('/api/host-transfer')) throw new Error('offline')
      return undefined
    })
    const s = await ready()
    await s.close()
    expect(s.getState()).toMatchObject({ phase: 'gone', unknownOutcome: true })
    expect(revokes()).toHaveLength(0)
  })

  it('close → timeout → nothing revoked, outcome unknown', async () => {
    responders.push((c) => (c.method === 'DELETE' && c.path.startsWith('/api/host-transfer') ? new Promise<Response>(() => {}) : undefined))
    const s = await ready()
    const closing = s.close()
    await vi.advanceTimersByTimeAsync(10_000)
    await closing
    expect(s.getState()).toMatchObject({ phase: 'gone', unknownOutcome: true })
    expect(revokes()).toHaveLength(0)
  })

  it('close → an unexpected status (500) revokes nothing either', async () => {
    responders.push((c) => (c.method === 'DELETE' && c.path.startsWith('/api/host-transfer') ? json({}, 500) : undefined))
    const s = await ready()
    await s.close()
    expect(s.getState()).toMatchObject({ phase: 'gone', unknownOutcome: true })
    expect(revokes()).toHaveLength(0)
  })

  it('a 404 while polling means the entry is gone: no DELETE, no revoke', async () => {
    responders.push((c) => (c.method === 'GET' ? json({ reason: 'not_found' }, 404) : undefined))
    const s = await ready()
    await vi.advanceTimersByTimeAsync(PAIRING_POLL_MS)
    expect(s.getState()).toMatchObject({ phase: 'gone', unknownOutcome: true })
    expect(entryDeletes()).toHaveLength(0)
    expect(revokes()).toHaveLength(0)
    await s.close()
    expect(entryDeletes()).toHaveLength(0)
  })

  it('a poll that fails keeps polling', async () => {
    let n = 0
    responders.push((c) => {
      if (c.method === 'GET' && n++ === 0) throw new Error('blip')
      return undefined
    })
    const s = await ready()
    await vi.advanceTimersByTimeAsync(PAIRING_POLL_MS * 3)
    expect(polls().length).toBeGreaterThanOrEqual(3)
    expect(s.getState().phase).toBe('ready')
    await s.close()
  })

  it('the countdown reaching the deadline closes without a seen claim: DELETE then revoke on 204', async () => {
    const s = await ready()
    await vi.advanceTimersByTimeAsync(PAIRING_TTL_MS)
    expect(entryDeletes()).toHaveLength(1)
    expect(revokes()).toHaveLength(2)
    expect(s.getState()).toMatchObject({ phase: 'expired', seenClaim: false })
  })

  it('the deadline with a 404 revokes nothing and says the outcome is unknown', async () => {
    responders.push((c) => (c.method === 'DELETE' && c.path.startsWith('/api/host-transfer') ? json({}, 404) : undefined))
    const s = await ready()
    await vi.advanceTimersByTimeAsync(PAIRING_TTL_MS)
    expect(s.getState()).toMatchObject({ phase: 'gone', unknownOutcome: true })
    expect(revokes()).toHaveLength(0)
  })

  it('closing while the mints are in flight revokes what was minted and creates no entry', async () => {
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    responders.push(async (c) => {
      if (c.method === 'POST' && c.path === '/api/devices') await gate
      return undefined
    })
    const s = createPairingSession(input)
    const started = s.start()
    const closing = s.close()
    release()
    await started
    await closing
    expect(entryCreates()).toHaveLength(0)
    expect(revokes()).toHaveLength(2)
    expect(s.getState().phase).toBe('closed')
  })

  it('an abort signal closes the session', async () => {
    const ctl = new AbortController()
    input.signal = ctl.signal
    const s = await ready()
    ctl.abort()
    await vi.advanceTimersByTimeAsync(0)
    expect(s.getState().phase).toBe('closed')
    expect(entryDeletes()).toHaveLength(1)
  })

  it('a listener can unsubscribe', async () => {
    const s = createPairingSession(input)
    const fn = vi.fn()
    const off = s.subscribe(fn)
    off()
    await s.start()
    expect(fn).not.toHaveBeenCalled()
    await s.close()
  })

  it('never logs or exposes a token across a whole session', async () => {
    const states: PairingState[] = []
    const s = createPairingSession(input)
    s.subscribe((st) => states.push(st))
    await s.start()
    await vi.advanceTimersByTimeAsync(PAIRING_POLL_MS)
    await s.close()
    expectNoTokenLeak(states, s.getState())
  })
})
