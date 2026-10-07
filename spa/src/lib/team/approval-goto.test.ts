// spa/src/lib/team/approval-goto.test.ts — after a decision made in this window, the window goes to the requester's tab
// (lead-team spec §2 "How this spec reads U22" (a), §15; plan v3 P9b-1): the tab already showing its tmux session is
// activated, else one is opened as the session list opens one; nothing moves when there is no tmux, the session is not
// in this host's list, or the host is hidden here. Real stores throughout.
import { describe, it, expect, beforeEach } from 'vitest'
import { gotoRequester, parseOriginTmux } from './approval-goto'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostStore } from '../../stores/useHostStore'
import { usePaneFocusStore } from '../../stores/usePaneFocusStore'
import type { Session } from '../host-api'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import type { Approval } from './types'

const H = 'h1'
const H2 = 'h2'
const approval = (tmux = 'purdex:@1.%2', over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'approved', created_at: 1_000, deadline_at: 541_000, lease_until: 31_000,
  ...over,
})
const session = (over: Partial<Session> = {}): Session => ({ code: 'c01', name: 'purdex', cwd: '/w/purdex', mode: 'terminal', tmux_instance: '77:1700', ...over })

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const blank = (id: string): PaneLayout => leaf(id, { kind: 'new-tab' })
const split = (id: string, children: PaneLayout[]): PaneLayout =>
  ({ type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })
const tmuxPane = (hostId: string, sessionCode: string, terminated?: 'tmux-restarted'): PaneContent =>
  ({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '', ...(terminated ? { terminated } : {}) })
const openTabs = (list: Tab[], activeTabId: string | null) =>
  useTabStore.setState({ tabs: Object.fromEntries(list.map((t) => [t.id, t])), tabOrder: list.map((t) => t.id), activeTabId })

/** Tab `tO` (a blank tab) active in workspace `O`; returns the workspace ids. */
function onOtherTab(extra: Tab[] = []) {
  openTabs([...extra, tab('tO', blank('pO'))], 'tO')
  const wsO = useWorkspaceStore.getState().addWorkspace('O')
  useWorkspaceStore.getState().addTabToWorkspace(wsO.id, 'tO')
  useWorkspaceStore.getState().setActiveWorkspace(wsO.id)
  return { wsO: wsO.id }
}

/** Every store gotoRequester may write, by reference: "nothing changes" means none of them was written. */
const snapshotStores = () => ({
  tabs: useTabStore.getState().tabs,
  tabOrder: useTabStore.getState().tabOrder,
  activeTabId: useTabStore.getState().activeTabId,
  workspaces: useWorkspaceStore.getState().workspaces,
  activeWorkspaceId: useWorkspaceStore.getState().activeWorkspaceId,
  recent: usePaneFocusStore.getState().recent,
  focusRequest: usePaneFocusStore.getState().focusRequest,
})

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useHostStore.setState({ hostOrder: [H, H2] })
  useShownHostsStore.setState({ ids: [H, H2] })
  useSessionStore.setState({ sessions: { [H]: [session()] }, activeHostId: null, activeCode: null })
  usePaneFocusStore.setState({ recent: {}, focusRequest: null })
})

describe('parseOriginTmux', () => {
  it('parses <session>:@<win>.%<pane>; \'\' and \'a\' give null', () => {
    expect(parseOriginTmux('nexen33:@95.%95')).toEqual({ session: 'nexen33', pane: '%95' })
    expect(parseOriginTmux('purdex:@1.%2')).toEqual({ session: 'purdex', pane: '%2' })
    expect(parseOriginTmux('')).toBeNull()
    expect(parseOriginTmux('a')).toBeNull()
  })

  it('the session is cut at the FIRST colon; no dot gives an empty pane; an empty session name is null', () => {
    expect(parseOriginTmux('a:b:@1.%2')).toEqual({ session: 'a', pane: '%2' })
    expect(parseOriginTmux('purdex:@1')).toEqual({ session: 'purdex', pane: '' })
    expect(parseOriginTmux(':@1.%2')).toBeNull()
  })
})

describe('gotoRequester', () => {
  it('a live pane of that host and session is activated, its pane focused and its workspace shown; no tab is added', () => {
    const shown = tab('tS', split('sS', [blank('pS1'), leaf('pS2', tmuxPane(H, 'c01'))]))
    onOtherTab([shown])
    const wsS = useWorkspaceStore.getState().addWorkspace('S')
    useWorkspaceStore.getState().addTabToWorkspace(wsS.id, 'tS')
    usePaneFocusStore.getState().touch('tS', 'pS1')

    expect(gotoRequester(H, approval())).toBe('activated')

    expect(useTabStore.getState().tabOrder).toEqual(['tS', 'tO'])
    expect(useTabStore.getState().activeTabId).toBe('tS')
    expect(usePaneFocusStore.getState().recent.tS?.[0]).toBe('pS2')
    expect(usePaneFocusStore.getState().focusRequest).toMatchObject({ tabId: 'tS', paneId: 'pS2', taken: false })
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsS.id)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsS.id)?.activeTabId).toBe('tS')
  })

  it('no live pane: one tmux-session tab with the list\'s code, name and tmux_instance is added, inserted and activated', () => {
    const { wsO } = onOtherTab()

    expect(gotoRequester(H, approval())).toBe('opened')

    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    expect(tabOrder).toHaveLength(2)
    const added = tabOrder.find((id) => id !== 'tO')!
    expect(activeTabId).toBe(added)
    expect(tabs[added].layout).toMatchObject({
      type: 'leaf',
      pane: { content: { kind: 'tmux-session', hostId: H, sessionCode: 'c01', mode: 'terminal', cachedName: 'purdex', tmuxInstance: '77:1700' } },
    })
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsO)?.tabs).toEqual(['tO', added])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsO)
  })

  it('no live pane and no active workspace: the new tab\'s workspace is shown, as on a notification click', () => {
    openTabs([tab('tO', blank('pO'))], 'tO')
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addTabToWorkspace(wsA.id, 'tO')
    useWorkspaceStore.getState().setActiveWorkspace(null)

    expect(gotoRequester(H, approval())).toBe('opened')

    const added = useTabStore.getState().activeTabId!
    expect(useWorkspaceStore.getState().findWorkspaceByTab(added)?.id).toBe(wsA.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsA.id)
  })

  it('the same session name on another host is not a match', () => {
    // (1) Only ANOTHER host lists a session of that name: nothing to switch to on this host.
    useSessionStore.setState({ sessions: { [H]: [], [H2]: [session({ code: 'c09' })] } })
    onOtherTab()
    const before = snapshotStores()
    expect(gotoRequester(H, approval())).toBe('none')
    expect(snapshotStores()).toEqual(before)
    expect(useTabStore.getState().tabs).toBe(before.tabs)

    // (2) Both hosts list it under the same code (codes collide across hosts) and only the other host's is in a tab:
    // that tab is not this requester's — a tab for this host's session opens.
    useSessionStore.setState({ sessions: { [H]: [session()], [H2]: [session()] } })
    const elsewhere = tab('tX', leaf('pX', tmuxPane(H2, 'c01')))
    openTabs([elsewhere, tab('tO', blank('pO'))], 'tO')
    expect(gotoRequester(H, approval())).toBe('opened')
    const added = useTabStore.getState().activeTabId!
    expect(added).not.toBe('tX')
    expect(useTabStore.getState().tabs[added].layout).toMatchObject({ pane: { content: { hostId: H, sessionCode: 'c01' } } })
  })

  it('an ended pane on that code is not a match: a new tab opens', () => {
    const ended = tab('tE', leaf('pE', tmuxPane(H, 'c01', 'tmux-restarted')))
    onOtherTab([ended])

    expect(gotoRequester(H, approval())).toBe('opened')

    const { tabOrder, activeTabId } = useTabStore.getState()
    expect(tabOrder).toHaveLength(3)
    expect(activeTabId).not.toBe('tE')
    expect(activeTabId).not.toBe('tO')
    expect(usePaneFocusStore.getState().recent.tE).toBeUndefined()
  })

  it.each<[string, () => void, Approval]>([
    ['empty origin.tmux', () => {}, approval('')],
    ['a name not in the host\'s list', () => {}, approval('other:@1.%2')],
    ['the host\'s list not loaded', () => useSessionStore.setState({ sessions: {} }), approval()],
    ['a host hidden in this workbench', () => useShownHostsStore.setState({ ids: [H2] }), approval()],
  ])('%s: nothing changes', (_label, arrange, a) => {
    arrange()
    onOtherTab([tab('tS', leaf('pS', tmuxPane(H, 'c01')))])
    const before = snapshotStores()

    expect(gotoRequester(H, a)).toBe('none')

    expect(snapshotStores()).toEqual(before)
    expect(useTabStore.getState().tabs).toBe(before.tabs)
    expect(useWorkspaceStore.getState().workspaces).toBe(before.workspaces)
    expect(useTabStore.getState().activeTabId).toBe('tO')
  })

  it('a self_relay request goes to its requester the same way', () => {
    onOtherTab([tab('tS', leaf('pS', tmuxPane(H, 'c01')))])
    const relay = approval('purdex:@1.%2', { kind: 'self_relay', payload: { op_id: 'op-1', used_percentage: 71, window: 200_000 } })
    expect(gotoRequester(H, relay)).toBe('activated')
    expect(useTabStore.getState().activeTabId).toBe('tS')
  })
})
