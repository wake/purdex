// #1840 — the notification dispatcher finds an agent in ANY pane of a split tab, not only in its primary pane:
// - "has a tab" (the `notifyWithoutTab` gate) walks every pane of every tab;
// - "the user is looking at it" (no notification while the App is focused) is any pane of the active tab;
// - the click focuses the tab that holds the pane AND makes that pane the tab's focus target (usePaneFocusStore).
// The same for a terminal agent (tmux session) and a worker (execution pane); primary-pane behaviour is pinned.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { shouldNotify, handleNotificationClick, __resetDebounceStateForTests, useNotificationDispatcher } from './useNotificationDispatcher'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useWorkerTitlePrefetchStore } from '../stores/useWorkerTitlePrefetchStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { focusTargetOf } from '../lib/pane-focus'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

const HOST = 'h1'

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const blank = (id: string): PaneLayout => leaf(id, { kind: 'new-tab' })
const split = (id: string, children: PaneLayout[]): PaneLayout =>
  ({ type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

const kinds = [
  {
    name: 'a terminal agent (tmux session)',
    code: 'ses001',
    content: (): PaneContent => ({ kind: 'tmux-session', hostId: HOST, sessionCode: 'ses001', mode: 'terminal', cachedName: '', tmuxInstance: '' }),
    event: { agent_type: 'cc', status: 'waiting', raw_event_name: 'PdxPermissionRequest', detail: { tool_name: 'Bash' } },
  },
  {
    name: 'a worker (execution pane)',
    code: 'exec-e1',
    content: (): PaneContent => ({ kind: 'execution', executionId: 'e1', host: HOST }),
    event: { agent_type: 'cc', status: 'idle', raw_event_name: 'Stop', detail: {} },
  },
] as const

describe.each(kinds)('#1840: $name in a split tab', ({ code, content, event }) => {
  const CK = `${HOST}:${code}`
  /** The agent in the SECONDARY pane: [blank, agent]. */
  const secondaryTab = () => tab('tS', split('sS', [blank('pS1'), leaf('pS2', content())]))
  /** The agent in the PRIMARY pane of a split: [agent, blank]. */
  const primaryTab = () => tab('tP', split('sP', [leaf('pP1', content()), blank('pP2')]))
  const otherTab = () => tab('tO', blank('pO'))
  const openTabs = (list: Tab[], activeTabId: string | null) =>
    useTabStore.setState({ tabs: Object.fromEntries(list.map((t) => [t.id, t])), tabOrder: list.map((t) => t.id), activeTabId })

  let showNotification: ReturnType<typeof vi.fn>
  let focusMyWindow: ReturnType<typeof vi.fn>
  let dispatcher: { unmount: () => void } | null

  const fire = (broadcast_ts = 2) => {
    dispatcher ??= renderHook(() => useNotificationDispatcher())
    useAgentStore.setState({ lastEvents: { [CK]: { ...event, broadcast_ts } } })
  }
  const appFocused = (focused: boolean) => vi.spyOn(document, 'hasFocus').mockReturnValue(focused)

  beforeEach(() => {
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} }) // notifyWithoutTab = false
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    useHostStore.setState({ hostOrder: [HOST] })
    useShownHostsStore.setState({ ids: [HOST] })
    usePaneFocusStore.setState({ recent: {} })
    useExecutionStore.setState({ executions: {} })
    useExecutionListStore.setState({ byHost: {} })
    useWorkerTitlePrefetchStore.setState({ byKey: {} })
    useNexHostStore.setState({ byHost: {} })
    showNotification = vi.fn()
    focusMyWindow = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification, focusMyWindow }, writable: true, configurable: true })
    dispatcher = null
  })

  afterEach(() => {
    dispatcher?.unmount()
    vi.restoreAllMocks()
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
  })

  describe('secondary pane', () => {
    it('App in the background, the split tab active: notifies although notifyWithoutTab is off', () => {
      appFocused(false)
      openTabs([secondaryTab(), otherTab()], 'tS')
      fire()
      expect(showNotification).toHaveBeenCalledTimes(1)
      expect(showNotification.mock.calls[0][0].action).toEqual({ kind: 'open-session', hostId: HOST, sessionCode: code })
    })

    it('App in the background, another tab active: notifies although notifyWithoutTab is off', () => {
      appFocused(false)
      openTabs([secondaryTab(), otherTab()], 'tO')
      fire()
      expect(showNotification).toHaveBeenCalledTimes(1)
    })

    it('App focused and the split tab active: the user is looking at it — no notification', () => {
      appFocused(true)
      openTabs([secondaryTab(), otherTab()], 'tS')
      fire()
      expect(showNotification).not.toHaveBeenCalled()
    })

    it('App focused and the agent nested deep in the active tab: no notification', () => {
      appFocused(true)
      const deep = tab('tS', split('s1', [blank('p1'), split('s2', [blank('p2'), split('s3', [blank('p3'), leaf('p4', content())])])]))
      openTabs([deep, otherTab()], 'tS')
      fire()
      expect(showNotification).not.toHaveBeenCalled()
    })

    it('App focused and another tab active: notifies', () => {
      appFocused(true)
      openTabs([secondaryTab(), otherTab()], 'tO')
      fire()
      expect(showNotification).toHaveBeenCalledTimes(1)
    })

    it('the click focuses the split tab, its workspace, and that pane; marks read and raises the window', () => {
      openTabs([secondaryTab(), otherTab()], 'tO')
      const wsS = useWorkspaceStore.getState().addWorkspace('S')
      const wsO = useWorkspaceStore.getState().addWorkspace('O')
      useWorkspaceStore.getState().addTabToWorkspace(wsS.id, 'tS')
      useWorkspaceStore.getState().addTabToWorkspace(wsO.id, 'tO')
      useWorkspaceStore.getState().setActiveWorkspace(wsO.id)
      // The user last worked in the OTHER pane of the split tab.
      usePaneFocusStore.getState().touch('tS', 'pS1')
      useAgentStore.setState({ unread: { [CK]: true } })

      handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: code })

      const { activeTabId, tabs } = useTabStore.getState()
      expect(activeTabId).toBe('tS')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsS.id)
      expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsS.id)?.activeTabId).toBe('tS')
      expect(usePaneFocusStore.getState().recent.tS?.[0]).toBe('pS2')
      expect(focusTargetOf(tabs.tS, usePaneFocusStore.getState().recent.tS)).toBe('pS2')
      expect(useAgentStore.getState().unread[CK]).toBeUndefined()
      expect(focusMyWindow).toHaveBeenCalledTimes(1)
    })

    it('the click on a split tab that is already active still makes that pane the focus target', () => {
      openTabs([secondaryTab(), otherTab()], 'tS')
      usePaneFocusStore.getState().touch('tS', 'pS1')
      handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: code })
      expect(useTabStore.getState().activeTabId).toBe('tS')
      expect(usePaneFocusStore.getState().recent.tS?.[0]).toBe('pS2')
      // No activation happens in a tab on screen: the one-shot request moves the keyboard (#1840 A1).
      expect(usePaneFocusStore.getState().focusRequest).toMatchObject({ tabId: 'tS', paneId: 'pS2', taken: false })
    })
  })

  describe('primary pane (pinned: unchanged)', () => {
    it('App focused and the tab active: no notification', () => {
      appFocused(true)
      openTabs([primaryTab(), otherTab()], 'tP')
      fire()
      expect(showNotification).not.toHaveBeenCalled()
    })

    it('App focused and another tab active: notifies with notifyWithoutTab off', () => {
      appFocused(true)
      openTabs([primaryTab(), otherTab()], 'tO')
      fire()
      expect(showNotification).toHaveBeenCalledTimes(1)
    })

    it('App in the background and the tab active: notifies', () => {
      appFocused(false)
      openTabs([primaryTab(), otherTab()], 'tP')
      fire()
      expect(showNotification).toHaveBeenCalledTimes(1)
    })

    it('the click focuses the tab and its primary pane', () => {
      openTabs([primaryTab(), otherTab()], 'tO')
      handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: code })
      const { activeTabId, tabs } = useTabStore.getState()
      expect(activeTabId).toBe('tP')
      expect(focusTargetOf(tabs.tP, usePaneFocusStore.getState().recent.tP)).toBe('pP1')
    })
  })

  it('no pane anywhere shows the agent: still quiet with notifyWithoutTab off (gate kept)', () => {
    appFocused(false)
    openTabs([tab('tB', split('sB', [blank('pB1'), blank('pB2')])), otherTab()], 'tO')
    fire()
    expect(showNotification).not.toHaveBeenCalled()
  })

  it('two tabs hold the agent (one as a secondary pane, one as the primary): the click lands on the primary-pane tab', () => {
    // The secondary-pane tab comes first, so a first-match walk would wrongly land on it.
    openTabs([secondaryTab(), primaryTab(), otherTab()], 'tO')
    handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: code })
    expect(useTabStore.getState().activeTabId).toBe('tP')
    expect(usePaneFocusStore.getState().recent.tP?.[0]).toBe('pP1')
    expect(usePaneFocusStore.getState().recent.tS).toBeUndefined()
  })

  it('the same code on ANOTHER host in the active tab does not silence this one', () => {
    appFocused(true)
    useHostStore.setState({ hostOrder: [HOST, 'h2'] })
    const elsewhere: PaneContent = content().kind === 'execution'
      ? { kind: 'execution', executionId: 'e1', host: 'h2' }
      : { kind: 'tmux-session', hostId: 'h2', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '' }
    openTabs([tab('tX', split('sX', [blank('pX1'), leaf('pX2', elsewhere)])), secondaryTab()], 'tX')
    fire()
    expect(showNotification).toHaveBeenCalledTimes(1)
  })
})

// #1840 review A2: a tmux restart hands `$N` out again, so an ENDED pane can carry a NEW live session's code. An ended
// (terminated) pane is not "a tab of" the agent, does not count as "on screen", and never takes the click.
describe('#1840 A2: an ended tmux pane is not the agent', () => {
  const CODE = 'ses001'
  const CK = `${HOST}:${CODE}`
  const tmuxPane = (terminated?: 'tmux-restarted'): PaneContent =>
    ({ kind: 'tmux-session', hostId: HOST, sessionCode: CODE, mode: 'terminal', cachedName: '', tmuxInstance: '', ...(terminated ? { terminated } : {}) })
  const openTabs = (list: Tab[], activeTabId: string | null) =>
    useTabStore.setState({ tabs: Object.fromEntries(list.map((t) => [t.id, t])), tabOrder: list.map((t) => t.id), activeTabId })
  const otherTab = () => tab('tO', blank('pO'))
  let showNotification: ReturnType<typeof vi.fn>
  let dispatcher: { unmount: () => void } | null

  const fire = () => {
    dispatcher ??= renderHook(() => useNotificationDispatcher())
    useAgentStore.setState({ lastEvents: { [CK]: { agent_type: 'cc', status: 'waiting', raw_event_name: 'PdxPermissionRequest', broadcast_ts: 2, detail: { tool_name: 'Bash' } } } })
  }

  beforeEach(() => {
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} }) // notifyWithoutTab = false
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    useHostStore.setState({ hostOrder: [HOST] })
    useShownHostsStore.setState({ ids: [HOST] })
    usePaneFocusStore.setState({ recent: {} })
    showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification, focusMyWindow: vi.fn() }, writable: true, configurable: true })
    dispatcher = null
  })

  afterEach(() => {
    dispatcher?.unmount()
    vi.restoreAllMocks()
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  })

  it('only an ended pane on the code: no tab, so quiet with notifyWithoutTab off', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    openTabs([tab('tE', split('sE', [blank('pE1'), leaf('pE2', tmuxPane('tmux-restarted'))])), otherTab()], 'tO')
    fire()
    expect(showNotification).not.toHaveBeenCalled()
  })

  it('only an ended pane on the code, its tab active and the App focused: not "on screen" — notifies with notifyWithoutTab on', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    useNotificationSettingsStore.getState().setNotifyWithoutTab('cc', true)
    openTabs([tab('tE', split('sE', [leaf('pE1', tmuxPane('tmux-restarted')), blank('pE2')])), otherTab()], 'tE')
    fire()
    expect(showNotification).toHaveBeenCalledTimes(1)
  })

  it('an ended primary and the live session in a secondary pane: the click lands on the live pane', () => {
    openTabs([tab('tE', split('sE', [leaf('pE1', tmuxPane('tmux-restarted')), leaf('pE2', tmuxPane())])), otherTab()], 'tO')
    handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: CODE })
    expect(useTabStore.getState().activeTabId).toBe('tE')
    expect(usePaneFocusStore.getState().recent.tE?.[0]).toBe('pE2')
  })

  it('an ended primary in an earlier tab, the live session in a later tab: the click lands on the live one', () => {
    openTabs([tab('tE', leaf('pE', tmuxPane('tmux-restarted'))), tab('tL', split('sL', [blank('pL1'), leaf('pL2', tmuxPane())])), otherTab()], 'tO')
    handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: CODE })
    expect(useTabStore.getState().activeTabId).toBe('tL')
    expect(usePaneFocusStore.getState().recent.tL?.[0]).toBe('pL2')
    expect(usePaneFocusStore.getState().recent.tE).toBeUndefined()
  })
})

describe('#1840: a worker in a secondary pane is titled from its own pane', () => {
  it('the notification title uses the secondary pane fromTitle (not the execution id)', () => {
    const CK = `${HOST}:exec-e1`
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    useHostStore.setState({ hostOrder: [HOST] })
    useShownHostsStore.setState({ ids: [HOST] })
    useNotificationSettingsStore.setState({ agents: {} })
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useExecutionStore.setState({ executions: {} })
    useExecutionListStore.setState({ byHost: {} })
    useWorkerTitlePrefetchStore.setState({ byKey: {} })
    useNexHostStore.setState({ byHost: {} })
    const t = tab('tS', split('sS', [blank('pS1'), leaf('pS2', { kind: 'execution', executionId: 'e1', host: HOST, fromTitle: 'Old terminal' })]))
    useTabStore.setState({ tabs: { tS: t }, tabOrder: ['tS'], activeTabId: null, visitHistory: [] })
    const showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    const { unmount } = renderHook(() => useNotificationDispatcher())
    try {
      useAgentStore.setState({ lastEvents: { [CK]: { agent_type: 'cc', status: 'idle', raw_event_name: 'Stop', broadcast_ts: 2, detail: {} } } })
      expect(showNotification).toHaveBeenCalledTimes(1)
      expect(showNotification.mock.calls[0][0].title).toBe('Old terminal')
    } finally {
      unmount()
      vi.restoreAllMocks()
      Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
      localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
    }
  })
})

describe('#1840: shouldNotify takes "visible in the active tab" as a boolean', () => {
  const settings: NotificationSettings = { enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false }
  const base = { derived: 'waiting', eventName: 'PdxPermissionRequest', compositeKey: 'h1:ses001', hasTab: true, settings }

  beforeEach(() => __resetDebounceStateForTests())
  afterEach(() => vi.restoreAllMocks())

  it('visible + App focused → quiet', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    expect(shouldNotify({ ...base, visibleInActiveTab: true })).toBe(false)
  })
  it('visible + App in the background → notifies', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    expect(shouldNotify({ ...base, visibleInActiveTab: true })).toBe(true)
  })
  it('not visible + App focused → notifies', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    expect(shouldNotify({ ...base, visibleInActiveTab: false })).toBe(true)
  })
})
