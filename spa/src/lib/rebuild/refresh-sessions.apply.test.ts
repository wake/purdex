// spa/src/lib/rebuild/refresh-sessions.apply.test.ts — #1310: a profile apply
// puts panes that arrived from the SOT on screen, and its operation-lock release
// reconciles them against a list fetched AFTER the write (#1309 + #1310 spec
// §3.1). The real `applySectionToStores` and the hook's real lock observer;
// only the fetch is faked.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type ParkedWorld } from '../../stores/useLocalProfilesStore'
import type { FreshSessions, Session } from '../host-api'
import type { PaneLayout, Tab, TmuxSessionContent, Workspace } from '../../types/tab'
import { __resetMasterWorldForTest } from '../profile/master-world'
import { buildTabsSection } from '../profile/sections'
import { openAttachGate } from './attach-gate'
import { __resetForTests, note } from './session-version'
import { noteReconciledSessions } from './revive'

vi.mock('./cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('./provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

const { listSessionsFresh } = vi.hoisted(() => ({ listSessionsFresh: vi.fn<(hostId: string) => Promise<FreshSessions>>() }))
vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessionsFresh: (hostId: string) => listSessionsFresh(hostId),
}))

const { applySectionToStores } = await import('../profile/apply-to-stores')
const { createOperationLockObserver } = await import('../../hooks/useMultiHostEventWs')
const { __resetRefreshForTests } = await import('./refresh-sessions')

const M = 'host-master'
const EPOCH = '9f3c1a0b7d2e4c61'
const ctx = { masterHostId: M }

function leaf(paneId: string, over: Partial<TmuxSessionContent> = {}): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content: { kind: 'tmux-session', hostId: M, sessionCode: `c-${paneId}`, mode: 'terminal', cachedName: paneId, tmuxInstance: 'inst', ...over } } }
}
const tab = (id: string, layout: PaneLayout = leaf(`p-${id}`)): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })
const ws = (id: string, tabs: string[]): Workspace => ({ id, name: id.toUpperCase(), tabs, activeTabId: tabs[0] ?? null })
/** The live session a pane made by `leaf(paneId)` is bound to. */
const liveOf = (paneId: string): Session => ({ code: `c-${paneId}`, name: paneId, cwd: '', mode: 'terminal', tmux_instance: 'inst' })

function paneOf(tabs: Record<string, Tab>, tabId: string): TmuxSessionContent {
  const layout = tabs[tabId]?.layout
  if (!layout || layout.type !== 'leaf' || layout.pane.content.kind !== 'tmux-session') throw new Error(`fixture: ${tabId}`)
  return layout.pane.content
}
const onScreen = (tabId: string) => paneOf(useTabStore.getState().tabs, tabId)

/** The master world on screen: workspace `wa` holding `a1` (live on `c-p-a1`). */
function masterOnScreen(): void {
  const a1 = tab('a1')
  useTabStore.setState({ tabs: { a1 }, tabOrder: ['a1'], activeTabId: 'a1', visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1'])], activeWorkspaceId: 'wa', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
}

/** `wa` as the SOT has it now: `a1` and an arriving `a2`. */
function incoming(a2: Tab): unknown {
  return JSON.parse(JSON.stringify(buildTabsSection(ws('wa', ['a1', 'a2']), { a1: tab('a1'), a2 })))
}

const versioned = (seq: number, sessions: Session[]): FreshSessions => ({ kind: 'versioned', epoch: EPOCH, seq, sessions })

let unsubscribe: () => void = () => {}

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetForTests()
  __resetRefreshForTests()
  listSessionsFresh.mockReset()
  useHostStore.setState({ hosts: { [M]: { id: M, name: M, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0 } }, hostOrder: [M], activeHostId: M, runtime: {} })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useSessionStore.setState({ sessions: {} })
  masterOnScreen()
  openAttachGate(M) // the live connection reconciled a versioned list
  note(M, { epoch: EPOCH, seq: 1 })
  noteReconciledSessions(M, [liveOf('p-a1')]) // the list reconciled before the apply
  unsubscribe = useRebuildStore.subscribe(createOperationLockObserver())
})

afterEach(() => {
  unsubscribe()
  __resetRefreshForTests()
  useHostStore.getState().reset()
  __resetMasterWorldForTest()
  localStorage.clear()
})

describe('a profile apply is reconciled by its lock release (#1310)', () => {
  it('an arriving pane on a session the fresh list lacks is marked session-closed after the release', async () => {
    listSessionsFresh.mockResolvedValue(versioned(2, [liveOf('p-a1')]))
    const outcome = await applySectionToStores('tabs.wa', incoming(tab('a2')), ctx)
    expect(outcome).toMatchObject({ ok: true })
    expect(useRebuildStore.getState().lockedBy).toBeNull()
    await vi.waitFor(() => expect(onScreen('a2').terminated).toBe('session-closed'))
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(onScreen('a1').terminated).toBeUndefined()
  })

  it('a fresh list that has the session changes nothing', async () => {
    listSessionsFresh.mockResolvedValue(versioned(2, [liveOf('p-a1'), liveOf('p-a2')]))
    await applySectionToStores('tabs.wa', incoming(tab('a2')), ctx)
    await vi.waitFor(() => expect(useSessionStore.getState().sessions[M]).toHaveLength(2))
    expect(onScreen('a2').terminated).toBeUndefined()
    expect(onScreen('a2').sessionCode).toBe('c-p-a2')
  })

  it('an arriving tmux-restarted pane is revived from the FETCHED list, not the pre-apply one', async () => {
    const LATE: Session = { code: 'late01', name: 'late', cwd: '', mode: 'terminal', tmux_instance: '222:2000' }
    const restarted = tab('a2', leaf('p-a2', { sessionCode: 'dead01', cachedName: 'late', tmuxInstance: '111:1000', terminated: 'tmux-restarted' }))
    let answer!: (v: FreshSessions) => void
    listSessionsFresh.mockReturnValue(new Promise<FreshSessions>((resolve) => { answer = resolve }))
    await applySectionToStores('tabs.wa', incoming(restarted), ctx)
    expect(onScreen('a2')).toMatchObject({ sessionCode: 'dead01', terminated: 'tmux-restarted' }) // nothing in the old list revives it
    answer(versioned(2, [liveOf('p-a1'), LATE]))
    await vi.waitFor(() => expect(onScreen('a2')).toMatchObject({ sessionCode: 'late01', tmuxInstance: '222:2000' }))
    expect(onScreen('a2').terminated).toBeUndefined()
  })

  it('a slave on screen: the apply writes the parked master; the refresh runs and changes nothing on screen', async () => {
    const SLAVE = 'slave-1'
    const master = useTabStore.getState()
    const parked: ParkedWorld = { tabs: master.tabs, workspaces: useWorkspaceStore.getState().workspaces, activeWorkspaceId: 'wa', activeTabId: 'a1' }
    const st = tab('st1', leaf('SLAVE-p1'))
    useLocalProfilesStore.setState({ slaves: { [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, world: null } }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: parked, worldEpoch: 1 })
    useTabStore.setState({ tabs: { st1: st }, tabOrder: ['st1'], activeTabId: 'st1', visitHistory: ['st1'], worldId: SLAVE, worldEpoch: 1 })
    useWorkspaceStore.setState({ workspaces: [ws('SLAVE-ws', ['st1'])], activeWorkspaceId: 'SLAVE-ws', worldId: SLAVE, worldEpoch: 1 })
    const screenTabs = useTabStore.getState().tabs

    listSessionsFresh.mockResolvedValue(versioned(2, [liveOf('SLAVE-p1')])) // lacks the arriving `c-p-a2`
    expect(await applySectionToStores('tabs.wa', incoming(tab('a2')), ctx)).toMatchObject({ ok: true })
    await vi.waitFor(() => expect(useSessionStore.getState().sessions[M]).toEqual([liveOf('SLAVE-p1')]))

    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabs).toBe(screenTabs)
    const parkedAfter = useLocalProfilesStore.getState().parkedMaster!
    expect(paneOf(parkedAfter.tabs, 'a2').terminated).toBeUndefined() // a parked world is not reconciled
  })
})
