// spa/src/lib/profile/start.integration.test.ts — `attachMaster` end to end:
// the real start layer, lease, collector, executor, section store,
// apply-to-stores and zustand stores; only the network (`./api`, served by the
// in-memory daemon), the digest and `shapeTable` are replaced.
//
// These are the sequences the PR review found (start.ts, EVERY ATTACH IS A NEW
// ONE): they cross the start layer and the executor, so neither side's unit
// tests can pin them.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { clearSectionStore, loadSectionStore } from './section-store'
import { __resetProfileSyncForTest, attachMaster, profileSyncState, startProfileSync } from './start'
import { FakeDaemon } from './test-fake-daemon'

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  // FNV-1a under eight seeds: deterministic, synchronous, 64 lower-case hex.
  const hex64 = (text: string): string => {
    let out = ''
    for (let seed = 0; seed < 8; seed += 1) {
      let x = (0x811c9dc5 ^ Math.imul(seed + 1, 0x9e3779b1)) >>> 0
      for (let i = 0; i < text.length; i += 1) x = Math.imul(x ^ text.charCodeAt(i), 0x01000193) >>> 0
      out += x.toString(16).padStart(8, '0')
    }
    return out
  }
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => hex64(actual.structuralKey(payload))) }
})

vi.mock('./api', () => ({
  listProfiles: vi.fn(),
  getSection: vi.fn(),
  putSection: vi.fn(),
  deleteSection: vi.fn(),
  putAttachment: vi.fn(),
  deleteAttachment: vi.fn(),
}))

vi.mock('./projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./projections')>()
  return { ...actual, shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })) }
})

vi.mock('../client-identity', () => ({ getClientId: () => 'c_aaaaaaaaaaaa', isClientIdPersisted: () => true }))

const api = vi.mocked(await import('./api'))

const M = 'host-master'
const H2 = 'host-two'
const PROFILE = 'p_0123456789ab'

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

function renameH2(name: string): void {
  const { hosts } = useHostStore.getState()
  useHostStore.setState({ hosts: { ...hosts, [H2]: { ...hosts[H2], name } } })
}

const h2Name = (): string => useHostStore.getState().hosts[H2].name
const settle = async (): Promise<void> => {
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
}

let daemon: FakeDaemon
let stop: () => void = () => {}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  __resetProfileSyncForTest()
  daemon = new FakeDaemon(PROFILE)
  vi.clearAllMocks()
  api.listProfiles.mockImplementation(async () => daemon.list())
  api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
  api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
  api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
  api.putAttachment.mockResolvedValue({ kind: 'ok', value: { attached: true } })
  api.deleteAttachment.mockResolvedValue({ kind: 'ok', value: { detached: true } })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, attachGeneration: 0, masterEndpoint: null, suspension: null })
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2], activeHostId: M, runtime: { [M]: { status: 'connected' } } })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  stop()
  stop = () => {}
  clearSectionStore()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

/** Attached with `push`, everything on the daemon, the first reconciliation over. */
async function attachedAndSettled(): Promise<void> {
  stop = startProfileSync()
  expect(await attachMaster(M, PROFILE, 'push')).toEqual({ ok: true })
  await settle()
  expect(daemon.live()).toEqual(['hosts', 'settings', 'workspaces'])
  expect(profileSyncState().status?.profile).toBe('synced')
  expect(useProfileStore.getState().pendingDirection).toBeNull()
}

describe('attachMaster, again, to the same master', () => {
  it('ATTACK B — everything is already in sync: the new direction is cleared again, and a conflict hours later is the user\'s', async () => {
    await attachedAndSettled()
    const writes = daemon.writes.length

    expect(await attachMaster(M, PROFILE, 'push')).toEqual({ ok: true })
    expect(useProfileStore.getState().pendingDirection).toBe('push')
    await settle()
    expect(useProfileStore.getState().pendingDirection).toBeNull() // not left behind
    expect(daemon.writes.slice(writes).filter((w) => w.outcome === 'applied')).toEqual([]) // nothing had to be written
    expect(profileSyncState().status?.profile).toBe('synced')

    // hours later: another client moves `hosts` while this one edits it
    await vi.advanceTimersByTimeAsync(3 * 3_600_000)
    const row = daemon.rows.get('hosts')!
    daemon.rows.set('hosts', { ...row, rev: row.rev + 1, hash: 'f'.repeat(64), writer: 'c_bbbbbbbbbbbb' })
    renameH2('edited-here')
    await settle()
    expect(profileSyncState().status?.sections.hosts).toBe('locked:conflict')
    expect(h2Name()).toBe('edited-here')
    expect(daemon.rows.get('hosts')!.writer).toBe('c_bbbbbbbbbbbb')
  })

  it('R1 — `pull` while a local edit is unsent and the SOT has not moved: the SOT wins, the daemon is not written', async () => {
    await attachedAndSettled()
    const onTheSot = h2Name()

    // offline: the edit stays home
    useHostStore.getState().setRuntime(M, { status: 'disconnected' })
    renameH2('edited-offline')
    await settle()
    expect(loadSectionStore(PROFILE).sections.hosts.currentHash).not.toBe(loadSectionStore(PROFILE).sections.hosts.base.hash)
    const revs = daemon.revs()
    const writes = daemon.writes.length

    expect(await attachMaster(M, PROFILE, 'pull')).toEqual({ ok: true })
    useHostStore.getState().setRuntime(M, { status: 'connected' })
    await settle()

    expect(h2Name()).toBe(onTheSot)
    expect(daemon.writes.slice(writes)).toEqual([])
    expect(daemon.revs()).toEqual(revs)
    expect(profileSyncState().status?.profile).toBe('synced')
    expect(useProfileStore.getState().pendingDirection).toBeNull()
  })
})

describe('the attachment comes first', () => {
  it('not one profile request goes out while the attachment PUT is unanswered — a reload or a lease takeover included', async () => {
    await attachedAndSettled()
    stop() // the window goes away…
    vi.clearAllMocks()
    let release: () => void = () => {}
    api.putAttachment.mockReturnValue(new Promise((r) => (release = () => r({ kind: 'ok', value: { attached: true } }))))
    renameH2('edited-while-closed')

    stop = startProfileSync() // …and comes back: master in the store, bases in the section store, a dirty section
    await settle()
    expect(api.putAttachment).toHaveBeenCalledTimes(1)
    expect(api.listProfiles).not.toHaveBeenCalled()
    expect(api.getSection).not.toHaveBeenCalled()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.deleteSection).not.toHaveBeenCalled()

    release()
    await settle()
    expect(api.listProfiles).toHaveBeenCalled()
    expect((daemon.rows.get('hosts')!.payload as { hosts: Record<string, { name: string }> }).hosts[H2].name).toBe('edited-while-closed')
  })
})

describe('the master host is re-pointed in place', () => {
  it('not one request reaches the new address with the old daemon\'s bases', async () => {
    await attachedAndSettled()
    vi.clearAllMocks()
    const { hosts } = useHostStore.getState()
    useHostStore.setState({ hosts: { ...hosts, [M]: { ...hosts[M], ip: '10.9.9.9' } } }) // also an edit of the `hosts` section
    renameH2('edited-after')
    await settle()
    await vi.advanceTimersByTimeAsync(120_000)
    for (const fn of Object.values(api)) expect(fn).not.toHaveBeenCalled()
    expect(profileSyncState().blocked).toBe('master-endpoint-changed')
  })
})

describe('the old driver stands still while an attach is being made', () => {
  /** Attached and settled; then an edit made offline — dirty, unsent, the SOT not moved. */
  async function dirtyAndOffline(): Promise<{ onTheSot: string; writes: number; revs: Record<string, number> }> {
    await attachedAndSettled()
    const onTheSot = h2Name()
    useHostStore.getState().setRuntime(M, { status: 'disconnected' })
    renameH2('edited-offline')
    await settle()
    return { onTheSot, writes: daemon.writes.length, revs: daemon.revs() }
  }

  it('C-1 — `pull`, the attachment PUT takes its time, the host comes back meanwhile: the old driver pushes NOTHING; then the SOT wins', async () => {
    const before = await dirtyAndOffline()
    let release: () => void = () => {}
    vi.clearAllMocks()
    api.putAttachment.mockReturnValueOnce(new Promise((r) => (release = () => r({ kind: 'ok', value: { attached: true } }))))
    const attaching = attachMaster(M, PROFILE, 'pull')
    await settle()
    useHostStore.getState().setRuntime(M, { status: 'connected' }) // the old driver's cue to flush its dirty section
    await settle()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(daemon.writes.slice(before.writes)).toEqual([])
    for (const fn of [api.listProfiles, api.getSection, api.putSection, api.deleteSection]) expect(fn).not.toHaveBeenCalled()

    release()
    expect(await attaching).toEqual({ ok: true })
    await settle()
    expect(h2Name()).toBe(before.onTheSot)
    expect(daemon.writes.slice(before.writes)).toEqual([])
    expect(daemon.revs()).toEqual(before.revs)
    expect(profileSyncState().status?.profile).toBe('synced')
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('the attach fails: the old mode is back and carries on with what it had — the edit goes out after all', async () => {
    const before = await dirtyAndOffline()
    api.putAttachment.mockResolvedValueOnce({ kind: 'failed', reason: 'server', status: 500, message: 'nope' })
    expect(await attachMaster(M, PROFILE, 'pull')).toEqual({ ok: false, reason: 'server' })
    expect(useProfileStore.getState().suspension).toBeNull()
    useHostStore.getState().setRuntime(M, { status: 'connected' })
    await settle()
    expect(daemon.writes.slice(before.writes).map((w) => [w.key, w.outcome])).toEqual([['hosts', 'applied']])
    expect(h2Name()).toBe('edited-offline')
  })
})

