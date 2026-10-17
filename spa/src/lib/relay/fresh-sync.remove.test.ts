// spa/src/lib/relay/fresh-sync.remove.test.ts — a connected host DELETED (the real `deleteHostCascade`, which clears the Nexen
// cache entry first and removes the host after, all in one synchronous run) leaves nothing behind in the relay fresh sync: no
// `/api/info` asked for a host that is about to go, no orphan Nexen entry written by that answer, no list. The re-point cues
// (fresh-sync.repoint.test.ts) must keep working next to it, so a re-point followed by a delete is covered here too.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startRelayFresh } from './fresh-sync'
import type { RelayFresh } from './fresh'
import { useRelayFreshStore } from '../../stores/useRelayFreshStore'
import { useHostStore } from '../../stores/useHostStore'
import { startNexHostInvalidation, useNexHostStore } from '../../stores/useNexHostStore'
import { deleteHostCascade } from '../host-lifecycle'

vi.mock('./fresh', async (orig) => ({ ...(await orig<typeof import('./fresh')>()), fetchRelayFresh: vi.fn() }))
vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), fetchInfo: vi.fn() }))
vi.mock('../nex/nex-api', () => ({ releaseLease: vi.fn(), pinnedLeaseRelease: vi.fn() }))
import { fetchRelayFresh } from './fresh'
import { fetchInfo } from '../host-api'

const A = 'h-a'
const B = 'h-b'
const cfg = (id: string, over: Record<string, unknown> = {}) => ({ id, name: id, ip: '1.1.1.1', port: 7860, token: 't', order: 0, ...over })
const fresh = (id: string): RelayFresh => ({ session_id: id, ref: '_aaaaaa', op_id: `op-${id}`, path: `/p/${id}.md`, written_at: 1000, code: `c-${id}` })
const setStatus = (status: 'connected' | 'disconnected') => useHostStore.setState((s) => ({ runtime: { ...s.runtime, [A]: { status } } }) as never)
const repoint = (over: Record<string, unknown>) => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], ...over } } }) as never)
const settle = async () => { for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0)) }

const ORDERS = [
  { name: 'relay fresh first (main.tsx)', nexFirst: false },
  { name: 'Nexen cache first', nexFirst: true },
]

let stops: Array<() => void> = []
function boot(nexFirst: boolean) {
  if (nexFirst) stops = [startNexHostInvalidation(), startRelayFresh()]
  else stops = [startRelayFresh(), startNexHostInvalidation()]
}

beforeEach(() => {
  localStorage.clear()
  vi.mocked(fetchInfo).mockReset().mockImplementation(async () => ({ ok: true, json: async () => ({ capabilities: ['relay.half.v1'] }) }) as Response)
  vi.mocked(fetchRelayFresh).mockReset().mockImplementation(async () => [fresh('one')])
  useRelayFreshStore.getState().reset()
  useNexHostStore.setState({ byHost: {} })
  useHostStore.setState({
    hosts: { [A]: cfg(A), [B]: cfg(B, { ip: '2.2.2.2', order: 1 }) },
    hostOrder: [A, B],
    activeHostId: B,
    runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'disconnected' } },
  } as never)
})
afterEach(() => {
  stops.forEach((s) => s())
  stops = []
  useNexHostStore.setState({ byHost: {} })
})

describe.each(ORDERS)('a connected host deleted, $name', ({ nexFirst }) => {
  async function connectedWithList() {
    boot(nexFirst)
    setStatus('connected')
    await settle()
    expect(useRelayFreshStore.getState().byHost[A]?.map((s) => s.session_id)).toEqual(['one'])
    expect(useNexHostStore.getState().byHost[A]).toBeDefined()
    vi.mocked(fetchInfo).mockClear()
    vi.mocked(fetchRelayFresh).mockClear()
  }

  it('asks no /api/info, leaves no Nexen entry and no list behind', async () => {
    await connectedWithList()
    deleteHostCascade(A)
    await settle()
    expect(useHostStore.getState().hosts[A]).toBeUndefined()
    expect(vi.mocked(fetchInfo)).not.toHaveBeenCalled()
    expect(vi.mocked(fetchRelayFresh)).not.toHaveBeenCalled()
    expect(useNexHostStore.getState().byHost[A]).toBeUndefined()
    expect(useRelayFreshStore.getState().byHost[A]).toBeUndefined()
  })

  it('a re-point and then a delete leaves nothing either (the re-point was answered, the delete asks nothing more)', async () => {
    await connectedWithList()
    repoint({ ip: '9.9.9.9' })
    await settle()
    expect(vi.mocked(fetchInfo)).toHaveBeenCalled()
    expect(useNexHostStore.getState().byHost[A]).toBeDefined()
    vi.mocked(fetchInfo).mockClear()
    vi.mocked(fetchRelayFresh).mockClear()
    deleteHostCascade(A)
    await settle()
    expect(vi.mocked(fetchInfo)).not.toHaveBeenCalled()
    expect(vi.mocked(fetchRelayFresh)).not.toHaveBeenCalled()
    expect(useNexHostStore.getState().byHost[A]).toBeUndefined()
    expect(useRelayFreshStore.getState().byHost[A]).toBeUndefined()
  })

  it('the other host is untouched by the delete', async () => {
    await connectedWithList()
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, [B]: { status: 'connected' } } }) as never)
    await settle()
    const bEntry = useNexHostStore.getState().byHost[B]
    expect(bEntry).toBeDefined()
    deleteHostCascade(A)
    await settle()
    expect(useNexHostStore.getState().byHost[B]).toBe(bEntry)
  })
})
