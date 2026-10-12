// spa/src/lib/team/host-roots.test.ts — XR-3: the wire parse, the input routing, and the per-lead-host read / write rules.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { useHostStore } from '../../stores/useHostStore'
import {
  changeHostRoot, classifyRootInput, retainHostRootsPoll, configureHostRoots, dismissHostRootChange, hasPending, inUseWho, parseHostRoots, refreshHostRoots,
  resetHostRoots, useHostRootsStore, viewOf, type PairedHostRoots,
} from './host-roots'

const host = (over: Partial<PairedHostRoots> = {}): PairedHostRoots => ({ hostId: 'h-air', alias: 'air26', status: 'ok', roots: [], changes: [], ...over })
const wire = (over: Record<string, unknown> = {}) => ({ host_id: 'h-air', alias: 'air26', status: 'ok', roots: ['/a'], rev: 1, seen_at: 1, changes: [], ...over })
const change = (over: Record<string, unknown> = {}) => ({ id: 'c1', host_id: 'h-air', op: 'add', path: '/x', state: 'pending', created_at: 1, updated_at: 1, ...over })

describe('parseHostRoots', () => {
  it('reads {hosts:[…]}', () => {
    const r = parseHostRoots({ hosts: [wire({ changes: [change(), change({ id: 'c2', state: 'refused', code: 'roots_in_use', detail: 'x' })] })] })
    expect(r).toEqual([{ hostId: 'h-air', alias: 'air26', status: 'ok', roots: ['/a'], changes: [
      { id: 'c1', op: 'add', path: '/x', state: 'pending', code: '', detail: '' },
      { id: 'c2', op: 'add', path: '/x', state: 'refused', code: 'roots_in_use', detail: 'x' },
    ] }])
  })
  it('a wrong outer shape is null', () => {
    expect(parseHostRoots([wire()])).toBeNull()
    expect(parseHostRoots({ hosts: 'x' })).toBeNull()
    expect(parseHostRoots(null)).toBeNull()
  })
  it('drops malformed hosts and changes; an unknown status is unknown; an unknown change state is dropped', () => {
    const r = parseHostRoots({ hosts: [wire({ alias: '' }), 7, wire({ status: 'weird', roots: ['/a', 3, ''], changes: [change({ state: 'done' }), change({ op: 'zap', id: 'c3' }), change({ id: '' }), change({ id: 'ok' })] })] })
    expect(r).toHaveLength(1)
    expect(r![0].status).toBe('unknown')
    expect(r![0].roots).toEqual(['/a'])
    expect(r![0].changes.map((c) => c.id)).toEqual(['ok'])
  })
})

describe('classifyRootInput', () => {
  const hosts = [host()]
  it('alias:/path (any case) is a grant', () => {
    expect(classifyRootInput('air26:/x', hosts)).toMatchObject({ kind: 'remote', path: '/x' })
    expect(classifyRootInput(' AIR26:/x ', hosts)).toMatchObject({ kind: 'remote', path: '/x' })
  })
  it('a plain path is local', () => { expect(classifyRootInput('/x', hosts)).toEqual({ kind: 'local' }) })
  it('x:/… with no such host has its own refusal', () => {
    expect(classifyRootInput('foo:/x', hosts)).toEqual({ kind: 'unknown_host', name: 'foo' })
  })
  it('alias:relative is not absolute', () => { expect(classifyRootInput('air26:x', hosts)).toEqual({ kind: 'not_absolute' }) })
  it('without the host list (no routes / not read) nothing is routed', () => {
    expect(classifyRootInput('air26:/x', null)).toEqual({ kind: 'local' })
    expect(classifyRootInput('foo:/x', null)).toEqual({ kind: 'local' })
  })
  it('a name with a slash before the colon is a path', () => { expect(classifyRootInput('/a/b:c', hosts)).toEqual({ kind: 'local' }) })
})

describe('inUseWho', () => {
  it('title（ref）, else the ref, else 8 characters of the op id', () => {
    expect(inUseWho({ title: 'T', ref: '_r', opId: 'o' })).toBe('T（_r）')
    expect(inUseWho({ title: '', ref: '_r', opId: 'o' })).toBe('_r')
    expect(inUseWho({ title: '', ref: '', opId: '0123456789abcdef' })).toBe('01234567')
  })
})

const deferred = <T,>() => { let resolve!: (v: T) => void; let reject!: (e: unknown) => void; const promise = new Promise<T>((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const L = 'lead-1'

beforeEach(() => {
  resetHostRoots()
  configureHostRoots({ identity: () => 'id-1' })
})

describe('refreshHostRoots', () => {
  it('stores the view; one request per lead host at a time', async () => {
    const d = deferred<PairedHostRoots[]>()
    const fetch = vi.fn(() => d.promise)
    configureHostRoots({ fetch })
    const a = refreshHostRoots(L)
    const b = refreshHostRoots(L)
    expect(fetch).toHaveBeenCalledTimes(1)
    d.resolve([host()])
    await Promise.all([a, b])
    expect(viewOf(useHostRootsStore.getState(), L)).toMatchObject({ load: 'ok', hosts: [host()] })
  })
  it('404 hides the feature (unsupported)', async () => {
    configureHostRoots({ fetch: async () => 'unsupported' })
    await refreshHostRoots(L)
    expect(viewOf(useHostRootsStore.getState(), L).load).toBe('unsupported')
  })
  it('an answer for a lead host that was re-pointed meanwhile is dropped', async () => {
    const d = deferred<PairedHostRoots[]>()
    let id = 'id-1'
    configureHostRoots({ fetch: () => d.promise, identity: () => id })
    const p = refreshHostRoots(L)
    id = 'id-2'
    d.resolve([host()])
    await p
    expect(viewOf(useHostRootsStore.getState(), L).load).toBe('unknown')
  })
  it('a failed read keeps what was shown', async () => {
    configureHostRoots({ fetch: async () => [host()] })
    await refreshHostRoots(L)
    configureHostRoots({ fetch: async () => { throw new Error('down') } })
    await refreshHostRoots(L)
    expect(viewOf(useHostRootsStore.getState(), L).hosts).toEqual([host()])
  })
  it('hasPending sees only pending changes', () => {
    expect(hasPending([host({ changes: [{ id: 'a', op: 'add', path: '/x', state: 'refused', code: '', detail: '' }] })])).toBe(false)
    expect(hasPending([host({ changes: [{ id: 'a', op: 'add', path: '/x', state: 'pending', code: '', detail: '' }] })])).toBe(true)
  })
})

describe('changeHostRoot', () => {
  it('posts then re-reads; no second write while one is out', async () => {
    const d = deferred<void>()
    const post = vi.fn(() => d.promise)
    const fetch = vi.fn(async () => [host({ roots: ['/x'] })])
    configureHostRoots({ post, fetch })
    const first = changeHostRoot(L, host(), 'add', '/x')
    const second = await changeHostRoot(L, host(), 'add', '/y')
    expect(second.ok).toBe(false)
    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith(L, 'air26', 'add', '/x')
    d.resolve()
    expect((await first).ok).toBe(true)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(viewOf(useHostRootsStore.getState(), L)).toMatchObject({ writing: false, hosts: [{ roots: ['/x'] }] })
  })
  it('a read that was out when the write finished is followed by one more', async () => {
    const first = deferred<PairedHostRoots[]>()
    const fetch = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue([host({ roots: ['/new'] })])
    configureHostRoots({ post: async () => {}, fetch })
    const pending = refreshHostRoots(L)
    const w = changeHostRoot(L, host(), 'add', '/new')
    await Promise.resolve(); await Promise.resolve()
    first.resolve([host()])
    await Promise.all([pending, w])
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    await vi.waitFor(() => expect(viewOf(useHostRootsStore.getState(), L).hosts[0]?.roots).toEqual(['/new']))
  })
  it('409 roots_in_use on a remove lands on that row with who; the view is re-read', async () => {
    const fetch = vi.fn(async () => [host({ roots: ['/x'] })])
    configureHostRoots({
      fetch,
      post: async () => { throw new ApprovalApiError(409, 'roots_in_use', '', null, { error: 'roots_in_use', uses: [{ title: 'T', ref: '_r', cwd: '/x/y' }, { op_id: 'abcdefghij', cwd: '/x' }] }) },
    })
    const r = await changeHostRoot(L, host({ roots: ['/x'] }), 'remove', '/x')
    expect(r).toEqual({ ok: false, error: { kind: 'in_use', who: ['T（_r）', 'abcdefgh'] } })
    expect(viewOf(useHostRootsStore.getState(), L).rowError).toEqual({ hostKey: 'h-air', path: '/x', error: { kind: 'in_use', who: ['T（_r）', 'abcdefgh'] } })
    expect(fetch).toHaveBeenCalled()
  })
  it('the next action clears the row error', async () => {
    configureHostRoots({ fetch: async () => [host()], post: async () => { throw new ApprovalApiError(409, 'roots_in_use', '', null, { uses: [] }) } })
    await changeHostRoot(L, host(), 'remove', '/x')
    configureHostRoots({ post: async () => {} })
    const d = deferred<void>()
    configureHostRoots({ post: () => d.promise })
    const p = changeHostRoot(L, host(), 'add', '/y')
    expect(viewOf(useHostRootsStore.getState(), L).rowError).toBeNull()
    d.resolve(); await p
  })
  it('400 shapes: unknown_host, bad_root (detail), not_a_root', async () => {
    configureHostRoots({ fetch: async () => [host()] })
    configureHostRoots({ post: async () => { throw new ApprovalApiError(400, 'unknown_host') } })
    expect(await changeHostRoot(L, host(), 'add', '/x')).toEqual({ ok: false, error: { kind: 'unknown_host', name: 'air26' } })
    configureHostRoots({ post: async () => { throw new ApprovalApiError(400, 'bad_root', 'not_absolute') } })
    expect(await changeHostRoot(L, host(), 'add', '/x')).toEqual({ ok: false, error: { kind: 'bad_root', detail: 'not_absolute' } })
    configureHostRoots({ post: async () => { throw new ApprovalApiError(400, 'not_a_root') } })
    expect(await changeHostRoot(L, host(), 'remove', '/x')).toEqual({ ok: false, error: { kind: 'not_a_root' } })
  })
})

describe('a write and the reads around it', () => {
  it('a read that began before the write finished does not commit; a failed follow-up leaves the view dirty (polling) until one lands', async () => {
    const old = deferred<PairedHostRoots[]>()
    const fetch = vi.fn().mockReturnValueOnce(old.promise).mockRejectedValueOnce(new Error('down')).mockResolvedValue([host({ roots: ['/new'] })])
    configureHostRoots({ post: async () => {}, fetch })
    const first = refreshHostRoots(L)
    await changeHostRoot(L, host(), 'add', '/new')
    old.resolve([host()]) // the pre-write answer: dropped, never shown
    await first
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2)) // the follow-up (fails)
    expect(viewOf(useHostRootsStore.getState(), L)).toMatchObject({ load: 'unknown', dirty: true })
    await refreshHostRoots(L) // the next (poll) read lands
    expect(viewOf(useHostRootsStore.getState(), L)).toMatchObject({ load: 'ok', dirty: false, hosts: [{ roots: ['/new'] }] })
  })
  it('a remove error stays on the row when the host is renamed meanwhile (keyed by host id)', async () => {
    configureHostRoots({
      fetch: async () => [host({ alias: 'renamed', roots: ['/x'] })],
      post: async () => { throw new ApprovalApiError(409, 'roots_in_use', '', null, { uses: [{ ref: '_r' }] }) },
    })
    await changeHostRoot(L, host({ roots: ['/x'] }), 'remove', '/x')
    expect(viewOf(useHostRootsStore.getState(), L).rowError).toMatchObject({ hostKey: 'h-air', path: '/x' })
  })
})

describe('the poll of a shown editor', () => {
  it('a lead host re-pointed to another daemon drops the old view and reads the new one, even with nothing pending', async () => {
    let id = 'id-1'
    const fetch = vi.fn(async () => (id === 'id-1' ? [host({ alias: 'old' })] : [host({ alias: 'new' })]))
    configureHostRoots({ fetch, identity: () => id })
    const release = retainHostRootsPoll(L)
    await vi.waitFor(() => expect(viewOf(useHostRootsStore.getState(), L).hosts[0]?.alias).toBe('old'))
    id = 'id-2'
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts } }) // any host-store change makes the poll look
    await vi.waitFor(() => expect(viewOf(useHostRootsStore.getState(), L).hosts[0]?.alias).toBe('new'))
    release()
  })
  it('keeps polling while the view is dirty (a write finished, its re-read failed), then stops', async () => {
    vi.useFakeTimers()
    try {
      const fetch = vi.fn().mockRejectedValueOnce(new Error('down')).mockResolvedValue([host()])
      configureHostRoots({ post: async () => {}, fetch })
      const release = retainHostRootsPoll(L) // first read: fails
      await vi.advanceTimersByTimeAsync(0)
      await changeHostRoot(L, host(), 'add', '/x') // post ok, the re-read is the 2nd fetch → lands, so force the dirty state
      useHostRootsStore.getState().patch(L, { dirty: true })
      const n = fetch.mock.calls.length
      await vi.advanceTimersByTimeAsync(10_000)
      expect(fetch.mock.calls.length).toBe(n + 1)
      expect(viewOf(useHostRootsStore.getState(), L).dirty).toBe(false)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(fetch.mock.calls.length).toBe(n + 1)
      release()
    } finally { vi.useRealTimers() }
  })
})

describe('dismissHostRootChange', () => {
  it('404 and 409 are quiet and still re-read', async () => {
    const fetch = vi.fn(async () => [host()])
    configureHostRoots({ fetch, dismiss: async () => { throw new ApprovalApiError(404, 'no_such_change') } })
    await dismissHostRootChange(L, 'c1')
    configureHostRoots({ dismiss: async () => { throw new ApprovalApiError(409, 'pending') } })
    await dismissHostRootChange(L, 'c1')
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(viewOf(useHostRootsStore.getState(), L).writing).toBe(false)
  })
})
