// spa/src/lib/task-groups/root-cache.test.ts — a session's lineage root, kept on this device (TG-2r). `rootOf` is a pure cache read; `resolveRoot` is the one place that asks the daemon when the cache lacks an answer.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const readFirstPage = vi.fn()
vi.mock('../team/unattended-reader', () => ({ readFirstPage: (...a: unknown[]) => readFirstPage(...a), refreshHost: (...a: unknown[]) => readFirstPage(...a) }))

import { useRootCacheStore, addressOf, titleOf, rootOf, resolveRoot, rootsSupported } from './root-cache'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useHostStore } from '../../stores/useHostStore'
import type { SessionQuota } from '../team/types'

const q = (session_id: string, root_session_id: string): SessionQuota => ({ session_id, root_session_id, title: '', address: `mlab/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })
const supportQuota = (hostId: string, yes = true) => useUnattendedStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], quotaSupport: yes ? 'yes' : 'no' } } }) as never)

beforeEach(() => {
  readFirstPage.mockReset()
  useRootCacheStore.setState({ roots: {}, addresses: {}, titles: {} })
  useUnattendedStore.setState({ byHost: {} })
  useHostStore.setState({ hosts: {}, runtime: {} })
})

describe('rootOf', () => {
  it('is a pure cache read: a hit answers, a miss is null and asks nobody', () => {
    useRootCacheStore.getState().ingest('h1', [q('S1', 'ROOT')])
    expect(rootOf('h1', 'S1')).toBe('ROOT')
    expect(rootOf('h1', 'S2')).toBeNull()
    expect(rootOf('h2', 'S1')).toBeNull()
    expect(readFirstPage).not.toHaveBeenCalled()
  })
})

describe('resolveRoot', () => {
  it('answers from the cache without reading the daemon', async () => {
    supportQuota('h1')
    useRootCacheStore.getState().ingest('h1', [q('S1', 'ROOT')])
    await expect(resolveRoot('h1', 'S1')).resolves.toBe('ROOT')
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('on a miss reads the daemon, caches what it lists and answers', async () => {
    supportQuota('h1')
    readFirstPage.mockResolvedValue({ quotas: [q('S1', 'ROOT'), q('S2', 'S2')] })
    await expect(resolveRoot('h1', 'S1')).resolves.toBe('ROOT')
    expect(readFirstPage).toHaveBeenCalledWith('h1')
    expect(rootOf('h1', 'S2')).toBe('S2') // the whole page is kept
  })

  it('is null when the daemon cannot be read, does not list the session, or lacks team.relay_quota.v1', async () => {
    supportQuota('h1')
    readFirstPage.mockRejectedValue(new Error('down'))
    await expect(resolveRoot('h1', 'S1')).resolves.toBeNull()
    readFirstPage.mockResolvedValue({ quotas: [q('S2', 'S2')] })
    await expect(resolveRoot('h1', 'S1')).resolves.toBeNull()
    readFirstPage.mockClear()
    await expect(resolveRoot('h3', 'S1')).resolves.toBeNull() // no capability: never asked
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('does not cache a page that comes back after the host was re-pointed (it is the old daemon\'s)', async () => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, token: 't' } } } as never)
    supportQuota('h1')
    let answer: (v: unknown) => void = () => {}
    readFirstPage.mockReturnValue(new Promise((r) => { answer = r }))
    const pending = resolveRoot('h1', 'S1')
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.9', port: 7860, token: 't' } } } as never)
    answer({ quotas: [q('S1', 'OLD')] })
    await expect(pending).resolves.toBeNull()
    expect(rootOf('h1', 'S1')).toBeNull()
  })

  it('does not guess when the page has no quota rows (an older daemon)', async () => {
    supportQuota('h1')
    readFirstPage.mockResolvedValue({})
    await expect(resolveRoot('h1', 'S1')).resolves.toBeNull()
  })
})

describe('rootsSupported', () => {
  it('needs the host to list team.relay_quota.v1 and to answer as the daemon it is stored as', () => {
    expect(rootsSupported('h1')).toBe(false)
    supportQuota('h1')
    expect(rootsSupported('h1')).toBe(true)
    supportQuota('h1', false)
    expect(rootsSupported('h1')).toBe(false)
  })
})

describe('store', () => {
  it('ingest keeps earlier entries (a root never changes) and prune keeps what the predicate keeps', () => {
    const s = useRootCacheStore.getState()
    s.ingest('h1', [q('S1', 'R1')])
    s.ingest('h1', [q('S2', 'R2')])
    expect(rootOf('h1', 'S1')).toBe('R1')
    s.prune((_hostId, id) => id === 'S2')
    expect(rootOf('h1', 'S1')).toBeNull()
    expect(rootOf('h1', 'S2')).toBe('R2')
    s.dropHost('h1')
    expect(useRootCacheStore.getState().roots).toEqual({})
  })
})

describe('addresses (TG-5b)', () => {
  it('keeps the address of the row beside its root, and keeps it when a later read no longer lists the session', () => {
    useRootCacheStore.getState().ingest('h1', [q('S1', 'ROOT')])
    expect(addressOf('h1', 'S1')).toBe('mlab/S1')
    useRootCacheStore.getState().ingest('h1', [q('S2', 'R2')])
    expect(addressOf('h1', 'S1')).toBe('mlab/S1')
    expect(addressOf('h1', 'S2')).toBe('mlab/R2'.replace('R2', 'S2'))
    expect(addressOf('h1', 'nope')).toBeNull()
  })

  it('an empty address is not stored; a listed row with an empty one forgets the old, and a new one replaces it', () => {
    useRootCacheStore.getState().ingest('h1', [{ ...q('S1', 'ROOT'), address: '' }])
    expect(addressOf('h1', 'S1')).toBeNull()
    useRootCacheStore.getState().ingest('h1', [{ ...q('S1', 'ROOT'), address: 'mlab/new-name' }])
    expect(addressOf('h1', 'S1')).toBe('mlab/new-name')
    useRootCacheStore.getState().ingest('h1', [{ ...q('S1', 'ROOT'), address: '' }])
    expect(addressOf('h1', 'S1')).toBeNull()
    useRootCacheStore.getState().ingest('h1', [{ ...q('S1', 'ROOT'), address: 'mlab/again' }])
    expect(addressOf('h1', 'S1')).toBe('mlab/again')
  })

  it('keeps the pdx title the row carried, beside the address, and forgets it with the host', () => {
    useRootCacheStore.getState().ingest('h1', [{ ...q('S1', 'ROOT'), title: 'purdex-iface-lead' }, q('S2', 'R2')])
    expect(titleOf('h1', 'S1')).toBe('purdex-iface-lead')
    expect(titleOf('h1', 'S2')).toBeNull() // the row carried none
    useRootCacheStore.getState().ingest('h1', [q('S2', 'R2')])
    expect(titleOf('h1', 'S1')).toBe('purdex-iface-lead') // a session no row lists keeps its last title
    useRootCacheStore.getState().ingest('h1', [{ ...q('S1', 'ROOT'), title: undefined }])
    expect(titleOf('h1', 'S1')).toBeNull() // a listed row without one has none now
    useRootCacheStore.getState().dropHost('h1')
    expect(titleOf('h1', 'S1')).toBeNull()
  })

  it('dropHost and prune take the addresses with the roots', () => {
    useRootCacheStore.getState().ingest('h1', [q('S1', 'ROOT'), q('S2', 'R2')])
    useRootCacheStore.getState().prune((_h, id) => id === 'S1')
    expect(addressOf('h1', 'S2')).toBeNull()
    expect(addressOf('h1', 'S1')).toBe('mlab/S1')
    useRootCacheStore.getState().dropHost('h1')
    expect(addressOf('h1', 'S1')).toBeNull()
  })

  it('a cache persisted before addresses existed loads with none', async () => {
    useRootCacheStore.persist.setOptions({ name: useRootCacheStore.persist.getOptions().name })
    const merged = useRootCacheStore.persist.getOptions().merge!({ roots: { h1: { S1: 'ROOT' } } }, useRootCacheStore.getState())
    expect(merged.roots).toEqual({ h1: { S1: 'ROOT' } })
    expect(merged.addresses).toEqual({})
    expect(merged.titles).toEqual({})
  })
})
