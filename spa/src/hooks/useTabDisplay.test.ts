import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useAgentStore } from '../stores/useAgentStore'
import type { SubagentRef } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useI18nStore } from '../stores/useI18nStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { compositeKey } from '../lib/composite-key'
import { useTabDisplay } from './useTabDisplay'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useWorkerSettingsStore, DEFAULT_WORKER_SETTINGS } from '../stores/useWorkerSettingsStore'
import { defaultExecutionState } from '../lib/nex/event-reducer'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import type { ExecutionSummary } from '../lib/nex/types'
import { useNexHostStore } from '../stores/useNexHostStore'
import { CC_ICON_VARIANTS, CODEX_ICON_VARIANTS, CC_COLOR_ICON_VARIANTS } from '../lib/agent-icons'
import { ICON_MAP } from '../components/tab-icon-map'
import { conversationRebuildContent } from '../lib/nex/open-conversation-rebuild'

function makeTab(
  overrides: Partial<{ hostId: string; sessionCode: string; terminated: boolean; cachedName: string }> = {},
): Tab {
  const tab = createTab({
    kind: 'tmux-session',
    hostId: overrides.hostId ?? 'h1',
    sessionCode: overrides.sessionCode ?? 'sc1',
    mode: 'terminal',
    cachedName: overrides.cachedName ?? '',
    tmuxInstance: '',
    terminated: overrides.terminated ? 'session-closed' : undefined,
  } as never)
  return { ...tab, id: 't1' }
}

beforeEach(() => {
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {} })
  useAgentStore.setState({
    unread: {},
    statuses: {},
    subagents: {},
    agentTypes: {},
    oscTitles: {},
  })
  useUISettingsStore.setState({
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
    codexIconVariant: 'openai',
    dynamicTabName: false,
    showAgentTitleInStatusBar: false,
    stripAgentTitleMarker: true,
  })
  useI18nStore.setState({ t: (k: string) => k })
})

describe('useTabDisplay — label resolution', () => {
  it('uses session name from session store when available', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'my-session' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('my-session')
  })

  it('falls back to sessionCode when session not found', () => {
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('sc1')
  })

  it('falls back to cachedName when session not found and cachedName present', () => {
    const { result } = renderHook(() => useTabDisplay(makeTab({ cachedName: 'cached' })))
    expect(result.current.displayTitle).toBe('cached')
  })

  it('scopes session lookup to the tabs own hostId (no cross-host collisions)', () => {
    useSessionStore.setState({
      sessions: {
        h1: [{ code: 'sc1', name: 'correct' }] as never,
        h2: [{ code: 'sc1', name: 'wrong-host' }] as never,
      },
      activeHostId: null,
      activeCode: null,
    })
    const { result } = renderHook(() => useTabDisplay(makeTab({ hostId: 'h1' })))
    expect(result.current.displayTitle).toBe('correct')
  })
})

describe('useTabDisplay — agent title override', () => {
  it('uses pane_title - sessionLabel when dynamicTabName=true and agent type exists', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: 'plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'claude' },
    })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('plan review - base')
  })

  it('ignores oscTitles when pane_title is absent', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({
      agentTypes: { 'h1:sc1': 'cc' },
      oscTitles: { 'h1:sc1': 'claude' },
    })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('base')
  })

  it('preserves plain sessionLabel when dynamicTabName=false', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: 'plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: false })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('base')
  })

  it('ignores pane_title on terminated session', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: 'plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({
      agentTypes: { 'h1:sc1': 'cc' },
    })
    // The closed-terminal suffix is UI copy (`page.pane.terminated`): read it in English, not through the key stub.
    useI18nStore.getState().setLocale('en')
    const { result } = renderHook(() => useTabDisplay(makeTab({ terminated: true, cachedName: 'base' })))
    expect(result.current.displayTitle).toBe('base（Terminated）')
  })

  it('a conversation-ended tab is titled by its conversation, with the closed-terminal suffix', () => {
    useUISettingsStore.setState({ dynamicTabName: true })
    useI18nStore.getState().setLocale('en')
    const tab: Tab = {
      ...createTab(conversationRebuildContent('h1', {
        session_id: 'aaaaaaaa-1111-2222-3333-444444444444', title: 'Fix the login bug', title_source: 'ai', cwd: '/w',
        cwd_exists: true, last_activity_at: 5, last_in: 'terminal',
      }, 'proj-2', 'i', 1)),
      id: 't1',
    }
    const { result } = renderHook(() => useTabDisplay(tab))
    expect(result.current.displayTitle).toBe('Fix the login bug（Terminated）')
    expect(result.current.isTerminated).toBe(true)
  })

  it('strips the cc marker from pane_title by default', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: '✳ plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('plan review - base')
  })

  it('keeps the marker when stripAgentTitleMarker is off', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: '✳ plan review' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true, stripAgentTitleMarker: false })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('✳ plan review - base')
  })

  it('falls back to the base label when the title is only the marker', () => {
    useSessionStore.setState({
      sessions: { h1: [{ code: 'sc1', name: 'base', pane_title: '✳ ' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useUISettingsStore.setState({ dynamicTabName: true })
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.displayTitle).toBe('base')
  })
})

describe('useTabDisplay — icon resolution', () => {
  it('returns agent icon when agentType is present and not terminated', () => {
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.IconComponent).toBeDefined()
  })

  it('returns pane icon on terminated session regardless of agentType', () => {
    useAgentStore.setState({ agentTypes: { 'h1:sc1': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(makeTab({ terminated: true })))
    expect(result.current.IconComponent).toBeDefined()
    expect(result.current.isTerminated).toBe(true)
  })

  it('re-resolves the cc agent icon when the cc icon variant changes', () => {
    const tab = makeTab()
    const ck = compositeKey('h1', 'sc1')
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    useUISettingsStore.setState({ tabIndicatorStyle: 'iconDot', ccIconVariant: 'bot', codexIconVariant: 'openai' })

    const { result, rerender } = renderHook(() => useTabDisplay(tab))
    const first = result.current.IconComponent
    act(() => {
      useUISettingsStore.setState({ ccIconVariant: 'star' }) // valid CcIconVariant
    })
    rerender()
    expect(result.current.IconComponent).not.toBe(first) // variant flows through the extracted hook
  })
})

describe('useTabDisplay — host offline', () => {
  it('flags host offline when runtime status is not connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.isHostOffline).toBe(true)
  })

  it('does not flag host offline when tab is terminated', () => {
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    const { result } = renderHook(() => useTabDisplay(makeTab({ terminated: true })))
    expect(result.current.isHostOffline).toBe(false)
  })

  it('does not flag host offline when host is connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } } as never)
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.isHostOffline).toBe(false)
  })
})

describe('useTabDisplay — agent store fields', () => {
  it('exposes agentStatus, unread, subagentCount, tabIndicatorStyle', () => {
    useAgentStore.setState({
      statuses: { 'h1:sc1': 'running' },
      unread: { 'h1:sc1': true },
      subagents: {
        'h1:sc1': [
          { id: 's1', type: 'cc', started_at: 0, source_pid: 0, source_start_time: '' } satisfies SubagentRef,
        ],
      },
    })
    useUISettingsStore.setState({ tabIndicatorStyle: 'dot' })
    const { result } = renderHook(() => useTabDisplay(makeTab()))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.isUnread).toBe(true)
    expect(result.current.subagentCount).toBe(1)
    expect(result.current.tabIndicatorStyle).toBe('dot')
  })
})

describe('useTabDisplay — execution (worker) tab (spec §8.1 / §8.3 / §8.4)', () => {
  const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
    ({ id: 'e1', state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w/repo', mount_kind: 'dev', brief: 'Fix the bug\nmore',
      labels: {}, created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary

  const execTab = (over: { host?: string; fromTitle?: string } = {}): Tab => ({
    id: 'tx', pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: 'px', content: {
      kind: 'execution', executionId: 'e1', host: over.host ?? 'h1', ...(over.fromTitle !== undefined ? { fromTitle: over.fromTitle } : {}),
    } } },
  })

  const setLiveSummary = (over: Partial<ExecutionSummary> = {}) =>
    useExecutionStore.setState({ executions: { [executionKey('h1', 'e1')]: { ...defaultExecutionState(), summary: summary(over) } } })

  const setTitleSupported = (supported: boolean) =>
    useNexHostStore.setState({
      byHost: {
        h1: {
          info: null, error: null, fetchedAt: 0, generation: 0, fingerprint: '',
          phase: 'ready',
          capabilities: (supported ? { session_title: { sources: ['ai'], max_bytes: 200 } } : {}) as never,
        },
      },
    })

  beforeEach(() => {
    useExecutionStore.setState({ executions: {} })
    useExecutionListStore.setState({ byHost: {} })
    useWorkerSettingsStore.setState({ ...DEFAULT_WORKER_SETTINGS })
    useNexHostStore.setState({ byHost: {} })
  })

  it('reads the light from the exec-<id> key and gives a non-undefined icon', () => {
    const key = compositeKey('h1', 'exec-e1')
    useAgentStore.setState({ statuses: { [key]: 'running' }, unread: { [key]: true }, agentTypes: { [key]: 'cc' } })
    setLiveSummary()
    const { result } = renderHook(() => useTabDisplay(execTab()))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.isUnread).toBe(true)
    expect(result.current.IconComponent).toBe(CC_ICON_VARIANTS.bot)
    expect(result.current.displayTitle).toBe('Fix the bug - repo')
  })

  it('with no summary anywhere: Robot icon and the locale label', () => {
    const { result } = renderHook(() => useTabDisplay(execTab()))
    expect(result.current.IconComponent).toBeDefined()
    expect(result.current.IconComponent).toBe(ICON_MAP.Robot)
    expect(result.current.displayTitle).toBe('page.pane.execution')
  })

  it('falls back to the host list row for the summary', () => {
    useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), items: [summary({ provider: 'codex', brief: 'Row brief' })] } } })
    const { result } = renderHook(() => useTabDisplay(execTab()))
    expect(result.current.displayTitle).toBe('Row brief - repo')
    expect(result.current.IconComponent).toBe(CODEX_ICON_VARIANTS.openai)
  })

  it('the pre-handoff title wins over the brief', () => {
    setLiveSummary()
    const { result } = renderHook(() => useTabDisplay(execTab({ fromTitle: 'Old terminal' })))
    expect(result.current.displayTitle).toBe('Old terminal - repo')
  })

  it('honours the icon style setting', () => {
    setLiveSummary()
    useWorkerSettingsStore.setState({ iconStyle: 'color' })
    const { result } = renderHook(() => useTabDisplay(execTab()))
    expect(result.current.IconComponent).toBe(CC_COLOR_ICON_VARIANTS.bot)
    act(() => { useWorkerSettingsStore.setState({ iconStyle: 'custom', customIcon: '' }) })
    expect(result.current.IconComponent).toBe(ICON_MAP.Robot)
  })

  // Permission channel PC2 / spec §5.4: the worker tab title gets the 「（等待核准）」 suffix while a request is pending —
  // UI copy, so read it through the real locales, not the key stub.
  describe('awaiting approval suffix', () => {
    const pending = { request_id: 'r1', tool_name: 'Bash', since: 1_700_000_000_000 }
    afterEach(() => { useI18nStore.getState().setLocale('en'); useI18nStore.setState({ t: (k: string) => k }) })

    it('pending_permission set: the suffix (en, then zh-TW)', () => {
      useI18nStore.getState().setLocale('en')
      setLiveSummary({ state: 'running', pending_permission: pending })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix the bug - repo (awaiting approval)')
      act(() => { useI18nStore.getState().setLocale('zh-TW') })
      expect(result.current.displayTitle).toBe('Fix the bug - repo（等待核准）')
    })

    it('the suffix goes away when pending_permission turns null', () => {
      useI18nStore.getState().setLocale('zh-TW')
      setLiveSummary({ state: 'running', pending_permission: pending })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix the bug - repo（等待核准）')
      act(() => { setLiveSummary({ state: 'running', pending_permission: null }) })
      expect(result.current.displayTitle).toBe('Fix the bug - repo')
    })

    it.each([
      ['terminated', {}], ['rejected', {}], ['failed', {}], ['idle', { archived: true }],
    ])('pending + %s %j: no suffix', (state, extra) => {
      useI18nStore.getState().setLocale('zh-TW')
      setLiveSummary({ state, ...extra, pending_permission: pending })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix the bug - repo')
    })

    it('the field absent (old daemon): no suffix', () => {
      useI18nStore.getState().setLocale('zh-TW')
      setLiveSummary({ state: 'running' })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix the bug - repo')
    })

    it('a list row that is awaiting approval carries the suffix too (no pane open)', () => {
      useI18nStore.getState().setLocale('zh-TW')
      useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), items: [summary({ state: 'running', brief: 'Row brief', pending_permission: pending })] } } })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Row brief - repo（等待核准）')
    })

    it('with no worker title the locale label gets the suffix', () => {
      useI18nStore.getState().setLocale('en')
      setLiveSummary({ state: 'running', brief: '', cwd: '', pending_permission: pending })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Execution (awaiting approval)')
    })
  })

  describe('phase E: session_title gated by the host capability', () => {
    it('with the capability, session_title wins over the brief', () => {
      setTitleSupported(true)
      setLiveSummary({ session_title: { text: 'Fix login', source: 'ai' } })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix login - repo')
    })

    it('without the capability, the same summary falls back to the brief', () => {
      setTitleSupported(false)
      setLiveSummary({ session_title: { text: 'Fix login', source: 'ai' } })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix the bug - repo')
    })

    it('a session_title containing markup-looking text renders literally', () => {
      setTitleSupported(true)
      setLiveSummary({ session_title: { text: 'Fix <b>login</b>', source: 'custom' } })
      const { result } = renderHook(() => useTabDisplay(execTab()))
      expect(result.current.displayTitle).toBe('Fix <b>login</b> - repo')
    })
  })
})
