// spa/src/lib/profile/tab-group.integration.test.ts — a `tabs.<workspace>` row written by an ordinal-4 client (no `group` /
// `groupRank`) meeting this build (task groups spec §4.2 / §4.4, TG-1b-2 task 3): the REAL collector, section store,
// apply-to-stores and executor on this side, an in-memory daemon on the other (style of task-groups.integration.test.ts).
// Only the network, the digest and `shapeTable` are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { Tab } from '../../types/tab'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import type { ProfileSectionKey } from './types'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { clearSectionStore } from './section-store'
import { PROJECTIONS, SECTION_SCHEMA_ORDINAL, WIRE_MARKERS, fingerprintOf, sectionFingerprint } from './projections'
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
const OLD = 'c_cccccccccccc'
const HOST = 'host-a1'
const KEY = 'tabs.wa'
const MANUAL = 'group\u0000aaaaaaaa'

const host: HostConfig = { id: HOST, name: HOST, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, daemonId: 'mini-lab:278cbm' }
const tab = (id: string, extra: Partial<Tab> = {}): Tab => ({
  id, pinned: false, locked: false, createdAt: 1,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: HOST, sessionCode: `c-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'inst' } } },
  ...extra,
})

function device(tabs: Tab[]): void {
  useHostStore.setState({ hosts: { [HOST]: host }, hostOrder: [HOST], activeHostId: HOST, runtime: {} })
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [{ id: 'wa', name: 'A', tabs: tabs.map((t) => t.id), activeTabId: tabs[0]?.id ?? null }], activeWorkspaceId: 'wa' })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
}

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
    hostId: HOST,
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

const tabWrites = (from: number, clientId?: string) =>
  daemon.writes.slice(from).filter((w) => w.op === 'put' && w.key === KEY && (clientId === undefined || w.clientId === clientId))

beforeEach(async () => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  daemon = new FakeDaemon(PROFILE)
  vi.clearAllMocks()
  h.shape = {
    hosts: ['fp-hosts', 1],
    settings: [await sectionFingerprint('settings'), SECTION_SCHEMA_ORDINAL.settings],
    workspaces: ['fp-workspaces', 1],
    tabs: [await sectionFingerprint('tabs'), SECTION_SCHEMA_ORDINAL.tabs],
  }
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

/** What an ordinal-4 client writes into `tabs.wa`: this build's rows without `group` / `groupRank`, its fingerprint, ordinal 4. */
async function ordinal4ClientWrites(): Promise<{ rev: number; hash: string }> {
  const cur = daemon.rows.get(KEY)!
  const payload = JSON.parse(JSON.stringify(cur.payload)) as { tabs: Record<string, Record<string, unknown>> }
  for (const t of Object.values(payload.tabs)) {
    delete t.group
    delete t.groupRank
  }
  const legacyList = PROJECTIONS.tabs.filter((p) => p !== 'tabs.*.group' && p !== 'tabs.*.groupRank')
  const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload: payload as unknown as Record<string, unknown>, fingerprint: await fingerprintOf([...legacyList, ...WIRE_MARKERS.tabs]), ordinal: 4, writer: OLD }
  daemon.rows.set(KEY, row)
  return { rev: row.rev, hash: row.hash! }
}

describe('an ordinal-4 tabs row (spec §4.2 / §4.4)', () => {
  it('lands on a CLEAN local section: the fields clear and, the rebuilt hash being the row\'s, nothing is written — silence', async () => {
    device([tab('ta1', { group: MANUAL, groupRank: 'V' }), tab('ta2')])
    await attach(A, 'push')
    expect((daemon.rows.get(KEY)!.payload as { tabs: Record<string, Tab> }).tabs.ta1).toMatchObject({ group: MANUAL, groupRank: 'V' })
    problems.length = 0
    const before = daemon.writes.length

    const written = await ordinal4ClientWrites()
    executor!.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: KEY, rev: written.rev, hash: written.hash, writerClientId: OLD })
    await settle()

    const ta1 = useTabStore.getState().tabs.ta1
    expect(Object.hasOwn(ta1, 'group')).toBe(false)
    expect(Object.hasOwn(ta1, 'groupRank')).toBe(false)
    expect(executor!.status().profile).toBe('synced')
    // a tab without the fields builds exactly the ordinal-4 payload, so there is nothing to upgrade until the next real edit
    expect(tabWrites(before, A)).toEqual([])
    expect(daemon.rows.get(KEY)).toMatchObject({ ordinal: 4, writer: OLD })
    const quiet = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.length).toBe(quiet)
  })

  it('does NOT pull over a DIRTY local section: the fields stay and the section is in conflict', async () => {
    device([tab('ta1', { group: MANUAL, groupRank: 'V' }), tab('ta2')])
    await attach(A, 'push')
    problems.length = 0

    await ordinal4ClientWrites() // the SOT moves before A's own edit goes out
    useTabStore.getState().setTabGroupRank('ta1', 'W')
    await vi.advanceTimersByTimeAsync(2_000)

    expect(executor!.status().sections[KEY]).toBe('locked:conflict')
    expect(useTabStore.getState().tabs.ta1).toMatchObject({ group: MANUAL, groupRank: 'W' })
    expect(daemon.rows.get(KEY)).toMatchObject({ ordinal: 4, writer: OLD })
  })
})
