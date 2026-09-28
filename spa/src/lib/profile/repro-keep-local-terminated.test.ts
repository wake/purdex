// spa/src/lib/profile/repro-keep-local-terminated.test.ts — REPRO (Profile Sync bug, 2026-09-28): a `tabs.<ws>` section
// locked by a 409, the user rebuilds a terminated pane WHILE the lock stands, then answers 「保留這台裝置的」(keep local).
// Keep-local restores the SENT snapshot (sync-state.ts `resolved` → `restoreLocal: conflict.localHash`), so the
// rebuild made during the lock is overwritten: the pane goes back to the dead binding, `terminated: 'session-closed'`
// and the old run's `agentExited`. Real executor / collector / apply; network, digest and shapeTable faked.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { PaneLayout, Tab, TmuxSessionContent, Workspace } from '../../types/tab'
import type { ProfileIndexEntry, Result } from './api'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { __resetMasterWorldForTest } from './master-world'
import { clearSectionStore } from './section-store'
import type { ProfileSectionKey } from './types'

vi.mock('../rebuild/cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('../rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
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

const M = 'host-a26'
const PROFILE = 'p_0123456789ab'
const INST = '15260:1790482657'
const OLD: TmuxSessionContent = { kind: 'tmux-session', hostId: M, sessionCode: 'oldc03', mode: 'terminal', cachedName: 'a26', tmuxInstance: INST }

const leaf = (content: TmuxSessionContent): PaneLayout => ({ type: 'leaf', pane: { id: 'p1', content } })
const tab = (layout: PaneLayout): Tab => ({ id: 't1', pinned: false, locked: false, createdAt: 1, layout })
const ws = (id: string, tabs: string[]): Workspace => ({ id, name: id.toUpperCase(), tabs, activeTabId: tabs[0] ?? null })
const index = (): Result<ProfileIndexEntry[]> => ({ kind: 'ok', value: [{ id: PROFILE, name: 'p', createdAt: 0, updatedAt: 0, sections: [], attachments: [] }] })
const pane = (): TmuxSessionContent => {
  const l = useTabStore.getState().tabs.t1.layout
  if (l.type !== 'leaf' || l.pane.content.kind !== 'tmux-session') throw new Error('not a tmux leaf')
  return l.pane.content
}

let executor: Executor
let collector: Collector

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  vi.clearAllMocks()
  __resetMasterWorldForTest()
  useHostStore.setState({ hosts: { [M]: { id: M, name: M, ip: '10.0.0.4', port: 7860, token: 'tok', order: 0 } }, hostOrder: [M], activeHostId: M, runtime: {} })
  useShownHostsStore.setState({ ids: [M] })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useSessionStore.setState({ sessions: {} })
  useTabStore.setState({ tabs: { t1: tab(leaf(OLD)) }, tabOrder: ['t1'], activeTabId: 't1', visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [ws('wa', ['t1'])], activeWorkspaceId: 'wa', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  api.getSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
  api.deleteSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
})

afterEach(() => {
  collector.stop()
  executor.dispose()
  clearSectionStore()
  useHostStore.getState().reset()
  __resetMasterWorldForTest()
  localStorage.clear()
  vi.useRealTimers()
})

it('keep-local after a rebuild made during the lock must not bring the dead pane back', async () => {
  executor = createExecutor({
    hostId: M, profileId: PROFILE, isLeader: () => true, isReachable: () => true, autoSync: () => true,
    onProblem: () => {}, buildNow: (key) => buildSectionPayload(key as ProfileSectionKey),
  })
  const ex = executor
  collector = startCollector({ onSection: (r) => ex.onSection(r) })

  // attached, everything synced at rev 1
  api.listProfiles.mockResolvedValue(index())
  api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
  await collector.primeAll()
  executor.onReconnected()
  await vi.advanceTimersByTimeAsync(0)
  expect(executor.status().sections['tabs.wa']).toBe('synced')

  // 13:29:47 — the agent exits and the session dies: this client writes agentExited + terminated...
  // ...and the other client wrote the same pane first with its own capturedAt → this push gets 409.
  const theirs = { order: ['t1'], tabs: { t1: tab(leaf({ ...OLD, terminated: 'session-closed', rebuild: { sessionName: 'a26', tmuxInstance: INST, capturedAt: 111, agentExited: { at: 1_000, reason: 'session-end' } } as never })) } }
  const theirHash = await hashSection(theirs)
  api.putSection.mockResolvedValue({ kind: 'conflict', rev: 2, hash: theirHash, payload: theirs as unknown as Record<string, unknown> })
  useTabStore.getState().setPaneContent('t1', 'p1', {
    ...OLD, terminated: 'session-closed',
    rebuild: { sessionName: 'a26', tmuxInstance: INST, capturedAt: 222, agentExited: { at: 1_000, reason: 'session-end' } } as never,
  })
  await vi.advanceTimersByTimeAsync(2_000)
  expect(executor.status().sections['tabs.wa']).toBe('locked:conflict')

  // 13:29:53 — WHILE LOCKED, the user rebuilds: new session `hakmez` in the same tmux instance, pane re-pointed
  // (what engine.ts `repointPane` writes: terminated dropped, the rebuild record carried over).
  const rebuilt: TmuxSessionContent = { ...pane(), sessionCode: 'hakmez', cachedName: 'a26', tmuxInstance: INST }
  delete rebuilt.terminated
  useTabStore.getState().setPaneContent('t1', 'p1', rebuilt)
  await vi.advanceTimersByTimeAsync(2_000)
  expect(pane().sessionCode).toBe('hakmez')
  expect(executor.status().sections['tabs.wa']).toBe('locked:conflict')

  // ~13:31 — the user answers 「保留這台裝置的」
  api.putSection.mockResolvedValue({ kind: 'applied', rev: 3 })
  executor.resolve('tabs.wa', 'local')
  await vi.advanceTimersByTimeAsync(5_000)

  // "keep this device's" = what this device holds NOW: the live, rebuilt pane.
  expect(pane()).not.toHaveProperty('terminated')
  expect(pane().sessionCode).toBe('hakmez')
})
