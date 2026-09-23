// spa/src/lib/profile/executor.lock-release.integration.test.ts — #1369 critic: the apply's payload can go stale
// AFTER the apply's last look at the stores. `withOperationLock` releases the lock in its `finally`, synchronously,
// and the hook's lock observer (useMultiHostEventWs.ts `createOperationLockObserver`) runs
// `reconcileAfterLockRelease` right there — which, for a host that is attach-ready but not versioned & live, runs
// the revive pass synchronously and rewrites tab layouts. All of that happens before the outcome reaches the
// executor. The real executor, the real collector, the real apply, the real lock and its real observer; only the
// network (`./api`), the digest and `shapeTable` are faked.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { Session } from '../host-api'
import type { PaneLayout, Tab, TmuxSessionContent, Workspace } from '../../types/tab'
import type { ProfileIndexEntry, Result, SectionMeta } from './api'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { __resetMasterWorldForTest } from './master-world'
import { clearSectionStore } from './section-store'
import type { ProfileSectionKey } from './types'
import { openAttachGate } from '../rebuild/attach-gate'
import { __resetForTests } from '../rebuild/session-version'
import { noteReconciledSessions } from '../rebuild/revive'

vi.mock('../rebuild/cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('../rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  // FNV-1a under eight seeds: deterministic, synchronous, 64 lower-case hex (the section store validates hashes).
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

const api = vi.mocked(await import('./api'))
const { createOperationLockObserver } = await import('../../hooks/useMultiHostEventWs')
const { __resetRefreshForTests } = await import('../rebuild/refresh-sessions')

const M = 'host-master'
const PROFILE = 'p_0123456789ab'
const OTHER_CLIENT = 'c_bbbbbbbbbbbb'

function leaf(paneId: string, over: Partial<TmuxSessionContent> = {}): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content: { kind: 'tmux-session', hostId: M, sessionCode: `c-${paneId}`, mode: 'terminal', cachedName: paneId, tmuxInstance: 'inst', ...over } } }
}
const tab = (id: string, layout: PaneLayout = leaf(`p-${id}`)): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })
const ws = (id: string, tabs: string[]): Workspace => ({ id, name: id.toUpperCase(), tabs, activeTabId: tabs[0] ?? null })
const liveOf = (paneId: string): Session => ({ code: `c-${paneId}`, name: paneId, cwd: '', mode: 'terminal', tmux_instance: 'inst' })
/** The session a tmux restart left behind under the arriving pane's name. */
const LATE: Session = { code: 'late01', name: 'late', cwd: '', mode: 'terminal', tmux_instance: '222:2000' }

function meta(section: string, rev: number, hash: string): SectionMeta {
  const kind = section.startsWith('tabs.') ? 'tabs' : section
  const shape: Record<string, [string, number]> = { hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] }
  return { section, rev, hash, fingerprint: shape[kind][0], ordinal: shape[kind][1], writer: OTHER_CLIENT, updatedAt: 0 }
}
function index(sections: SectionMeta[]): Result<ProfileIndexEntry[]> {
  return { kind: 'ok', value: [{ id: PROFILE, name: 'p', createdAt: 0, updatedAt: 0, sections, attachments: [] }] }
}

/** Every `tabs.wa` payload this client PUT. */
const tabsPuts = (): unknown[] => api.putSection.mock.calls.filter((c) => c[2] === 'tabs.wa').map((c) => (c[3] as { payload: unknown }).payload)

let executor: Executor
let collector: Collector
let unsubscribe: () => void = () => {}
const problems: Array<{ kind: string; section?: string; detail: string }> = []

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  vi.clearAllMocks()
  __resetMasterWorldForTest()
  __resetForTests()
  __resetRefreshForTests()
  useHostStore.setState({ hosts: { [M]: { id: M, name: M, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0 } }, hostOrder: [M], activeHostId: M, runtime: {} })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useSessionStore.setState({ sessions: {} })
  useTabStore.setState({ tabs: { a1: tab('a1') }, tabOrder: ['a1'], activeTabId: 'a1', visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1'])], activeWorkspaceId: 'wa', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  // Attach-ready but NOT versioned & live (an old daemon, or a connection before its first versioned frame): the lock
  // observer's release runs the revive pass SYNCHRONOUSLY over the list reconciled last.
  openAttachGate(M)
  noteReconciledSessions(M, [liveOf('p-a1'), LATE])
  unsubscribe = useRebuildStore.subscribe(createOperationLockObserver())
  api.getSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
  api.deleteSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
})

afterEach(() => {
  unsubscribe()
  collector.stop()
  executor.dispose()
  clearSectionStore()
  __resetRefreshForTests()
  useHostStore.getState().reset()
  __resetMasterWorldForTest()
  localStorage.clear()
  vi.useRealTimers()
})

it('a pull whose lock release revives a pane: the payload the apply hashed is never PUT — only what the stores hold after the revive (#1369 critic)', async () => {
  executor = createExecutor({
    hostId: M,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    buildNow: (key) => buildSectionPayload(key as ProfileSectionKey), // what start.ts wires
  })
  const ex = executor
  collector = startCollector({ onSection: (r) => ex.onSection(r) })

  // first attach: every section created at rev 1
  api.listProfiles.mockResolvedValue(index([]))
  api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
  await collector.primeAll()
  executor.onReconnected()
  await vi.advanceTimersByTimeAsync(0)
  expect(executor.status().sections['tabs.wa']).toBe('synced')
  api.putSection.mockClear()
  api.putSection.mockResolvedValue({ kind: 'applied', rev: 3 })

  // Another client adds `a2` (a tmux-restarted pane named `late`) and `a3` (on a host this device does not know: the
  // apply marks it host-removed, so the stores never hold what arrived — a mismatch, pushed back).
  const restarted = tab('a2', leaf('p-a2', { sessionCode: 'dead01', cachedName: 'late', tmuxInstance: '111:1000', terminated: 'tmux-restarted' }))
  const elsewhere = tab('a3', leaf('p-a3', { hostId: 'host-unknown-here' }))
  const incoming = { order: ['a1', 'a2', 'a3'], tabs: { a1: tab('a1'), a2: restarted, a3: elsewhere } }
  const sotHash = await hashSection(incoming)
  api.getSection.mockResolvedValue({ kind: 'ok', value: { ...meta('tabs.wa', 2, sotHash), payload: JSON.parse(JSON.stringify(incoming)) as Record<string, unknown> } })
  executor.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'tabs.wa', rev: 2, hash: sotHash, writerClientId: OTHER_CLIENT })
  await vi.advanceTimersByTimeAsync(5_000)

  // the lock release revived `a2` on `late01`, and `a3` is host-removed
  const a2 = useTabStore.getState().tabs.a2.layout
  expect(a2.type === 'leaf' && a2.pane.content).toMatchObject({ sessionCode: 'late01', tmuxInstance: '222:2000' })
  const a3 = useTabStore.getState().tabs.a3.layout
  expect(a3.type === 'leaf' && a3.pane.content).toMatchObject({ terminated: 'host-removed' })

  // What went out is what the stores hold NOW — never the pre-revive snapshot the apply hashed.
  const now = buildSectionPayload('tabs.wa')?.payload
  expect(tabsPuts().length).toBeGreaterThan(0)
  for (const sent of tabsPuts()) expect(JSON.stringify(sent)).not.toContain('dead01')
  expect(tabsPuts().at(-1)).toEqual(now)
  expect(executor.status().sections['tabs.wa']).toBe('synced')
})
