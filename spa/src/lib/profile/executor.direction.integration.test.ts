// spa/src/lib/profile/executor.direction.integration.test.ts — the first
// reconciliation of a SECOND client, end to end: the real collector, section
// store, apply-to-stores and zustand stores on this side, and on the other an
// in-memory daemon that keeps P1's compare-and-set semantics (revs only go up,
// a wrong baseRev is a 409 carrying the live section, a delete leaves a
// tombstone that takes baseRev 0 only). Client A is not faked either: it is the
// same executor, run first against the empty daemon.
//
// This is the case the real-machine acceptance found: A pushed its world, B —
// brand new, nothing local — attached, and B's `workspaces` went straight to
// `locked:conflict`, because an empty list is a payload too and B had never
// agreed with anyone. The attach has a direction now.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { startCollector, type Collector } from './collector'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { buildWorkspacesSection } from './sections'
import { clearSectionStore } from './section-store'
import { FakeDaemon, type FakeRow as Row } from './test-fake-daemon'

const h = vi.hoisted(() => ({ clientId: 'c_aaaaaaaaaaaa' }))

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
  return { ...actual, shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })) }
})

vi.mock('../client-identity', () => ({ getClientId: () => h.clientId, isClientIdPersisted: () => true }))

const api = vi.mocked(await import('./api'))

const M = 'host-master'
const H2 = 'host-two'
const PROFILE = 'p_0123456789ab'
const A = 'c_aaaaaaaaaaaa'
const B = 'c_bbbbbbbbbbbb'

/* ─── the clients ─── */

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

function leaf(paneId: string): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content: { kind: 'tmux-session', hostId: M, sessionCode: `c-${paneId}`, mode: 'terminal', cachedName: paneId, tmuxInstance: 'inst' } } }
}

function tab(id: string): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: leaf(`p-${id}`) }
}

function ws(id: string, tabs: string[]): Workspace {
  return { id, name: id.toUpperCase(), tabs, activeTabId: tabs[0] ?? null }
}

function world(h2Name: string, workspaces: Workspace[], tabs: Tab[]): void {
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1, name: h2Name }) }, hostOrder: [M, H2], activeHostId: M, runtime: {} })
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces, activeWorkspaceId: workspaces[0]?.id ?? null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
}

let daemon: FakeDaemon
let executor: Executor | null = null
let collector: Collector | null = null
const problems: Array<{ kind: string; section?: string; detail: string }> = []

interface Run {
  settled: ReturnType<typeof vi.fn<() => void>>
  direction: { value: 'push' | 'pull' | null }
}

/** One client's session: attach with `direction`, connect, and let everything play out. */
async function attach(clientId: string, direction: 'push' | 'pull'): Promise<Run> {
  h.clientId = clientId
  const run: Run = { settled: vi.fn<() => void>(), direction: { value: direction } }
  // what start.ts does in the callback
  run.settled.mockImplementation(() => (run.direction.value = null))
  executor = createExecutor({
    hostId: M,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    initialDirection: () => run.direction.value,
    onInitialSettled: run.settled,
  })
  const ex = executor
  collector = startCollector({ onSection: (r) => ex.onSection(r) })
  await collector.primeAll()
  executor.onReconnected()
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
  return run
}

/** The client goes away: a different machine comes next, with its own storage. */
function leave(): void {
  collector?.stop()
  executor?.dispose()
  collector = null
  executor = null
  clearSectionStore()
  localStorage.clear()
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  daemon = new FakeDaemon(PROFILE)
  vi.clearAllMocks()
  api.listProfiles.mockImplementation(async () => daemon.list())
  api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
  api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
  api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
})

afterEach(() => {
  leave()
  vi.useRealTimers()
})

/** A's world on the daemon: host-two renamed, two workspaces with a tab each. */
async function clientAHasPushed(): Promise<void> {
  world('named-by-A', [ws('wa1', ['ta1']), ws('wa2', ['ta2'])], [tab('ta1'), tab('ta2')])
  const run = await attach(A, 'push')
  expect(run.settled).toHaveBeenCalledTimes(1)
  expect(daemon.live()).toEqual(['hosts', 'settings', 'tabs.wa1', 'tabs.wa2', 'workspaces'])
  leave()
  problems.length = 0
}

describe('a second client attaches', () => {
  it('PULL — B is brand new: it ends up with A\'s world, nothing is left locked, and B has written NOTHING', async () => {
    await clientAHasPushed()
    const revsBefore = daemon.revs()
    const writesBefore = daemon.writes.length

    world(H2, [], [])
    const run = await attach(B, 'pull')

    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.tabs)).toEqual([['ta1'], ['ta2']])
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual(['ta1', 'ta2'])
    expect(useHostStore.getState().hosts[H2].name).toBe('named-by-A')

    const status = executor!.status()
    expect(status.profile).toBe('synced')
    expect(Object.values(status.sections).filter((s) => s !== 'synced')).toEqual([])
    expect(Object.keys(status.sections).sort()).toEqual(['hosts', 'settings', 'tabs.wa1', 'tabs.wa2', 'workspaces'])

    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(daemon.revs()).toEqual(revsBefore)
    expect(run.settled).toHaveBeenCalledTimes(1)
    expect(run.direction.value).toBeNull()
    expect(problems.filter((p) => p.kind !== 'sections-unrendered')).toEqual([])
  })

  it('PULL — B had a workspace of its own: it is replaced, and its tabs never reach the SOT', async () => {
    await clientAHasPushed()
    const revsBefore = daemon.revs()

    world(H2, [ws('wb1', ['tb1'])], [tab('tb1')])
    const run = await attach(B, 'pull')

    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    expect(daemon.writes.filter((w) => w.clientId === B)).toEqual([])
    expect(daemon.revs()).toEqual(revsBefore)
    expect(run.settled).toHaveBeenCalledTimes(1)
    expect(executor!.status().profile).toBe('synced')
  })

  it('PUSH — B has one workspace of its own: the SOT ends up holding B\'s world only, A\'s tabs are tombstones, B is untouched', async () => {
    await clientAHasPushed()

    world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
    const run = await attach(B, 'push')

    expect(daemon.live()).toEqual(['hosts', 'settings', 'tabs.wb1', 'workspaces'])
    expect(daemon.rows.get('tabs.wa1')).toMatchObject({ hash: null, writer: B })
    expect(daemon.rows.get('tabs.wa2')).toMatchObject({ hash: null, writer: B })
    expect((daemon.rows.get('workspaces')!.payload as { order: string[] }).order).toEqual(['wb1'])
    expect(daemon.rows.get('workspaces')!.writer).toBe(B)
    expect(daemon.rows.get('hosts')!.writer).toBe(B)
    expect(daemon.writes.filter((w) => w.clientId === B && w.outcome === 'conflict')).toEqual([])

    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wb1'])
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['tb1'])
    expect(useHostStore.getState().hosts[H2].name).toBe('named-by-B')

    const status = executor!.status()
    expect(status.profile).toBe('synced')
    expect(Object.keys(status.sections).sort()).toEqual(['hosts', 'settings', 'tabs.wb1', 'workspaces'])
    expect(run.settled).toHaveBeenCalledTimes(1)
  })

  it('after the first reconciliation a conflict is the user\'s again', async () => {
    await clientAHasPushed()
    world(H2, [], [])
    const run = await attach(B, 'pull')
    expect(run.direction.value).toBeNull()

    // B edits offline-ish: the SOT moves under it (A writes) before B's push goes out
    const hosts = daemon.rows.get('hosts')!
    daemon.rows.set('hosts', { ...hosts, rev: hosts.rev + 1, hash: 'f'.repeat(64), writer: A })
    const { hosts: mine } = useHostStore.getState()
    useHostStore.setState({ hosts: { ...mine, [H2]: { ...mine[H2], name: 'edited-by-B' } } })
    await vi.advanceTimersByTimeAsync(2_000)

    expect(executor!.status().sections.hosts).toBe('locked:conflict')
    expect(run.settled).toHaveBeenCalledTimes(1)
  })
})

describe('a NEWER Purdex writes a section (spec §4.4: an old client must not write anything once any shape has moved)', () => {
  /** What curl did on the real machines: a legal CAS write with another fingerprint, ordinal 99 and a store this build does not know. */
  function newerWriterWritesSettings(): { rev: number; hash: string } {
    const cur = daemon.rows.get('settings')!
    const row: Row = {
      rev: cur.rev + 1,
      hash: 'e'.repeat(64),
      payload: { ...(cur.payload as Record<string, unknown>), 'purdex-from-the-future': { x: 1 } },
      fingerprint: 'fp-settings-of-a-newer-purdex',
      ordinal: 99,
      writer: 'c_cccccccccccc',
    }
    daemon.rows.set('settings', row)
    return { rev: row.rev, hash: row.hash! }
  }

  function renameFirstWorkspace(name: string): void {
    const { workspaces } = useWorkspaceStore.getState()
    useWorkspaceStore.setState({ workspaces: workspaces.map((w, i) => (i === 0 ? { ...w, name } : w)) })
  }

  it('the attached client locks the WHOLE profile on the pull, and its next edit of ANOTHER section never reaches the daemon; a client attaching later locks on the index', async () => {
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    expect(executor!.status().profile).toBe('synced')
    const writesBefore = daemon.writes.length
    const revsBefore = daemon.revs()

    const written = newerWriterWritesSettings()
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: written.rev, hash: written.hash, writerClientId: 'c_cccccccccccc' })
    await vi.advanceTimersByTimeAsync(1_000)
    expect(executor!.status()).toMatchObject({ profile: 'locked:schema', schemaLock: { section: 'settings', verdict: 'sot-is-newer' } })
    expect(executor!.status().sections.settings).toBe('synced') // not locked:invalid — the PROFILE is what is locked

    renameFirstWorkspace('renamed-under-the-lock') // on the real machines this went out: workspaces rev 9 → 10
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(daemon.revs()).toEqual({ ...revsBefore, settings: written.rev })

    // a reconnect re-reads the index: the same shape is listed there, the lock stays, still nothing goes out
    executor!.onReconnected()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status().profile).toBe('locked:schema')
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    leave()

    // the second old client: it meets the newer shape on its first index
    world(H2, [ws('wb1', ['tb1'])], [tab('tb1')])
    await attach(B, 'push')
    renameFirstWorkspace('b-renamed')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status().profile).toBe('locked:schema')
    expect(daemon.writes.filter((w) => w.clientId === B)).toEqual([])
    expect(daemon.revs()).toEqual({ ...revsBefore, settings: written.rev })
  })
})

describe('another client removes a workspace, and the tabs deletion gets here before the workspaces change', () => {
  it('the workspace keeps its tabs until `workspaces` says it is gone; this client writes NOTHING; no orphan, no leftover section, no problem', async () => {
    await clientAHasPushed()
    world(H2, [], [])
    await attach(B, 'pull')
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    problems.length = 0
    const writesBefore = daemon.writes.length

    // A removes wa2: PUT workspaces (without it) + DELETE tabs.wa2
    const remaining = useWorkspaceStore.getState().workspaces.filter((w) => w.id !== 'wa2')
    const payload = JSON.parse(JSON.stringify(buildWorkspacesSection(remaining))) as Record<string, unknown>
    const wsRow = daemon.rows.get('workspaces')!
    daemon.rows.set('workspaces', { ...wsRow, rev: wsRow.rev + 1, hash: await hashSection(payload), payload, writer: A })
    const tabsRow = daemon.rows.get('tabs.wa2')!
    daemon.rows.set('tabs.wa2', { ...tabsRow, rev: tabsRow.rev + 1, hash: null, payload: null, writer: A })

    // only the DELETE's event arrives (the other one is late — or lost)
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'tabs.wa2', rev: tabsRow.rev + 1, hash: null, writerClientId: A })
    // wa2 must never be seen EMPTIED: it keeps its tab until it goes as a whole
    const emptied: string[][] = []
    const unsubscribe = useWorkspaceStore.subscribe((next) => {
      const wa2 = next.workspaces.find((w) => w.id === 'wa2')
      if (wa2 !== undefined && wa2.tabs.length === 0) emptied.push(wa2.tabs)
    })
    for (let i = 0; i < 10; i += 1) await vi.advanceTimersByTimeAsync(1_000)
    unsubscribe()
    expect(emptied).toEqual([])
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1'])
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['ta1'])
    expect(executor!.status()).toMatchObject({ profile: 'synced' })
    expect(Object.keys(executor!.status().sections).sort()).toEqual(['hosts', 'settings', 'tabs.wa1', 'workspaces'])
    expect(daemon.writes.slice(writesBefore)).toEqual([]) // no orphan re-created, no second delete
    expect(daemon.live()).toEqual(['hosts', 'settings', 'tabs.wa1', 'workspaces'])
    expect(problems).toEqual([])
  })
})
