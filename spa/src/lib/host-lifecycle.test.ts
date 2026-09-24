// spa/src/lib/host-lifecycle.test.ts — Tests for host delete cascade and session-closed detection
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import * as nexApi from '../lib/nex/nex-api'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useNexHostStore, type NexHostEntry } from '../stores/useNexHostStore'
import { useExecutionListStore, type HostListCache } from '../stores/useExecutionListStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useWorkspaceSettingsStore } from '../stores/useWorkspaceSettingsStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useUndoToast } from '../stores/useUndoToast'
import { createTab } from '../types/tab'
import { getPrimaryPane, scanPaneTree } from './pane-tree'
import { HOST_DELETE_LOCK_OWNER, deleteHostCascade, deleteHostWithUndoToast, startPeerCacheInvalidation } from './host-lifecycle'
import { emptyPeerHostEntry, usePeerStore } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'
import { useLocalProfilesStore, type ParkedWorld } from '../stores/useLocalProfilesStore'
import { STORAGE_KEYS } from './storage/keys'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore, withOperationLock } from '../stores/useRebuildStore'
import { syncIdOfSync } from './profile/host-identity'
import { __resetMasterWorldForTest } from './profile/master-world'
import { __resetHostReresolveForTest } from './host-reresolve'
import type { Tab } from '../types/tab'
import type { Session } from './host-api'

vi.mock('../lib/nex/nex-api', () => ({ releaseLease: vi.fn(), pinnedLeaseRelease: vi.fn() }))

function makeSession(code: string, name: string = code): Session {
  return { code, name, mode: 'terminal', cwd: '~' }
}

const HOST_A = 'host-a'
const HOST_B = 'host-b'
/** HOST_A has a verified daemon: its wire id is `WIRE_A`. HOST_B has none: its wire id is its local id. */
const DAEMON_A = 'lab-a:aaaaaa'
const WIRE_A = syncIdOfSync(DAEMON_A)

function makeSessionTab(hostId: string, code: string, mode: 'terminal' = 'terminal'): Tab {
  return createTab({ kind: 'tmux-session', hostId, sessionCode: code, mode, cachedName: '', tmuxInstance: '' })
}

function resetAllStores() {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  vi.mocked(nexApi.releaseLease).mockReset().mockResolvedValue(undefined)
  vi.mocked(nexApi.pinnedLeaseRelease).mockReset().mockReturnValue(vi.fn(async () => {}))
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  useHostStore.setState({
    hosts: {
      [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0, daemonId: DAEMON_A },
      [HOST_B]: { id: HOST_B, name: 'Host B', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: [HOST_A, HOST_B],
    activeHostId: HOST_A,
    runtime: {},
  })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, agentTypes: {}, models: {} })
  useExecutionStore.setState({ executions: {} })
  useNexHostStore.setState({ byHost: {} })
  useExecutionListStore.setState({ byHost: {} })
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useHostSettingsStore.setState({ hosts: {} })
  useWorkspaceStore.getState().reset()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
  useUndoToast.setState({ toast: null })
}

describe('host delete cascade — this device only (host ownership spec §3.4)', () => {
  beforeEach(resetAllStores)

  const contentOf = (tabId: string) => getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content

  it('the host\'s panes (tmux, daemon file source, execution) are rewritten to its wire id — none marked, no tab closed', () => {
    const tmux = makeSessionTab(HOST_A, 'dev001')
    const editor = createTab({ kind: 'editor', source: { type: 'daemon', hostId: HOST_A }, filePath: '/a' } as never)
    const exec = createTab({ kind: 'execution', executionId: 'exc_1', host: HOST_A })
    for (const t of [tmux, editor, exec]) useTabStore.getState().addTab(t)

    deleteHostCascade(HOST_A)

    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual([tmux.id, editor.id, exec.id].sort())
    expect(contentOf(tmux.id)).toEqual({ kind: 'tmux-session', hostId: WIRE_A, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    expect(contentOf(editor.id)).toMatchObject({ source: { type: 'daemon', hostId: WIRE_A } })
    expect(contentOf(exec.id)).toEqual({ kind: 'execution', executionId: 'exc_1', host: WIRE_A })
    expect(JSON.stringify(useTabStore.getState().tabs)).not.toContain('host-removed')
  })

  it('another host\'s panes are untouched — the same objects', () => {
    const tabB = makeSessionTab(HOST_B, 'stg001')
    useTabStore.getState().addTab(tabB)
    const before = useTabStore.getState().tabs[tabB.id]

    deleteHostCascade(HOST_A)

    expect(useTabStore.getState().tabs[tabB.id]).toBe(before)
  })

  it('a host with no daemonId: its wire id is its local id — nothing is rewritten, nothing marked', () => {
    const tabB = makeSessionTab(HOST_B, 'stg001')
    useTabStore.getState().addTab(tabB)
    useHostSettingsStore.getState().set(HOST_B, 'editor', { homePath: '/b' })
    const tabs = useTabStore.getState().tabs
    const settings = useHostSettingsStore.getState().hosts

    deleteHostCascade(HOST_B)

    expect(useHostStore.getState().hosts[HOST_B]).toBeUndefined()
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(useHostSettingsStore.getState().hosts).toBe(settings)
  })

  it('a duplicate of another host (identity conflict): its refs get the sync id of its daemon, not its local id (plan §0.7)', () => {
    const DUP = 'host-d'
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [DUP]: { id: DUP, name: 'dup', ip: '9.9.9.9', port: 7860, order: 2, daemonId: DAEMON_A } }, hostOrder: [...s.hostOrder, DUP] }))
    const t = makeSessionTab(DUP, 'dup001')
    useTabStore.getState().addTab(t)

    deleteHostCascade(DUP)

    expect(contentOf(t.id)).toMatchObject({ hostId: WIRE_A })
  })

  it('a legacy hostless execution pane is left alone — deleting the first host pins nothing', () => {
    const legacy = createTab({ kind: 'execution', executionId: 'exc_1' })
    useTabStore.getState().addTab(legacy)
    const before = useTabStore.getState().tabs[legacy.id]

    deleteHostCascade(HOST_A) // HOST_A is hostOrder[0]

    expect(useTabStore.getState().tabs[legacy.id]).toBe(before)
    expect(contentOf(legacy.id)).toEqual({ kind: 'execution', executionId: 'exc_1' })
  })

  it('host settings and New Tab columns are kept — re-keyed to the wire id', () => {
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/a' })
    useHostSettingsStore.getState().set(HOST_B, 'editor', { homePath: '/tmp/b' })
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: true, columns: [[`sessions:${HOST_A}`], [`headless:${HOST_A}`], [`sessions:${HOST_B}`]] },
        '2col': { enabled: false, columns: [[], []] },
        '1col': { enabled: true, columns: [[`sessions:${HOST_A}`]] },
      },
      knownIds: [`sessions:${HOST_A}`, `headless:${HOST_A}`, `sessions:${HOST_B}`],
    })

    deleteHostCascade(HOST_A)

    expect(useHostSettingsStore.getState().hosts).toEqual({ [WIRE_A]: { editor: { homePath: '/tmp/a' } }, [HOST_B]: { editor: { homePath: '/tmp/b' } } })
    const persisted = JSON.parse(localStorage.getItem(STORAGE_KEYS.HOST_SETTINGS)!)
    expect(persisted.state.hosts[WIRE_A]).toEqual({ editor: { homePath: '/tmp/a' } })
    const { presets, knownIds } = useNewTabLayoutStore.getState()
    expect(presets['3col'].columns).toEqual([[`sessions:${WIRE_A}`], [`headless:${WIRE_A}`], [`sessions:${HOST_B}`]])
    expect(presets['1col'].columns).toEqual([[`sessions:${WIRE_A}`]])
    expect(knownIds).toEqual([`sessions:${WIRE_A}`, `headless:${WIRE_A}`, `sessions:${HOST_B}`])
  })

  it('the refs are rewritten BEFORE the host row goes (the build maps them to the same wire id throughout)', () => {
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    const seen: string[] = []
    const unsub = useHostStore.subscribe((s) => {
      if (!s.hosts[HOST_A]) seen.push((contentOf(t.id) as { hostId: string }).hostId)
    })
    deleteHostCascade(HOST_A)
    unsub()
    expect(seen).toEqual([WIRE_A])
  })

  it('cascade cleans AgentStore entries', () => {
    const event: NormalizedEvent = { agent_type: 'cc', status: 'idle', raw_event_name: 'PdxStop', broadcast_ts: Date.now() }
    useAgentStore.getState().handleNormalizedEvent(HOST_A, 'dev001', event)
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBe('idle')

    deleteHostCascade(HOST_A)

    expect(useAgentStore.getState().lastEvents[`${HOST_A}:dev001`]).toBeUndefined()
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBeUndefined()
  })

  it('clears useExecutionStore and useNexHostStore entries for the removed host only', () => {
    useExecutionStore.getState().applyEvents(HOST_A, 'exc_1', [
      { seq: 1, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 },
    ])
    useExecutionStore.getState().applyEvents(HOST_B, 'exc_1', [
      { seq: 1, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 },
    ])
    const nexEntry: NexHostEntry = { info: null, capabilities: null, phase: 'unavailable', error: 'x', fetchedAt: 1, generation: 1, fingerprint: '' }
    useNexHostStore.setState({ byHost: { [HOST_A]: nexEntry, [HOST_B]: nexEntry } })

    deleteHostCascade(HOST_A)

    expect(Object.keys(useExecutionStore.getState().executions)).toEqual([`${HOST_B}:exc_1`])
    expect(Object.keys(useNexHostStore.getState().byHost)).toEqual([HOST_B])
  })

  it('deleteHostCascade clears the execution-list host while preserving the other host', () => {
    const listCache: HostListCache = { items: [], phase: 'ready', error: null, lastSeq: 4, refreshRevision: 2 }
    useExecutionListStore.setState({ byHost: { [HOST_A]: listCache, [HOST_B]: listCache } })

    deleteHostCascade(HOST_A)

    expect(Object.keys(useExecutionListStore.getState().byHost)).toEqual([HOST_B])
    expect(useExecutionListStore.getState().byHost[HOST_B]).toBe(listCache)
  })

  // Plan §0.8 + PR #1413 attacker (high #2): the release is a side effect at the daemon that no store rollback can take
  // back, so it is sent only once the deletion has COMMITTED — to the endpoint and auth pinned while the host was
  // still configured (the row is gone by then).
  it('a held lease on the host is released once, after the deletion committed, to the endpoint pinned before removal', () => {
    useExecutionStore.getState().setLease(HOST_A, 'exc_1', { leaseId: 'ls_1', expiresAt: Date.now() + 30_000 })
    useExecutionStore.getState().setLease(HOST_A, 'exc_2', null) // no lease held — nothing to release
    useExecutionStore.getState().setLease(HOST_B, 'exc_3', { leaseId: 'ls_3', expiresAt: Date.now() + 30_000 })
    let pinnedWhilePresent: boolean | null = null
    let sentAfterRemoval: boolean | null = null
    const release = vi.fn(async () => {
      sentAfterRemoval = useHostStore.getState().hosts[HOST_A] === undefined
    })
    vi.mocked(nexApi.pinnedLeaseRelease).mockImplementation((id: string) => {
      pinnedWhilePresent = useHostStore.getState().hosts[id] !== undefined
      return release
    })

    deleteHostCascade(HOST_A)

    expect(nexApi.pinnedLeaseRelease).toHaveBeenCalledWith(HOST_A)
    expect(pinnedWhilePresent).toBe(true)
    expect(release).toHaveBeenCalledTimes(1)
    expect(release).toHaveBeenCalledWith('exc_1', 'ls_1')
    expect(sentAfterRemoval).toBe(true)
    expect(useExecutionStore.getState().executions[`${HOST_A}:exc_1`]).toBeUndefined()
  })

  it('with an after-commit list (the hosts apply) the release is handed over, not sent', () => {
    useExecutionStore.getState().setLease(HOST_A, 'exc_1', { leaseId: 'ls_1', expiresAt: Date.now() + 30_000 })
    const release = vi.fn(async () => {})
    vi.mocked(nexApi.pinnedLeaseRelease).mockReturnValue(release)
    const afterCommit: Array<() => void> = []

    deleteHostCascade(HOST_A, null, afterCommit)

    expect(release).not.toHaveBeenCalled()
    expect(afterCommit).toHaveLength(1)
    afterCommit[0]()
    expect(release).toHaveBeenCalledExactlyOnceWith('exc_1', 'ls_1')
  })

  it('no lease held: nothing is pinned, nothing handed over', () => {
    const afterCommit: Array<() => void> = []
    deleteHostCascade(HOST_A, null, afterCommit)
    expect(nexApi.pinnedLeaseRelease).not.toHaveBeenCalled()
    expect(afterCommit).toEqual([])
  })

  it('a release that rejects (or throws) does not stop the deletion', () => {
    useExecutionStore.getState().setLease(HOST_A, 'exc_1', { leaseId: 'ls_1', expiresAt: Date.now() + 30_000 })
    useExecutionStore.getState().setLease(HOST_A, 'exc_2', { leaseId: 'ls_2', expiresAt: Date.now() + 30_000 })
    const release = vi.fn().mockImplementationOnce(() => { throw new Error('sync throw') }).mockRejectedValueOnce(new Error('offline'))
    vi.mocked(nexApi.pinnedLeaseRelease).mockReturnValue(release)

    expect(() => deleteHostCascade(HOST_A)).not.toThrow()
    expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined()
    expect(release).toHaveBeenCalledTimes(2)
  })

  it('cascade cleans SessionStore entries', () => {
    useSessionStore.getState().replaceHost(HOST_A, [makeSession('dev001', 'Dev')])
    deleteHostCascade(HOST_A)
    expect(useSessionStore.getState().sessions[HOST_A]).toBeUndefined()
  })

  it('does not record to the history store', () => {
    useTabStore.getState().addTab(makeSessionTab(HOST_A, 'dev001'))
    deleteHostCascade(HOST_A)
    expect(useHistoryStore.getState().closedTabs).toHaveLength(0)
  })

  it('keeps workspace membership: nothing closes', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Dev WS')
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)

    deleteHostCascade(HOST_A)

    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab.id)?.id).toBe(ws.id)
  })

  it('aborts with a no-op undo when the removal would be vetoed (last host) — nothing rewritten', () => {
    useHostStore.setState({ hosts: { [HOST_A]: useHostStore.getState().hosts[HOST_A] }, hostOrder: [HOST_A], activeHostId: HOST_A, runtime: {} })
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/a' })
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)
    const tabs = useTabStore.getState().tabs

    const restore = deleteHostCascade(HOST_A)

    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toEqual({ homePath: '/tmp/a' })
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(() => restore()).not.toThrow()
    expect(useTabStore.getState().tabs).toBe(tabs)
  })

  it('an unknown host: a no-op', () => {
    const tabs = useTabStore.getState().tabs
    const restore = deleteHostCascade('nope')
    expect(useHostStore.getState().hostOrder).toEqual([HOST_A, HOST_B])
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(() => restore()).not.toThrow()
  })

  it('a rewrite that cannot be written stops the deletion before anything is cleared: it throws, the host stays', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)
    useSessionStore.getState().replaceHost(HOST_A, [makeSession('dev001')])
    const real = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.TABS) throw new DOMException('quota', 'QuotaExceededError')
      real.call(this, k, v)
    })
    try {
      expect(() => deleteHostCascade(HOST_A)).toThrow()
    } finally {
      vi.restoreAllMocks()
    }
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useSessionStore.getState().sessions[HOST_A]).toBeDefined()
    expect(contentOf(tab.id)).toMatchObject({ hostId: HOST_A })
  })

  // --- undo: the device-local state the cascade cleared ---

  it('undo skips every restore when the same host id was recreated during the undo window', () => {
    useSessionStore.getState().replaceHost(HOST_A, [makeSession('dev001', 'Dev')])
    const restore = deleteHostCascade(HOST_A)
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, [HOST_A]: { id: HOST_A, name: 'Host A (recreated)', ip: '9.9.9.9', port: 7860, order: 2 } },
      hostOrder: [...s.hostOrder, HOST_A],
    }))
    const freshSessions: Session[] = [makeSession('dev999', 'Fresh')]
    useSessionStore.getState().replaceHost(HOST_A, freshSessions)

    restore()

    expect(useHostStore.getState().hosts[HOST_A]?.name).toBe('Host A (recreated)')
    expect(useHostStore.getState().hosts[HOST_A]?.ip).toBe('9.9.9.9')
    expect(useSessionStore.getState().sessions[HOST_A]).toEqual(freshSessions)
  })

  it('undo restores the host at its original position', () => {
    const restore = deleteHostCascade(HOST_A)
    expect(useHostStore.getState().hostOrder).toEqual([HOST_B])
    restore()
    expect(useHostStore.getState().hostOrder).toEqual([HOST_A, HOST_B])
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
  })

  it('undo restores sessions', () => {
    const sessions: Session[] = [makeSession('dev001', 'Dev')]
    useSessionStore.getState().replaceHost(HOST_A, sessions)
    const restore = deleteHostCascade(HOST_A)
    expect(useSessionStore.getState().sessions[HOST_A]).toBeUndefined()
    restore()
    expect(useSessionStore.getState().sessions[HOST_A]).toEqual(sessions)
  })

  it('undo restores AgentStore data and models', () => {
    const event: NormalizedEvent = { agent_type: 'cc', status: 'running', model: 'claude-sonnet-4-20250514', raw_event_name: 'PdxUserPromptSubmit', broadcast_ts: Date.now() }
    useAgentStore.getState().handleNormalizedEvent(HOST_A, 'dev001', event)
    const restore = deleteHostCascade(HOST_A)
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBeUndefined()
    restore()
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBe('running')
    expect(useAgentStore.getState().lastEvents[`${HOST_A}:dev001`]).toBeDefined()
    expect(useAgentStore.getState().models[`${HOST_A}:dev001`]).toBe('claude-sonnet-4-20250514')
  })

  it('does not affect other hosts during cascade', () => {
    const eventB: NormalizedEvent = { agent_type: 'cc', status: 'running', raw_event_name: 'PdxUserPromptSubmit', broadcast_ts: Date.now() }
    useAgentStore.getState().handleNormalizedEvent(HOST_B, 'stg001', eventB)
    deleteHostCascade(HOST_A)
    expect(useAgentStore.getState().statuses[`${HOST_B}:stg001`]).toBe('running')
  })
})

// Plan H1c T3 (§0.6): the undo puts the host row back VERBATIM (#1396 — daemonId and aliases included) and runs the
// re-resolve pass's body for that host at once, synchronously: every reference that names it by its wire id — the ones
// the deletion wrote and any that arrived meanwhile (`d1_X` means X on every device) — points at it again.
describe('host delete undo — the references come back (host ownership spec §3.4)', () => {
  beforeEach(resetAllStores)
  afterEach(() => { vi.useRealTimers() })

  const contentOf = (tabId: string) => getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content

  it('#1396: the row comes back verbatim — daemonId and syncAliases included — at its place, active again', () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST_A]: { ...s.hosts[HOST_A], syncAliases: ['old-id'], token: 'tok' } } }))
    const before = useHostStore.getState().hosts[HOST_A]
    const undo = deleteHostCascade(HOST_A)
    expect(useHostStore.getState().activeHostId).toBe(HOST_B)

    undo()

    expect(useHostStore.getState().hosts[HOST_A]).toEqual(before)
    expect(useHostStore.getState().hostOrder).toEqual([HOST_A, HOST_B])
    expect(useHostStore.getState().activeHostId).toBe(HOST_A)
  })

  it('a host added meanwhile keeps its place; the restored one goes back after the hosts it followed', () => {
    const undo = deleteHostCascade(HOST_B)
    useHostStore.getState().addHost({ id: 'host-c', name: 'C', ip: '7.7.7.7', port: 7860 })
    undo()
    expect(useHostStore.getState().hostOrder).toEqual([HOST_A, HOST_B, 'host-c'])
    expect(useHostStore.getState().hostOrder.map((id) => useHostStore.getState().hosts[id].order)).toEqual([0, 1, 2])
  })

  it('every reference on the wire id is back on the local id — panes, host settings, New Tab columns', () => {
    const tmux = makeSessionTab(HOST_A, 'dev001')
    const exec = createTab({ kind: 'execution', executionId: 'exc_1', host: HOST_A })
    useTabStore.getState().addTab(tmux)
    useTabStore.getState().addTab(exec)
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/a' })
    useNewTabLayoutStore.setState({
      presets: { '3col': { enabled: true, columns: [[`sessions:${HOST_A}`], [`headless:${HOST_A}`], []] }, '2col': { enabled: false, columns: [[], []] }, '1col': { enabled: true, columns: [[`sessions:${HOST_A}`]] } },
      knownIds: [`sessions:${HOST_A}`, `headless:${HOST_A}`],
    })
    const undo = deleteHostCascade(HOST_A)
    expect(contentOf(tmux.id)).toMatchObject({ hostId: WIRE_A })

    undo()

    expect(contentOf(tmux.id)).toMatchObject({ hostId: HOST_A })
    expect(contentOf(exec.id)).toMatchObject({ host: HOST_A })
    expect(useHostSettingsStore.getState().hosts).toEqual({ [HOST_A]: { editor: { homePath: '/tmp/a' } } })
    expect(useNewTabLayoutStore.getState().presets['3col'].columns).toEqual([[`sessions:${HOST_A}`], [`headless:${HOST_A}`], []])
    expect(useNewTabLayoutStore.getState().knownIds).toEqual([`sessions:${HOST_A}`, `headless:${HOST_A}`])
  })

  it('a reference on the wire id that ARRIVED inside the undo window resolves too — d1_X is X everywhere', () => {
    const undo = deleteHostCascade(HOST_A)
    const arrived = makeSessionTab(WIRE_A, 'pulled')
    useTabStore.getState().addTab(arrived)
    undo()
    expect(contentOf(arrived.id)).toMatchObject({ hostId: HOST_A })
  })

  it('a reference on another wire id, and an unresolvable legacy id, are left alone', () => {
    const WIRE_Y = syncIdOfSync('lab-y:yyyyyy')
    const other = makeSessionTab(WIRE_Y, 'y')
    const legacy = makeSessionTab('gone01', 'l')
    useTabStore.getState().addTab(other)
    useTabStore.getState().addTab(legacy)
    const undo = deleteHostCascade(HOST_A)
    undo()
    expect(contentOf(other.id)).toMatchObject({ hostId: WIRE_Y })
    expect(contentOf(legacy.id)).toMatchObject({ hostId: 'gone01' })
  })

  it('a host recreated with the same id in the window is not overwritten, and nothing is rewritten for it by the undo', () => {
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    const undo = deleteHostCascade(HOST_A)
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST_A]: { id: HOST_A, name: 'Recreated', ip: '9.9.9.9', port: 7860, order: 2 } }, hostOrder: [...s.hostOrder, HOST_A] }))
    undo()
    expect(useHostStore.getState().hosts[HOST_A]).toEqual({ id: HOST_A, name: 'Recreated', ip: '9.9.9.9', port: 7860, order: 2 })
    expect(contentOf(t.id)).toMatchObject({ hostId: WIRE_A })
  })

  it('a duplicate re-added by the undo (identity conflict again) rewrites nothing', () => {
    const DUP = 'host-d'
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [DUP]: { id: DUP, name: 'dup', ip: '9.9.9.9', port: 7860, order: 2, daemonId: DAEMON_A } }, hostOrder: [...s.hostOrder, DUP] }))
    const t = makeSessionTab(DUP, 'dup001')
    useTabStore.getState().addTab(t)
    const undo = deleteHostCascade(DUP)
    expect(contentOf(t.id)).toMatchObject({ hostId: WIRE_A })
    const tabs = useTabStore.getState().tabs

    undo()

    expect(useHostStore.getState().hosts[DUP]).toBeDefined()
    expect(useTabStore.getState().tabs).toBe(tabs)
  })

  it('under a held grant (the hosts apply) the undo is synchronous: resolved before it returns, the lock still the holder\'s', () => {
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    const grant = useRebuildStore.getState().acquireOperationLock('profile-sync')
    const undo = deleteHostCascade(HOST_A, grant)

    undo()

    expect(contentOf(t.id)).toMatchObject({ hostId: HOST_A })
    expect(useRebuildStore.getState().lockGrant).toBe(grant)
    useRebuildStore.getState().releaseOperationLock(grant)
  })

  it('no grant, the lock free: resolved before the undo returns, and the lock released', () => {
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    const undo = deleteHostCascade(HOST_A)
    undo()
    expect(contentOf(t.id)).toMatchObject({ hostId: HOST_A })
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('no grant, the lock held elsewhere (UI undo during a rebuild): the host is back at once, a scheduled pass finishes the refs', async () => {
    vi.useFakeTimers()
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    const undo = deleteHostCascade(HOST_A)
    const other = useRebuildStore.getState().acquireOperationLock('rebuild:batch')

    undo()

    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(contentOf(t.id)).toMatchObject({ hostId: WIRE_A })
    await vi.advanceTimersByTimeAsync(0)
    expect(contentOf(t.id)).toMatchObject({ hostId: WIRE_A }) // still held
    useRebuildStore.getState().releaseOperationLock(other)
    await vi.advanceTimersByTimeAsync(500)
    expect(contentOf(t.id)).toMatchObject({ hostId: HOST_A })
  })

  it('a no-daemonId host: nothing was rewritten, nothing is', () => {
    const t = makeSessionTab(HOST_B, 'b')
    useTabStore.getState().addTab(t)
    const tabs = useTabStore.getState().tabs
    const undo = deleteHostCascade(HOST_B)
    undo()
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(useHostStore.getState().hosts[HOST_B]).toBeDefined()
  })
})

// PR #1413 attacker (high #1): the cascade is ONE unit. A write that fails anywhere in it — the host store's own persist
// (`removeHost` → `purdex-hosts` quota), after the rewrite and every clear already ran — puts every store it touched
// back exactly as it was before the call, and throws: no reference left on the wire id, no device-local cache lost.
describe('host delete cascade — a write fails half-way', () => {
  beforeEach(resetAllStores)
  afterEach(() => { vi.restoreAllMocks() })

  /** Every store the cascade touches holds HOST_A data (and HOST_B's). */
  function seedEverything(): void {
    useTabStore.getState().addTab(makeSessionTab(HOST_A, 'dev001'))
    useTabStore.getState().addTab(makeSessionTab(HOST_B, 'stg001'))
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/a' })
    useNewTabLayoutStore.setState({
      presets: { '3col': { enabled: true, columns: [[`sessions:${HOST_A}`], [`sessions:${HOST_B}`], []] }, '2col': { enabled: false, columns: [[], []] }, '1col': { enabled: true, columns: [[`headless:${HOST_A}`]] } },
      knownIds: [`sessions:${HOST_A}`, `sessions:${HOST_B}`, `headless:${HOST_A}`],
    })
    useSessionStore.getState().replaceHost(HOST_A, [makeSession('dev001')])
    useAgentStore.getState().handleNormalizedEvent(HOST_A, 'dev001', { agent_type: 'cc', status: 'running', model: 'm', raw_event_name: 'PdxUserPromptSubmit', broadcast_ts: 1 })
    useExecutionStore.getState().applyEvents(HOST_A, 'exc_1', [{ seq: 1, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 }])
    useExecutionStore.getState().setLease(HOST_A, 'exc_1', { leaseId: 'ls_1', expiresAt: 9_999_999_999_999 })
    const listCache: HostListCache = { items: [], phase: 'ready', error: null, lastSeq: 4, refreshRevision: 2 }
    useExecutionListStore.setState({ byHost: { [HOST_A]: listCache, [HOST_B]: listCache } })
    const nexEntry: NexHostEntry = { info: null, capabilities: null, phase: 'unavailable', error: 'x', fetchedAt: 1, generation: 1, fingerprint: '' }
    useNexHostStore.setState({ byHost: { [HOST_A]: nexEntry, [HOST_B]: nexEntry } })
    seedPeers(HOST_A, HOST_B)
    useHostStore.setState({ runtime: { [HOST_A]: { status: 'connected' } } })
  }

  const PERSISTED = [STORAGE_KEYS.TABS, STORAGE_KEYS.HOSTS, STORAGE_KEYS.HOST_SETTINGS, STORAGE_KEYS.NEW_TAB_LAYOUT, STORAGE_KEYS.LOCAL_PROFILES]

  function everything(): string {
    const h = useHostStore.getState()
    const a = useAgentStore.getState()
    return JSON.stringify({
      tabs: useTabStore.getState().tabs,
      parked: [useLocalProfilesStore.getState().parkedMaster, useLocalProfilesStore.getState().slaves],
      hostSettings: useHostSettingsStore.getState().hosts,
      newtab: [useNewTabLayoutStore.getState().presets, useNewTabLayoutStore.getState().knownIds],
      sessions: useSessionStore.getState().sessions,
      agent: [a.lastEvents, a.statuses, a.unread, a.models, a.agentTypes, a.subagents],
      executions: useExecutionStore.getState().executions,
      lists: useExecutionListStore.getState().byHost,
      nex: useNexHostStore.getState().byHost,
      peers: usePeerStore.getState().byHost,
      cwd: useSessionCwdStore.getState().byHost,
      host: [h.hosts, h.hostOrder, h.activeHostId, h.devHostId, h.runtime],
      storage: PERSISTED.map((k) => localStorage.getItem(k)),
    })
  }

  /** `purdex-hosts`' next `times` writes throw — the first is `removeHost`'s, the cascade's last step. */
  function failHostsWrites(times: number): void {
    const real = Storage.prototype.setItem
    let left = times
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.HOSTS && left > 0) {
        left--
        throw new DOMException('quota', 'QuotaExceededError')
      }
      real.call(this, k, v)
    })
  }

  it('the host store\'s persist fails at removeHost: it throws, and EVERY store is back as it was — memory and storage', () => {
    seedEverything()
    const before = everything()
    expect(before).toContain(`"hostId":"${HOST_A}"`)
    failHostsWrites(1)

    expect(() => deleteHostCascade(HOST_A)).toThrow('quota')

    vi.restoreAllMocks()
    expect(everything()).toBe(before)
    expect(JSON.stringify(useTabStore.getState().tabs)).not.toContain(WIRE_A)
  })

  it('no lease is released for a deletion that did not happen — nothing pinned is ever sent', () => {
    seedEverything()
    const release = vi.fn(async () => {})
    vi.mocked(nexApi.pinnedLeaseRelease).mockReturnValue(release)
    failHostsWrites(1)
    expect(() => deleteHostCascade(HOST_A)).toThrow()
    expect(release).not.toHaveBeenCalled()
    expect(nexApi.releaseLease).not.toHaveBeenCalled()
    expect(useExecutionStore.getState().executions[`${HOST_A}:exc_1`].lease).toEqual({ leaseId: 'ls_1', expiresAt: 9_999_999_999_999 })
  })

  it('the rollback fails too: the error says so, and keeps the original', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    seedEverything()
    // `removeHost` fails; putting the tab store back (its second write — the first was the rewrite) fails too
    const real = Storage.prototype.setItem
    let tabWrites = 0
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, k: string, v: string) {
      if (k === STORAGE_KEYS.HOSTS) throw new DOMException('quota', 'QuotaExceededError')
      if (k === STORAGE_KEYS.TABS && ++tabWrites >= 2) throw new DOMException('tabs quota', 'QuotaExceededError')
      real.call(this, k, v)
    })
    expect(() => deleteHostCascade(HOST_A)).toThrow(/^quota \(rollback incomplete — tabs quota\)$/)
  })

  it('a clear that throws (a store action, not a persist) is rolled back the same way', () => {
    seedEverything()
    const before = everything()
    vi.spyOn(useSessionCwdStore.getState(), 'forgetHost').mockImplementation(() => { throw new Error('cwd blew up') })
    expect(() => deleteHostCascade(HOST_A)).toThrow('cwd blew up')
    vi.restoreAllMocks()
    expect(everything()).toBe(before)
  })
})

// PR #1413 attacker (medium #3): the Hosts page's deletion rewrites the tab tree, and whatever rewrites the tab tree
// holds the operation lock (useRebuildStore) — a rebuild in flight must not see its pane moved under it. Held
// elsewhere, the deletion is retried every 250 ms for up to 4 s, then given up and said.
describe('deleteHostWithUndoToast — the operation lock', () => {
  const MESSAGES = { deleted: 'A deleted', busy: 'busy, try again', stale: 'A changed or gone, not deleted' }
  const contentOf = (tabId: string) => getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content

  beforeEach(resetAllStores)
  afterEach(() => { vi.useRealTimers() })

  it('the lock free: deleted at once, under the lock (owner host-delete), released after; the undo toast is up', async () => {
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    let heldBy: string | null = null
    const unsub = useTabStore.subscribe(() => { heldBy = useRebuildStore.getState().lockedBy })

    const done = deleteHostWithUndoToast(HOST_A, MESSAGES)

    unsub()
    expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined() // synchronously
    expect(heldBy).toBe(HOST_DELETE_LOCK_OWNER)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
    expect(useUndoToast.getState().toast).toMatchObject({ message: MESSAGES.deleted })
    expect(typeof useUndoToast.getState().toast?.action).toBe('function')
    await expect(done).resolves.toBe('deleted')
  })

  it('a rebuild holds the lock and never lets go: nothing is rewritten, nothing deleted — after ~4 s the user is told', async () => {
    vi.useFakeTimers()
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    let finishRebuild!: () => void
    const rebuild = withOperationLock('rebuild:batch', () => new Promise<void>((r) => { finishRebuild = r }), () => undefined)
    const tabs = useTabStore.getState().tabs

    const done = deleteHostWithUndoToast(HOST_A, MESSAGES)
    await vi.advanceTimersByTimeAsync(3_900)
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useUndoToast.getState().toast).toBeNull()
    await vi.advanceTimersByTimeAsync(400)

    await expect(done).resolves.toBe('busy')
    expect(useUndoToast.getState().toast).toEqual({ message: MESSAGES.busy, action: undefined, actionLabel: undefined })
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(contentOf(t.id)).toMatchObject({ hostId: HOST_A })
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useRebuildStore.getState().lockedBy).toBe('rebuild:batch')
    finishRebuild()
    await rebuild
  })

  it('the rebuild finishes inside the window: the next retry deletes', async () => {
    vi.useFakeTimers()
    const t = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(t)
    let finishRebuild!: () => void
    const rebuild = withOperationLock('rebuild:batch', () => new Promise<void>((r) => { finishRebuild = r }), () => undefined)

    const done = deleteHostWithUndoToast(HOST_A, MESSAGES)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    finishRebuild()
    await rebuild
    await vi.advanceTimersByTimeAsync(250)

    await expect(done).resolves.toBe('deleted')
    expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined()
    expect(contentOf(t.id)).toMatchObject({ hostId: WIRE_A })
    expect(useUndoToast.getState().toast).toMatchObject({ message: MESSAGES.deleted })
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  // PR #1413 critic (high): the deletion is of the host the user CONFIRMED. A retry that finds the id gone, gone and
  // back (another window's undo re-adds it verbatim), or pointed elsewhere deletes nothing and says so — never
  // "deleted" with an Undo for something that did not happen, never another entity under the same id.
  describe('the host changed while the deletion waited for the lock', () => {
    async function waitingDeletion(meanwhile: () => void) {
      vi.useFakeTimers()
      const t = makeSessionTab(HOST_A, 'dev001')
      useTabStore.getState().addTab(t)
      let finishRebuild!: () => void
      const rebuild = withOperationLock('rebuild:batch', () => new Promise<void>((r) => { finishRebuild = r }), () => undefined)
      const done = deleteHostWithUndoToast(HOST_A, MESSAGES)
      await vi.advanceTimersByTimeAsync(500)
      meanwhile()
      const tabs = useTabStore.getState().tabs
      finishRebuild()
      await rebuild
      await vi.advanceTimersByTimeAsync(250)
      return { done, tabs }
    }

    it('gone: nothing deleted, no Undo — the stale notice', async () => {
      const { done, tabs } = await waitingDeletion(() => useHostStore.getState().removeHost(HOST_A))
      await expect(done).resolves.toBe('stale')
      expect(useUndoToast.getState().toast).toEqual({ message: MESSAGES.stale, action: undefined, actionLabel: undefined })
      expect(useTabStore.getState().tabs).toBe(tabs)
      expect(useHostStore.getState().hostOrder).toEqual([HOST_B])
      expect(useRebuildStore.getState().lockedBy).toBeNull()
    })

    it('gone and back under the same id, byte for byte (another window\'s undo): not deleted', async () => {
      const row = useHostStore.getState().hosts[HOST_A]
      const { done } = await waitingDeletion(() => {
        useHostStore.getState().removeHost(HOST_A)
        useHostStore.setState((st) => ({ hosts: { ...st.hosts, [HOST_A]: row }, hostOrder: [HOST_A, ...st.hostOrder] }))
      })
      await expect(done).resolves.toBe('stale')
      expect(useHostStore.getState().hosts[HOST_A]).toEqual(row)
      expect(useUndoToast.getState().toast?.message).toBe(MESSAGES.stale)
    })

    it('re-pointed (another endpoint): not deleted', async () => {
      const { done } = await waitingDeletion(() => useHostStore.getState().updateHost(HOST_A, { ip: '9.9.9.9' }))
      await expect(done).resolves.toBe('stale')
      expect(useHostStore.getState().hosts[HOST_A]).toMatchObject({ ip: '9.9.9.9' })
      expect(useUndoToast.getState().toast?.message).toBe(MESSAGES.stale)
    })

    it('another token (the daemonId unchanged): not deleted', async () => {
      const { done } = await waitingDeletion(() => useHostStore.getState().updateHost(HOST_A, { token: 'other-token' }))
      expect(useHostStore.getState().hosts[HOST_A]?.daemonId).toBe(DAEMON_A)
      await expect(done).resolves.toBe('stale')
      expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    })

    it('renamed only (not another entity): still deleted', async () => {
      const { done } = await waitingDeletion(() => useHostStore.getState().updateHost(HOST_A, { name: 'Renamed A' }))
      await expect(done).resolves.toBe('deleted')
      expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined()
    })
  })

  it('the host is already gone at the click: stale at once, nothing taken', async () => {
    await expect(deleteHostWithUndoToast('nope', MESSAGES)).resolves.toBe('stale')
    expect(useUndoToast.getState().toast?.message).toBe(MESSAGES.stale)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('the last host (the cascade refuses it): no success, no Undo — stale', async () => {
    useHostStore.getState().removeHost(HOST_B)
    await expect(deleteHostWithUndoToast(HOST_A, MESSAGES)).resolves.toBe('stale')
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useUndoToast.getState().toast?.action).toBeUndefined()
  })

  it('a deletion that fails releases the lock and rejects, with no toast', async () => {
    vi.spyOn(useSessionCwdStore.getState(), 'forgetHost').mockImplementationOnce(() => { throw new Error('cwd blew up') })
    await expect(deleteHostWithUndoToast(HOST_A, MESSAGES)).rejects.toThrow('cwd blew up')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
    expect(useUndoToast.getState().toast).toBeNull()
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
  })
})

describe('session-closed detection', () => {
  beforeEach(resetAllStores)

  it('marks tabs as terminated when sessions disappear', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    // Simulate session-closed detection (from useMultiHostEventWs)
    const newSessions: Session[] = [] // dev001 no longer exists
    const newCodes = new Set(newSessions.map((s) => s.code))
    const closedCodes = new Set<string>()

    for (const t of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(t.layout, (pane) => {
        const c = pane.content
        if (c.kind === 'tmux-session' && c.hostId === HOST_A && !c.terminated && !newCodes.has(c.sessionCode)) {
          closedCodes.add(c.sessionCode)
        }
      })
    }

    for (const code of closedCodes) {
      useTabStore.getState().markTerminated(HOST_A, code, 'session-closed')
    }

    const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    expect(content.kind).toBe('tmux-session')
    if (content.kind === 'tmux-session') {
      expect(content.terminated).toBe('session-closed')
    }
  })

  it('does not double-mark already-terminated tabs', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().markTerminated(HOST_A, 'dev001', 'session-closed')
    const before = useTabStore.getState().tabs[tab.id]

    // Run detection again
    const newCodes = new Set<string>()
    const closedCodes = new Set<string>()
    for (const t of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(t.layout, (pane) => {
        const c = pane.content
        if (c.kind === 'tmux-session' && c.hostId === HOST_A && !c.terminated && !newCodes.has(c.sessionCode)) {
          closedCodes.add(c.sessionCode)
        }
      })
    }
    // Already terminated — should not be in closedCodes
    expect(closedCodes.size).toBe(0)

    // Verify tab unchanged
    const after = useTabStore.getState().tabs[tab.id]
    expect(after).toBe(before)
  })

  it('only marks sessions not in the new list', () => {
    const tab1 = makeSessionTab(HOST_A, 'dev001')
    const tab2 = makeSessionTab(HOST_A, 'dev002')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)

    // dev001 still exists, dev002 is gone
    const newSessions: Session[] = [makeSession('dev001', 'Dev')]
    const newCodes = new Set(newSessions.map((s) => s.code))
    const closedCodes = new Set<string>()

    for (const t of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(t.layout, (pane) => {
        const c = pane.content
        if (c.kind === 'tmux-session' && c.hostId === HOST_A && !c.terminated && !newCodes.has(c.sessionCode)) {
          closedCodes.add(c.sessionCode)
        }
      })
    }

    expect(closedCodes.has('dev002')).toBe(true)
    expect(closedCodes.has('dev001')).toBe(false)

    for (const code of closedCodes) {
      useTabStore.getState().markTerminated(HOST_A, code, 'session-closed')
    }

    // dev001 should not be terminated
    const c1 = getPrimaryPane(useTabStore.getState().tabs[tab1.id].layout).content
    if (c1.kind === 'tmux-session') {
      expect(c1.terminated).toBeUndefined()
    }

    // dev002 should be terminated
    const c2 = getPrimaryPane(useTabStore.getState().tabs[tab2.id].layout).content
    if (c2.kind === 'tmux-session') {
      expect(c2.terminated).toBe('session-closed')
    }
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// §3.4 — #541 Cross-store rehydrate order invariants harness
//
// These tests validate that `deleteHostCascade` (and its undo callback) behave
// correctly under all rehydrate-timing combinations:
//   A. Both useHostStore + useHostSettingsStore fully rehydrated
//   B. Only useHostSettingsStore rehydrated (hosts store still empty — e.g. it
//      rehydrated first in a race)
//   C. Only useHostStore rehydrated (hostSettings store not yet rehydrated)
//   + interleaved-write gate, hostWasRecreated, last-host veto, and workspace
//     settings isolation.
//
// ─────────────────────────────────────────────────────────────────────────────

describe('#541 cross-store rehydrate order invariants', () => {
  const hA = 'hA'
  const hB = 'hB'

  // Wipe all singleton stores to a blank canvas before each test.
  //
  // IMPORTANT: We use setState WITHOUT replace-mode (no `true` second arg) for
  // all stores. This keeps Zustand action methods intact in the store state
  // object, which host-lifecycle.ts needs because it snapshots store state at
  // call time (e.g. `const sessionStore = useSessionStore.getState()`) and then
  // calls action methods on the snapshot (e.g. `sessionStore.removeHost(...)`).
  // Replace-mode would wipe those methods, causing "not a function" errors.
  //
  // The data fields we set here ({hosts:{}, ...}) are shallowly merged on top
  // of the current state, overwriting the default mlab host that
  // createDefaultState() bakes in — so we get a clean slate without clobbering
  // the action closures.
  beforeEach(() => {
    localStorage.clear()
    // Merge-mode: set ALL mutable data fields to blank without clobbering
    // action methods (Finding D fix: explicit field-by-field reset prevents
    // cross-test leakage of fields not listed).
    useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
    useHostSettingsStore.setState({ hosts: {} })
    useWorkspaceSettingsStore.setState({ workspaces: {} })
    // visitHistory added — codex Finding D flagged it as a cross-test leak vector
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    // oscTitles + ccStatus added — codex Finding D flagged them as cross-test leak vectors
    useAgentStore.setState({
      lastEvents: {},
      statuses: {},
      unread: {},
      subagents: {},
      agentTypes: {},
      models: {},
      oscTitles: {},
      ccStatus: {},
    })
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    useWorkspaceStore.getState().reset()
    useUndoToast.setState({ toast: null })
  })

  // ── Seed helpers ──────────────────────────────────────────────────────────

  /** Both stores fully rehydrated: hA + hB in hostStore, hA has settings. */
  function seedBothRehydrated() {
    // Merge-mode: preserve action methods in store state
    useHostStore.setState({
      hosts: {
        [hA]: { id: hA, name: 'Host A', ip: '127.0.0.1', port: 7860, order: 0 },
        [hB]: { id: hB, name: 'Host B', ip: '127.0.0.1', port: 7861, order: 1 },
      },
      hostOrder: [hA, hB],
      runtime: {},
      activeHostId: hA,
    })
    useHostSettingsStore.setState({ hosts: { [hA]: { editor: { homePath: '/tmp/a' } } } })
  }

  /** Only hostStore rehydrated; hostSettings store is empty (hasn't rehydrated yet). */
  function seedHostOnly() {
    // Merge-mode: preserve action methods in store state
    useHostStore.setState({
      hosts: {
        [hA]: { id: hA, name: 'Host A', ip: '127.0.0.1', port: 7860, order: 0 },
        [hB]: { id: hB, name: 'Host B', ip: '127.0.0.1', port: 7861, order: 1 },
      },
      hostOrder: [hA, hB],
      runtime: {},
      activeHostId: hA,
    })
    // hostSettings intentionally left as beforeEach blank { hosts: {} }
  }

  /** Only hostSettings store rehydrated; hostStore is empty (hasn't rehydrated yet). */
  function seedSettingsOnly() {
    // Merge-mode: preserve action methods in store state
    useHostSettingsStore.setState({ hosts: { [hA]: { editor: { homePath: '/tmp/a' } } } })
    // hostStore intentionally left as beforeEach blank { hosts: {} }
  }

  // ── Case 1: Rehydrate order A — both stores rehydrated ───────────────────

  it('A: both rehydrated — removeHost keeps hostSettings (spec §3.4); undo restores the host, settings as they are', () => {
    seedBothRehydrated()

    const restore = deleteHostCascade(hA)

    // Cascade: hA gone from the host store; its settings are the workbench's and stay (no daemonId: same key)
    expect(useHostStore.getState().hosts[hA]).toBeUndefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
    // hB untouched
    expect(useHostStore.getState().hosts[hB]).toBeDefined()

    restore()
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
  })

  // ── Case 2: Rehydrate order B — hostSettings only, hostStore empty ────────

  it('B: settings-only rehydrate — removeHost is no-op (host not in store); hostSettings unchanged', () => {
    seedSettingsOnly()

    // hA is NOT in hostStore.hosts, so deleteHostCascade's pre-check aborts
    const restore = deleteHostCascade(hA)

    // Settings must be untouched — cascade aborted before touching anything
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })

    // Undo must be a safe no-op
    expect(() => restore()).not.toThrow()
    // Settings still intact after undo no-op
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
  })

  // ── Case 3: Rehydrate order C — hostStore only, hostSettings empty ────────

  it('C: host-only rehydrate — removeHost cascades; undo restores host, hostSettings stays empty', () => {
    seedHostOnly()

    const restore = deleteHostCascade(hA)

    // Cascade ran: hA removed from hostStore, hostSettings was already empty (no-op clear)
    expect(useHostStore.getState().hosts[hA]).toBeUndefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()

    // Undo: hostStore restored, hostSettings still empty (nothing was snapshotted)
    restore()
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    expect(useHostStore.getState().hostOrder).toContain(hA)
    // hostSettings was empty at snapshot time — undo must not invent new data
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()
  })

  // ── Case 4: Interleaved write during cascade — undo must NOT clobber ──────

  it('interleaved write: fresh hostSettings write during undo window is preserved by undo gate', () => {
    seedBothRehydrated()

    const restore = deleteHostCascade(hA)

    // Simulate a BroadcastChannel sync or re-add writing fresh settings for hA
    // (recreate host first so hostWasRecreated guard kicks in)
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [hA]: { id: hA, name: 'Host A (fresh)', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, hA],
    }))
    // Write new settings for the recreated host
    useHostSettingsStore.setState((s) => ({
      hosts: { ...s.hosts, [hA]: { editor: { homePath: '/new' } } },
    }))

    restore()

    // Undo must NOT overwrite the freshly written settings (hostWasRecreated gate)
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/new' } })
    // And the recreated host survives
    expect(useHostStore.getState().hosts[hA]?.name).toBe('Host A (fresh)')
  })

  // ── Case 5: hostWasRecreated gate covers all 5 restore categories ─────────

  it('hostWasRecreated: recreating same-id host during undo window blocks all 5 restore categories', () => {
    seedBothRehydrated()

    // Seed additional data in all cascaded stores so we can assert each is gated
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'PdxStop',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(hA, 'dev001', event)
    // Also seed statuses directly so the snapshot is non-vacuous (handleNormalizedEvent
    // may or may not write statuses depending on event.status; seed directly to be sure)
    useAgentStore.setState((s) => ({
      statuses: { ...s.statuses, [`${hA}:dev001`]: 'running' as const },
    }))
    useSessionStore.getState().replaceHost(hA, [{ code: 'dev001', name: 'Dev', mode: 'terminal', cwd: '~' }])
    // Seed a tab in a workspace so the tab-restore gate (category 5) can be asserted
    const ws6 = useWorkspaceStore.getState().addWorkspace('WS for gate test')
    const tab6 = makeSessionTab(hA, 'dev001')
    useTabStore.getState().addTab(tab6)
    useWorkspaceStore.getState().addTabToWorkspace(ws6.id, tab6.id)

    const restore = deleteHostCascade(hA)

    // Recreate hA as a different entity during undo window
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [hA]: { id: hA, name: 'Recreated A', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, hA],
    }))

    restore()

    // hostWasRecreated = true → all 5 restore categories must be blocked:
    // 1. host position/order — recreated entry stays, snapshot hostOrder NOT applied
    expect(useHostStore.getState().hosts[hA]?.name).toBe('Recreated A')
    // 2. hostSettings — never cleared by the cascade (spec §3.4), so nothing to restore
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
    // 3. sessions — not restored
    expect(useSessionStore.getState().sessions[hA]).toBeUndefined()
    // 4. agentStore statuses — not restored (seeded directly above; snapshot captured it; gate must block)
    expect(useAgentStore.getState().statuses[`${hA}:dev001`]).toBeUndefined()
    // 5. tabs — never closed (spec §3.4): the tab and its workspace membership are as they were
    expect(useTabStore.getState().tabs[tab6.id]).toBeDefined()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab6.id)?.id).toBe(ws6.id)
  })

  // ── Case 6: Last-host veto ────────────────────────────────────────────────

  it('last-host veto: removeHost with only one host is no-op; cascade aborted', () => {
    // Seed only hA — removeHost will veto because hosts.length <= 1
    // Merge-mode: preserve action methods
    useHostStore.setState({
      hosts: {
        [hA]: { id: hA, name: 'Host A', ip: '127.0.0.1', port: 7860, order: 0 },
      },
      hostOrder: [hA],
      runtime: {},
      activeHostId: hA,
    })
    useHostSettingsStore.setState({ hosts: { [hA]: { editor: { homePath: '/tmp/a' } } } })

    const restore = deleteHostCascade(hA)

    // Host still present (veto)
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    // Settings untouched (cascade never ran)
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
    // Undo is safe no-op
    expect(() => restore()).not.toThrow()
  })

  // ── Case 7: Cascade regression — PR-1 contract ───────────────────────────

  it('cascade regression: removeHost keeps hostSettings.hA (host ownership spec §3.4 replaces the PR-1 contract)', () => {
    seedBothRehydrated()

    deleteHostCascade(hA)

    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
    // hB settings not affected (hB had no settings — stays undefined)
    expect(useHostStore.getState().hosts[hB]).toBeDefined()
  })

  // ── Case 8: Undo regression — PR-1 contract ──────────────────────────────

  it('undo regression: cascade then undo restores hosts + hostSettings (PR-1 contract)', () => {
    seedBothRehydrated()

    const restore = deleteHostCascade(hA)
    // Verify cascade ran
    expect(useHostStore.getState().hosts[hA]).toBeUndefined()

    restore()

    // Both restored
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    expect(useHostStore.getState().hostOrder).toContain(hA)
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
  })

  // ── Case 9: Workspace cross — useWorkspaceSettingsStore isolation ─────────

  it('workspace cross: removeHost does NOT clear useWorkspaceSettingsStore entries for hA-owned workspace', () => {
    seedBothRehydrated()

    // Seed a workspace with settings that reference hA indirectly (as module owner context)
    const WS_ID = 'ws-hA'
    // Merge-mode: preserve action methods
    useWorkspaceSettingsStore.setState({ workspaces: { [WS_ID]: { 'some-module': { hostId: hA, value: 42 } } } })

    deleteHostCascade(hA)

    // deleteHostCascade only uses useWorkspaceStore (tab membership), NOT useWorkspaceSettingsStore
    // — the module-settings store for workspaces must remain untouched as collateral-clear regression
    expect(useWorkspaceSettingsStore.getState().workspaces[WS_ID]).toEqual({
      'some-module': { hostId: hA, value: 42 },
    })
  })
})

// ── Peer cache invalidation (peer-info-panel spec §3.1) ───────────────────────
//
// A cached peer address belongs to a daemon identity. These tests drive the
// real cascade and the real `useHostStore.updateHost`, not the peer store's own
// `forgetHost`: a `forgetHost` nobody calls satisfies nothing.

function seedPeers(...hostIds: string[]) {
  const byHost: Record<string, ReturnType<typeof emptyPeerHostEntry>> = {}
  const cwdByHost: Record<string, Record<string, { cwd: string; fetchedAt: number; loading: boolean; error: string | null }>> = {}
  for (const id of hostIds) {
    byHost[id] = { ...emptyPeerHostEntry(), fetchedAt: 1, rows: { s1: {
      address: `${id}/agent`, ref: '_3k9f2m', title: 'title', titleSource: 'user',
      deliverable: true, reason: '', tmuxInstance: '1:1',
      agent: { type: 'cc', peerName: 'agent', status: 'idle' },
    } } }
    cwdByHost[id] = { s1: { cwd: `/somewhere/${id}`, fetchedAt: 1, loading: false, error: null } }
  }
  usePeerStore.setState({ byHost })
  useSessionCwdStore.setState({ byHost: cwdByHost })
}

describe('peer cache invalidation', () => {
  let stop: () => void = () => {}
  beforeEach(() => {
    resetAllStores()
    usePeerStore.setState({ byHost: {} })
    useSessionCwdStore.setState({ byHost: {} })
  })
  afterEach(() => { stop(); stop = () => {} })

  // The drift guard for the seeded row: these tests only ever check that the
  // rows are present or gone, so a v3-shaped fixture would sit here green
  // forever.
  it('the seeded row is a v4 row: a six-digit ref and a colon-free address', () => {
    seedPeers(HOST_A)
    const row = usePeerStore.getState().byHost[HOST_A].rows.s1
    expect(row.ref).toMatch(/^_[0-9a-z]{6}$/)
    expect(row.address).toMatch(/^[a-z0-9][a-z0-9.-]*\/(tmux:.+|_[0-9a-z]{6}|[a-z0-9][a-z0-9-]*)$/)
  })

  it('removing a host clears its peer rows through the cascade, leaving other hosts alone', () => {
    seedPeers(HOST_A, HOST_B)
    deleteHostCascade(HOST_A)
    expect(usePeerStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(usePeerStore.getState().byHost[HOST_B]).toBeDefined()
  })

  it('removing a host clears its cwd readings through the cascade too', () => {
    seedPeers(HOST_A, HOST_B)
    deleteHostCascade(HOST_A)
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(useSessionCwdStore.getState().byHost[HOST_B]).toBeDefined()
  })

  it('updateHost changing the ip clears the host peer rows and cwd readings', () => {
    stop = startPeerCacheInvalidation()
    seedPeers(HOST_A, HOST_B)
    useHostStore.getState().updateHost(HOST_A, { ip: '9.9.9.9' })
    expect(usePeerStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(usePeerStore.getState().byHost[HOST_B]).toBeDefined()
    expect(useSessionCwdStore.getState().byHost[HOST_B]).toBeDefined()
  })

  it('updateHost changing the port clears the host peer rows and cwd readings', () => {
    stop = startPeerCacheInvalidation()
    seedPeers(HOST_A)
    useHostStore.getState().updateHost(HOST_A, { port: 7861 })
    expect(usePeerStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeUndefined()
  })

  it('updateHost changing the token clears the host peer rows and cwd readings', () => {
    stop = startPeerCacheInvalidation()
    seedPeers(HOST_A)
    useHostStore.getState().updateHost(HOST_A, { token: 'rotated' })
    expect(usePeerStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeUndefined()
  })

  it('updateHost changing only the name keeps the peer rows and cwd readings', () => {
    stop = startPeerCacheInvalidation()
    seedPeers(HOST_A)
    useHostStore.getState().updateHost(HOST_A, { name: 'renamed' })
    expect(usePeerStore.getState().byHost[HOST_A]).toBeDefined()
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeDefined()
  })

  it('a host removed without the cascade (sync replace) is forgotten too', () => {
    stop = startPeerCacheInvalidation()
    seedPeers(HOST_A, HOST_B)
    useHostStore.setState((s) => ({
      hosts: { [HOST_B]: s.hosts[HOST_B] },
      hostOrder: [HOST_B],
    }))
    expect(usePeerStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeUndefined()
    expect(usePeerStore.getState().byHost[HOST_B]).toBeDefined()
  })

  it('stops watching once disposed', () => {
    const dispose = startPeerCacheInvalidation()
    dispose()
    seedPeers(HOST_A)
    useHostStore.getState().updateHost(HOST_A, { ip: '9.9.9.9' })
    expect(usePeerStore.getState().byHost[HOST_A]).toBeDefined()
    expect(useSessionCwdStore.getState().byHost[HOST_A]).toBeDefined()
  })
})

// Profile Sync P3b: the tabs on screen are ONE world; the master's and every other
// local profile's are parked in `useLocalProfilesStore`. They all use this device's
// hosts, so the deletion's rewrite reaches the parked worlds too (spec §3.4).
describe('host delete cascade — parked worlds', () => {
  beforeEach(resetAllStores)

  function parkedWorld(...tabs: Tab[]): ParkedWorld {
    const record: Record<string, Tab> = {}
    for (const t of tabs) record[t.id] = t
    return { workspaces: [{ id: `ws-${tabs[0].id}`, name: 'W', tabs: tabs.map((t) => t.id), activeTabId: tabs[0].id }], tabs: record, activeWorkspaceId: `ws-${tabs[0].id}`, activeTabId: tabs[0].id }
  }

  const contentIn = (world: ParkedWorld | null | undefined, tabId: string) => (world ? getPrimaryPane(world.tabs[tabId].layout).content : undefined)

  /** A slave `on` on screen (its tab is live), the master and the slave `off` parked — every world has a tab on A and one on B. */
  function threeWorlds() {
    const live = { a: makeSessionTab(HOST_A, 'live-a'), b: makeSessionTab(HOST_B, 'live-b') }
    const master = { a: makeSessionTab(HOST_A, 'm-a'), b: makeSessionTab(HOST_B, 'm-b') }
    const off = { a: makeSessionTab(HOST_A, 'o-a'), b: makeSessionTab(HOST_B, 'o-b') }
    useTabStore.getState().addTab(live.a)
    useTabStore.getState().addTab(live.b)
    useTabStore.setState({ worldId: 'on', worldEpoch: 1 })
    useWorkspaceStore.setState({ worldId: 'on', worldEpoch: 1 })
    useLocalProfilesStore.setState({
      slaves: { on: { id: 'on', name: 'On', createdAt: 1, world: null }, off: { id: 'off', name: 'Off', createdAt: 2, world: parkedWorld(off.a, off.b) } },
      slaveOrder: ['on', 'off'],
      activeProfileId: 'on',
      parkedMaster: parkedWorld(master.a, master.b),
      worldEpoch: 1,
    })
    return { live, master, off }
  }

  it('the host\'s panes carry its wire id in the parked master and in every parked slave — none marked, none closed; other hosts\' untouched', () => {
    const { master, off } = threeWorlds()
    const masterB = useLocalProfilesStore.getState().parkedMaster!.tabs[master.b.id]

    deleteHostCascade(HOST_A)

    const lp = useLocalProfilesStore.getState()
    expect(contentIn(lp.parkedMaster, master.a.id)).toMatchObject({ hostId: WIRE_A })
    expect(contentIn(lp.slaves.off.world, off.a.id)).toMatchObject({ hostId: WIRE_A })
    expect(lp.parkedMaster!.tabs[master.b.id]).toBe(masterB)
    expect(contentIn(lp.slaves.off.world, off.b.id)).toMatchObject({ hostId: HOST_B })
    expect(JSON.stringify([lp.parkedMaster, lp.slaves])).not.toContain('terminated')
    expect(Object.keys(lp.parkedMaster!.tabs)).toEqual([master.a.id, master.b.id])
    expect(lp.slaves.on.world).toBeNull()
  })

  it('a legacy hostless execution pane in a parked world is left alone', () => {
    threeWorlds()
    const exec = createTab({ kind: 'execution', executionId: 'e1' } as never)
    const pm = useLocalProfilesStore.getState().parkedMaster!
    useLocalProfilesStore.setState({ parkedMaster: { ...pm, tabs: { ...pm.tabs, [exec.id]: exec } } })

    deleteHostCascade(HOST_A) // HOST_A is hostOrder[0]

    expect(contentIn(useLocalProfilesStore.getState().parkedMaster, exec.id)).toEqual({ kind: 'execution', executionId: 'e1' })
  })

  it('no parked world: the local-profiles store is not written at all', () => {
    useTabStore.getState().addTab(makeSessionTab(HOST_A, 'x'))
    const before = useLocalProfilesStore.getState()
    const undo = deleteHostCascade(HOST_A)
    undo()
    expect(useLocalProfilesStore.getState()).toBe(before)
  })

  it('the veto (last host) touches no parked world', () => {
    threeWorlds()
    useHostStore.setState({ hosts: { [HOST_A]: useHostStore.getState().hosts[HOST_A] }, hostOrder: [HOST_A] })
    const before = useLocalProfilesStore.getState()
    deleteHostCascade(HOST_A)
    expect(useLocalProfilesStore.getState()).toBe(before)
  })

  it('undo: the parked master and the parked slave are back on the local id too', () => {
    const { master, off, live } = threeWorlds()
    const undo = deleteHostCascade(HOST_A)
    undo()
    const lp = useLocalProfilesStore.getState()
    expect(contentIn(lp.parkedMaster, master.a.id)).toMatchObject({ hostId: HOST_A })
    expect(contentIn(lp.slaves.off.world, off.a.id)).toMatchObject({ hostId: HOST_A })
    expect(getPrimaryPane(useTabStore.getState().tabs[live.a.id].layout).content).toMatchObject({ hostId: HOST_A })
  })
})
