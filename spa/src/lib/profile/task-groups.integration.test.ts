// spa/src/lib/profile/task-groups.integration.test.ts — the manual task groups through Profile Sync, end to end (task
// groups spec §4.1 / §4.4, TG-1a-2 tasks 2–3): the REAL collector, section store, apply-to-stores, executor and zustand
// stores on this side, an in-memory daemon with P1's compare-and-set semantics on the other (style of
// tabs-local-only.integration.test.ts and executor.direction.integration.test.ts). One process plays every device;
// a device "leaves" by clearing its storage. Only the network, the digest and `shapeTable` are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { useManualGroupsStore, type ManualGroup } from '../../stores/useManualGroupsStore'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import type { ProfileSectionKey } from './types'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { clearSectionStore } from './section-store'
import { PROJECTIONS, SECTION_SCHEMA_ORDINAL, fingerprintOf, sectionFingerprint } from './projections'
import { syncIdOfSync } from './host-identity'
import { FakeDaemon, type FakeRow as Row } from './test-fake-daemon'

const h = vi.hoisted(() => ({
  clientId: 'c_aaaaaaaaaaaa',
  shape: null as null | Record<'hosts' | 'settings' | 'workspaces' | 'tabs', [string, number]>,
}))

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

vi.mock('./api', () => ({ listProfiles: vi.fn(), getSection: vi.fn(), putSection: vi.fn(), deleteSection: vi.fn() }))

vi.mock('./projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./projections')>()
  return { ...actual, shapeTable: vi.fn(async () => h.shape ?? { hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] }) }
})

vi.mock('../client-identity', () => ({ getClientId: () => h.clientId, isClientIdPersisted: () => true }))

const api = vi.mocked(await import('./api'))

const PROFILE = 'p_0123456789ab'
const A = 'c_aaaaaaaaaaaa'
const B = 'c_bbbbbbbbbbbb'
const OLD = 'c_cccccccccccc'
const DAEMON = 'mini-lab:278cbm'
const WIRE = syncIdOfSync(DAEMON)
const TG = 'purdex-task-groups'

/* ─── the devices: the same daemon under a different local host id on each ─── */

function host(id: string): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, daemonId: DAEMON }
}

function device(localHost: string, groups: Record<string, ManualGroup> = {}): void {
  useHostStore.setState({ hosts: { [localHost]: host(localHost) }, hostOrder: [localHost], activeHostId: localHost, runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useManualGroupsStore.setState({ order: Object.keys(groups), groups })
}

const group = (members: ManualGroup['members'], name = 'G'): ManualGroup => ({ name, members, createdAt: 1 })
const store = () => useManualGroupsStore.getState()
const sotGroups = (): Record<string, { members: Array<{ host: string; root: string }> }> => ((daemon.rows.get('settings')?.payload as Record<string, { groups?: never }>)[TG]?.groups ?? {}) as never

let daemon: FakeDaemon
let executor: Executor | null = null
let collector: Collector | null = null
const problems: Array<{ kind: string; section?: string; detail: string }> = []

const settle = async (): Promise<void> => {
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
}

async function attach(clientId: string, direction: 'push' | 'pull', master: string): Promise<void> {
  h.clientId = clientId
  let initial: 'push' | 'pull' | null = direction
  executor = createExecutor({
    hostId: master,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    buildNow: (key) => buildSectionPayload(key as ProfileSectionKey),
    initialDirection: () => initial,
    onInitialSettled: () => (initial = null),
  })
  const ex = executor
  collector = startCollector({ onSection: (r) => ex.onSection(r) })
  await collector.primeAll()
  executor.onReconnected()
  await settle()
}

function leave(): void {
  collector?.stop()
  executor?.dispose()
  collector = null
  executor = null
  clearSectionStore()
  localStorage.clear()
}

const settingsWrites = (from: number, clientId?: string) =>
  daemon.writes.slice(from).filter((w) => w.op === 'put' && w.key === 'settings' && (clientId === undefined || w.clientId === clientId))

beforeEach(async () => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  daemon = new FakeDaemon(PROFILE)
  vi.clearAllMocks()
  h.shape = { hosts: ['fp-hosts', 1], settings: [await sectionFingerprint('settings'), SECTION_SCHEMA_ORDINAL.settings], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] }
  api.listProfiles.mockImplementation(async () => daemon.list())
  api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
  api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
  api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
})

afterEach(() => {
  leave()
  h.shape = null
  vi.useRealTimers()
})

/** The settings an ordinal-11 client writes: this build's payload without the task-groups store, its fingerprint and ordinal 11. */
async function ordinal11ClientWrites(): Promise<{ rev: number; hash: string }> {
  const cur = daemon.rows.get('settings')!
  const payload = JSON.parse(JSON.stringify(cur.payload)) as Record<string, unknown>
  delete payload[TG]
  const legacyList = PROJECTIONS.settings.filter((p) => !p.startsWith(`${TG}.`))
  const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: await fingerprintOf(legacyList), ordinal: 11, writer: OLD }
  daemon.rows.set('settings', row)
  return { rev: row.rev, hash: row.hash! }
}

describe('a group made on one device reaches another (spec §4.1)', () => {
  it('A\'s group arrives on B with the member host turned into B\'s own local id, and B writes nothing back', async () => {
    device('host-a1', { aaaaaaaa: group([{ host: 'host-a1', root: 'r1' }, { host: 'host-a1', root: 'r2' }]) })
    await attach(A, 'push', 'host-a1')
    expect(sotGroups().aaaaaaaa.members).toEqual([{ host: WIRE, root: 'r1' }, { host: WIRE, root: 'r2' }])
    leave()

    device('host-b2')
    const before = daemon.writes.length
    await attach(B, 'pull', 'host-b2')
    expect(store().order).toEqual(['aaaaaaaa'])
    expect(store().groups.aaaaaaaa.members).toEqual([{ host: 'host-b2', root: 'r1' }, { host: 'host-b2', root: 'r2' }])
    expect(settingsWrites(before, B)).toEqual([])
    expect(executor!.status().sections.settings).toBe('synced')
  })

  it('two devices with different local host ids adding the same two members in a different order converge — nobody pushes the other\'s back', async () => {
    device('host-a1', { aaaaaaaa: group([{ host: 'host-a1', root: 'r1' }]) })
    await attach(A, 'push', 'host-a1')
    leave()
    device('host-b2')
    await attach(B, 'pull', 'host-b2')
    const before = daemon.writes.length

    // B adds r3, r2 (in that order) on top of r1; A's own store would hold them as r2, r3 — the payload is the same
    store().patchGroup('aaaaaaaa', { members: [{ host: 'host-b2', root: 'r1' }, { host: 'host-b2', root: 'r3' }, { host: 'host-b2', root: 'r2' }] })
    await settle()
    expect(settingsWrites(before, B).map((w) => w.outcome)).toEqual(['applied'])
    const sot = JSON.stringify(sotGroups())
    expect(sotGroups().aaaaaaaa.members).toEqual([{ host: WIRE, root: 'r1' }, { host: WIRE, root: 'r2' }, { host: WIRE, root: 'r3' }])

    // the same members, the other order, on B again: the build is the same, so nothing goes out
    const quiet = daemon.writes.length
    store().patchGroup('aaaaaaaa', { members: [{ host: 'host-b2', root: 'r2' }, { host: 'host-b2', root: 'r3' }, { host: 'host-b2', root: 'r1' }] })
    await settle()
    expect(daemon.writes.length).toBe(quiet)
    expect(JSON.stringify(sotGroups())).toBe(sot)
    expect(executor!.status().sections.settings).toBe('synced')
  })

  it('a slave on screen edits the master\'s groups: settings belong to the master, so the edit goes out like any other', async () => {
    device('host-a1', { aaaaaaaa: group([{ host: 'host-a1', root: 'r1' }]) })
    await attach(A, 'push', 'host-a1')
    // the master is parked and the local workbench `on` is on screen
    useTabStore.setState({ worldId: 'on', worldEpoch: 1 })
    useWorkspaceStore.setState({ worldId: 'on', worldEpoch: 1 })
    useLocalProfilesStore.setState({
      activeProfileId: 'on',
      worldEpoch: 1,
      parkedMaster: { tabs: {}, workspaces: [], activeWorkspaceId: null, activeTabId: null },
      slaves: { on: { id: 'on', name: 'On', createdAt: 1, shownHostIds: [], world: null } },
      slaveOrder: ['on'],
    })
    await settle()
    const before = daemon.writes.length
    store().addGroup('bbbbbbbb', group([{ host: 'host-a1', root: 'r9' }], 'B'))
    await settle()
    expect(settingsWrites(before, A).map((w) => w.outcome)).toEqual(['applied'])
    expect(Object.keys(sotGroups()).sort()).toEqual(['aaaaaaaa', 'bbbbbbbb'])
  })
})

describe('an ordinal-11 settings row (spec §4.1 / §4.4)', () => {
  it('lands on a CLEAN local section: the store clears, the SOT is upgraded once, then silence', async () => {
    device('host-a1', { aaaaaaaa: group([{ host: 'host-a1', root: 'r1' }]) })
    await attach(A, 'push', 'host-a1')
    expect(Object.keys(sotGroups())).toEqual(['aaaaaaaa'])
    problems.length = 0
    const before = daemon.writes.length

    const written = await ordinal11ClientWrites()
    executor!.onRemoteEvent({ hostId: 'host-a1', profileId: PROFILE, section: 'settings', rev: written.rev, hash: written.hash, writerClientId: OLD })
    await settle()

    expect(store().order).toEqual([])
    expect(store().groups).toEqual({})
    expect(executor!.status().profile).toBe('synced')
    expect(settingsWrites(before, A).map((w) => w.outcome)).toEqual(['applied'])
    expect(daemon.rows.get('settings')).toMatchObject({ ordinal: SECTION_SCHEMA_ORDINAL.settings, writer: A })
    const quiet = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.length).toBe(quiet)
  })

  it('does NOT pull over a DIRTY local section: the local edit stays and the section is in conflict', async () => {
    device('host-a1', { aaaaaaaa: group([{ host: 'host-a1', root: 'r1' }]) })
    await attach(A, 'push', 'host-a1')
    problems.length = 0

    await ordinal11ClientWrites() // the SOT moves before A's own edit goes out
    store().addGroup('bbbbbbbb', group([{ host: 'host-a1', root: 'r7' }], 'B'))
    await vi.advanceTimersByTimeAsync(2_000)

    expect(executor!.status().sections.settings).toBe('locked:conflict')
    expect(Object.keys(store().groups).sort()).toEqual(['aaaaaaaa', 'bbbbbbbb'])
    expect(daemon.rows.get('settings')).toMatchObject({ ordinal: 11, writer: OLD })
  })
})
