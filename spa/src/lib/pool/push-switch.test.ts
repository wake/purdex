import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { HostConfigConflictError } from '../host-config-api'
import { useHostStore } from '../../stores/useHostStore'
import { clearPushQueue, pendingPush, pushStateOf, pushTargets, readPush, writePushAll, type PushHost } from './push-switch'

const fetchHostConfig = vi.fn()
const putHostConfig = vi.fn()
vi.mock('../host-config-api', async (orig) => ({
  ...(await orig<typeof import('../host-config-api')>()),
  fetchHostConfig: (...a: unknown[]) => fetchHostConfig(...a),
  putHostConfig: (...a: unknown[]) => putHostConfig(...a),
}))

const answer = (push: boolean, revision = 1) => ({ wwdAlerts: { items: { push }, revision } })
const host = (id: string, over: Partial<PushHost> = {}): PushHost => ({ hostConfigId: id, alias: id, connected: true, on: null, revision: null, error: null, ...over })
const setRuntime = (r: Record<string, 'connected' | 'disconnected'>) => useHostStore.setState({
  hosts: Object.fromEntries(Object.keys(r).map((id) => [id, { id, name: id, ip: '1', port: 1, token: 't', order: 0 }])),
  hostOrder: Object.keys(r),
  runtime: Object.fromEntries(Object.entries(r).map(([id, status]) => [id, { status }])),
} as never)

let server: Record<string, { push: boolean; revision: number }>
beforeEach(() => {
  fetchHostConfig.mockReset(); putHostConfig.mockReset(); clearPushQueue()
  server = { A: { push: false, revision: 1 }, B: { push: true, revision: 4 } }
  fetchHostConfig.mockImplementation(async (id: string) => answer(server[id].push, server[id].revision))
  putHostConfig.mockImplementation(async (id: string, _c: string, items: { push: boolean }) => {
    server[id] = { push: items.push, revision: server[id].revision + 1 }
    return { wwdAlerts: undefined, items, revision: server[id].revision }
  })
  setRuntime({ A: 'connected', B: 'connected' })
})
afterEach(() => clearPushQueue())

describe('pushTargets', () => {
  it('every App host that fronts a pool host, once each, in host order — not only the connected ones', () => {
    setRuntime({ B: 'connected', A: 'disconnected' })
    const t = pushTargets({ w1: 'A', w2: 'B', w3: 'A' }, [{ host_id: 'w1', alias: 'mlab' }, { host_id: 'w2', alias: 'air26' }])
    expect(t).toEqual([{ hostConfigId: 'B', alias: 'air26' }, { hostConfigId: 'A', alias: 'mlab' }])
  })
})

describe('pushTargets — only hosts the pool lists (codex R1)', () => {
  it('an owner kept from before, whose pool host is gone from the list, is not written', () => {
    const t = pushTargets({ w1: 'A', gone: 'B' }, [{ host_id: 'w1', alias: 'mlab' }])
    expect(t).toEqual([{ hostConfigId: 'A', alias: 'mlab' }])
  })
})

describe('the queue and the state of the connection now (codex R1)', () => {
  it('a host that was connected when read but is offline when pressed is queued, not written to fail', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    const r = await writePushAll(true, [host('B', { connected: true })])
    expect(r[0]).toMatchObject({ queued: true })
    expect(putHostConfig).not.toHaveBeenCalled()
    expect(pendingPush()).toEqual(['B'])
  })

  it('a host that was offline when read but is connected when pressed is written now', async () => {
    const r = await writePushAll(true, [host('B', { connected: false })])
    expect(r[0].ok).toBe(true)
    expect(putHostConfig).toHaveBeenCalledWith('B', 'wwd_alerts', { push: true }, 4)
    expect(pendingPush()).toEqual([])
  })

  it('a write that keeps failing is tried once per connection, not in a loop; it stays queued and the next connection tries again', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    putHostConfig.mockRejectedValue(new Error('500'))
    await writePushAll(true, [host('B', { connected: false })])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(1))
    await new Promise((r) => setTimeout(r, 50))
    expect(putHostConfig).toHaveBeenCalledTimes(1)
    expect(pendingPush()).toEqual(['B'])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'disconnected' } } } as never)
    putHostConfig.mockImplementation(async (id: string, _c: string, items: { push: boolean }) => { server[id] = { push: items.push, revision: 9 }; return {} })
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(pendingPush()).toEqual([]))
  })
})

describe('one write at a time per host (codex attack)', () => {
  it('a queued value being written when a newer press arrives: the older write ends first, the newer one is sent after it, and the last value is the newer', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    await writePushAll(true, [host('B', { connected: false })]) // queued: true
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const puts: boolean[] = []
    putHostConfig.mockImplementation(async (id: string, _c: string, items: { push: boolean }) => {
      puts.push(items.push)
      if (puts.length === 1) await gate // the first (queued) write is still in the air
      server[id] = { push: items.push, revision: server[id].revision + 1 }
      return {}
    })
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(puts).toEqual([true]))
    const press = writePushAll(false, [host('B')]) // the reader presses again meanwhile
    await new Promise((r) => setTimeout(r, 20))
    expect(puts).toEqual([true]) // not started yet: it waits behind the one in the air
    release()
    await press
    expect(puts).toEqual([true, false])
    expect(server.B.push).toBe(false)
    expect(pendingPush()).toEqual([])
  })

  it('a host that drops after the connection was checked: the failed write goes back to the queue and is written when it returns', async () => {
    fetchHostConfig.mockImplementationOnce(async () => { setRuntime({ A: 'connected', B: 'disconnected' }); throw new Error('network lost') })
    const r = await writePushAll(true, [host('B')])
    expect(r[0]).toMatchObject({ ok: false, queued: true })
    expect(pendingPush()).toEqual(['B'])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledWith('B', 'wwd_alerts', { push: true }, 4))
    await vi.waitFor(() => expect(pendingPush()).toEqual([]))
  })

  it('a queued write that failed is tried again only when THAT host connects again — not on every runtime update (latency, another host)', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    putHostConfig.mockRejectedValue(new Error('500'))
    await writePushAll(true, [host('B', { connected: false })])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(1))
    // updates that do not change B's connection: B's own latency, and another host's status
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected', latency: 12 } } } as never)
    useHostStore.setState({ runtime: { A: { status: 'disconnected' }, B: { status: 'connected', latency: 12 } } } as never)
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected', latency: 40 } } } as never)
    await new Promise((r) => setTimeout(r, 50))
    expect(putHostConfig).toHaveBeenCalledTimes(1)
    expect(pendingPush()).toEqual(['B'])
    // B drops and comes back: now it is tried again
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'disconnected' } } } as never)
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(2))
  })

  it('a failure while still connected is an error on the line, not a queued write', async () => {
    putHostConfig.mockRejectedValue(new Error('500'))
    const r = await writePushAll(true, [host('B')])
    expect(r[0]).toMatchObject({ ok: false })
    expect(r[0].queued).toBeUndefined()
    expect(pendingPush()).toEqual([])
  })
})

describe('readPush / pushStateOf', () => {
  it('both on, both off, one of each, one unreadable', async () => {
    server.A.push = true
    expect(pushStateOf(await readPush([{ hostConfigId: 'A', alias: 'a' }, { hostConfigId: 'B', alias: 'b' }]))).toBe('on')
    server.A.push = false; server.B.push = false
    expect(pushStateOf(await readPush([{ hostConfigId: 'A', alias: 'a' }, { hostConfigId: 'B', alias: 'b' }]))).toBe('off')
    server.B.push = true
    expect(pushStateOf(await readPush([{ hostConfigId: 'A', alias: 'a' }, { hostConfigId: 'B', alias: 'b' }]))).toBe('partial')
    fetchHostConfig.mockImplementation(async (id: string) => { if (id === 'B') throw new Error('down'); return answer(true) })
    const hs = await readPush([{ hostConfigId: 'A', alias: 'a' }, { hostConfigId: 'B', alias: 'b' }])
    expect(hs[1]).toMatchObject({ on: null })
    expect(pushStateOf(hs)).toBe('on') // the one that cannot be read counts as neither
  })

  it('nothing readable is unknown; a shape it does not know reads as null; an offline host is not asked', async () => {
    setRuntime({ A: 'disconnected', B: 'connected' })
    fetchHostConfig.mockResolvedValue({ wwdAlerts: { items: { push: 'yes' }, revision: 2 } })
    const hs = await readPush([{ hostConfigId: 'A', alias: 'a' }, { hostConfigId: 'B', alias: 'b' }])
    expect(hs.map((h) => h.on)).toEqual([null, null])
    expect(hs[0].connected).toBe(false)
    expect(fetchHostConfig).toHaveBeenCalledTimes(1)
    expect(pushStateOf(hs)).toBe('unknown')
    expect(pushStateOf([])).toBe('unknown')
  })
})

describe('writePushAll — connected hosts are written now', () => {
  it('writes both, each on the revision it just read', async () => {
    const r = await writePushAll(true, [host('A'), host('B')])
    expect(putHostConfig).toHaveBeenCalledWith('A', 'wwd_alerts', { push: true }, 1)
    expect(putHostConfig).toHaveBeenCalledWith('B', 'wwd_alerts', { push: true }, 4)
    expect(r.map((x) => x.ok)).toEqual([true, true])
    expect(pendingPush()).toEqual([])
  })

  it('a 409 whose current copy already has the target value is a success, with no second write', async () => {
    putHostConfig.mockRejectedValueOnce(new HostConfigConflictError({ items: { push: true }, revision: 9 }))
    const r = await writePushAll(true, [host('A')])
    expect(r[0].ok).toBe(true)
    expect(putHostConfig).toHaveBeenCalledTimes(1)
  })

  it('a 409 with another value is sent again on the new revision, once', async () => {
    putHostConfig.mockRejectedValueOnce(new HostConfigConflictError({ items: { push: false }, revision: 9 }))
    const r = await writePushAll(true, [host('A')])
    expect(putHostConfig).toHaveBeenCalledTimes(2)
    expect(putHostConfig).toHaveBeenLastCalledWith('A', 'wwd_alerts', { push: true }, 9)
    expect(r[0].ok).toBe(true)
  })

  it('a second 409 is a failure, not a loop', async () => {
    putHostConfig.mockRejectedValue(new HostConfigConflictError({ items: { push: false }, revision: 9 }))
    const r = await writePushAll(true, [host('A')])
    expect(putHostConfig).toHaveBeenCalledTimes(2)
    expect(r[0].ok).toBe(false)
  })

  it('one host failing does not stop the next', async () => {
    putHostConfig.mockImplementationOnce(async () => { throw new Error('500') })
    const r = await writePushAll(true, [host('A'), host('B')])
    expect(r.map((x) => x.ok)).toEqual([false, true])
    expect(putHostConfig).toHaveBeenCalledTimes(2)
  })
})

describe('writePushAll — a host that is offline waits in the queue', () => {
  it('goes into the queue instead of being skipped, and is written when it connects', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    const r = await writePushAll(true, [host('A'), host('B', { connected: false })])
    expect(r.find((x) => x.hostConfigId === 'B')).toMatchObject({ ok: false, queued: true })
    expect(putHostConfig).toHaveBeenCalledTimes(1)
    expect(pendingPush()).toEqual(['B'])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(2))
    expect(putHostConfig).toHaveBeenLastCalledWith('B', 'wwd_alerts', { push: true }, 4)
    await vi.waitFor(() => expect(pendingPush()).toEqual([]))
  })

  it('two writes to the same offline host: only the last one is sent', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    await writePushAll(true, [host('B', { connected: false })])
    await writePushAll(false, [host('B', { connected: false })])
    expect(pendingPush()).toEqual(['B'])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(putHostConfig).toHaveBeenCalledTimes(1))
    expect(putHostConfig).toHaveBeenCalledWith('B', 'wwd_alerts', { push: false }, 4)
  })

  it('a host that is connected is never queued', async () => {
    await writePushAll(true, [host('A'), host('B')])
    expect(pendingPush()).toEqual([])
  })

  it('once the queue is empty, the subscription is gone: a later reconnect writes nothing', async () => {
    setRuntime({ A: 'connected', B: 'disconnected' })
    await writePushAll(true, [host('B', { connected: false })])
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await vi.waitFor(() => expect(pendingPush()).toEqual([]))
    putHostConfig.mockClear()
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'disconnected' } } } as never)
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } } as never)
    await new Promise((r) => setTimeout(r, 20))
    expect(putHostConfig).not.toHaveBeenCalled()
  })
})
