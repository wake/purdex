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
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import type { ProfileSectionKey } from './types'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { buildHostsSection, buildWorkspacesSection } from './sections'
import { identityOfSync, syncIdOfSync } from './host-identity'
import { clearSectionStore, saveConflict } from './section-store'
import { PROJECTIONS, SECTION_SCHEMA_ORDINAL, fingerprintOf, sectionFingerprint } from './projections'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { FakeDaemon, type FakeRow as Row } from './test-fake-daemon'

const h = vi.hoisted(() => ({
  clientId: 'c_aaaaaaaaaaaa',
  /** This build's shape table as the executor reads it; `null` = the placeholder table below. */
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
async function attach(clientId: string, direction: 'push' | 'pull', master = M): Promise<Run> {
  h.clientId = clientId
  const run: Run = { settled: vi.fn<() => void>(), direction: { value: direction } }
  // what start.ts does in the callback
  run.settled.mockImplementation(() => (run.direction.value = null))
  executor = createExecutor({
    hostId: master,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    buildNow: (key) => buildSectionPayload(key as ProfileSectionKey), // what start.ts wires
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
  h.shape = null
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

describe('lastSuccessAt after the first reconciliation (P3d-4c F1: R4 — stamped when a host answer leaves the profile synced)', () => {
  it('PULL — B is brand new: the reconciliation ends synced, and lastSuccessAt is set', async () => {
    await clientAHasPushed()
    world(H2, [], [])
    await attach(B, 'pull')
    expect(executor!.status().profile).toBe('synced')
    expect(executor!.status().lastSuccessAt).not.toBeNull()
  })

  it('PULL — B had a workspace of its own (it goes when `workspaces` is pulled): lastSuccessAt is set', async () => {
    await clientAHasPushed()
    world(H2, [ws('wb1', ['tb1'])], [tab('tb1')])
    await attach(B, 'pull')
    expect(executor!.status().profile).toBe('synced')
    expect(executor!.status().lastSuccessAt).not.toBeNull()
  })

  it('PUSH — into an empty profile: lastSuccessAt is set', async () => {
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    expect(executor!.status().profile).toBe('synced')
    expect(executor!.status().lastSuccessAt).not.toBeNull()
  })

  it('PUSH — over another world (orphans deleted): lastSuccessAt is set', async () => {
    await clientAHasPushed()
    world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
    await attach(B, 'push')
    expect(executor!.status().profile).toBe('synced')
    expect(executor!.status().lastSuccessAt).not.toBeNull()
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

describe('workspace-scoped settings wait for `workspaces` (the builder and the applier both go by the master workspace set)', () => {
  const WSS = 'purdex-workspace-settings'
  const ENTRY = { files: { root: '/set-on-A' } }

  beforeEach(() => useWorkspaceSettingsStore.setState({ workspaces: {} }))
  afterEach(() => useWorkspaceSettingsStore.setState({ workspaces: {} }))

  const scopedOnSot = (): unknown => (daemon.rows.get('settings')!.payload as Record<string, Record<string, unknown>>)[WSS].workspaces

  async function write(key: string, payload: unknown): Promise<{ rev: number; hash: string }> {
    const cur = daemon.rows.get(key)
    const plain = JSON.parse(JSON.stringify(payload)) as Record<string, unknown>
    const shape: Record<string, [string, number]> = { settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] }
    const [fingerprint, ordinal] = shape[key.startsWith('tabs.') ? 'tabs' : key]
    const row: Row = { rev: (cur?.rev ?? 0) + 1, hash: await hashSection(plain), payload: plain, fingerprint, ordinal, writer: A }
    daemon.rows.set(key, row)
    return { rev: row.rev, hash: row.hash! }
  }

  /** On another machine: A creates `wa2` and gives it a scoped setting — `workspaces`, `tabs.wa2`, then `settings`. */
  async function clientACreatesWa2WithASetting(): Promise<Record<'workspaces' | 'settings', { rev: number; hash: string }>> {
    const workspaces = await write('workspaces', buildWorkspacesSection([...useWorkspaceStore.getState().workspaces, ws('wa2', [])]))
    await write('tabs.wa2', { order: [], tabs: {} })
    const settings = daemon.rows.get('settings')!.payload as Record<string, Record<string, unknown>>
    return { workspaces, settings: await write('settings', { ...settings, [WSS]: { workspaces: { ...(settings[WSS].workspaces as object), wa2: ENTRY } } }) }
  }

  async function expectNothingLost(writesBefore: number): Promise<void> {
    for (let i = 0; i < 10; i += 1) await vi.advanceTimersByTimeAsync(1_000)
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wb1', 'wa2'])
    expect(useWorkspaceSettingsStore.getState().workspaces).toEqual({ wa2: ENTRY })
    expect(scopedOnSot()).toEqual({ wa2: ENTRY }) // NOT pushed back without it: that would delete A's setting for everyone
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(executor!.status().profile).toBe('synced')
    // `tabs.wa2` is listed before its workspace is here: said once, as in the PULL test above
    expect(problems.filter((p) => p.kind !== 'sections-unrendered')).toEqual([])
  }

  it('DATA LOSS, reproduced: both moved while this client was away — `settings` is not pulled before `workspaces`', async () => {
    world(H2, [ws('wb1', [])], [])
    await attach(B, 'push')
    const writesBefore = daemon.writes.length
    await clientACreatesWa2WithASetting()
    executor!.onReconnected() // the index shows `settings` AND `workspaces` moved; `settings` comes first in it
    await expectNothingLost(writesBefore)
  })

  it('the two events arrive in SOT order but the `settings` read is the faster one: it is still applied second', async () => {
    world(H2, [ws('wb1', [])], [])
    await attach(B, 'push')
    const writesBefore = daemon.writes.length
    const written = await clientACreatesWa2WithASetting()
    api.getSection.mockImplementation(async (_h, _p, key) => {
      if (key === 'workspaces') await new Promise((r) => setTimeout(r, 300))
      return daemon.get(key)
    })
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'workspaces', rev: written.workspaces.rev, hash: written.workspaces.hash, writerClientId: A })
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: written.settings.rev, hash: written.settings.hash, writerClientId: A })
    await expectNothingLost(writesBefore)
  })

  it('a `settings` read already on the wire when `workspaces` moves is judged again when it lands: not applied, asked again afterwards', async () => {
    world(H2, [ws('wb1', [])], [])
    await attach(B, 'push')
    const writesBefore = daemon.writes.length
    api.getSection.mockImplementation(async (_h, _p, key) => {
      await new Promise((r) => setTimeout(r, key === 'settings' ? 300 : 600))
      return daemon.get(key) // the daemon serves what it holds WHEN it answers
    })
    // an unrelated `settings` write starts a read …
    const cur = daemon.rows.get('settings')!
    const first = await write('settings', { ...(cur.payload as object), 'purdex-layout': { tabPosition: 'left' } })
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: first.rev, hash: first.hash, writerClientId: A })
    await vi.advanceTimersByTimeAsync(100)
    // … and while it is out, A creates wa2 with its setting: the read will come back carrying an entry for a workspace not here yet
    const written = await clientACreatesWa2WithASetting()
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'workspaces', rev: written.workspaces.rev, hash: written.workspaces.hash, writerClientId: A })
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: written.settings.rev, hash: written.settings.hash, writerClientId: A })
    await expectNothingLost(writesBefore)
  })

  it('PUSH side: a scoped setting of a workspace created here does not reach the SOT before the `workspaces` that lists it', async () => {
    world(H2, [ws('wb1', [])], [])
    await attach(B, 'push')
    const writesBefore = daemon.writes.length
    let failed = false
    api.putSection.mockImplementation(async (_h, _p, key, body) => {
      if (key === 'workspaces' && !failed) {
        failed = true
        return { kind: 'failed', reason: 'network', status: 0, message: 'dropped' }
      }
      return daemon.put(key, body)
    })
    useWorkspaceStore.setState({ workspaces: [...useWorkspaceStore.getState().workspaces, ws('wb2', [])] })
    useWorkspaceSettingsStore.getState().set('wb2', 'files', { root: '/set-on-B' })
    for (let i = 0; i < 10; i += 1) await vi.advanceTimersByTimeAsync(1_000)
    const order = daemon.writes.slice(writesBefore).filter((w) => w.outcome === 'applied').map((w) => w.key)
    expect(order.indexOf('workspaces')).toBeGreaterThanOrEqual(0)
    expect(order.indexOf('workspaces')).toBeLessThan(order.indexOf('settings'))
    expect(scopedOnSot()).toEqual({ wb2: { files: { root: '/set-on-B' } } })
    expect(executor!.status().profile).toBe('synced')
  })
})

/* ─── P3e: settings ordinal 3 → 4 (newtab `profiles` → `presets`) — coexistence, no ping-pong ─── */

const NEWTAB = 'purdex-newtab-layout'
type Layout = Record<'3col' | '2col' | '1col', { enabled: boolean; columns: string[][] }>
const LAYOUT_A: Layout = { '3col': { enabled: true, columns: [['x'], ['y'], []] }, '2col': { enabled: true, columns: [['y'], ['x']] }, '1col': { enabled: true, columns: [['x', 'y']] } }
const LAYOUT_C: Layout = { '3col': { enabled: false, columns: [[], [], ['z']] }, '2col': { enabled: true, columns: [['z'], []] }, '1col': { enabled: true, columns: [['z']] } }
const LAYOUT_B: Layout = { '3col': { enabled: true, columns: [['b'], [], []] }, '2col': { enabled: false, columns: [['b'], []] }, '1col': { enabled: true, columns: [['b']] } }

/** The two REAL settings shapes: this build's, and the ordinal-3 one (the same list with `presets` swapped back to `profiles`). */
async function realSettingsShapes(): Promise<{ current: [string, number]; legacy: [string, number] }> {
  const legacyList = PROJECTIONS.settings.map((p) => (p === `${NEWTAB}.presets` ? `${NEWTAB}.profiles` : p))
  expect(legacyList).not.toEqual(PROJECTIONS.settings)
  expect(SECTION_SCHEMA_ORDINAL.settings).toBe(7) // 5: host-sync-identity (wire host ids) — the same paths as 4; 6: host looks (host ownership H2c); 7: shown hosts (H2d)
  return { current: [await sectionFingerprint('settings'), SECTION_SCHEMA_ORDINAL.settings], legacy: [await fingerprintOf(legacyList), 3] }
}

/** The placeholder shape table with the given kinds replaced — e.g. a REAL settings or hosts pair. */
function shapeWith(over: Partial<NonNullable<typeof h.shape>>): NonNullable<typeof h.shape> {
  return { hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1], ...over }
}

function shapeWithSettings(settings: [string, number]): NonNullable<typeof h.shape> {
  return shapeWith({ settings })
}

const settingsPuts = (from: number, clientId?: string) =>
  daemon.writes.slice(from).filter((w) => w.op === 'put' && w.key === 'settings' && (clientId === undefined || w.clientId === clientId))

const settingsProblems = () => problems.filter((p) => p.section === 'settings')

function resetNewTab(): void {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
}

describe('P3e NEW side: this build (settings ordinal 4) meets settings an ordinal-3 client wrote (newtab `profiles`)', () => {
  let shapes: Awaited<ReturnType<typeof realSettingsShapes>>

  let putCallsAtStart = 0

  beforeEach(async () => {
    shapes = await realSettingsShapes()
    resetNewTab()
    putCallsAtStart = api.putSection.mock.calls.length
  })
  afterEach(() => {
    // Whatever path got it there, a settings PUT at this build's ordinal is in this build's shape:
    // an ordinal-4 row whose payload still says `profiles` would be a fingerprint/payload mismatch on the SOT.
    const bodies = api.putSection.mock.calls.slice(putCallsAtStart).filter((c) => c[2] === 'settings').map((c) => c[3] as { payload: Record<string, unknown>; ordinal: number })
    for (const b of bodies.filter((x) => x.ordinal === shapes.current[1])) {
      expect(Object.hasOwn((b.payload[NEWTAB] ?? {}) as object, 'profiles')).toBe(false)
    }
    resetNewTab()
  })

  /** What an ordinal-3 client writes: the same stores, the newtab layout under `profiles`, its fingerprint and ordinal 3. */
  async function oldClientWritesSettings(layout: Layout, writer = 'c_cccccccccccc'): Promise<{ rev: number; hash: string }> {
    const cur = daemon.rows.get('settings')!
    const payload = JSON.parse(JSON.stringify({ ...(cur.payload as Record<string, unknown>), [NEWTAB]: { profiles: layout } })) as Record<string, unknown>
    const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: shapes.legacy[0], ordinal: shapes.legacy[1], writer }
    daemon.rows.set('settings', row)
    return { rev: row.rev, hash: row.hash! }
  }

  /** The settings row the SOT ends with: ordinal 4, this build's fingerprint, the layout under `presets`. */
  function expectSotUpgraded(layout: Layout, writer: string): void {
    const row = daemon.rows.get('settings')!
    expect(row).toMatchObject({ fingerprint: shapes.current[0], ordinal: shapes.current[1], writer })
    expect((row.payload as Record<string, unknown>)[NEWTAB]).toEqual({ presets: layout })
  }

  /** Over the next 60 s of fake time nothing more is written for settings and the profile is synced. */
  async function expectQuiet(): Promise<void> {
    const before = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(settingsPuts(before)).toEqual([])
    expect(daemon.writes.slice(before)).toEqual([])
    expect(executor!.status().profile).toBe('synced')
    expect(executor!.status().sections.settings).toBe('synced')
  }

  it('ATTACH (pull): the old row lands under presets, ONE settings PUT upgrades the SOT to ordinal 4, then silence', async () => {
    // A — an old client — has pushed its world; its settings row is ordinal 3 with `profiles`
    h.shape = shapeWithSettings(shapes.legacy)
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    leave()
    await oldClientWritesSettings(LAYOUT_A, A)
    problems.length = 0
    resetNewTab()
    const writesBefore = daemon.writes.length

    h.shape = shapeWithSettings(shapes.current)
    world(H2, [], [])
    await attach(B, 'pull')

    expect(useNewTabLayoutStore.getState().presets).toEqual(LAYOUT_A)
    expect(settingsProblems().map((p) => p.kind)).toEqual(['pull-hash-mismatch'])
    expect(settingsPuts(writesBefore, B).map((w) => w.outcome)).toEqual(['applied'])
    expect(daemon.writes.slice(writesBefore).filter((w) => w.key !== 'settings')).toEqual([])
    expectSotUpgraded(LAYOUT_A, B)
    await expectQuiet()
    expect(useNewTabLayoutStore.getState().presets).toEqual(LAYOUT_A)
  })

  it('REMOTE EVENT while clean: pulled like any other (i-am-newer), ONE PUT, then silence', async () => {
    h.shape = shapeWithSettings(shapes.current)
    world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
    await attach(B, 'push')
    expect(executor!.status().profile).toBe('synced')
    problems.length = 0
    const writesBefore = daemon.writes.length

    const written = await oldClientWritesSettings(LAYOUT_C)
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: written.rev, hash: written.hash, writerClientId: 'c_cccccccccccc' })
    for (let i = 0; i < 5; i += 1) await vi.advanceTimersByTimeAsync(1_000)

    expect(useNewTabLayoutStore.getState().presets).toEqual(LAYOUT_C)
    expect(executor!.status().profile).toBe('synced') // not locked:schema, not locked:invalid
    expect(settingsProblems().map((p) => p.kind)).toEqual(['pull-hash-mismatch'])
    expect(settingsPuts(writesBefore).map((w) => w.outcome)).toEqual(['applied'])
    expectSotUpgraded(LAYOUT_C, B)
    await expectQuiet()
  })

  it('CONFLICT answered keep-sot, the SOT side ordinal 3: the SOT layout lands under presets — never undefined, never the defaults', async () => {
    h.shape = shapeWithSettings(shapes.current)
    world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
    await attach(B, 'push')
    problems.length = 0

    // the SOT moves (an old client) before B's own edit goes out
    await oldClientWritesSettings(LAYOUT_C)
    useNewTabLayoutStore.setState({ presets: LAYOUT_B })
    await vi.advanceTimersByTimeAsync(2_000)
    expect(executor!.status().sections.settings).toBe('locked:conflict')

    const writesBefore = daemon.writes.length
    const seen: unknown[] = []
    const unsubscribe = useNewTabLayoutStore.subscribe((s) => seen.push(s.presets))
    executor!.resolve('settings', 'sot')
    for (let i = 0; i < 5; i += 1) await vi.advanceTimersByTimeAsync(1_000)
    unsubscribe()

    expect(useNewTabLayoutStore.getState().presets).toEqual(LAYOUT_C)
    const defaults = useNewTabLayoutStore.getInitialState().presets
    expect(seen.filter((p) => p === undefined || JSON.stringify(p) === JSON.stringify(defaults))).toEqual([])
    expect(settingsProblems().filter((p) => p.kind === 'pull-invalid')).toEqual([])
    expect(settingsPuts(writesBefore).map((w) => w.outcome)).toEqual(['applied'])
    expectSotUpgraded(LAYOUT_C, B)
    await expectQuiet()
  })

  it('RESTART with a persisted conflict whose LOCAL side is an old build\'s payload (profiles), answered keep-local: restoreLocal lands it under presets and ONE canonical PUT goes out', async () => {
    h.shape = shapeWithSettings(shapes.current)
    world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
    await attach(B, 'push')
    const base = daemon.rows.get('settings')!
    // what the old build (before the upgrade, same storage) had stashed: its sent snapshot, `profiles` inside
    const localPayload = JSON.parse(JSON.stringify({ ...(base.payload as Record<string, unknown>), [NEWTAB]: { profiles: LAYOUT_B } })) as Record<string, unknown>
    const localHash = await hashSection(localPayload)
    const sot = await oldClientWritesSettings(LAYOUT_C)
    // the old build goes away (a restart into this build): executor and collector stop, storage stays
    collector?.stop()
    executor?.dispose()
    collector = null
    executor = null
    expect(
      saveConflict(PROFILE, 'settings', { base: { rev: base.rev, hash: base.hash }, currentHash: localHash, conflict: { localHash, sot: { rev: sot.rev, hash: sot.hash } } }, { [localHash]: localPayload }),
    ).toBe('ok')
    resetNewTab()
    problems.length = 0
    const writesBefore = daemon.writes.length
    const putCallsBefore = api.putSection.mock.calls.length

    // this build starts over the same storage — no attach direction, the conflict is restored
    h.clientId = B
    executor = createExecutor({
      hostId: M, profileId: PROFILE, isLeader: () => true, isReachable: () => true, autoSync: () => true,
      onProblem: (p) => problems.push(p), buildNow: (key) => buildSectionPayload(key as ProfileSectionKey), initialDirection: () => null, onInitialSettled: () => undefined,
    })
    const ex = executor
    collector = startCollector({ onSection: (r) => ex.onSection(r) })
    await collector.primeAll()
    executor.onReconnected()
    for (let i = 0; i < 3; i += 1) await vi.advanceTimersByTimeAsync(1_000)
    expect(executor.status().sections.settings).toBe('locked:conflict')

    executor.resolve('settings', 'local')
    for (let i = 0; i < 5; i += 1) await vi.advanceTimersByTimeAsync(1_000)

    expect(useNewTabLayoutStore.getState().presets).toEqual(LAYOUT_B)
    expect(settingsProblems()).toEqual([]) // no restore-invalid, no apply-threw
    // Keep-local pushes the restored snapshot — upcast first: the old build's `profiles` never goes out
    // under this build's ordinal 4 / fingerprint, not even transiently. ONE PUT, canonical shape.
    const puts = api.putSection.mock.calls.slice(putCallsBefore).filter((c) => c[2] === 'settings').map((c) => c[3] as { payload: Record<string, unknown>; ordinal: number; hash: string })
    expect(puts.map((b) => [b.payload[NEWTAB], b.ordinal])).toEqual([[{ presets: LAYOUT_B }, shapes.current[1]]])
    expect(Object.hasOwn(puts[0].payload[NEWTAB] as object, 'profiles')).toBe(false)
    expect(puts[0].hash).not.toBe(localHash)
    expect(puts[0].hash).toBe(await hashSection(puts[0].payload))
    expect(settingsPuts(writesBefore).map((w) => w.outcome)).toEqual(['applied'])
    expectSotUpgraded(LAYOUT_B, B)
    await expectQuiet()
    expect(useNewTabLayoutStore.getState().presets).toEqual(LAYOUT_B)
  })
})

describe('P3e OLD side: an ordinal-3 client (the real old pair) meets the settings row this build writes (ordinal 4)', () => {
  let shapes: Awaited<ReturnType<typeof realSettingsShapes>>

  let putCallsAtStart = 0

  beforeEach(async () => {
    shapes = await realSettingsShapes()
    resetNewTab()
    putCallsAtStart = api.putSection.mock.calls.length
  })
  afterEach(() => {
    // Whatever path got it there, a settings PUT at this build's ordinal is in this build's shape:
    // an ordinal-4 row whose payload still says `profiles` would be a fingerprint/payload mismatch on the SOT.
    const bodies = api.putSection.mock.calls.slice(putCallsAtStart).filter((c) => c[2] === 'settings').map((c) => c[3] as { payload: Record<string, unknown>; ordinal: number })
    for (const b of bodies.filter((x) => x.ordinal === shapes.current[1])) {
      expect(Object.hasOwn((b.payload[NEWTAB] ?? {}) as object, 'profiles')).toBe(false)
    }
    resetNewTab()
  })

  function renameFirstWorkspace(name: string): void {
    const { workspaces } = useWorkspaceStore.getState()
    useWorkspaceStore.setState({ workspaces: workspaces.map((w, i) => (i === 0 ? { ...w, name } : w)) })
  }

  it('locks the whole profile (locked:schema, sot-is-newer) and writes NOTHING — after the event, after onReconnected, after an edit of another section', async () => {
    h.shape = shapeWithSettings(shapes.legacy) // the old client
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    expect(executor!.status().profile).toBe('synced')
    const writesBefore = daemon.writes.length

    // this build (another machine) writes settings: its real pair, the layout under presets
    const cur = daemon.rows.get('settings')!
    const payload = JSON.parse(JSON.stringify({ ...(cur.payload as Record<string, unknown>), [NEWTAB]: { presets: LAYOUT_B } })) as Record<string, unknown>
    const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: shapes.current[0], ordinal: shapes.current[1], writer: B }
    daemon.rows.set('settings', row)
    const revsAfterNewWrite = daemon.revs()

    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: row.rev, hash: row.hash!, writerClientId: B })
    await vi.advanceTimersByTimeAsync(1_000)
    expect(executor!.status()).toMatchObject({
      profile: 'locked:schema',
      schemaLock: { section: 'settings', verdict: 'sot-is-newer', mine: { fingerprint: shapes.legacy[0], ordinal: 3 }, sot: { fingerprint: shapes.current[0], ordinal: shapes.current[1] } },
    })
    expect(daemon.writes.slice(writesBefore)).toEqual([])

    executor!.onReconnected()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status().profile).toBe('locked:schema')
    expect(daemon.writes.slice(writesBefore)).toEqual([])

    renameFirstWorkspace('renamed-under-the-lock')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status().profile).toBe('locked:schema')
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(daemon.revs()).toEqual(revsAfterNewWrite)
    expect(daemon.rows.get('settings')).toEqual(row) // the ordinal-4 row is left exactly as written
  })
})

/* ─── host-daemon-id D6: hosts ordinal 1 → 2 (`hosts.*.daemonId`) — coexistence, upcast, no ping-pong ─── */

/** The two REAL hosts shapes: this build's, and the ordinal-1 one (the same list without `daemonId`). */
async function realHostsShapes(): Promise<{ current: [string, number]; legacy: [string, number] }> {
  const legacyList = PROJECTIONS.hosts.filter((p) => p !== 'hosts.*.daemonId' && p !== 'hosts.*.aliases')
  expect(legacyList).toHaveLength(PROJECTIONS.hosts.length - 2)
  expect(SECTION_SCHEMA_ORDINAL.hosts).toBe(3)
  return { current: [await sectionFingerprint('hosts'), SECTION_SCHEMA_ORDINAL.hosts], legacy: [await fingerprintOf(legacyList), 1] }
}

const hostsPuts = (from: number, clientId?: string) =>
  daemon.writes.slice(from).filter((w) => w.op === 'put' && w.key === 'hosts' && (clientId === undefined || w.clientId === clientId))

const hostsProblems = () => problems.filter((p) => p.section === 'hosts')

type HostsRow = { hosts: Record<string, Partial<HostConfig> & { aliases?: string[] }>; hostOrder: string[] }
const sotHosts = (): HostsRow => daemon.rows.get('hosts')!.payload as unknown as HostsRow

/** The wire key of a host claiming `daemonId` (host-sync-identity: canonical rows are keyed by it). */
const wireOf = (daemonId: string): string => syncIdOfSync(daemonId)

/** Give the local host `id` a claimed daemon identity, as `observeDaemonId` would have. */
function claim(id: string, daemonId: string): void {
  const { hosts } = useHostStore.getState()
  useHostStore.setState({ hosts: { ...hosts, [id]: { ...hosts[id], daemonId } } })
}

/** Over the next 60 s of fake time nothing more is written and the profile (hosts included) is synced. */
async function expectHostsQuiet(): Promise<void> {
  const before = daemon.writes.length
  await vi.advanceTimersByTimeAsync(60_000)
  expect(hostsPuts(before)).toEqual([])
  expect(daemon.writes.slice(before)).toEqual([])
  expect(executor!.status().profile).toBe('synced')
  expect(executor!.status().sections.hosts).toBe('synced')
}

describe('D6 NEW side: this build (hosts ordinal 3, wire ids) meets hosts an ordinal-1 client wrote (local ids, no daemonId)', () => {
  let shapes: Awaited<ReturnType<typeof realHostsShapes>>

  beforeEach(async () => {
    shapes = await realHostsShapes()
  })

  /** The SOT's hosts row: this build's ordinal and fingerprint, written by `writer`. */
  function expectSotUpgraded(writer: string): void {
    expect(daemon.rows.get('hosts')).toMatchObject({ fingerprint: shapes.current[0], ordinal: shapes.current[1], writer })
  }

  /** What an ordinal-1 client holds of this world: local-id keys, no daemonId, no aliases (host-sync-identity: it never translated). */
  function legacyRowOfStore(): HostsRow {
    const payload = buildHostsSection(useHostStore.getState(), identityOfSync({})) as unknown as HostsRow
    const copy = JSON.parse(JSON.stringify(payload)) as HostsRow
    for (const row of Object.values(copy.hosts)) delete row.daemonId
    return copy
  }

  it('ATTACH (pull): A\'s hosts land, the LOCAL daemonId is kept (D6 upcast, before matching), ONE canonical hosts PUT, the tabs that name it go canonical once, then silence', async () => {
    h.shape = shapeWith({ hosts: shapes.legacy }) // A is an old client: its hosts carry no daemonId
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    leave()
    expect(daemon.rows.get('hosts')).toMatchObject({ ordinal: 1, fingerprint: shapes.legacy[0] })
    expect(Object.hasOwn(sotHosts().hosts[M], 'daemonId')).toBe(false)
    problems.length = 0
    const writesBefore = daemon.writes.length

    h.shape = shapeWith({ hosts: shapes.current })
    world(H2, [], [])
    claim(M, 'mini:b') // B verified the master daemon before attaching
    await attach(B, 'pull')

    expect(useHostStore.getState().hosts[H2].name).toBe('named-by-A') // A's hosts landed
    expect(useHostStore.getState().hosts[M].daemonId).toBe('mini:b') // …and B's claim survived the pull
    expect(Object.hasOwn(useHostStore.getState().hosts[H2], 'daemonId')).toBe(false)
    expect(executor!.status().profile).toBe('synced') // never locked:schema / locked:invalid
    expect(hostsProblems().map((p) => p.kind)).toEqual(['pull-hash-mismatch'])
    expect(hostsPuts(writesBefore, B).map((w) => w.outcome)).toEqual(['applied'])
    // A's pane on the master (a legacy id) resolved through the alias, and its section went canonical ONCE
    const paneHost = (useTabStore.getState().tabs.ta1.layout as Extract<PaneLayout, { type: 'leaf' }>).pane.content
    expect(paneHost).toMatchObject({ hostId: M })
    expect(paneHost).not.toHaveProperty('terminated')
    expect(daemon.writes.slice(writesBefore).filter((w) => w.key !== 'hosts').map((w) => [w.key, w.outcome])).toEqual([['tabs.wa1', 'applied']])
    expectSotUpgraded(B)
    expect(sotHosts().hosts[wireOf('mini:b')]).toMatchObject({ daemonId: 'mini:b', aliases: [M] })
    expect(sotHosts().hosts[H2].name).toBe('named-by-A')
    await expectHostsQuiet()
    expect(useHostStore.getState().hosts[M].daemonId).toBe('mini:b')
  })

  it('REMOTE EVENT while clean: an old client\'s hosts edit (local-id keys) is pulled (i-am-newer), the local daemonId kept, ONE PUT, then silence', async () => {
    h.shape = shapeWith({ hosts: shapes.current })
    world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
    claim(M, 'mini:b')
    await attach(B, 'push')
    expect(executor!.status().profile).toBe('synced')
    expect(sotHosts().hosts[wireOf('mini:b')].daemonId).toBe('mini:b')
    problems.length = 0
    const writesBefore = daemon.writes.length

    // an ordinal-1 client renames host-two: it never knew `daemonId`, and it keys by its (shared, pre-fix) local ids
    const old = legacyRowOfStore()
    old.hosts[H2].name = 'renamed-by-old'
    const payload = old as unknown as Record<string, unknown>
    const cur = daemon.rows.get('hosts')!
    const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: shapes.legacy[0], ordinal: 1, writer: 'c_cccccccccccc' }
    daemon.rows.set('hosts', row)
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'hosts', rev: row.rev, hash: row.hash!, writerClientId: 'c_cccccccccccc' })
    for (let i = 0; i < 5; i += 1) await vi.advanceTimersByTimeAsync(1_000)

    expect(useHostStore.getState().hosts[H2].name).toBe('renamed-by-old')
    expect(useHostStore.getState().hosts[M].daemonId).toBe('mini:b')
    expect(executor!.status().profile).toBe('synced')
    expect(hostsProblems().map((p) => p.kind)).toEqual(['pull-hash-mismatch'])
    expect(hostsPuts(writesBefore).map((w) => w.outcome)).toEqual(['applied'])
    expectSotUpgraded(B)
    expect(sotHosts().hosts[wireOf('mini:b')].daemonId).toBe('mini:b')
    expect(sotHosts().hosts[H2].name).toBe('renamed-by-old')
    await expectHostsQuiet()
  })

  // host-sync-identity §11.6: a row carrying a daemonId matches BY daemonId ONLY. Two devices that claim DIFFERENT
  // daemons for the master's address disagree about what the master is; the pull no longer adopts the other claim
  // (D6's "SOT wins" on daemonId) — the master's daemon has no row, so it is `removes-master-host`: locked:invalid,
  // nothing written, B's claim untouched (PR 3's mismatch pause is the user-facing half).
  it('TWO new clients with DIFFERENT daemonIds for the master: B\'s upgrade PUT loses to C\'s, the pull of C\'s row is refused (removes-master-host) — bounded, no loop', async () => {
    h.shape = shapeWith({ hosts: shapes.legacy })
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    leave()
    problems.length = 0
    const writesBefore = daemon.writes.length

    const C = 'c_cccccccccccc'
    const put = api.putSection.getMockImplementation()!
    let cWrote = false
    api.putSection.mockImplementation(async (hostId, profileId, key, body) => {
      if (key === 'hosts' && !cWrote) {
        cWrote = true
        const cur = daemon.rows.get('hosts')!
        const theirs = JSON.parse(JSON.stringify(cur.payload)) as HostsRow
        theirs.hosts[M].daemonId = 'mini:c'
        const payload = theirs as unknown as Record<string, unknown>
        daemon.put('hosts', { baseRev: cur.rev, hash: await hashSection(payload), payload, fingerprint: shapes.current[0], ordinal: shapes.current[1], clientId: C })
      }
      return put(hostId, profileId, key, body)
    })

    h.shape = shapeWith({ hosts: shapes.current })
    world(H2, [], [])
    claim(M, 'mini:b')
    await attach(B, 'pull')
    for (let i = 0; i < 10; i += 1) await vi.advanceTimersByTimeAsync(1_000)

    expect(daemon.writes.slice(writesBefore).filter((w) => w.key === 'hosts').map((w) => [w.clientId, w.outcome])).toEqual([[C, 'applied'], [B, 'conflict']])
    expect(executor!.status().sections.hosts).toBe('locked:invalid')
    expect(executor!.status().detail.hosts.invalidReason).toBe('removes-master-host')
    expect(useHostStore.getState().hosts[M].daemonId).toBe('mini:b')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(hostsPuts(writesBefore, B)).toHaveLength(1) // bounded: the one upgrade attempt, no loop
    expect(sotHosts().hosts[M].daemonId).toBe('mini:c') // C's row is left as it is
  })

  // codex R1 P1 (D6): the upcast must not carry this device's claim onto another incarnation. host-sync-identity:
  // a re-point arriving under the writer's LOCAL id (its claim dropped by the re-point, spec D3) does not match a
  // local host that holds a claim (§11.6) — the host is recreated under a new local id, the old one cascaded. Either
  // way the old claim is never PUT, and nothing loops.
  for (const writer of [{ name: 'a NEW client (daemonId cleared, keyed by its local id)', legacy: false }, { name: 'an OLD client (ordinal 1)', legacy: true }]) {
    it(`a re-point by ${writer.name} lands WITHOUT the local daemonId, and the old claim is never PUT`, async () => {
      h.shape = shapeWith({ hosts: shapes.current })
      world('named-by-B', [ws('wb1', ['tb1'])], [tab('tb1')])
      claim(H2, 'mini:b')
      await attach(B, 'push')
      expect(sotHosts().hosts[wireOf('mini:b')].daemonId).toBe('mini:b')
      problems.length = 0
      const putCallsBefore = api.putSection.mock.calls.length

      const moved = legacyRowOfStore()
      moved.hosts[H2].ip = '10.7.7.7'
      const payload = moved as unknown as Record<string, unknown>
      const cur = daemon.rows.get('hosts')!
      const shape = writer.legacy ? shapes.legacy : shapes.current
      const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: shape[0], ordinal: shape[1], writer: 'c_eeeeeeeeeeee' }
      daemon.rows.set('hosts', row)
      executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'hosts', rev: row.rev, hash: row.hash!, writerClientId: 'c_eeeeeeeeeeee' })
      for (let i = 0; i < 5; i += 1) await vi.advanceTimersByTimeAsync(1_000)
      await vi.advanceTimersByTimeAsync(60_000)

      const hosts = Object.values(useHostStore.getState().hosts)
      const moved7 = hosts.filter((h) => h.ip === '10.7.7.7')
      expect(moved7).toHaveLength(1)
      expect(Object.hasOwn(moved7[0], 'daemonId')).toBe(false)
      expect(hosts.filter((h) => h.daemonId === 'mini:b')).toEqual([]) // the old claim is gone with its incarnation
      const hostsBodies = api.putSection.mock.calls.slice(putCallsBefore).filter((c) => c[2] === 'hosts').map((c) => JSON.stringify(c[3]))
      expect(hostsBodies.filter((b) => b.includes('mini:b'))).toEqual([]) // the wrong daemonId never goes out
      expect(hostsBodies.length).toBeLessThanOrEqual(1) // at most the one re-key of the recreated host
      expect(executor!.status().profile).toBe('synced')
    })
  }
})

describe('D6 OLD side: an ordinal-1 hosts client meets the hosts row this build writes (ordinal 3)', () => {
  let shapes: Awaited<ReturnType<typeof realHostsShapes>>

  beforeEach(async () => {
    shapes = await realHostsShapes()
  })

  it('locks the whole profile (locked:schema, sot-is-newer) and writes NOTHING — after the event, after onReconnected, after a hosts edit', async () => {
    h.shape = shapeWith({ hosts: shapes.legacy }) // the old client
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    expect(executor!.status().profile).toBe('synced')
    const writesBefore = daemon.writes.length

    // this build (another machine) writes hosts: its real pair, a daemonId inside
    const cur = daemon.rows.get('hosts')!
    const theirs = JSON.parse(JSON.stringify(cur.payload)) as HostsRow
    theirs.hosts[M].daemonId = 'mini:new'
    const payload = theirs as unknown as Record<string, unknown>
    const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: shapes.current[0], ordinal: shapes.current[1], writer: B }
    daemon.rows.set('hosts', row)
    const revsAfterNewWrite = daemon.revs()

    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'hosts', rev: row.rev, hash: row.hash!, writerClientId: B })
    await vi.advanceTimersByTimeAsync(1_000)
    expect(executor!.status()).toMatchObject({
      profile: 'locked:schema',
      schemaLock: { section: 'hosts', verdict: 'sot-is-newer', mine: { fingerprint: shapes.legacy[0], ordinal: 1 }, sot: { fingerprint: shapes.current[0], ordinal: shapes.current[1] } },
    })
    expect(daemon.writes.slice(writesBefore)).toEqual([])

    executor!.onReconnected()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status().profile).toBe('locked:schema')
    expect(daemon.writes.slice(writesBefore)).toEqual([])

    const { hosts } = useHostStore.getState()
    useHostStore.setState({ hosts: { ...hosts, [H2]: { ...hosts[H2], name: 'renamed-under-the-lock' } } })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status().profile).toBe('locked:schema')
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(daemon.revs()).toEqual(revsAfterNewWrite)
    expect(daemon.rows.get('hosts')).toEqual(row) // the ordinal-2 row is left exactly as written
    expect(Object.hasOwn(useHostStore.getState().hosts[M], 'daemonId')).toBe(false) // nothing was applied
  })
})

/* ─── host-sync-identity PR 2: two clients, one daemon, INDEPENDENT local ids — through the real executor ─── */

describe('host-sync-identity: B added the SAME daemon under its own id (the case that shipped broken)', () => {
  const DAEMON = 'mini-lab:278cbm'
  const mlab = (id: string): HostConfig => ({ ...host(id), ip: '100.64.0.2', daemonId: DAEMON })
  const paneOn = (hostId: string): PaneLayout => ({ type: 'leaf', pane: { id: 'p1', content: { kind: 'tmux-session', hostId, sessionCode: 'c1', mode: 'terminal', cachedName: 'one', tmuxInstance: 'inst' } } })

  it('PULL: B keeps its id, nothing is locked or branded host-removed, its own extra host goes; B writes ONE hosts PUT (its own id as an alias), then silence — and A, back, writes nothing', async () => {
    h.shape = null
    useHostStore.setState({ hosts: { aaaaaa: mlab('aaaaaa') }, hostOrder: ['aaaaaa'], activeHostId: 'aaaaaa', runtime: {} })
    useTabStore.setState({ tabs: { ta1: { ...tab('ta1'), layout: paneOn('aaaaaa') } }, tabOrder: ['ta1'], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [ws('wa1', ['ta1'])], activeWorkspaceId: 'wa1' })
    useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
    await attach(A, 'push', 'aaaaaa')
    expect(Object.keys(sotHosts().hosts)).toEqual([syncIdOfSync(DAEMON)])
    leave()
    problems.length = 0
    const writesBefore = daemon.writes.length

    useHostStore.setState({ hosts: { bbbbbb: mlab('bbbbbb'), onlyb1: { ...host('onlyb1'), ip: '10.9.9.9', order: 1 } }, hostOrder: ['bbbbbb', 'onlyb1'], activeHostId: 'bbbbbb', runtime: {} })
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    await attach(B, 'pull', 'bbbbbb')
    await vi.advanceTimersByTimeAsync(60_000)

    expect(executor!.status().profile).toBe('synced')
    expect(Object.values(executor!.status().sections).filter((st) => st !== 'synced')).toEqual([])
    expect(Object.keys(useHostStore.getState().hosts)).toEqual(['bbbbbb'])
    const content = (useTabStore.getState().tabs.ta1.layout as Extract<PaneLayout, { type: 'leaf' }>).pane.content
    expect(content).toMatchObject({ hostId: 'bbbbbb' })
    expect(content).not.toHaveProperty('terminated')
    // every section B built hashes as A's did, but hosts: B's row adds B's own id as an alias (spec §11.7)
    expect(daemon.writes.slice(writesBefore).map((w) => [w.key, w.clientId, w.outcome])).toEqual([['hosts', B, 'applied']])
    expect(sotHosts().hosts[syncIdOfSync(DAEMON)].aliases).toEqual(['aaaaaa', 'bbbbbb'])
    expect(problems.filter((p) => p.kind !== 'sections-unrendered')).toEqual([]) // no pull-hash-mismatch either (#1369)
    const quiet = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.length).toBe(quiet)

    // A reattaches (pull) with its own world: it takes B's row, whose aliases already list A's id — nothing to write
    leave()
    useHostStore.setState({ hosts: { aaaaaa: mlab('aaaaaa') }, hostOrder: ['aaaaaa'], activeHostId: 'aaaaaa', runtime: {} })
    useTabStore.setState({ tabs: { ta1: { ...tab('ta1'), layout: paneOn('aaaaaa') } }, tabOrder: ['ta1'], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [ws('wa1', ['ta1'])], activeWorkspaceId: 'wa1' })
    const beforeA = daemon.writes.length
    await attach(A, 'pull', 'aaaaaa')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.slice(beforeA)).toEqual([])
    expect(executor!.status().profile).toBe('synced')
  })

  // #1369 (real machine, #1366 acceptance): B's only problem after that pull was `pull-hash-mismatch · hosts` — for
  // the designed own-alias write-back. It is not a problem: the log stays empty, and the write-back goes once.
  it('PULL: B\'s problem log stays EMPTY — the own-alias write-back is one hosts PUT, not a problem; the SOT row lists both ids; a second pass pushes nothing', async () => {
    h.shape = null
    useHostStore.setState({ hosts: { aaaaaa: mlab('aaaaaa') }, hostOrder: ['aaaaaa'], activeHostId: 'aaaaaa', runtime: {} })
    useTabStore.setState({ tabs: { ta1: { ...tab('ta1'), layout: paneOn('aaaaaa') } }, tabOrder: ['ta1'], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [ws('wa1', ['ta1'])], activeWorkspaceId: 'wa1' })
    useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
    await attach(A, 'push', 'aaaaaa')
    expect(sotHosts().hosts[syncIdOfSync(DAEMON)].aliases).toEqual(['aaaaaa'])
    leave()
    problems.length = 0
    const writesBefore = daemon.writes.length

    useHostStore.setState({ hosts: { bbbbbb: mlab('bbbbbb') }, hostOrder: ['bbbbbb'], activeHostId: 'bbbbbb', runtime: {} })
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    await attach(B, 'pull', 'bbbbbb')
    await vi.advanceTimersByTimeAsync(60_000)

    // (a brand-new B lists `tabs.wa1` before its workspace arrives: the once-only notice the attach tests above allow too)
    const logged = () => problems.filter((p) => p.kind !== 'sections-unrendered')
    expect(logged()).toEqual([])
    expect(hostsProblems()).toEqual([])
    expect(executor!.status().profile).toBe('synced')
    expect(daemon.writes.slice(writesBefore).map((w) => [w.key, w.clientId, w.outcome])).toEqual([['hosts', B, 'applied']])
    expect(sotHosts().hosts[syncIdOfSync(DAEMON)].aliases).toEqual(['aaaaaa', 'bbbbbb'])
    await expectHostsQuiet()
    expect(logged()).toEqual([])
  })
})

/* ─── host-sync-identity: an alpha.434 client (hosts ordinal 2, no `aliases`, local-id keys) meets this build's rows ─── */

describe('host-sync-identity OLD side: an ordinal-2 hosts client meets the hosts row this build writes (ordinal 3)', () => {
  it('locks the whole profile (locked:schema, sot-is-newer) and writes nothing — so it never applies a wire id', async () => {
    const legacyList = PROJECTIONS.hosts.filter((p) => p !== 'hosts.*.aliases')
    const old: [string, number] = [await fingerprintOf(legacyList), 2]
    const current: [string, number] = [await sectionFingerprint('hosts'), SECTION_SCHEMA_ORDINAL.hosts]
    h.shape = shapeWith({ hosts: old })
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    const writesBefore = daemon.writes.length

    const cur = daemon.rows.get('hosts')!
    const wire = syncIdOfSync('mini:new')
    const payload = { hosts: { [wire]: { ...host(M), id: wire, daemonId: 'mini:new' } }, hostOrder: [wire] } as unknown as Record<string, unknown>
    daemon.rows.set('hosts', { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: current[0], ordinal: current[1], writer: B })
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'hosts', rev: cur.rev + 1, hash: daemon.rows.get('hosts')!.hash!, writerClientId: B })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(executor!.status()).toMatchObject({ profile: 'locked:schema', schemaLock: { section: 'hosts', verdict: 'sot-is-newer' } })
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(Object.keys(useHostStore.getState().hosts)).toEqual([M, H2]) // nothing applied
  })
})

/* ─── host-sync-identity: the wire marker in the fingerprint — tabs and settings lock an old client ON THEIR OWN ─── */

describe('host-sync-identity: an alpha.434 client (ordinal-2-era shapes) meets ONE section this build writes', () => {
  /** The real shape tables: alpha.434's (no marker, no `aliases`) and this build's. */
  async function tables(): Promise<{ old: NonNullable<typeof h.shape>; mine: NonNullable<typeof h.shape> }> {
    const fp = (paths: readonly string[]) => fingerprintOf(paths)
    const old = {
      hosts: [await fp(PROJECTIONS.hosts.filter((p) => p !== 'hosts.*.aliases')), 2],
      settings: [await fp(PROJECTIONS.settings), 4],
      workspaces: [await fp(PROJECTIONS.workspaces), 1],
      tabs: [await fp(PROJECTIONS.tabs), 1],
    } as NonNullable<typeof h.shape>
    const mine = {
      hosts: [await sectionFingerprint('hosts'), SECTION_SCHEMA_ORDINAL.hosts],
      settings: [await sectionFingerprint('settings'), SECTION_SCHEMA_ORDINAL.settings],
      workspaces: [await sectionFingerprint('workspaces'), SECTION_SCHEMA_ORDINAL.workspaces],
      tabs: [await sectionFingerprint('tabs'), SECTION_SCHEMA_ORDINAL.tabs],
    } as NonNullable<typeof h.shape>
    return { old, mine }
  }

  it.each(['tabs.wa1', 'settings'] as const)('a new %s row alone (hosts untouched) → the old client locks the whole profile (sot-is-newer), applies and writes NOTHING', async (section) => {
    const { old, mine } = await tables()
    h.shape = old
    world('named-by-A', [ws('wa1', ['ta1'])], [tab('ta1')])
    await attach(A, 'push')
    expect(executor!.status().profile).toBe('synced')
    const writesBefore = daemon.writes.length
    const tabsBefore = useTabStore.getState().tabs

    const cur = daemon.rows.get(section)!
    const kind = section === 'settings' ? 'settings' : 'tabs'
    // this build rewrote it: wire ids inside (here: the master's sync id on the pane)
    const payload = JSON.parse(JSON.stringify(cur.payload).split(`"${M}"`).join(`"${syncIdOfSync('mini:new')}"`)) as Record<string, unknown>
    const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: mine[kind][0], ordinal: mine[kind][1], writer: B }
    daemon.rows.set(section, row)
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section, rev: row.rev, hash: row.hash!, writerClientId: B })
    await vi.advanceTimersByTimeAsync(60_000)

    expect(executor!.status()).toMatchObject({ profile: 'locked:schema', schemaLock: { section, verdict: 'sot-is-newer' } })
    expect(daemon.writes.slice(writesBefore)).toEqual([])
    expect(useTabStore.getState().tabs).toBe(tabsBefore) // no wire id was applied, nothing branded host-removed
    expect(daemon.rows.get(section)).toEqual(row)
  })

  it('the NEW side meets the old client\'s tabs row (i-am-newer): pulled, rewritten canonical ONCE, then silence', async () => {
    const { old, mine } = await tables()
    h.shape = mine
    world('named-by-B', [ws('wa1', ['ta1'])], [tab('ta1')])
    claim(M, 'mini:b')
    await attach(B, 'push')
    expect(executor!.status().profile).toBe('synced')
    problems.length = 0

    // the old client (shared, pre-fix ids) pins its tab: local ids on the pane, its own shape
    const cur = daemon.rows.get('tabs.wa1')!
    const legacy = JSON.parse(JSON.stringify(cur.payload).split(`"${syncIdOfSync('mini:b')}"`).join(`"${M}"`)) as { tabs: Record<string, { pinned: boolean }> }
    legacy.tabs.ta1.pinned = true
    const payload = legacy as unknown as Record<string, unknown>
    const row: Row = { rev: cur.rev + 1, hash: await hashSection(payload), payload, fingerprint: old.tabs[0], ordinal: old.tabs[1], writer: 'c_cccccccccccc' }
    daemon.rows.set('tabs.wa1', row)
    const writesBefore = daemon.writes.length
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'tabs.wa1', rev: row.rev, hash: row.hash!, writerClientId: 'c_cccccccccccc' })
    await vi.advanceTimersByTimeAsync(60_000)

    expect(useTabStore.getState().tabs.ta1.pinned).toBe(true)
    expect((useTabStore.getState().tabs.ta1.layout as Extract<PaneLayout, { type: 'leaf' }>).pane.content).not.toHaveProperty('terminated')
    expect(daemon.writes.slice(writesBefore).map((w) => [w.key, w.outcome])).toEqual([['tabs.wa1', 'applied']])
    expect(daemon.rows.get('tabs.wa1')).toMatchObject({ fingerprint: mine.tabs[0], ordinal: mine.tabs[1], writer: B })
    expect(JSON.stringify(daemon.rows.get('tabs.wa1')!.payload)).toContain(syncIdOfSync('mini:b'))
    expect(executor!.status().profile).toBe('synced')
    const quiet = daemon.writes.length
    await vi.advanceTimersByTimeAsync(60_000)
    expect(daemon.writes.length).toBe(quiet)
  })
})
