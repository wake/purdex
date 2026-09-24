// spa/src/lib/profile/hosts-retired.integration.test.ts — host ownership H3a-2 (spec §5.1, §5.3): `hosts` has left
// the SYNC LOOP. The real start layer, lease, collector, executor, section store, apply-to-stores and zustand stores;
// only the network (`./api`, served by the in-memory daemon), the digest and `shapeTable` are replaced.
//
// The SOT holds a legacy `hosts` row with a token in it, written by an older client. Through every path that goes
// through the executor or `attachMaster` / `detachMaster`, this client never GETs, PUTs or DELETEs it; the row's
// rev / hash / payload are byte-identical at the end, and no token reaches this device or any section it writes.
//
// SCOPE: the sync loop only. The wizard still reads the row itself (`wizard-run.ts`, the removal preview) until
// H3b, whose own end-to-end test completes spec §5.3's list.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { deleteHostCascade } from '../host-lifecycle'
import { hashSection } from './hash'
import { clearSectionStore, loadSectionStore } from './section-store'
import { __resetProfileSyncForTest, attachMaster, detachMaster, profileSyncState, requestResolve, startProfileSync } from './start'
import { masterTagOf } from './sync-status'
import { FakeDaemon } from './test-fake-daemon'
import type { FakeRow } from './test-fake-daemon'

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  // FNV-1a under eight seeds: deterministic, synchronous, 64 lower-case hex (as start.integration.test.ts).
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
const OLD = 'c_oooooooooooo'
const OTHER = 'c_bbbbbbbbbbbb'
/** What an older client stored in the SOT's `hosts` row. It must never reach this device. */
const LEGACY_TOKEN = 'legacy-secret-token'

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

const settle = async (): Promise<void> => {
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
}

let daemon: FakeDaemon
let stop: () => void = () => {}
/** The legacy row as it was seeded — compared field by field at the end of every case. */
let legacy: FakeRow

/** The SOT's `hosts` row, written by an older client: every host of THAT device, a token included. */
async function seedLegacyHosts(): Promise<void> {
  const payload = { hosts: { [M]: { name: 'named-by-old', ip: '10.0.0.1', port: 7860, token: LEGACY_TOKEN, order: 0 }, 'host-old-only': { name: 'old-only', ip: '10.0.0.9', port: 7860, token: LEGACY_TOKEN, order: 1 } }, hostOrder: [M, 'host-old-only'] }
  daemon.rows.set('hosts', { rev: 3, hash: await hashSection(payload), payload, fingerprint: 'fp-hosts', ordinal: 1, writer: OLD })
  legacy = structuredClone(daemon.rows.get('hosts')!)
}

/** Another client writes `key`: the SOT's payload with `value` at `path`, a new rev, the payload's real hash. */
async function writtenElsewhere(key: string, path: string[], value: unknown): Promise<void> {
  const cur = daemon.rows.get(key)!
  const payload = JSON.parse(JSON.stringify(cur.payload)) as Record<string, unknown>
  let at = payload
  for (const step of path.slice(0, -1)) at = at[step] as Record<string, unknown>
  at[path[path.length - 1]] = value
  daemon.rows.set(key, { ...cur, rev: cur.rev + 1, hash: await hashSection(payload), payload, writer: OTHER })
}

/** The master host goes away and comes back: the executor reindexes (start.ts: `onReconnected`). */
async function reconnect(): Promise<void> {
  useHostStore.getState().setRuntime(M, { status: 'disconnected' })
  await settle()
  useHostStore.getState().setRuntime(M, { status: 'connected' })
  await settle()
}

/** Attached with `direction`, the first reconciliation over. */
async function attached(direction: 'push' | 'pull'): Promise<void> {
  expect(await attachMaster(M, PROFILE, direction)).toEqual({ ok: true })
  await settle()
  expect(profileSyncState().status?.profile).toBe('synced')
}

/** Every request the sync loop made that named a section, as `op:key`. */
function requests(): string[] {
  return [
    ...api.getSection.mock.calls.map((c) => `get:${c[2]}`),
    ...api.putSection.mock.calls.map((c) => `put:${c[2]}`),
    ...api.deleteSection.mock.calls.map((c) => `delete:${c[2]}`),
  ]
}

/** Spec §5.3 for the sync loop: nothing named `hosts`, the row intact, no host list or token anywhere it could travel. */
function expectHostsUntouched(): void {
  expect(requests().filter((r) => r.endsWith(':hosts'))).toEqual([])
  expect(daemon.writes.filter((w) => w.key === 'hosts')).toEqual([])
  expect(daemon.rows.get('hosts')).toEqual(legacy)
  expect(profileSyncState().status?.sections ?? {}).not.toHaveProperty('hosts')
  expect(loadSectionStore(PROFILE).sections).not.toHaveProperty('hosts')
  // (9) no PUT payload of any section carries a host list or a token — hosts appear only as references
  for (const [, , key, body] of api.putSection.mock.calls) {
    const text = JSON.stringify(body.payload)
    expect(text, key).not.toContain('"token"')
    expect(text, key).not.toContain(LEGACY_TOKEN)
    expect(text, key).not.toContain('"hostOrder"')
  }
  // the legacy row's content reached nothing on this device: not the host store, not the section store's stash
  for (let i = 0; i < localStorage.length; i += 1) {
    const k = localStorage.key(i)!
    expect(localStorage.getItem(k) ?? '', k).not.toContain(LEGACY_TOKEN)
  }
  expect(useHostStore.getState().hosts).not.toHaveProperty('host-old-only')
  expect(useHostStore.getState().hosts[M]?.name).not.toBe('named-by-old')
}

beforeEach(async () => {
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
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, pendingPullHosts: null, attachGeneration: 0, attachId: null, masterEndpoint: null, suspension: null, pendingDetaches: [] })
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2], activeHostId: M, runtime: { [M]: { status: 'connected' } } })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [{ id: 'ws1', name: 'WS1', tabs: [], activeTabId: null }], activeWorkspaceId: 'ws1' })
  useLayoutStore.setState({ tabPosition: 'top' })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  await seedLegacyHosts()
  stop = startProfileSync()
})

afterEach(() => {
  stop()
  stop = () => {}
  clearSectionStore()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('the sync loop never reads, writes or deletes the SOT `hosts` row (host ownership H3a-2, spec §5.3)', () => {
  it('(1) push attach: everything else is written, `hosts` is left as the old client wrote it', async () => {
    await attached('push')
    expect(daemon.live()).toEqual(['hosts', 'settings', 'tabs.ws1', 'workspaces'])
    expect(requests()).toEqual(expect.arrayContaining(['put:settings', 'put:workspaces', 'put:tabs.ws1']))
    expectHostsUntouched()
  })

  it('(2) pull attach: every other section is pulled, `hosts` is not — the host list stays this device\'s', async () => {
    await attached('push')
    await writtenElsewhere('settings', ['purdex-layout', 'tabPosition'], 'both')
    await writtenElsewhere('workspaces', ['workspaces', 'ws1', 'name'], 'Renamed elsewhere')
    const hostsBefore = useHostStore.getState().hosts
    const writes = daemon.writes.length
    vi.clearAllMocks() // `requests()` counts from here — the api mocks keep their implementations

    await attached('pull')
    expect(new Set(api.getSection.mock.calls.map((c) => c[2]))).toEqual(new Set(['settings', 'tabs.ws1', 'workspaces']))
    expect(useLayoutStore.getState().tabPosition).toBe('both')
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Renamed elsewhere')
    expect(daemon.writes.slice(writes)).toEqual([])
    expect(useHostStore.getState().hosts).toBe(hostsBefore) // not even replaced by an equal copy
    expectHostsUntouched()
  })

  describe('a `settings` conflict, answered from the page', () => {
    /** Attached; then, offline, this device edits `settings` while another client writes it: `locked:conflict`. */
    async function conflicted(): Promise<void> {
      await attached('push')
      useHostStore.getState().setRuntime(M, { status: 'disconnected' })
      await settle()
      useLayoutStore.setState({ tabPosition: 'left' })
      await writtenElsewhere('settings', ['purdex-layout', 'tabPosition'], 'both')
      await settle()
      useHostStore.getState().setRuntime(M, { status: 'connected' })
      await settle()
      expect(profileSyncState().status?.sections.settings).toBe('locked:conflict')
    }
    const resolve = (keep: 'local' | 'sot'): boolean =>
      requestResolve('settings', keep, profileSyncState().status!.locks.settings, masterTagOf({ hostId: M, profileId: PROFILE }, useProfileStore.getState().attachGeneration))

    it('(3) keep local (the restore-local path): this device\'s settings go back to the host; `hosts` is not touched', async () => {
      await conflicted()
      expect(resolve('local')).toBe(true)
      await settle()
      expect(profileSyncState().status?.profile).toBe('synced')
      expect(daemon.rows.get('settings')!.writer).toBe('c_aaaaaaaaaaaa')
      expect(useLayoutStore.getState().tabPosition).toBe('left')
      expectHostsUntouched()
    })

    it('(4) keep the SOT\'s: the host\'s settings land here; `hosts` is not touched', async () => {
      await conflicted()
      expect(resolve('sot')).toBe(true)
      await settle()
      expect(profileSyncState().status?.profile).toBe('synced')
      expect(useLayoutStore.getState().tabPosition).toBe('both')
      expect(daemon.rows.get('settings')!.writer).toBe(OTHER)
      expectHostsUntouched()
    })
  })

  it('(5) push, first reconciliation, with an unrendered tabs.* on the SOT: the orphan sweep deletes `tabs.ghost`, never `hosts`', async () => {
    daemon.rows.set('tabs.ghost', { rev: 2, hash: await hashSection({ order: ['g'], tabs: { g: {} } }), payload: { order: ['g'], tabs: { g: {} } }, fingerprint: 'fp-tabs', ordinal: 1, writer: OTHER })
    await attached('push')
    expect(daemon.writes.filter((w) => w.op === 'delete').map((w) => [w.key, w.outcome])).toEqual([['tabs.ghost', 'applied']])
    expect(daemon.live()).toEqual(['hosts', 'settings', 'tabs.ws1', 'workspaces'])
    expectHostsUntouched()
  })

  it('(6) a pull of `settings`, then its push: neither names `hosts`', async () => {
    await attached('push')
    await writtenElsewhere('settings', ['purdex-layout', 'tabPosition'], 'both')
    vi.clearAllMocks()
    await reconnect() // the index shows `settings` moved: pulled
    expect(useLayoutStore.getState().tabPosition).toBe('both')
    useLayoutStore.setState({ tabPosition: 'left' }) // …and edited here: pushed
    await settle()
    expect(requests()).toEqual(expect.arrayContaining(['get:settings', 'put:settings']))
    expect(daemon.rows.get('settings')!.writer).toBe('c_aaaaaaaaaaaa')
    expectHostsUntouched()
  })

  it('(7) "stop sync, keep local" (detachMaster): `hosts` stays on the SOT', async () => {
    await attached('push')
    expect(await detachMaster()).toEqual({ ok: true })
    await settle()
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(api.deleteSection).not.toHaveBeenCalled()
    expectHostsUntouched()
  })

  it('(8) a device-local host add, rename and delete: no request names `hosts`, and the sync leaves the host store alone', async () => {
    await attached('push')
    vi.clearAllMocks()

    const added = useHostStore.getState().addHost({ name: 'added-here', ip: '10.0.0.3', port: 7860, token: 'tok-3' })
    await settle()
    useHostStore.getState().updateHost(H2, { name: 'renamed-here' })
    await settle()
    deleteHostCascade(H2)
    await settle()
    const afterEdits = useHostStore.getState().hosts
    await reconnect()
    await vi.advanceTimersByTimeAsync(60_000)

    expect(useHostStore.getState().hosts).toBe(afterEdits) // nothing the sync did replaced it
    expect(Object.keys(afterEdits).sort()).toEqual([M, added].sort())
    expect(requests().filter((r) => r.startsWith('put:') || r.startsWith('delete:'))).not.toContain('put:hosts')
    expect(profileSyncState().status?.profile).toBe('synced')
    expectHostsUntouched()
  })
})
