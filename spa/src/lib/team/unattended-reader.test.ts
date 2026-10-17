// spa/src/lib/team/unattended-reader.test.ts — the one reader of a host's first unattended page, shared by the unattended
// panel and the team settings (journal v5 spec §3.9): single-flight per host, the quota store bracketed around it, its
// answer kept as the host's last read, bounded by a timeout, and given up only when nobody waits for it any more.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { getUnattended } from './unattended-api'
import { useRelayQuotaStore, quotaKey } from './relay-quota'
import { refetchHost, resetWriter } from './relay-quota-writer'
import { HOST_READ_TIMEOUT_MS, readFirstPage, refreshHost, resetReader, watchHost } from './unattended-reader'
import { startUnattendedSupport } from './unattended-support'
import { useHostStore } from '../../stores/useHostStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import type { SessionQuota, UnattendedView } from './types'

vi.mock('./unattended-api', () => ({ getUnattended: vi.fn() }))
const mockedGet = vi.mocked(getUnattended)
vi.mock('../host-api', () => ({
  fetchHostInfo: vi.fn(async () => ({ host_id: 'd', tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '', capabilities: ['relay.unattended.v1', 'team.relay_quota.v1'] })),
}))

const A = 'host-a'
const B = 'host-b'
const page = (over: Partial<UnattendedView> = {}): UnattendedView => ({ on: true, since: 1, changed_at: 1, approved: [], truncated: false, ...over })
const quota = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: id, root_session_id: `root-${id}`, address: `mlab/${id}`, is_lead: true, self_left: 2, member_pool_left: 1, rev: 4, ...over,
})
const st = () => useRelayQuotaStore.getState()
const flush = async () => { for (let i = 0; i < 5; i++) await Promise.resolve() }

/** Every GET held open: its host, signal and release, in order. */
function controlled() {
  const calls: { hostId: string; signal?: AbortSignal; resolve: (v: UnattendedView) => void; reject: (e: unknown) => void }[] = []
  mockedGet.mockImplementation((hostId, _q, signal) => new Promise<UnattendedView>((resolve, reject) => { calls.push({ hostId, signal, resolve, reject }) }))
  return calls
}
const codeOf = (p: Promise<unknown>) => p.then(() => 'resolved', (e: unknown) => (e instanceof ApprovalApiError ? e.code : String(e)))

beforeEach(() => {
  resetReader()
  resetWriter()
  mockedGet.mockReset()
  st().reset()
  useUnattendedStore.getState().reset()
  useUnattendedStore.getState().setQuotaSupport(A, 'yes')
  useHostStore.setState({
    hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, [B]: { id: B, name: 'air26', ip: '2', port: 2, token: 't', order: 1 } },
    hostOrder: [A, B], activeHostId: A, runtime: {},
  })
})
afterEach(() => { resetReader(); vi.useRealTimers(); useHostStore.getState().reset() })

describe('single-flight', () => {
  it('two reads of one host while the first is out make one request, and both get its answer', async () => {
    const calls = controlled()
    const one = readFirstPage(A)
    const two = readFirstPage(A)
    expect(calls).toHaveLength(1)
    calls[0].resolve(page({ quotas: [quota('L')] }))
    expect((await one).quotas).toEqual([quota('L')])
    expect((await two).quotas).toEqual([quota('L')])
  })

  it('a read after the first answered asks again; hosts are separate', async () => {
    mockedGet.mockResolvedValue(page())
    await readFirstPage(A)
    await readFirstPage(A)
    await readFirstPage(B)
    expect(mockedGet.mock.calls.map(([h, q]) => [h, q])).toEqual([[A, undefined], [A, undefined], [B, undefined]])
  })

  it('a host re-pointed while its read is out gets a read of its own; the old answer lands nowhere', async () => {
    const calls = controlled()
    const old = readFirstPage(A)
    st().forgetHost(A) // what the host store's re-point does (unattended-support.ts)
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], ip: '9.9.9.9' } } }))
    const fresh = readFirstPage(A)
    expect(calls).toHaveLength(2)
    calls[1].resolve(page({ quotas: [quota('new', { rev: 1 })] }))
    await fresh
    calls[0].resolve(page({ quotas: [quota('old', { rev: 99 })] }))
    await old
    expect(st().reads[A]).toEqual({ ok: true, quotas: [quota('new', { rev: 1 })] })
    expect(st().confirmed[quotaKey(A, 'root-old')]).toBeUndefined()
    expect(st().gets).toEqual({})
  })

  // JV-5b review: endpoint and token alone do not name a connection. A disconnect and a reconnect to the same daemon is a new
  // connection generation (unattended-support.ts fences the store): the old connection's read joins nothing and writes nothing.
  it('a reconnect (same endpoint and token): a new reader does not join the old read, whose late answer writes nothing', async () => {
    const tick = () => new Promise((r) => setTimeout(r, 0))
    useHostStore.setState({ runtime: { [A]: { status: 'connected' } } })
    const stop = startUnattendedSupport()
    try {
      await tick() // the probe answered: team.relay_quota.v1
      const calls = controlled()
      const old = readFirstPage(A)
      useHostStore.setState({ runtime: { [A]: { status: 'disconnected' } } })
      useHostStore.setState({ runtime: { [A]: { status: 'connected' } } })
      await tick()
      const fresh = readFirstPage(A)
      expect(calls).toHaveLength(2) // not joined to the old connection's read
      calls[0].resolve(page({ quotas: [quota('old', { rev: 99 })] }))
      await old
      expect(st().reads[A]).toBeUndefined()
      expect(st().confirmed[quotaKey(A, 'root-old')]).toBeUndefined()
      calls[1].resolve(page({ quotas: [quota('new')] }))
      await fresh
      expect(st().reads[A]).toEqual({ ok: true, quotas: [quota('new')] })
      expect(st().confirmed[quotaKey(A, 'root-new')]).toBeDefined()
    } finally { stop() }
  })

  it('a removed host is not asked', async () => {
    useHostStore.setState((s) => ({ hosts: { [B]: s.hosts[B] } as typeof s.hosts }))
    expect(await codeOf(readFirstPage(A))).toBe('no_host')
    expect(mockedGet).not.toHaveBeenCalled()
  })
})

describe('the quota store', () => {
  it('a quota host: the GET is bracketed by beginGet / endGet, its rows confirmed, its quota part kept as the host\'s read', async () => {
    const calls = controlled()
    const p = readFirstPage(A)
    expect(st().gets[A]?.depth).toBe(1)
    st().applyEvent(A, { op: 'changed', root_session_id: 'root-L', self_left: 7, member_pool_left: 1, rev: 9 }) // buffered meanwhile
    calls[0].resolve(page({ quotas: [quota('L', { rev: 4 })], held: [], approved: [] }))
    await p
    expect(st().gets).toEqual({})
    expect(st().confirmed[quotaKey(A, 'root-L')]).toEqual({ self_left: 7, member_pool_left: 1, rev: 9 })
    expect(st().reads[A]).toEqual({ ok: true, quotas: [quota('L', { rev: 4 })], held: [] })
  })

  it('a host without the capability: no GET bracket, nothing confirmed, but its answer is still kept', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [quota('L')] }))
    await readFirstPage(B)
    expect(st().confirmed).toEqual({})
    expect(st().reads[B]).toEqual({ ok: true, quotas: [quota('L')] })
  })

  it('a failed GET: every caller gets its code, the read is the failure, the buffered events are released', async () => {
    const calls = controlled()
    const one = codeOf(readFirstPage(A))
    const two = codeOf(readFirstPage(A))
    st().applyEvent(A, { op: 'changed', root_session_id: 'r', self_left: 5, member_pool_left: 0, rev: 2 })
    calls[0].reject(new ApprovalApiError(503, 'not_ready'))
    expect([await one, await two]).toEqual(['not_ready', 'not_ready'])
    expect(st().reads[A]).toEqual({ ok: false, code: 'not_ready' })
    expect(st().confirmed[quotaKey(A, 'r')]?.self_left).toBe(5)
    expect(st().gets).toEqual({})
  })

  it('an answer flagged list_failed is a failed read', async () => {
    mockedGet.mockResolvedValue(page({ list_failed: true }))
    expect(await codeOf(readFirstPage(A))).toBe('list_failed')
    expect(st().reads[A]).toEqual({ ok: false, code: 'list_failed' })
  })
})

describe('bounded, and given up only when nobody waits', () => {
  beforeEach(() => { vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }) })

  it('a silent host fails as timeout after HOST_READ_TIMEOUT_MS and its request is aborted', async () => {
    const calls = controlled()
    const p = codeOf(readFirstPage(A))
    await vi.advanceTimersByTimeAsync(HOST_READ_TIMEOUT_MS - 1)
    expect(calls[0].signal!.aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    expect(await p).toBe('timeout')
    expect(calls[0].signal!.aborted).toBe(true)
    expect(st().reads[A]).toEqual({ ok: false, code: 'timeout' })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a caller that lets go rejects as aborted; the request goes on while another caller still waits', async () => {
    const calls = controlled()
    const ctl = new AbortController()
    const leaving = codeOf(readFirstPage(A, ctl.signal))
    const staying = readFirstPage(A, new AbortController().signal)
    ctl.abort()
    expect(await leaving).toBe('aborted')
    expect(calls[0].signal!.aborted).toBe(false)
    calls[0].resolve(page({ quotas: [] }))
    expect((await staying).quotas).toEqual([])
  })

  it('when the last caller lets go the request is aborted, its timer cleared, and nothing is kept as the host\'s read', async () => {
    const calls = controlled()
    const one = new AbortController()
    const two = new AbortController()
    void codeOf(readFirstPage(A, one.signal))
    void codeOf(readFirstPage(A, two.signal))
    one.abort()
    expect(calls[0].signal!.aborted).toBe(false)
    two.abort()
    expect(calls[0].signal!.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    expect(st().gets).toEqual({}) // its GET bracket is closed
    expect(st().reads[A]).toBeUndefined() // letting go is not a failure of the host
    calls[0].resolve(page({ quotas: [quota('late')] })) // a late answer of the given-up request
    await flush()
    expect(st().reads[A]).toBeUndefined()
    readFirstPage(A).catch(() => {})
    expect(calls).toHaveLength(2) // the next read asks afresh
  })

  it('a caller without a signal holds the request to its end, whoever else lets go', async () => {
    const calls = controlled()
    const ctl = new AbortController()
    const held = readFirstPage(A)
    void codeOf(readFirstPage(A, ctl.signal))
    ctl.abort()
    expect(calls[0].signal!.aborted).toBe(false)
    calls[0].resolve(page({ quotas: [] }))
    expect((await held).quotas).toEqual([])
  })

  it('an already aborted signal is let go at once', async () => {
    const calls = controlled()
    const ctl = new AbortController()
    ctl.abort()
    expect(await codeOf(readFirstPage(A, ctl.signal))).toBe('aborted')
    expect(calls[0]?.signal?.aborted ?? true).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('re-reads (after pending_lineage)', () => {
  it('refreshHost asks again even while a read is out; later reads join the new one', async () => {
    const calls = controlled()
    const first = readFirstPage(A)
    const again = refreshHost(A)
    expect(calls).toHaveLength(2)
    const joined = readFirstPage(A)
    expect(calls).toHaveLength(2)
    calls[1].resolve(page({ quotas: [quota('real', { root_session_id: 'real-root' })] }))
    expect((await again).quotas?.[0].root_session_id).toBe('real-root')
    expect((await joined).quotas?.[0].root_session_id).toBe('real-root')
    calls[0].resolve(page({ quotas: [quota('prov')] }))
    await first
    // the replaced read answered last, but never writes: the last read and the numbers stay the re-read's (JV-5b review)
    expect(st().reads[A]).toEqual({ ok: true, quotas: [quota('real', { root_session_id: 'real-root' })] })
    expect(st().confirmed[quotaKey(A, 'root-prov')]).toBeUndefined()
    expect(st().confirmed[quotaKey(A, 'real-root')]).toBeDefined()
    expect(st().gets).toEqual({})
  })

  it('a replaced read that fails after the re-read does not write its failure either', async () => {
    const calls = controlled()
    void codeOf(readFirstPage(A))
    const again = refreshHost(A)
    calls[1].resolve(page({ quotas: [quota('real')] }))
    await again
    calls[0].reject(new ApprovalApiError(503, 'not_ready'))
    await flush()
    expect(st().reads[A]).toEqual({ ok: true, quotas: [quota('real')] })
  })

  it('a replaced read closes its GET bracket at once: 讀取中 lasts exactly as long as the read that counts', async () => {
    const calls = controlled()
    void codeOf(readFirstPage(A))
    expect(st().gets[A]?.depth).toBe(1)
    const again = refreshHost(A)
    expect(st().gets[A]?.depth).toBe(1) // the replaced one no longer counts
    calls[1].resolve(page({ quotas: [] }))
    await again
    expect(st().gets).toEqual({}) // not held open by the replaced read still on the wire
  })

  it('while a host is watched the writer\'s re-read asks it again; two watchers, one re-read; none left, none', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [] }))
    refetchHost(A)
    expect(mockedGet).not.toHaveBeenCalled()
    const one = watchHost(A)
    const two = watchHost(A)
    refetchHost(A)
    expect(mockedGet).toHaveBeenCalledTimes(1)
    one()
    one() // a second call of the same disposer is a no-op
    await flush()
    refetchHost(A)
    expect(mockedGet).toHaveBeenCalledTimes(2)
    two()
    await flush()
    refetchHost(A)
    expect(mockedGet).toHaveBeenCalledTimes(2)
  })
})
