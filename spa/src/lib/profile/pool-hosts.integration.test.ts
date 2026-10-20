// spa/src/lib/profile/pool-hosts.integration.test.ts — the pool hosts' order and looks through Profile Sync, end to end
// (monitoring tabs spec A3, MON-2a task 3): the REAL collector, section store, apply-to-stores, executor and zustand
// stores on this side, an in-memory daemon with P1's compare-and-set semantics on the other (style of
// task-groups.integration.test.ts). One process plays every device; a device "leaves" by clearing its storage.
// Only the network, the digest and `shapeTable` are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { usePoolHostsStore, type PoolHostLook } from '../../stores/usePoolHostsStore'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import type { ProfileSectionKey } from './types'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { clearSectionStore } from './section-store'
import { PROJECTIONS, SECTION_SCHEMA_ORDINAL, fingerprintOf, sectionFingerprint } from './projections'
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
const MLAB = '82b11c521df16c12'
const MSERV = 'a0b1c2d3e4f50617'
const PH = 'purdex-pool-hosts'

function host(id: string): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0 }
}

function device(local: { order?: string[]; looks?: Record<string, PoolHostLook> } = {}): void {
  useHostStore.setState({ hosts: { 'host-1': host('host-1') }, hostOrder: ['host-1'], activeHostId: 'host-1', runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  usePoolHostsStore.setState({ order: local.order ?? [], looks: local.looks ?? {} })
}

const store = () => usePoolHostsStore.getState()
const sotPoolHosts = () => (daemon.rows.get('settings')?.payload as Record<string, { order?: string[]; looks?: Record<string, PoolHostLook> }>)[PH]

let daemon: FakeDaemon
let executor: Executor | null = null
let collector: Collector | null = null
const problems: Array<{ kind: string; section?: string; detail: string }> = []

const settle = async (): Promise<void> => {
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
}

async function attach(clientId: string, direction: 'push' | 'pull'): Promise<void> {
  h.clientId = clientId
  let initial: 'push' | 'pull' | null = direction
  executor = createExecutor({
    hostId: 'host-1',
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

/** The settings an ordinal-12 client writes: this build's payload without the pool-hosts store, its fingerprint and ordinal 12. */
async function ordinal12ClientWrites(): Promise<{ rev: number; hash: string }> {
  const cur = daemon.rows.get('settings')!
  const payload = JSON.parse(JSON.stringify(cur.payload)) as Record<string, unknown>
  delete payload[PH]
  const legacyList = PROJECTIONS.settings.filter((p) => !p.startsWith(`${PH}.`))
  const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: await fingerprintOf(legacyList), ordinal: 12, writer: OLD }
  daemon.rows.set('settings', row)
  return { rev: row.rev, hash: row.hash! }
}

const MSERV_LOOK: PoolHostLook = { icon: 'Cpu', indicator: { shape: 'bars', data: ['cpu', 'mem'] } }

describe('looks and order travel between devices (spec A3)', () => {
  it('A\'s look and indicator reach B through the real collector and applier, and B writes nothing back', async () => {
    device({ order: [MSERV, MLAB], looks: { [MSERV]: MSERV_LOOK } })
    await attach(A, 'push')
    expect(sotPoolHosts()).toEqual({ order: [MSERV, MLAB], looks: { [MSERV]: MSERV_LOOK } })
    leave()

    device()
    const before = daemon.writes.length
    await attach(B, 'pull')
    expect(store().order).toEqual([MSERV, MLAB])
    expect(store().looks).toEqual({ [MSERV]: MSERV_LOOK })
    expect(settingsWrites(before, B)).toEqual([])
    expect(executor!.status().sections.settings).toBe('synced')
  })

  it('a reorder on B reaches the SOT once and comes back to A; equal content on both devices never ping-pongs', async () => {
    device({ order: [MLAB, MSERV] })
    await attach(A, 'push')
    leave()
    device()
    await attach(B, 'pull')
    const before = daemon.writes.length

    store().setOrder([MSERV, MLAB])
    await settle()
    expect(settingsWrites(before, B).map((w) => w.outcome)).toEqual(['applied'])
    expect(sotPoolHosts().order).toEqual([MSERV, MLAB])
    const rev = daemon.rows.get('settings')!.rev
    leave()

    device({ order: [MLAB, MSERV] })
    await attach(A, 'pull')
    expect(store().order).toEqual([MSERV, MLAB])
    leave()

    // a device that already holds exactly the SOT's content pushes nothing
    device({ order: [MSERV, MLAB] })
    const beforeC = daemon.writes.length
    await attach(A, 'push')
    expect(settingsWrites(beforeC, A).filter((w) => w.outcome === 'applied')).toEqual([])
    expect(daemon.rows.get('settings')!.rev).toBe(rev)
    const quiet = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.length).toBe(quiet)
  })
})

describe('an ordinal-12 settings row (spec A3)', () => {
  it('lands on a CLEAN local section: the store clears, the SOT is upgraded once, then silence', async () => {
    device({ order: [MLAB], looks: { [MLAB]: { icon: 'Cpu' } } })
    await attach(A, 'push')
    expect(sotPoolHosts().order).toEqual([MLAB])
    problems.length = 0
    const before = daemon.writes.length

    const written = await ordinal12ClientWrites()
    executor!.onRemoteEvent({ hostId: 'host-1', profileId: PROFILE, section: 'settings', rev: written.rev, hash: written.hash, writerClientId: OLD })
    await settle()

    expect(store().order).toEqual([])
    expect(store().looks).toEqual({})
    expect(executor!.status().profile).toBe('synced')
    expect(settingsWrites(before, A).map((w) => w.outcome)).toEqual(['applied'])
    expect(daemon.rows.get('settings')).toMatchObject({ ordinal: SECTION_SCHEMA_ORDINAL.settings, writer: A })
    const quiet = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.length).toBe(quiet)
  })

  it('does NOT pull over a DIRTY local section: the local look and order stay and the section is in conflict', async () => {
    device({ order: [MLAB], looks: { [MLAB]: { icon: 'Cpu' } } })
    await attach(A, 'push')
    problems.length = 0

    await ordinal12ClientWrites() // the SOT moves before A's own edit goes out
    store().setOrder([MSERV, MLAB])
    store().patchLook(MSERV, () => MSERV_LOOK)
    await vi.advanceTimersByTimeAsync(2_000)

    expect(executor!.status().sections.settings).toBe('locked:conflict')
    expect(store().order).toEqual([MSERV, MLAB])
    expect(store().looks[MSERV]).toEqual(MSERV_LOOK)
    expect(daemon.rows.get('settings')).toMatchObject({ ordinal: 12, writer: OLD })
  })
})
